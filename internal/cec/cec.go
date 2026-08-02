// Package cec implements the Cloud Elastic Controller (CEC).
//
// The CEC is the sole orchestrator of node membership for cloud-backed queues.
// It is a pure event loop over an inbox — it never polls on a fixed timer for
// capacity decisions. It receives capacity/idle/down events and drives a
// Cloud Resource Provider (CRP) via the crp.Provider interface.
//
// M1 scope: handle NeedCapacity with an in-flight guard and per-pool cooldown,
// and compute desired node counts. The CRP is a logging stub; no real cloud
// calls are made yet.
package cec

import (
	"log"
	"sync"
	"time"

	"github.com/xinlaoda/opentorque/internal/crp"
)

// Event kinds.
const (
	EventCapacity = "capacity" // NeedCapacity from scheduler
	EventNodeFree = "nodefree" // a node became free (start idle timer)
	EventNodeIdle = "nodeidle" // a free node has been idle >= cloud_idle_time
	EventNodeDown = "nodedown" // a node failed / disappeared
)

// Event is an event received by the CEC.
type Event struct {
	Kind      string
	Queue     string
	Provider  string
	SKU       string
	Jobs      []JobDemand // only for EventCapacity
	Shortfall Shortfall
	// Node-level events:
	PoolSize  int // current running node count for this pool (best effort)
	MinNodes  int
	MaxNodes  int
	IdleTime  time.Duration
	Reclaim   string
	// Full cloud definition (forwarded to the CRP).
	SubnetID string
	ImageID  string
	DiskSize int
	DiskType string
	SSHKey   string
	Location string
	RGName   string
}

// JobDemand is the resource demand of a single queued (cloud-bound) job.
type JobDemand struct {
	ID     string
	CPUs   int
	MemKB  int64
}

// Shortfall is the cumulative capacity shortfall reported by the scheduler.
type Shortfall struct {
	Cores   int // sum of cores that could not be placed this cycle
	Nodes   int // minimum additional nodes to clear the backlog
	Blocked int // count of jobs that could not start this cycle
}

// Pool tracks the state of one cloud-backed queue/pool.
type Pool struct {
	Queue   string
	Provider string
	SKU     string

	MinNodes int
	MaxNodes int
	IdleTime time.Duration
	Reclaim  string
	SubnetID string
	ImageID  string
	DiskSize int
	DiskType string
	SSHKey   string
	Location string
	RGName   string

	Running int               // VMs currently up/being tracked
	Inflight int              // VMs being provisioned (Ensured, not yet up)
	Provisioning map[string]string // vmID -> jobID bound during boot

	CooldownUntil time.Time
	LastScale     time.Time
	// node -> idleSince for scale-in timers (M3)
	IdleSince map[string]time.Time
}

// Controller is the Cloud Elastic Controller.
type Controller struct {
	mu       sync.Mutex
	provider crp.Provider
	pools    map[string]*Pool // by queue name
	Events   chan Event
	Cooldown time.Duration
}

// New creates a Controller wired to a single provider (M1: stub). For M2+
// multiple providers are supported via a registry; for now route by queue.
func New(provider crp.Provider) *Controller {
	return &Controller{
		provider: provider,
		pools:    make(map[string]*Pool),
		Events:   make(chan Event, 1024),
		Cooldown: 30 * time.Second,
	}
}

// PoolFor returns (creating if needed) the pool object for a queue based on an
// event's cloud config. It is called with the controller lock held.
func (c *Controller) ensurePool(ev Event) *Pool {
	p, ok := c.pools[ev.Queue]
	if !ok {
		p = &Pool{
			Queue:         ev.Queue,
			Provider:      ev.Provider,
			SKU:           ev.SKU,
			MinNodes:      ev.MinNodes,
			MaxNodes:      ev.MaxNodes,
			IdleTime:      ev.IdleTime,
			Reclaim:       ev.Reclaim,
			SubnetID:      ev.SubnetID,
			ImageID:       ev.ImageID,
			DiskSize:      ev.DiskSize,
			DiskType:      ev.DiskType,
			SSHKey:        ev.SSHKey,
			Location:      ev.Location,
			RGName:        ev.RGName,
			Provisioning:  make(map[string]string),
			IdleSince:     make(map[string]time.Time),
		}
		c.pools[ev.Queue] = p
	}
	if ev.Provider != "" {
		p.Provider = ev.Provider
		p.SKU = ev.SKU
	}
	if ev.MinNodes > 0 {
		p.MinNodes = ev.MinNodes
	}
	if ev.MaxNodes > 0 {
		p.MaxNodes = ev.MaxNodes
	}
	if ev.IdleTime > 0 {
		p.IdleTime = ev.IdleTime
	}
	if ev.Reclaim != "" {
		p.Reclaim = ev.Reclaim
	}
	return p
}

// Run is the CEC main event loop. It returns when stop is closed.
func (c *Controller) Run(stop <-chan struct{}) {
	log.Printf("[CEC] Cloud Elastic Controller started (provider=%s, cooldown=%s)", c.provider.Name(), c.Cooldown)
	for {
		select {
		case <-stop:
			log.Printf("[CEC] Stopping")
			return
		case ev := <-c.Events:
			c.handle(ev)
		}
	}
}

func (c *Controller) handle(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch ev.Kind {
	case EventCapacity:
		c.handleCapacity(ev)
	case EventNodeFree:
		c.handleNodeFree(ev)
	case EventNodeIdle:
		c.handleNodeIdle(ev)
	case EventNodeDown:
		c.handleNodeDown(ev)
	}
}

// desiredSize computes how many VMs the pool should target given the shortfall
// and in-flight/provisioning accounting, capped at MaxNodes.
func (p *Pool) desiredSize(ev Event) int {
	target := p.Running + ev.Shortfall.Nodes
	if p.MaxNodes > 0 && target > p.MaxNodes {
		target = p.MaxNodes
	}
	if target < p.MinNodes {
		target = p.MinNodes
	}
	return target
}

func (c *Controller) handleCapacity(ev Event) {
	p := c.ensurePool(ev)
	now := time.Now()
	if now.Before(p.CooldownUntil) {
		log.Printf("[CEC] queue=%s in cooldown until %s, skipping scale-out", ev.Queue, p.CooldownUntil.Format(time.RFC3339))
		return
	}
	target := p.desiredSize(ev)
	need := target - (p.Running + p.Inflight)
	if need <= 0 {
		log.Printf("[CEC] queue=%s shortfall=%d nodes but already at target=%d (running=%d inflight=%d), no-op",
			ev.Queue, ev.Shortfall.Nodes, target, p.Running, p.Inflight)
		return
	}

	// Cooldown applied around each scale-out to avoid flapping.
	p.LastScale = now
	p.CooldownUntil = now.Add(c.Cooldown)

	log.Printf("[CEC] queue=%s scaling OUT by %d (target=%d running=%d inflight=%d) cores_shortfall=%d blocked=%d",
		ev.Queue, need, target, p.Running, p.Inflight, ev.Shortfall.Cores, ev.Shortfall.Blocked)

	vms, err := c.provider.Ensure(crp.EnsureRequest{
		Provider:      ev.Provider,
		SKU:           ev.SKU,
		Count:         need,
		SubnetID:      p.SubnetID,
		ImageID:       p.ImageID,
		DiskSize:      p.DiskSize,
		DiskType:      p.DiskType,
		SSHKey:        p.SSHKey,
		Location:      p.Location,
		ResourceGroup: p.RGName,
		MinNodes:      p.MinNodes,
		MaxNodes:      p.MaxNodes,
	})
	if err != nil {
		log.Printf("[CEC] queue=%s provider Ensure error: %v", ev.Queue, err)
		return
	}
	// Bind these new VMs to the blocked jobs (M2 will pick per-job cores).
	for i, vm := range vms {
		var jobID string
		if i < len(ev.Jobs) {
			jobID = ev.Jobs[i].ID
		}
		p.Provisioning[vm.ID] = jobID
		log.Printf("[CEC] queue=%s bound vm=%s -> job=%s (sku=%s)", ev.Queue, vm.ID, jobIDOr(jobID, "(unbound)"), ev.SKU)
	}
	p.Inflight += len(vms)
	log.Printf("[CEC] queue=%s provisioned %d VM(s), inflight=%d", ev.Queue, len(vms), p.Inflight)
}

func (c *Controller) handleNodeFree(ev Event) {
	p := c.ensurePool(ev)
	if p.IdleTime <= 0 {
		return
	}
	// Start/refresh idle timer for scale-in (resolved in M3).
	if _, ok := p.IdleSince[ev.Queue]; !ok {
		p.IdleSince[ev.Queue] = time.Now()
	}
}

func (c *Controller) handleNodeIdle(ev Event) {
	p := c.ensurePool(ev)
	// M3: drain + deregister + reclaim. M1 logs only.
	log.Printf("[CEC] queue=%s node idle detected (scale-in hook; M3)", ev.Queue)
	delete(p.IdleSince, ev.Queue)
	if p.Running > p.MinNodes {
		p.Running--
	}
}

func (c *Controller) handleNodeDown(ev Event) {
	p := c.ensurePool(ev)
	if p.Running > 0 {
		p.Running--
	}
	log.Printf("[CEC] queue=%s node down; running=%d min=%d", ev.Queue, p.Running, p.MinNodes)
	if p.Running < p.MinNodes {
		log.Printf("[CEC] queue=%s below min, will restore floor on next capacity event", ev.Queue)
	}
}

// RegisterNodeUp is called by the server/watcher when a newly provisioned VM's
// MOM has registered and the node is free. It decrements inflight and later
// drives the PROVISIONING -> R transition.
func (c *Controller) RegisterNodeUp(queue, vmID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pools[queue]
	if !ok {
		return
	}
	if p.Inflight > 0 {
		p.Inflight--
	}
	p.Running++
	delete(p.Provisioning, vmID)
	log.Printf("[CEC] queue=%s node up (vm=%s), running=%d inflight=%d", queue, vmID, p.Running, p.Inflight)
}

func jobIDOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
