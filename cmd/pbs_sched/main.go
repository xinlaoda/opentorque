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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xinlaoda/opentorque/internal/sched/client"
	"github.com/xinlaoda/opentorque/internal/sched/config"
	"github.com/xinlaoda/opentorque/internal/sched/scheduler"
)

func main() {
	var (
		debug   = flag.Bool("D", false, "Run in foreground (debug mode)")
		pbsHome = flag.String("p", "/var/spool/torque", "PBS home directory")
	)
	flag.Parse()
	_ = debug

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

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

	// Determine cycle interval
	interval := time.Duration(cfg.SchedulerInterval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	// Signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("[SCHED] Starting scheduling loop (interval=%v)", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case sig := <-sigCh:
			log.Printf("[SCHED] Received signal %v, shutting down", sig)
			return
		case <-ticker.C:
			runOneCycle(sched, cfg)
		}
	}
}

// runOneCycle performs a single scheduling cycle with a fresh connection.
func runOneCycle(sched *scheduler.Scheduler, cfg *config.Config) {
	log.Printf("[SCHED] Starting cycle (server=%s)", cfg.Server)
	conn, err := client.Connect(cfg.Server)
	if err != nil {
		log.Printf("[SCHED] Cannot connect to server %s: %v", cfg.Server, err)
		return
	}
	defer conn.Close()
	log.Printf("[SCHED] Connected, running scheduling cycle")

	dispatched, err := sched.RunCycle(conn)
	if err != nil {
		log.Printf("[SCHED] Cycle error: %v", err)
		return
	}
	log.Printf("[SCHED] Cycle finished: dispatched %d job(s)", dispatched)
}

func init() {
	// Ensure consistent output format
	fmt.Sprintf("") // avoid unused import
}
