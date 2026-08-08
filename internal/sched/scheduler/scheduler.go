// Package scheduler implements PBS job scheduling algorithms.
// It supports FIFO, round-robin, fair-share, priority-based sorting,
// and starvation prevention.
package scheduler

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xinlaoda/opentorque/internal/sched/client"
	"github.com/xinlaoda/opentorque/internal/sched/config"
)

// JobInfo holds scheduling-relevant attributes for a single job.
type JobInfo struct {
	ID        string
	Name      string
	Owner     string
	State     string
	Queue     string
	Priority  int
	QueueTime time.Time
	Walltime  time.Duration
	MemReq    int64    // requested memory in KB
	CPUReq    int      // requested CPUs
	Host      string   // -l host=<node> pinning (empty = anywhere)
	HostGroup string   // -l host=@<group> pin to a host group / node pool (1.3)
	Features  []string // -l feature=<list> required node properties
	Nodes     int      // requested node count from -l nodes=/select= (1 for single) (1.4)
	PPN       int      // requested processors per node (1.4, default 1)

	// Scheduling state
	CanNotRun     bool
	CanNeverRun   bool
	StarvingSince time.Time
}

// NodeInfo holds scheduling-relevant attributes for a compute node.
type NodeInfo struct {
	Name       string
	State      string
	NumProcs   int
	FreeCPUs   int
	TotalMem   int64 // KB
	AvailMem   int64 // KB
	LoadAvg    float64
	Jobs       []string
	UsedCPUs   int      // CPUs currently consumed by running jobs (from node used_cpus)
	Properties []string // node properties/features used for feature matching
	Groups     []string // named host groups / node pools this node belongs to (1.3)
	Dynamic    bool     // auto-registered cloud/dynamic node (prefer local statics)
}

// QueueInfo holds scheduling-relevant attributes for a queue.
type QueueInfo struct {
	Name       string
	Type       string
	Enabled    bool
	Started    bool
	Priority   int
	MaxRunning int
	Running    int
	Queued     int
	Jobs       []*JobInfo
	// NaccessPolicy: "shared" (default) packs; "exclusive"/"singleuser" allow
	// one job per node (1.5). HostList restricts schedulable nodes (1.6).
	NaccessPolicy string
	HostList       []string

	// Cloud elasticity (cloud-backed queues). When CloudBacked is true the
	// queue's jobs may be scaled out onto dynamically provisioned VMs.
	CloudBacked           bool
	CloudProvider         string
	CloudSKU              string
	CloudMinNodes         int
	CloudMaxNodes         int
	CloudIdleTime         int // seconds a free node waits before scale-in
	CloudProvisionTimeout int // seconds a provisioned-but-not-booted VM may wait before reclaim (0 = default)
	CloudReclaim          string
	CloudSubnetID         string
	CloudImageID          string
	CloudDiskSize         int
	CloudDiskType         string
	CloudSSHKey           string
	CloudLocation         string
	CloudRGName           string
	CloudCooldown         int // seconds between scale-out actions (0 = global default)
	CloudScaleHeadroom    int // extra VMs to provision beyond exact shortfall
	CloudDrainTimeout     int // seconds a reclaim may spend draining before giving up (0 = default)
}

// CapacityEvent is an event emitted by a scheduling cycle when a cloud-backed
// queue has more queued demand than the current static pool can satisfy. The
// scheduler accumulates demand across multiple queued jobs (lookahead) so the
// Cloud Elastic Controller (CEC) can provision the right number of VMs in one
// pass instead of one per cycle.
type CapacityEvent struct {
	Queue    string
	Provider string
	SKU      string
	// Jobs is the list of queued job IDs that contributed to this demand.
	Jobs []string
	// Shortfall summarizes how much capacity is missing.
	Cores            int
	Nodes            int
	Blocked          int
	MinNodes         int
	MaxNodes         int
	IdleTime         int
	ProvisionTimeout int
	Reclaim          string
	// Full cloud definition (passed through to the CEC/CRP).
	SubnetID      string
	ImageID       string
	DiskSize      int
	DiskType      string
	SSHKey        string
	Location      string
	RGName        string
	Cooldown      int // seconds between scale-outs for this pool (0 = global default)
	ScaleHeadroom int // extra VMs to provision beyond exact shortfall
	DrainTimeout  int // seconds a reclaim may spend draining before giving up (0 = default)
}

// CycleResult carries the outcome of a scheduling cycle: how many jobs were
// dispatched and any capacity events to forward to the CEC.
type CycleResult struct {
	Dispatched     int
	CapacityEvents []CapacityEvent
	FreeNodes      []string            // names of nodes with available capacity after this cycle
	IdleNodes      []string            // names of nodes with no running jobs and free capacity (scale-in candidates)
	QueuedByQueue  map[string][]string // per-cloud-queue, job IDs still queued after this cycle (catch qdel-during-provisioning)
	// AliveJobs is the set of job IDs that still exist in the server after this
	// cycle (queued + running). The CEC uses it to avoid tearing down a
	// provisioning VM whose bound job merely started running.
	AliveJobs []string
}

// ServerInfo holds the complete server state snapshot for one scheduling cycle.
type ServerInfo struct {
	Name       string
	Queues     []*QueueInfo
	Nodes      []*NodeInfo
	TotalJobs  int
	MaxRunning int
}

// Scheduler orchestrates job scheduling decisions.
type Scheduler struct {
	cfg       *config.Config
	fairUsage map[string]float64 // user -> accumulated usage for fair-share
}

// New creates a new Scheduler with the given configuration.
func New(cfg *config.Config) *Scheduler {
	return &Scheduler{
		cfg:       cfg,
		fairUsage: make(map[string]float64),
	}
}

// RunCycle performs one complete scheduling cycle (unlimited):
// 1. Query server for current state
// 2. Build scheduling data structures
// 3. Apply sorting and algorithm
// 4. Dispatch jobs to nodes
// The returned CycleResult includes any cloud capacity events.
func (s *Scheduler) RunCycle(conn *client.Conn) (*CycleResult, error) {
	return s.runCycle(conn, false)
}

// RunCycleLimited performs a bounded scheduling cycle, used for event-triggered
// runs from the pbs_server. It attempts at most DefaultQueueDepth jobs, starts
// at most SchedMaxJobStart jobs, and yields after MaxSchedTime seconds. This
// keeps a flurry of job-submit events from monopolizing the scheduler; the
// periodic full cycle cleans up whatever a limited cycle leaves behind.
func (s *Scheduler) RunCycleLimited(conn *client.Conn) (*CycleResult, error) {
	return s.runCycle(conn, true)
}

// runCycle is the shared scheduling engine. limited=true applies the queue
// depth / max-start / max-time caps described on RunCycleLimited.
//
// It returns a CycleResult carrying both the number of dispatched jobs and any
// cloud capacity events computed during the cycle. A capacity event is emitted
// for a cloud-backed queue whenever the cycle encounters a job that cannot be
// placed on any current node AND that queue may scale out (cloud_backed). The
// demand is accumulated across subsequent jobs (lookahead), so the CEC can
// provision multiple VMs in one pass.
func (s *Scheduler) runCycle(conn *client.Conn, limited bool) (*CycleResult, error) {
	res := &CycleResult{}

	// Query current state from server
	sinfo, err := s.queryServer(conn)
	if err != nil {
		return res, fmt.Errorf("query server: %w", err)
	}

	// Initialize cycle: sort jobs, detect starvation, decay fair-share
	s.initCycle(sinfo)

	dispatched := 0

	// Event-triggered (limited) cycle caps
	maxAttempt := 0
	maxStarts := 0
	if limited {
		maxAttempt = s.cfg.DefaultQueueDepth
		maxStarts = s.cfg.SchedMaxJobStart
	}
	cycleStart := time.Now()

	// pendingCap tracks lookahead demand for cloud-backed jobs that could not
	// be placed this cycle. Keyed by queue name. It is filled as the iterator
	// walks jobs: when a cloud-backed queue's job blocks, we record it and keep
	// going (instead of breaking in strict FIFO) so the CEC can provision the
	// right number of VMs up front.
	pendingCap := make(map[string]*CapacityEvent)

	// Main scheduling loop: get next job, check resources, dispatch
	jobIter := s.newJobIterator(sinfo)
	for attempt := 0; ; attempt++ {
		if limited && maxAttempt > 0 && attempt >= maxAttempt {
			log.Printf("[SCHED] Limited cycle: reached default_queue_depth=%d, stopping", maxAttempt)
			break
		}
		if limited && s.cfg.MaxSchedTime > 0 && time.Since(cycleStart) >= time.Duration(s.cfg.MaxSchedTime)*time.Second {
			log.Printf("[SCHED] Limited cycle: max_sched_time=%ds reached, yielding", s.cfg.MaxSchedTime)
			break
		}
		jinfo := jobIter.next()
		if jinfo == nil {
			break
		}

		// Find a suitable node for this job
		node := s.findNodeForJob(sinfo, jinfo)
		if node == nil {
			jinfo.CanNotRun = true
			// Lookahead for cloud-backed queues: accumulate demand instead of
			// breaking in strict FIFO mode, so the CEC can scale out once.
			if q := queueForJob(sinfo, jinfo.Queue); q != nil && q.CloudBacked {
				ev, ok := pendingCap[q.Name]
				if !ok {
					ev = &CapacityEvent{
						Queue:            q.Name,
						Provider:         q.CloudProvider,
						SKU:              q.CloudSKU,
						MinNodes:         q.CloudMinNodes,
						MaxNodes:         q.CloudMaxNodes,
						IdleTime:         q.CloudIdleTime,
						ProvisionTimeout: q.CloudProvisionTimeout,
						Reclaim:          q.CloudReclaim,
						SubnetID:         q.CloudSubnetID,
						ImageID:          q.CloudImageID,
						DiskSize:         q.CloudDiskSize,
						DiskType:         q.CloudDiskType,
						SSHKey:           q.CloudSSHKey,
						Location:         q.CloudLocation,
						RGName:           q.CloudRGName,
						Cooldown:         q.CloudCooldown,
						ScaleHeadroom:    q.CloudScaleHeadroom,
						DrainTimeout:     q.CloudDrainTimeout,
					}
					pendingCap[q.Name] = ev
				}
				cpu := jinfo.CPUReq
				if cpu == 0 {
					cpu = 1
				}
				ev.Cores += cpu
				ev.Blocked++
				ev.Jobs = append(ev.Jobs, jinfo.ID)
				log.Printf("[SCHED] Cloud queue %s: job %s needs %d cores, no node available (blocked=%d)", q.Name, jinfo.ID, cpu, ev.Blocked)
				continue // do NOT break; keep looking across remaining jobs
			}
			// Non-cloud strict FIFO: stop after first blocked job.
			if s.cfg.StrictFIFO {
				log.Printf("[SCHED] Strict FIFO: job %s blocked, stopping cycle", jinfo.ID)
				break
			}
			continue
		}

		// Dispatch job to the selected node
		dest := fmt.Sprintf("%s/0", node.Name)
		if err := conn.RunJob(jinfo.ID, dest); err != nil {
			log.Printf("[SCHED] Failed to dispatch %s to %s: %v", jinfo.ID, dest, err)
			continue
		}

		// Update local state to reflect the dispatch
		node.FreeCPUs -= jinfo.CPUReq
		if jinfo.CPUReq == 0 {
			node.FreeCPUs--
		}
		node.Jobs = append(node.Jobs, jinfo.ID)
		dispatched++
		log.Printf("[SCHED] Dispatched %s to %s", jinfo.ID, node.Name)
		if limited && maxStarts > 0 && dispatched >= maxStarts {
			log.Printf("[SCHED] Limited cycle: reached sched_max_job_start=%d, stopping", maxStarts)
			break
		}

		// Update fair-share usage tracking
		if s.cfg.FairShare {
			s.fairUsage[jinfo.Owner] += 1.0
		}
	}

	// Convert pendingCap map into a slice, computing the node shortfall from
	// the accumulated core demand (each VM is assumed to provide the SKU's
	// core count; the CEC refines this in M2+).
	for _, ev := range pendingCap {
		coresPerNode := 1
		if ev.SKU != "" {
			// Heuristic: assume at least 2 cores/SKU unless set. The CEC will
			// query the cloud for the real core count; this is a floor.
			coresPerNode = 2
		}
		if ev.Cores > 0 {
			ev.Nodes = (ev.Cores + coresPerNode - 1) / coresPerNode // ceil
		}
		// Cap by MaxNodes (relative to current static pool size is done in CEC).
		if ev.MaxNodes > 0 && ev.Nodes >= ev.MaxNodes {
			ev.Nodes = ev.MaxNodes
		}
		if ev.Nodes < 1 {
			ev.Nodes = 1
		}
		log.Printf("[SCHED] Cloud queue %s: capacity event cores=%d nodes=%d blocked=%d", ev.Queue, ev.Cores, ev.Nodes, ev.Blocked)
		res.CapacityEvents = append(res.CapacityEvents, *ev)
	}

	res.Dispatched = dispatched

	// Capture which jobs remain queued per cloud-backed queue after this cycle.
	// Only state "Q" jobs are present in q.Jobs; if a VM is being provisioned for
	// a bound job ID that no longer appears here, the job was deleted (qdel) or
	// started elsewhere, so the CEC can release that still-booting VM.
	res.QueuedByQueue = make(map[string][]string)
	for _, q := range sinfo.Queues {
		if !q.CloudBacked {
			continue
		}
		for _, j := range q.Jobs {
			res.QueuedByQueue[q.Name] = append(res.QueuedByQueue[q.Name], j.ID)
		}
	}

	// Live job IDs (queued or running) so the CEC only releases a provisioning
	// VM when its bound job has actually been deleted, not merely dispatched.
	alive := make(map[string]bool)
	for _, q := range sinfo.Queues {
		for _, j := range q.Jobs {
			alive[j.ID] = true
		}
	}
	for _, n := range sinfo.Nodes {
		for _, jid := range n.Jobs {
			alive[jid] = true
		}
	}
	for id := range alive {
		res.AliveJobs = append(res.AliveJobs, id)
	}

	// Record nodes with available capacity so the CEC can tell when a
	// previously-provisioned VM's node has registered (cloud elasticity).
	for _, n := range sinfo.Nodes {
		if n.FreeCPUs > 0 {
			res.FreeNodes = append(res.FreeNodes, n.Name)
		}
		// A node is a scale-in (idle) candidate when it carries no running
		// jobs and has free capacity. Nodes that are down/offline already or
		// that carry jobs are excluded.
		if len(n.Jobs) == 0 && n.FreeCPUs > 0 {
			res.IdleNodes = append(res.IdleNodes, n.Name)
		}
	}

	return res, nil
}

// queueForJob returns the QueueInfo matching a job's queue name.
func queueForJob(sinfo *ServerInfo, name string) *QueueInfo {
	for _, q := range sinfo.Queues {
		if q.Name == name {
			return q
		}
	}
	return nil
}

// queryServer fetches all jobs, queues, and nodes from the server.
func (s *Scheduler) queryServer(conn *client.Conn) (*ServerInfo, error) {
	// Query queues
	queueObjs, err := conn.StatusQueue("")
	if err != nil {
		return nil, fmt.Errorf("status queues: %w", err)
	}

	// Query nodes
	nodeObjs, err := conn.StatusNode("")
	if err != nil {
		return nil, fmt.Errorf("status nodes: %w", err)
	}

	// Query jobs
	jobObjs, err := conn.StatusJob("")
	if err != nil {
		return nil, fmt.Errorf("status jobs: %w", err)
	}

	// Build node info
	nodes := make([]*NodeInfo, 0, len(nodeObjs))
	for _, obj := range nodeObjs {
		n := parseNodeInfo(obj)
		nodes = append(nodes, n)
	}

	// Build job info, grouped by queue
	jobsByQueue := make(map[string][]*JobInfo)
	for _, obj := range jobObjs {
		j := parseJobInfo(obj)
		if j.State == "Q" {
			jobsByQueue[j.Queue] = append(jobsByQueue[j.Queue], j)
		}
	}

	// Build queue info
	queues := make([]*QueueInfo, 0, len(queueObjs))
	for _, obj := range queueObjs {
		q := parseQueueInfo(obj)
		q.Jobs = jobsByQueue[q.Name]
		queues = append(queues, q)
	}

	// Sort queues by priority if configured
	if s.cfg.SortQueues {
		sort.Slice(queues, func(i, k int) bool {
			return queues[i].Priority > queues[k].Priority
		})
	}

	sinfo := &ServerInfo{
		Name:   conn.Server(),
		Queues: queues,
		Nodes:  nodes,
	}
	return sinfo, nil
}

// initCycle prepares the scheduling cycle: sort jobs, decay fair-share, detect starvation.
func (s *Scheduler) initCycle(sinfo *ServerInfo) {
	now := time.Now()

	// Decay fair-share usage based on half-life
	if s.cfg.FairShare && s.cfg.HalfLife > 0 {
		// Apply exponential decay
		decayFactor := math.Pow(0.5, float64(time.Duration(s.cfg.SchedulerInterval)*time.Second)/float64(s.cfg.HalfLife))
		for user, usage := range s.fairUsage {
			s.fairUsage[user] = usage * decayFactor
		}
	}

	// Sort jobs within each queue based on configured sort criterion
	for _, q := range sinfo.Queues {
		if len(q.Jobs) == 0 {
			continue
		}
		s.sortJobs(q.Jobs, now)
	}
}

// sortJobs sorts a job list based on the configured sort_by criterion.
func (s *Scheduler) sortJobs(jobs []*JobInfo, now time.Time) {
	switch s.cfg.SortBy {
	case "shortest_job_first":
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].Walltime < jobs[k].Walltime
		})
	case "longest_job_first":
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].Walltime > jobs[k].Walltime
		})
	case "high_priority_first":
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].Priority > jobs[k].Priority
		})
	case "low_priority_first":
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].Priority < jobs[k].Priority
		})
	case "smallest_memory_first":
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].MemReq < jobs[k].MemReq
		})
	case "largest_memory_first":
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].MemReq > jobs[k].MemReq
		})
	case "fair_share":
		sort.Slice(jobs, func(i, k int) bool {
			return s.fairUsage[jobs[i].Owner] < s.fairUsage[jobs[k].Owner]
		})
	default: // "fifo" — sort by queue time (earliest first)
		sort.Slice(jobs, func(i, k int) bool {
			return jobs[i].QueueTime.Before(jobs[k].QueueTime)
		})
	}

	// Starvation prevention: move starving jobs to the front
	if s.cfg.HelpStarvingJobs && s.cfg.MaxStarve > 0 {
		starveCutoff := now.Add(-s.cfg.MaxStarve)
		starvingJobs := make([]*JobInfo, 0)
		normalJobs := make([]*JobInfo, 0)
		for _, j := range jobs {
			if !j.QueueTime.IsZero() && j.QueueTime.Before(starveCutoff) {
				starvingJobs = append(starvingJobs, j)
			} else {
				normalJobs = append(normalJobs, j)
			}
		}
		if len(starvingJobs) > 0 {
			copy(jobs, append(starvingJobs, normalJobs...))
			log.Printf("[SCHED] %d starving jobs promoted to front of queue", len(starvingJobs))
		}
	}
}

// jobIterator provides different job iteration strategies.
type jobIterator struct {
	sched *Scheduler
	sinfo *ServerInfo
	mode  string // "round_robin", "by_queue", "flat"
	qIdx  int    // current queue index (round-robin/by-queue)
	jIdx  []int  // per-queue job index (round-robin)
}

// newJobIterator creates a job iterator based on the scheduling configuration.
func (s *Scheduler) newJobIterator(sinfo *ServerInfo) *jobIterator {
	mode := "flat"
	if s.cfg.RoundRobin {
		mode = "round_robin"
	} else if s.cfg.ByQueue {
		mode = "by_queue"
	}

	jIdx := make([]int, len(sinfo.Queues))
	return &jobIterator{
		sched: s,
		sinfo: sinfo,
		mode:  mode,
		qIdx:  0,
		jIdx:  jIdx,
	}
}

// next returns the next job to consider for scheduling, or nil when done.
func (it *jobIterator) next() *JobInfo {
	switch it.mode {
	case "round_robin":
		return it.nextRoundRobin()
	case "by_queue":
		return it.nextByQueue()
	default:
		return it.nextFlat()
	}
}

// nextRoundRobin cycles through queues, taking one job from each.
func (it *jobIterator) nextRoundRobin() *JobInfo {
	numQueues := len(it.sinfo.Queues)
	if numQueues == 0 {
		return nil
	}
	// Try each queue once per round
	for attempts := 0; attempts < numQueues; attempts++ {
		qIdx := it.qIdx % numQueues
		it.qIdx++
		q := it.sinfo.Queues[qIdx]
		if !q.Enabled || !q.Started || isRouteQueue(q) {
			continue
		}
		jIdx := it.jIdx[qIdx]
		if jIdx < len(q.Jobs) {
			j := q.Jobs[jIdx]
			it.jIdx[qIdx]++
			if !j.CanNotRun {
				return j
			}
		}
	}
	// Check if all queues are exhausted
	allDone := true
	for i, q := range it.sinfo.Queues {
		if it.jIdx[i] < len(q.Jobs) {
			allDone = false
			break
		}
	}
	if allDone {
		return nil
	}
	return it.nextRoundRobin() // recurse to find next eligible
}

// nextByQueue processes all jobs in one queue before moving to the next.
func (it *jobIterator) nextByQueue() *JobInfo {
	for it.qIdx < len(it.sinfo.Queues) {
		q := it.sinfo.Queues[it.qIdx]
		if !q.Enabled || !q.Started || isRouteQueue(q) {
			it.qIdx++
			continue
		}
		jIdx := it.jIdx[it.qIdx]
		if jIdx < len(q.Jobs) {
			j := q.Jobs[jIdx]
			it.jIdx[it.qIdx]++
			if !j.CanNotRun {
				return j
			}
			continue
		}
		it.qIdx++
	}
	return nil
}

// nextFlat returns jobs from all queues in a single sorted list.
func (it *jobIterator) nextFlat() *JobInfo {
	for it.qIdx < len(it.sinfo.Queues) {
		q := it.sinfo.Queues[it.qIdx]
		if !q.Enabled || !q.Started || isRouteQueue(q) {
			it.qIdx++
			continue
		}
		jIdx := it.jIdx[it.qIdx]
		if jIdx < len(q.Jobs) {
			j := q.Jobs[jIdx]
			it.jIdx[it.qIdx]++
			if !j.CanNotRun {
				return j
			}
			continue
		}
		it.qIdx++
	}
	return nil
}

// findNodeForJob selects the best available node for a job.
// countSchedulableForJob returns how many distinct schedulable nodes satisfy
// the job's placement constraints (host / host group / feature) with at least
// reqCPUs free. Used to gate multi-node dispatch (1.4).
func (s *Scheduler) countSchedulableForJob(sinfo *ServerInfo, jinfo *JobInfo, reqCPUs int) int {
	n := 0
	for _, node := range sinfo.Nodes {
		if !nodeSchedulable(node) {
			continue
		}
		if jinfo.Host != "" && !strings.EqualFold(node.Name, jinfo.Host) {
			continue
		}
		if jinfo.HostGroup != "" && !nodeHasGroup(node, jinfo.HostGroup) {
			continue
		}
		if len(jinfo.Features) > 0 && !nodeHasAllFeatures(node, jinfo.Features) {
			continue
		}
		if q := queueForJob(sinfo, jinfo.Queue); !queueNodeOK(q, node) {
			continue
		}
		if node.FreeCPUs >= reqCPUs {
			n++
		}
	}
	return n
}
// nodeSchedulable reports whether a node may accept new jobs. Free nodes are
// schedulable; nodes that are draining, exclusive/offline/down/busy are not;
// a node already running a job (job-exclusive) may still take more jobs if it
// has free capacity.
func nodeSchedulable(n *NodeInfo) bool {
	if n.State == "free" {
		return true
	}
	blocked := map[string]bool{"drain": true, "excl": true, "exclusive": true,
		"offline": true, "down": true, "busy": true}
	for _, tok := range strings.Split(n.State, ",") {
		t := strings.TrimSpace(tok)
		if blocked[t] {
			return false
		}
		if t == "job-exclusive" {
			return true
		}
	}
	return false
}

func (s *Scheduler) findNodeForJob(sinfo *ServerInfo, jinfo *JobInfo) *NodeInfo {
	cpuReq := jinfo.CPUReq
	if cpuReq == 0 {
		cpuReq = 1
	}
	// Multi-node (1.4): a job asking for N nodes must find N distinct
	// schedulable nodes each with >= PPN free CPUs; otherwise it stays blocked
	// (lec cloud lookahead runs normally). cpuReq below becomes the per-node ppn.
	if jinfo.Nodes > 1 {
		perNode := jinfo.PPN
		if perNode == 0 {
			perNode = cpuReq
		}
		if s.countSchedulableForJob(sinfo, jinfo, perNode) < jinfo.Nodes {
			return nil
		}
	}

	var candidates []*NodeInfo
	for _, n := range sinfo.Nodes {
		if !nodeSchedulable(n) {
			continue
		}
		// Host pinning: -l host=<node> restricts scheduling to that node.
		if jinfo.Host != "" && !strings.EqualFold(n.Name, jinfo.Host) {
			continue
		}
		// Feature matching: -l feature=a,b requires the node to carry all requested properties.
		if len(jinfo.Features) > 0 && !nodeHasAllFeatures(n, jinfo.Features) {
			continue
		}
		// Host group / node pool: -l host=@<group> requires membership in the group.
		if jinfo.HostGroup != "" && !nodeHasGroup(n, jinfo.HostGroup) {
			continue
		}
		// Queue policy (1.5/1.6): hostlist node-pool binding + naccesspolicy.
		if q := queueForJob(sinfo, jinfo.Queue); !queueNodeOK(q, n) {
			continue
		}
		if n.FreeCPUs >= cpuReq {
			candidates = append(candidates, n)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Local-first: statically-configured local nodes are always preferred over
	// auto-registered cloud/dynamic nodes. Cloud burst should only be used when
	// the local machines cannot satisfy demand, so a still-registered cloud VM
	// is never dispatched ahead of a free local node (which would keep the cloud
	// VM alive while local sits idle past its reclaim window). Within each
	// group the configured packing rule still applies.
	sort.SliceStable(candidates, func(i, k int) bool {
		if candidates[i].Dynamic != candidates[k].Dynamic {
			return !candidates[i].Dynamic // static (local) first
		}
		if s.cfg.LoadBalancing {
			// Load balancing: pick the node with the lowest load
			return candidates[i].LoadAvg < candidates[k].LoadAvg
		}
		// Default: pack jobs onto the most-loaded node (best fit)
		return candidates[i].FreeCPUs < candidates[k].FreeCPUs
	})

	return candidates[0]
}

// isRouteQueue reports whether a queue is a Route queue (jobs are forwarded to
// a destination rather than run locally). The server routes jobs out of route
// queues at commit time, so the scheduler should never dispatch from them.
func isRouteQueue(q *QueueInfo) bool {
	t := strings.ToLower(strings.TrimSpace(q.Type))
	return t == "route" || t == "r"
}

// queueNodeOK reports whether node n may be scheduled for a job in queue q,
// enforcing the queue hostlist (node-pool binding) and naccesspolicy (1.5/1.6).
// A nil q (no queue / routing) allows any node.
func queueNodeOK(q *QueueInfo, n *NodeInfo) bool {
	if q == nil {
		return true
	}
	if len(q.HostList) > 0 {
		ok := false
		for _, h := range q.HostList {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			if strings.HasPrefix(h, "@") {
				if nodeHasGroup(n, strings.TrimPrefix(h, "@")) {
					ok = true
					break
				}
			} else if strings.EqualFold(n.Name, h) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	switch q.NaccessPolicy {
	case "exclusive", "singleuser", "exclhost", "exec_host_exclusive":
		if len(n.Jobs) > 0 {
			return false // only a fully idle node may take an exclusive job
		}
	}
	return true
}
// nodeHasGroup reports whether node n belongs to the named host group / pool.
func nodeHasGroup(n *NodeInfo, g string) bool {
	for _, grp := range n.Groups {
		if strings.EqualFold(strings.TrimSpace(grp), g) {
			return true
		}
	}
	return false
}

// nodeHasAllFeatures reports whether node n carries every property in want.
func nodeHasAllFeatures(n *NodeInfo, want []string) bool {
	for _, f := range want {
		found := false
		for _, p := range n.Properties {
			if strings.EqualFold(p, f) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// --- Parsing helpers ---

func parseJobInfo(obj client.StatusObject) *JobInfo {
	j := &JobInfo{ID: obj.Name, CPUReq: 1}
	for _, a := range obj.Attrs {
		key := a.Name
		if a.HasResc && a.Resc != "" {
			key = a.Name + "." + a.Resc
		}
		switch key {
		case "Job_Name":
			j.Name = a.Value
		case "Job_Owner":
			j.Owner = a.Value
			if idx := strings.Index(j.Owner, "@"); idx > 0 {
				j.Owner = j.Owner[:idx]
			}
		case "job_state":
			j.State = a.Value
		case "queue":
			j.Queue = a.Value
		case "Priority":
			j.Priority, _ = strconv.Atoi(a.Value)
		case "qtime":
			if ts, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
				j.QueueTime = time.Unix(ts, 0)
			}
		case "Resource_List.walltime":
			j.Walltime = parseWalltime(a.Value)
		case "Resource_List.mem":
			j.MemReq = parseMemory(a.Value)
		case "Resource_List.ncpus":
			j.CPUReq, _ = strconv.Atoi(a.Value)
		case "Resource_List.nodes":
			n, pp := parseNodeSelectSpec(a.Value)
			j.Nodes = n
			j.PPN = pp
		case "Resource_List.select":
			n, pp := parseNodeSelectSpec(a.Value)
			j.Nodes = n
			j.PPN = pp
		case "Resource_List.ppn":
			if p, err := strconv.Atoi(a.Value); err == nil && p > 0 {
				j.PPN = p
			}
		case "Resource_List.host":
			if strings.HasPrefix(strings.TrimSpace(a.Value), "@") {
				j.HostGroup = strings.TrimPrefix(strings.TrimSpace(a.Value), "@")
			} else {
				j.Host = a.Value
			}
		case "Resource_List.feature", "Resource_List.features", "Resource_List.properties":
			for _, f := range strings.Split(a.Value, ",") {
				if f = strings.TrimSpace(f); f != "" {
					j.Features = append(j.Features, f)
				}
			}
		}
	}
	return j
}

func parseNodeInfo(obj client.StatusObject) *NodeInfo {
	n := &NodeInfo{Name: obj.Name}
	usedReported := false
	for _, a := range obj.Attrs {
		switch a.Name {
		case "state":
			n.State = a.Value
		case "np":
			n.NumProcs, _ = strconv.Atoi(a.Value)
			n.FreeCPUs = n.NumProcs // will subtract running jobs below
		case "ncpus":
			if n.NumProcs == 0 {
				n.NumProcs, _ = strconv.Atoi(a.Value)
				n.FreeCPUs = n.NumProcs
			}
		case "used_cpus":
			if u, err := strconv.Atoi(a.Value); err == nil && u >= 0 {
				n.UsedCPUs = u
				usedReported = true
			}
		case "totmem":
			n.TotalMem = parseMemory(a.Value)
		case "availmem":
			n.AvailMem = parseMemory(a.Value)
		case "loadave":
			n.LoadAvg, _ = strconv.ParseFloat(a.Value, 64)
		case "jobs":
			if a.Value != "" {
				n.Jobs = strings.Split(a.Value, ",")
				// Fallback only when the server did not report used_cpus (see
				// TODO 2.6): derive free CPUs from the running-job count. When
				// used_cpus is present it gives the authoritative per-job CPUReq
				// sum, so it is preferred.
				if !usedReported {
					n.FreeCPUs = n.NumProcs - len(n.Jobs)
					if n.FreeCPUs < 0 {
						n.FreeCPUs = 0
					}
				}
			}
		case "properties", "features":
			for _, p := range strings.Split(a.Value, ",") {
				if p = strings.TrimSpace(p); p != "" {
					n.Properties = append(n.Properties, p)
				}
			}
		case "hostgroups", "groups":
			for _, g := range strings.Split(a.Value, ",") {
				if g = strings.TrimSpace(g); g != "" {
					n.Groups = append(n.Groups, g)
				}
			}
		case "is_dynamic":
			n.Dynamic = (a.Value == "true")
		}
	}
	// If the server reported used_cpus (authoritative per-job CPUReq sum), use it
	// to compute the true number of free CPUs regardless of attr order.
	if usedReported {
		n.FreeCPUs = n.NumProcs - n.UsedCPUs
		if n.FreeCPUs < 0 {
			n.FreeCPUs = 0
		}
	}
	return n
}

func parseQueueInfo(obj client.StatusObject) *QueueInfo {
	q := &QueueInfo{Name: obj.Name, Enabled: true, Started: true}
	for _, a := range obj.Attrs {
		switch a.Name {
		case "queue_type":
			q.Type = a.Value
		case "enabled":
			q.Enabled = a.Value == "True" || a.Value == "true"
		case "started":
			q.Started = a.Value == "True" || a.Value == "true"
		case "Priority":
			q.Priority, _ = strconv.Atoi(a.Value)
		case "max_running":
			q.MaxRunning, _ = strconv.Atoi(a.Value)
		case "state_count_running":
			q.Running, _ = strconv.Atoi(a.Value)
		case "state_count_queued":
			q.Queued, _ = strconv.Atoi(a.Value)
		case "naccesspolicy":
			q.NaccessPolicy = strings.ToLower(strings.TrimSpace(a.Value))
		case "hostlist":
			q.HostList = append(q.HostList, strings.Split(a.Value, ",")...)
		case "cloud_provider":
			q.CloudProvider = a.Value
			if a.Value != "" {
				q.CloudBacked = true
			}
		case "cloud_vm_sku":
			q.CloudSKU = a.Value
		case "cloud_min_nodes":
			q.CloudMinNodes, _ = strconv.Atoi(a.Value)
		case "cloud_max_nodes":
			q.CloudMaxNodes, _ = strconv.Atoi(a.Value)
		case "cloud_idle_time":
			q.CloudIdleTime, _ = strconv.Atoi(a.Value)
		case "cloud_provision_timeout":
			q.CloudProvisionTimeout, _ = strconv.Atoi(a.Value)
		case "cloud_reclaim":
			q.CloudReclaim = a.Value
		case "cloud_subnet_id":
			q.CloudSubnetID = a.Value
		case "cloud_image_id":
			q.CloudImageID = a.Value
		case "cloud_disk_size":
			q.CloudDiskSize, _ = strconv.Atoi(a.Value)
		case "cloud_disk_type":
			q.CloudDiskType = a.Value
		case "cloud_ssh_key":
			q.CloudSSHKey = a.Value
		case "cloud_location":
			q.CloudLocation = a.Value
		case "cloud_rg_name":
			q.CloudRGName = a.Value
		case "cloud_cooldown":
			q.CloudCooldown, _ = strconv.Atoi(a.Value)
		case "cloud_scale_headroom":
			q.CloudScaleHeadroom, _ = strconv.Atoi(a.Value)
		case "cloud_drain_timeout":
			q.CloudDrainTimeout, _ = strconv.Atoi(a.Value)
		}
	}
	return q
}

// parseNodeSelectSpec parses a TORQUE node request ("N", "N:ppn=M", or a
// select chunk) into a node count and processors-per-node. Returns nodes>=1,
// ppn>=1.
func parseNodeSelectSpec(spec string) (nodes, ppn int) {
	nodes, ppn = 1, 1
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return
	}
	first := strings.Split(spec, "+")[0]
	parts := strings.Split(first, ":")
	if n, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil && n > 0 {
		nodes = n
	}
	for _, kv := range parts[1:] {
		kv = strings.TrimSpace(kv)
		if strings.HasPrefix(kv, "ppn=") {
			if m, err := strconv.Atoi(strings.TrimSpace(kv[4:])); err == nil && m > 0 {
				ppn = m
			}
		}
	}
	return
}

func parseWalltime(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
	}
	return 0
}

func parseMemory(s string) int64 {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	multiplier := int64(1)
	if strings.HasSuffix(s, "kb") {
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "mb") {
		s = s[:len(s)-2]
		multiplier = 1024
	} else if strings.HasSuffix(s, "gb") {
		s = s[:len(s)-2]
		multiplier = 1024 * 1024
	} else if strings.HasSuffix(s, "tb") {
		s = s[:len(s)-2]
		multiplier = 1024 * 1024 * 1024
	}
	val, _ := strconv.ParseInt(s, 10, 64)
	return val * multiplier
}
