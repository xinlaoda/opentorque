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
	PoolSize         int // current running node count for this pool (best effort)
	MinNodes         int
	MaxNodes         int
	IdleTime         time.Duration
	ProvisionTimeout time.Duration // seconds a provisioned-but-not-booted VM may wait before reclaim (0 = default)
	Reclaim          string
	// Full cloud definition (forwarded to the CRP).
	SubnetID   string
	ImageID    string
	DiskSize   int
	DiskType   string
	SSHKey     string
	Location   string
	RGName     string
	ServerAddr string // pbs_server endpoint for cloud-init (ip:port)

	// M4 elasticity tuning knobs (per-pool).
	Cooldown      time.Duration // seconds between scale-outs (0 = global default)
	ScaleHeadroom int           // extra VMs beyond exact shortfall (burst cushion)
	DrainTimeout  time.Duration // min interval / give-up window between reclaim attempts
}

// JobDemand is the resource demand of a single queued (cloud-bound) job.
type JobDemand struct {
	ID    string
	CPUs  int
	MemKB int64
}

// Shortfall is the cumulative capacity shortfall reported by the scheduler.
type Shortfall struct {
	Cores   int // sum of cores that could not be placed this cycle
	Nodes   int // minimum additional nodes to clear the backlog
	Blocked int // count of jobs that could not start this cycle
}

// Pool tracks the state of one cloud-backed queue/pool.
type Pool struct {
	Queue    string
	Provider string
	SKU      string

	MinNodes   int
	MaxNodes   int
	IdleTime   time.Duration
	Reclaim    string
	SubnetID   string
	ImageID    string
	DiskSize   int
	DiskType   string
	SSHKey     string
	Location   string
	RGName     string
	ServerAddr string

	Running          int                  // VMs currently up/being tracked
	Inflight         int                  // VMs being provisioned (Ensured, not yet up)
	Provisioning     map[string]string    // vmID -> jobID bound during boot
	ProvisionTimeout time.Duration        // how long a still-booting VM may wait before reclaim (0 = default 10m)
	ProvisionedAt    map[string]time.Time // vmID -> when it entered Provisioning (for timeout sweep)

	// Owned tracks dynamic nodes this pool created and currently manages
	// (keyed by node name == VM name/id). A node stays Owned from the moment
	// it is created until it is reclaimed. Used to determine scale-in
	// candidates independent of the transient provisioning map.
	Owned map[string]bool
	// IdleSince holds name -> time the node last became idle (no running
	// jobs). A node remains an idle candidate only while it is in both Owned
	// and IdleSince for >= IdleTime.
	IdleSince map[string]time.Time

	// Hibernated tracks deallocated/hibernated VMs kept for fast resume
	// (vmID -> when suspended). The VM is retained (not destroyed) so the next
	// scale-out can Resume it quickly instead of re-provisioning + cloud-init.
	Hibernated map[string]time.Time

	CooldownUntil time.Time
	LastScale     time.Time

	// M4 tuning applied to this pool.
	Cooldown     time.Duration        // per-pool scale-out cooldown (0 = global default)
	Headroom     int                  // extra VMs beyond exact shortfall (burst cushion)
	DrainTimeout time.Duration        // min interval between reclaim attempts for the same node
	LastReclaim  map[string]time.Time // node -> last reclaim attempt (drain rate-limit)
}

// Controller is the Cloud Elastic Controller.
// NodeController is how the CEC drains and deregisters a node from the PBS
// server. It is injected by the caller (pbs_sched) and keeps the CEC decoupled
// from the wire client so the scale-in logic stays unit-testable.
type NodeController interface {
	// DrainNode marks a node offline so the scheduler stops dispatching jobs
	// to it during scale-in.
	DrainNode(name string) error
	// DeregisterNode removes the node from the server's node database.
	DeregisterNode(name string) error
}

type Controller struct {
	mu       sync.Mutex
	provider crp.Provider
	pools    map[string]*Pool // by queue name
	Events   chan Event
	Cooldown time.Duration
	nodes    NodeController
	// reclaimInterval is how often the CEC checks per-node idle timers for
	// scale-in. It is the only regular timer in the controller; capacity
	// decisions remain event-driven.
	reclaimInterval time.Duration
}

// New creates a Controller wired to a single provider (M1: stub). For M2+
// multiple providers are supported via a registry; for now route by queue.
func New(provider crp.Provider) *Controller {
	return &Controller{
		provider:        provider,
		pools:           make(map[string]*Pool),
		Events:          make(chan Event, 1024),
		Cooldown:        30 * time.Second,
		reclaimInterval: 3 * time.Second,
	}
}

// SetNodeController injects the node drain/deregister adapter. Call it before
// Run. If never set, scale-in stalemates (logs) but does not crash.
func (c *Controller) SetNodeController(nc NodeController) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes = nc
}

// SetReclaimInterval overrides the idle-check cadence (mainly for tests).
func (c *Controller) SetReclaimInterval(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reclaimInterval = d
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
			ServerAddr:    ev.ServerAddr,
			Provisioning:  make(map[string]string),
			ProvisionedAt: make(map[string]time.Time),
			Owned:         make(map[string]bool),
			IdleSince:     make(map[string]time.Time),
			LastReclaim:   make(map[string]time.Time),
			Hibernated:    make(map[string]time.Time),
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
	if ev.ProvisionTimeout > 0 {
		p.ProvisionTimeout = ev.ProvisionTimeout
	}
	if ev.Reclaim != "" {
		p.Reclaim = ev.Reclaim
	}
	if ev.Cooldown > 0 {
		p.Cooldown = ev.Cooldown
	}
	if ev.ScaleHeadroom > 0 {
		p.Headroom = ev.ScaleHeadroom
	}
	if ev.DrainTimeout > 0 {
		p.DrainTimeout = ev.DrainTimeout
	}
	return p
}

// Run is the CEC main event loop. It returns when stop is closed.
func (c *Controller) Run(stop <-chan struct{}) {
	log.Printf("[CEC] Cloud Elastic Controller started (provider=%s, cooldown=%s)", c.provider.Name(), c.Cooldown)
	ticker := time.NewTicker(c.reclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			log.Printf("[CEC] Stopping")
			return
		case ev := <-c.Events:
			c.handle(ev)
		case <-ticker.C:
			c.reclaimIdle()
		}
	}
}

// reclaimIdle sweeps every pool and reclaims (drain + deregister + CRP
// Reclaim) any owned node that has been idle for >= IdleTime, as long as the
// pool can still shrink (Running > MinNodes). This is the only timer in the
// CEC; it only advances pre-existing idle windows, it never triggers a
// capacity (scale-out) decision on its own.
func (c *Controller) reclaimIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for qname, p := range c.pools {
		c.sweepProvisioningTimeoutLocked(qname, p, now)
		if p.IdleTime <= 0 {
			continue
		}
		if p.Running <= p.MinNodes {
			continue
		}
		var candidates []string
		for name, idleSince := range p.IdleSince {
			if now.Sub(idleSince) < p.IdleTime {
				continue
			}
			candidates = append(candidates, name)
		}
		for _, name := range candidates {
			// Drain-timeout policy (M4.4): respect a minimum interval between
			// reclaim attempts for the same node so a node whose reclaim or
			// drain did not settle is not hammered on every sweep.
			if p.DrainTimeout > 0 {
				if last, ok := p.LastReclaim[name]; ok && now.Sub(last) < p.DrainTimeout {
					log.Printf("[CEC] queue=%s node %s still in drain cooldown; deferring reclaim", qname, name)
					continue
				}
			}
			c.reclaimNodeLocked(qname, p, name, now)
		}
	}
}

// coalesceCapacityEvents drains any additional EventCapacity entries already
// queued for the same pool and merges their shortfall into ev, so repeated
// capacity events across adjacent scheduling cycles become one scale-out. It
// combines metrics by taking the max per field and unioning the bound jobs.
// Called with the controller lock held; only the Run goroutine reads Events,
// so draining here is race-free. Non-coalescible events (different kind/queue)
// are logged and dropped -- they are re-emitted by the next scheduler cycle.
func (c *Controller) coalesceCapacityEvents(p *Pool, ev *Event) {
	for {
		select {
		case e2 := <-c.Events:
			if e2.Kind != EventCapacity || e2.Queue != p.Queue {
				log.Printf("[CEC] queue=%s dropped %s event during coalesce (re-emitted next cycle)", p.Queue, e2.Kind)
				continue
			}
			if e2.Shortfall.Nodes > ev.Shortfall.Nodes {
				ev.Shortfall.Nodes = e2.Shortfall.Nodes
			}
			if e2.Shortfall.Cores > ev.Shortfall.Cores {
				ev.Shortfall.Cores = e2.Shortfall.Cores
			}
			if e2.Shortfall.Blocked > ev.Shortfall.Blocked {
				ev.Shortfall.Blocked = e2.Shortfall.Blocked
			}
			seen := make(map[string]bool)
			for _, j := range ev.Jobs {
				seen[j.ID] = true
			}
			for _, j := range e2.Jobs {
				if !seen[j.ID] {
					seen[j.ID] = true
					ev.Jobs = append(ev.Jobs, j)
				}
			}
			log.Printf("[CEC] queue=%s coalesced capacity event (nodes=%d cores=%d blocked=%d)",
				p.Queue, ev.Shortfall.Nodes, ev.Shortfall.Cores, ev.Shortfall.Blocked)
		default:
			return
		}
	}
}

// reclaimNodeLocked drains, deregisters, and cloud-reclaims one idle dynamic
// node. Called with the controller lock held.
func (c *Controller) reclaimNodeLocked(qname string, p *Pool, name string, now time.Time) {
	if p.LastReclaim != nil {
		p.LastReclaim[name] = now
	}
	nodes := c.nodes
	if nodes == nil {
		log.Printf("[CEC] queue=%s want to reclaim idle node %s but no NodeController set; skipping", qname, name)
		delete(p.IdleSince, name)
		return
	}
	policy := p.Reclaim
	if policy == "" {
		policy = "deallocate"
	}
	log.Printf("[CEC] queue=%s reclaiming idle node %s (policy=%s, running=%d min=%d)", qname, name, policy, p.Running, p.MinNodes)

	// 1. Drain: mark offline so the scheduler stops dispatching to this node.
	if err := nodes.DrainNode(name); err != nil {
		log.Printf("[CEC] queue=%s drain node %s failed: %v; aborting reclaim", qname, name, err)
		return
	}
	// 2. Deregister: remove the node from the server database.
	if err := nodes.DeregisterNode(name); err != nil {
		log.Printf("[CEC] queue=%s deregister node %s failed: %v; keeping node", qname, name, err)
		return
	}
	// 3. Stop / deallocate / destroy the backing VM.
	destroy := false
	if policy == "destroy" {
		destroy = true
	}
	if err := c.provider.Reclaim(crp.VMRef{Provider: p.Provider, VMID: name}, policy, destroy); err != nil {
		log.Printf("[CEC] queue=%s provider reclaim of %s failed: %v (node already deregistered)", qname, name, err)
	}

	delete(p.Owned, name)
	delete(p.IdleSince, name)
	if p.LastReclaim != nil {
		delete(p.LastReclaim, name)
	}
	if p.Running > 0 {
		p.Running--
	}
	if policy == "hibernate" && !destroy {
		p.Hibernated[name] = now
		log.Printf("[CEC] queue=%s hibernated node %s for fast resume (running=%d)", qname, name, p.Running)
	}
	log.Printf("[CEC] queue=%s reclaimed node %s, running=%d", qname, name, p.Running)
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
	target := p.Running + ev.Shortfall.Nodes + p.Headroom
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
	// Coalesce any NeedCapacity events already queued for this pool so bursts
	// across adjacent cycles are handled in a single scale-out (M4.3).
	c.coalesceCapacityEvents(p, &ev)
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

	// Cooldown applied around each scale-out to avoid flapping. Per-pool
	// cloud_cooldown overrides the controller-wide default (M4.1).
	cd := p.Cooldown
	if cd <= 0 {
		cd = c.Cooldown
	}
	p.LastScale = now
	p.CooldownUntil = now.Add(cd)

	// Hibernate fast-resume: restore previously hibernated VMs in this pool
	// before provisioning brand-new ones (avoids re-provision + cloud-init
	// bootstrap; the VM and its OS disk are retained). Resumed VMs re-enter
	// Provisioning and are consumed by RegisterNodesUp when the MOM re-registers.
	resumed := 0
	for resumed < need && len(p.Hibernated) > 0 {
		var id string
		var suspendAt time.Time
		for vmID, t := range p.Hibernated {
			if id == "" || t.Before(suspendAt) {
				id, suspendAt = vmID, t
			}
		}
		delete(p.Hibernated, id)
		var jobID string
		if resumed < len(ev.Jobs) {
			jobID = ev.Jobs[resumed].ID
		}
		p.Provisioning[id] = jobID
		p.ProvisionedAt[id] = now
		p.Inflight++
		if err := c.provider.Resume(crp.VMRef{Provider: p.Provider, VMID: id}); err != nil {
			log.Printf("[CEC] queue=%s resume hibernated vm=%s failed: %v", ev.Queue, id, err)
		} else {
			log.Printf("[CEC] queue=%s resuming hibernated vm=%s -> job=%s (fast path)", ev.Queue, id, jobIDOr(jobID, "(unbound)"))
		}
		resumed++
	}
	need -= resumed

	if need <= 0 {
		log.Printf("[CEC] queue=%s shortfall met by resuming %d hibernated VM(s), inflight=%d", ev.Queue, resumed, p.Inflight)
		return
	}

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
		ServerAddr:    p.ServerAddr,
		Hibernate:     p.Reclaim == "hibernate",
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
		p.ProvisionedAt[vm.ID] = time.Now()
		log.Printf("[CEC] queue=%s bound vm=%s -> job=%s (sku=%s)", ev.Queue, vm.ID, jobIDOr(jobID, "(unbound)"), ev.SKU)
	}
	p.Inflight += len(vms)
	log.Printf("[CEC] queue=%s provisioned %d VM(s), inflight=%d", ev.Queue, len(vms), p.Inflight)
}

func (c *Controller) handleNodeFree(ev Event) {
	_ = c.ensurePool(ev)
	log.Printf("[CEC] queue=%s node-free event (idle tracking is driven by RegisterNodesIdle)", ev.Queue)
}

// handleNodeIdle is driven by the reclaim sweep (reclaimIdle). It is retained
// for the event taxonomy but does not itself act; the reclaim path lives in
// reclaimNodeLocked so that draining/deregistering is done exactly once.
func (c *Controller) handleNodeIdle(ev Event) {
	_ = c.ensurePool(ev)
	log.Printf("[CEC] queue=%s node-idle event (handled by reclaim sweep)", ev.Queue)
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
// drives the PROVISIONING -> R transition. It is idempotent: a node that is
// no longer tracked in Provisioning is ignored so repeated calls are safe.
func (c *Controller) RegisterNodeUp(queue, vmID string) {
	c.RegisterNodesUp(queue, []string{vmID})
}

// RegisterNodesUp records that the given nodes (which correspond to VMs this
// pool is still provisioning) have now booted and registered with the server.
// Any node not currently in the pool's Provisioning map is ignored, so this is
// safe to call every cycle with the full list of free nodes. It bumps Running
// and decrements Inflight for each consumed node.
func (c *Controller) RegisterNodesUp(queue string, nodes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pools[queue]
	if !ok {
		return
	}
	for _, n := range nodes {
		if _, isProv := p.Provisioning[n]; !isProv {
			continue
		}
		delete(p.Provisioning, n)
		if p.Inflight > 0 {
			p.Inflight--
		}
		p.Owned[n] = true
		p.Running++
		log.Printf("[CEC] queue=%s node up (vm=%s), running=%d inflight=%d", queue, n, p.Running, p.Inflight)
	}
}

// RegisterNodesIdle records, for each known cloud queue, the current set of
// nodes that are idle (no running jobs, free capacity). For every owned node
// the CEC starts/refreshes an idle timer when it is in the reported set and
// clears the timer when it leaves it (i.e. it became busy). This drives the
// scale-in idle window. Static (non-owned) nodes are ignored.
func (c *Controller) RegisterNodesIdle(queue string, idleNodes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pools[queue]
	if !ok {
		return
	}
	inIdle := make(map[string]bool, len(idleNodes))
	for _, n := range idleNodes {
		inIdle[n] = true
	}
	// Seed idle timers / mark which owned nodes are currently idle.
	now := time.Now()
	for name := range p.Owned {
		if inIdle[name] {
			if _, ok := p.IdleSince[name]; !ok {
				p.IdleSince[name] = now
				log.Printf("[CEC] queue=%s node %s idle timer started", queue, name)
			}
		} else {
			if _, wasIdle := p.IdleSince[name]; wasIdle {
				delete(p.IdleSince, name)
				log.Printf("[CEC] queue=%s node %s busy; idle timer cleared", queue, name)
			}
		}
	}
}

// SyncQueuedJobs reconciles the jobs still present in a cloud queue (queued
// or running) against the VMs currently being provisioned. A provisioning VM
// is destroyed only when its bound job ID no longer exists at all (qdel); a
// job that merely started running -- including on this very VM while it was
// still booting -- must not be torn down. Safe to call every cycle with the
// current live job set. Unbound provisioning VMs (no jobID) are left alone
// here; they are handled by the provisioning-timeout sweep.
func (c *Controller) SyncQueuedJobs(queue string, jobIDs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pools[queue]
	if !ok {
		return
	}
	alive := make(map[string]bool, len(jobIDs))
	for _, id := range jobIDs {
		alive[id] = true
	}
	var stale []string
	for vmID, jobID := range p.Provisioning {
		if jobID == "" {
			continue // unbound; timeout sweep handles it
		}
		if !alive[jobID] {
			stale = append(stale, vmID)
		}
	}
	for _, vmID := range stale {
		log.Printf("[CEC] queue=%s provisioning vm=%s bound to job no longer queued (qdel/started elsewhere); destroying orphaned VM", queue, vmID)
		if err := c.provider.Reclaim(crp.VMRef{Provider: p.Provider, VMID: vmID}, "deallocate", true); err != nil {
			log.Printf("[CEC] queue=%s destroy orphaned provisioning vm=%s failed: %v", queue, vmID, err)
			continue
		}
		delete(p.Provisioning, vmID)
		delete(p.ProvisionedAt, vmID)
		if p.Inflight > 0 {
			p.Inflight--
		}
		log.Printf("[CEC] queue=%s destroyed orphaned provisioning vm=%s, inflight=%d", queue, vmID, p.Inflight)
	}
}

// sweepProvisioningTimeoutLocked reclaims provisioning VMs that have been
// booting for longer than the pool's ProvisionTimeout (default 10m if 0). It
// runs on the reclaim ticker and only touches VMs still in Provisioning. Called
// with the controller lock held.
func (c *Controller) sweepProvisioningTimeoutLocked(qname string, p *Pool, now time.Time) {
	if len(p.ProvisionedAt) == 0 {
		return
	}
	timeout := p.ProvisionTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	var stale []string
	for vmID, since := range p.ProvisionedAt {
		if now.Sub(since) >= timeout {
			stale = append(stale, vmID)
		}
	}
	for _, vmID := range stale {
		log.Printf("[CEC] queue=%s provisioning vm=%s exceeded provisioning timeout (%s); destroying", qname, vmID, timeout)
		if err := c.provider.Reclaim(crp.VMRef{Provider: p.Provider, VMID: vmID}, "deallocate", true); err != nil {
			log.Printf("[CEC] queue=%s destroy timed-out provisioning vm=%s failed: %v", qname, vmID, err)
			continue
		}
		delete(p.Provisioning, vmID)
		delete(p.ProvisionedAt, vmID)
		if p.Inflight > 0 {
			p.Inflight--
		}
		log.Printf("[CEC] queue=%s destroyed timed-out provisioning vm=%s, inflight=%d", qname, vmID, p.Inflight)
	}
}

func jobIDOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
