// Command pbs_sched is the external PBS job scheduler daemon.
// It connects to the pbs_server, queries job/queue/node status,
// makes scheduling decisions based on configured algorithms,
// and dispatches jobs to compute nodes via RunJob requests.
//
// Usage:
//
//	pbs_sched [-D] [-p pbs_home]
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/xinlaoda/opentorque/internal/cec"
	"github.com/xinlaoda/opentorque/internal/crp"
	"github.com/xinlaoda/opentorque/internal/dis"
	"github.com/xinlaoda/opentorque/internal/sched/client"
	"github.com/xinlaoda/opentorque/internal/sched/config"
	"github.com/xinlaoda/opentorque/internal/sched/scheduler"
	"github.com/xinlaoda/opentorque/pkg/pbslog"
)

func main() {
	var (
		debug   = flag.Bool("D", false, "Run in foreground (debug mode)")
		pbsHome = flag.String("p", "/var/spool/torque", "PBS home directory")
	)
	flag.Parse()
	_ = debug

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	// Set up YYYYMMDD dated log files in sched_logs/
	logDir := filepath.Join(*pbsHome, "sched_logs")
	dl, err := pbslog.Setup(logDir, *debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot open sched log dir %s: %v\n", logDir, err)
	} else {
		defer dl.Close()
	}

	cfg := config.Load(*pbsHome)
	log.Printf("pbs_sched (Go) starting")
	log.Printf("[SCHED] PBS Home: %s", *pbsHome)
	log.Printf("[SCHED] Server: %s", cfg.Server)
	log.Printf("[SCHED] Mode: %s", cfg.SchedulerMode)
	log.Printf("[SCHED] Algorithms: sort_by=%s, strict_fifo=%v, round_robin=%v, by_queue=%v, fair_share=%v",
		cfg.SortBy, cfg.StrictFIFO, cfg.RoundRobin, cfg.ByQueue, cfg.FairShare)
	log.Printf("[SCHED] Starvation: help_starving=%v, max_starve=%v", cfg.HelpStarvingJobs, cfg.MaxStarve)
	log.Printf("[SCHED] Load balancing: %v", cfg.LoadBalancing)

	sched := scheduler.New(cfg)

	// --- Cloud Elastic Controller (CEC) ---
	// M2: Azure real-cloud provider via MSI + REST. The CEC listens for
	// CapacityEvents and drives the AzureCRP to scale out worker VMs.
	// When no cloud queues exist the CEC is inert.
	//
	// The Azure subscription ID is required. It is read from the environment
	// (AZURE_SUBSCRIPTION_ID) or defaults to the test subscription.
	azureSub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if azureSub == "" {
		azureSub = "a04b47d2-8e6c-4b00-9a04-221a694231ee"
		log.Printf("[SCHED] AZURE_SUBSCRIPTION_ID not set; defaulting to %s", azureSub)
	}
	cecStop := make(chan struct{})
	cecCtrl := cec.New(crp.NewAzureCRP(azureSub))
	cecCtrl.SetNodeController(&serverNodeController{server: cfg.Server})
	go cecCtrl.Run(cecStop)
	log.Printf("[SCHED] Cloud Elastic Controller armed (provider=azure/M2, subscription=%s)", azureSub)

	// cloudTracker remembers which queues are cloud-backed so the scheduler can
	// notify the CEC (via RegisterNodesUp) when a provisioned VM's node boots
	// and registers, even on cycles where no capacity shortfall is emitted.
	ct := newCloudTracker(cecCtrl)

	// Determine cycle interval
	interval := time.Duration(cfg.SchedulerInterval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	// Ignore SIGHUP so the scheduler survives when the parent shell exits
	signal.Ignore(syscall.SIGHUP)

	// Signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("[SCHED] Event-driven trigger port: %d (0 = polling only)", cfg.SchedTriggerPort)

	// Event channel: the server connects to the trigger socket and writes a
	// marker whenever a job/node event occurs, so a limited cycle can run
	// immediately instead of waiting for the polling ticker.
	eventCh := make(chan struct{}, 1)
	var listener net.Listener
	if cfg.SchedTriggerPort > 0 {
		l, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.SchedTriggerPort))
		if lerr != nil {
			log.Printf("[SCHED] Cannot listen on trigger port %d: %v (falling back to polling)", cfg.SchedTriggerPort, lerr)
		} else {
			listener = l
			defer listener.Close()
			go acceptTriggers(listener, eventCh)
			log.Printf("[SCHED] Listening for scheduler triggers on 127.0.0.1:%d", cfg.SchedTriggerPort)
		}
	}

	if cfg.SchedTriggerPort > 0 {
		// Give the listener a moment to bind so the server does not hit
		// "connection refused" for the first submit right after startup.
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("[SCHED] Starting scheduling loop (interval=%v)", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	minGap := time.Duration(cfg.SchedMinInterval) * time.Millisecond
	if minGap <= 0 {
		minGap = time.Millisecond
	}
	var lastRun time.Time

	for {
		select {
		case sig := <-sigCh:
			log.Printf("[SCHED] Received signal %v, shutting down", sig)
			close(cecStop)
			if listener != nil {
				listener.Close()
			}
			return
		case <-ticker.C:
			// Full periodic sweep - the guaranteed safety-net floor. Not
			// queue-depth limited; cleans up anything limited cycles left.
			runOneCycle(sched, cfg, false, ct)
			lastRun = time.Now()
		case <-eventCh:
			if !cfg.EventDriven {
				// Legacy pure-poll behavior: ignore event notifications.
				continue
			}
			// Coalesce a burst of events into a single limited cycle.
			for draining := true; draining; {
				select {
				case <-eventCh:
				default:
					draining = false
				}
			}
			// Anti-storm throttle: don't run limited cycles closer than
			// sched_min_interval apart.
			if !lastRun.IsZero() {
				if d := time.Since(lastRun); d < minGap {
					select {
					case <-time.After(minGap - d):
					case <-sigCh:
						return
					}
				}
			}
			runOneCycle(sched, cfg, true, ct)
			lastRun = time.Now()
		}
	}
}

// acceptTriggers accepts TCP connections from pbs_server and pushes a marker
// onto eventCh for each received trigger byte. It exits when the listener is
// closed. A single connection may send multiple triggers; each read is a
// separate event.
func acceptTriggers(listener net.Listener, eventCh chan<- struct{}) {
	buf := make([]byte, 1)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				if _, err := c.Read(buf); err != nil {
					return
				}
				select {
				case eventCh <- struct{}{}:
				default:
				}
			}
		}(conn)
	}
}

// runOneCycle performs a single scheduling cycle with a fresh connection.
// When limited is true it runs a bounded (event-triggered) cycle that respects
// default_queue_depth / sched_max_job_start / max_sched_time instead of a full
// sweep.
func runOneCycle(sched *scheduler.Scheduler, cfg *config.Config, limited bool, ct *cloudTracker) {
	log.Printf("[SCHED] Starting %s cycle (server=%s)", map[bool]string{true: "limited (event)", false: "full (polling)"}[limited], cfg.Server)
	conn, err := client.Connect(cfg.Server)
	if err != nil {
		log.Printf("[SCHED] Cannot connect to server %s: %v", cfg.Server, err)
		return
	}
	defer conn.Close()
	log.Printf("[SCHED] Connected, running scheduling cycle")

	var res *scheduler.CycleResult
	if limited {
		res, err = sched.RunCycleLimited(conn)
	} else {
		res, err = sched.RunCycle(conn)
	}
	if err != nil {
		log.Printf("[SCHED] Cycle error: %v", err)
		return
	}
	log.Printf("[SCHED] Cycle finished: dispatched %d job(s)", res.Dispatched)

	// Forward any cloud capacity events to the CEC.
	for _, ce := range res.CapacityEvents {
		ev := cec.Event{
			Kind:     cec.EventCapacity,
			Queue:    ce.Queue,
			Provider: ce.Provider,
			SKU:      ce.SKU,
			MinNodes: ce.MinNodes,
			MaxNodes: ce.MaxNodes,
			IdleTime: time.Duration(ce.IdleTime) * time.Second,
			Reclaim:  ce.Reclaim,
			SubnetID: ce.SubnetID,
			ImageID:  ce.ImageID,
			DiskSize: ce.DiskSize,
			DiskType: ce.DiskType,
			SSHKey:   ce.SSHKey,
			Location: ce.Location,
			RGName:   ce.RGName,
			ServerAddr: cfg.Server,
			Shortfall: cec.Shortfall{
				Cores:   ce.Cores,
				Nodes:   ce.Nodes,
				Blocked: ce.Blocked,
			},
		}
		for _, jid := range ce.Jobs {
			ev.Jobs = append(ev.Jobs, cec.JobDemand{ID: jid})
		}
		ct.rememberQueue(ce.Queue)
		log.Printf("[SCHED] Forwarding capacity event to CEC: queue=%s cores=%d nodes=%d blocked=%d", ev.Queue, ev.Shortfall.Cores, ev.Shortfall.Nodes, ev.Shortfall.Blocked)
		ct.ctrl.Events <- ev
	}

	// After the cycle, tell the CEC which of the free nodes correspond to
	// still-provisioning VMs for each known cloud queue. This drives the
	// PROVISIONING -> R / inflight-decrement transition even on cycles without
	// a capacity shortfall.
	for _, q := range ct.queues() {
		ct.ctrl.RegisterNodesUp(q, res.FreeNodes)
		ct.ctrl.RegisterNodesIdle(q, res.IdleNodes)
	}
}

// serverNodeController implements cec.NodeController against the live PBS
// server using the existing scheduler wire client: draining sets the node
// state to "offline" (the scheduler refuses offline nodes), and deregistration
// deletes the node from the server database. It dials a fresh connection per
// call so it stays independent of the scheduler cycle connection lifecycle.
type serverNodeController struct {
	server string
}

func (n *serverNodeController) DrainNode(name string) error {
	conn, err := client.Connect(n.server)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	attrs := []dis.SvrAttrl{{Name: "state", Value: "offline", Op: 1}}
	return conn.Manager(dis.MgrCmdSet, dis.MgrObjNode, name, attrs)
}

func (n *serverNodeController) DeregisterNode(name string) error {
	conn, err := client.Connect(n.server)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	return conn.Manager(dis.MgrCmdDelete, dis.MgrObjNode, name, nil)
}

// cloudTracker remembers the set of cloud-backed queues seen so the scheduler
// can notify the CEC about registered nodes on every cycle, not only on
// capacity-shortfall cycles.
type cloudTracker struct {
	mu     sync.Mutex
	ctrl   *cec.Controller
	known  map[string]struct{}
}

func newCloudTracker(ctrl *cec.Controller) *cloudTracker {
	return &cloudTracker{ctrl: ctrl, known: make(map[string]struct{})}
}

func (t *cloudTracker) rememberQueue(q string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.known[q] = struct{}{}
}

func (t *cloudTracker) queues() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.known))
	for q := range t.known {
		out = append(out, q)
	}
	return out
}

