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

	"github.com/opentorque/opentorque/internal/sched/client"
	"github.com/opentorque/opentorque/internal/sched/config"
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
	MemReq    int64 // requested memory in KB
	CPUReq    int   // requested CPUs

	// Scheduling state
	CanNotRun    bool
	CanNeverRun  bool
	StarvingSince time.Time
}

// NodeInfo holds scheduling-relevant attributes for a compute node.
type NodeInfo struct {
	Name      string
	State     string
	NumProcs  int
	FreeCPUs  int
	TotalMem  int64 // KB
	AvailMem  int64 // KB
	LoadAvg   float64
	Jobs      []string
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

// RunCycle performs one complete scheduling cycle:
// 1. Query server for current state
// 2. Build scheduling data structures
// 3. Apply sorting and algorithm
// 4. Dispatch jobs to nodes
func (s *Scheduler) RunCycle(conn *client.Conn) (int, error) {
	// Query current state from server
	sinfo, err := s.queryServer(conn)
	if err != nil {
		return 0, fmt.Errorf("query server: %w", err)
	}

	// Initialize cycle: sort jobs, detect starvation, decay fair-share
	s.initCycle(sinfo)

	dispatched := 0

	// Main scheduling loop: get next job, check resources, dispatch
	jobIter := s.newJobIterator(sinfo)
	for {
		jinfo := jobIter.next()
		if jinfo == nil {
			break
		}

		// Find a suitable node for this job
		node := s.findNodeForJob(sinfo, jinfo)
		if node == nil {
			jinfo.CanNotRun = true
			// In strict FIFO mode, stop scheduling after first blocked job
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

		// Update fair-share usage tracking
		if s.cfg.FairShare {
			s.fairUsage[jinfo.Owner] += 1.0
		}
	}

	return dispatched, nil
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
	sched  *Scheduler
	sinfo  *ServerInfo
	mode   string // "round_robin", "by_queue", "flat"
	qIdx   int    // current queue index (round-robin/by-queue)
	jIdx   []int  // per-queue job index (round-robin)
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
		if !q.Enabled || !q.Started {
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
		if !q.Enabled || !q.Started {
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
		if !q.Enabled || !q.Started {
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
func (s *Scheduler) findNodeForJob(sinfo *ServerInfo, jinfo *JobInfo) *NodeInfo {
	cpuReq := jinfo.CPUReq
	if cpuReq == 0 {
		cpuReq = 1
	}

	var candidates []*NodeInfo
	for _, n := range sinfo.Nodes {
		if n.State != "free" && !strings.Contains(n.State, "job-") {
			continue
		}
		if n.FreeCPUs >= cpuReq {
			candidates = append(candidates, n)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	if s.cfg.LoadBalancing {
		// Load balancing: pick the node with the lowest load
		sort.Slice(candidates, func(i, k int) bool {
			return candidates[i].LoadAvg < candidates[k].LoadAvg
		})
	} else {
		// Default: pack jobs onto the first available node (best fit)
		sort.Slice(candidates, func(i, k int) bool {
			return candidates[i].FreeCPUs < candidates[k].FreeCPUs
		})
	}

	return candidates[0]
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
			// Parse "nodes=N" to get CPU count approximation
			if n, err := strconv.Atoi(a.Value); err == nil && n > 0 {
				j.CPUReq = n
			}
		}
	}
	return j
}

func parseNodeInfo(obj client.StatusObject) *NodeInfo {
	n := &NodeInfo{Name: obj.Name}
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
		case "totmem":
			n.TotalMem = parseMemory(a.Value)
		case "availmem":
			n.AvailMem = parseMemory(a.Value)
		case "loadave":
			n.LoadAvg, _ = strconv.ParseFloat(a.Value, 64)
		case "jobs":
			if a.Value != "" {
				n.Jobs = strings.Split(a.Value, ",")
				n.FreeCPUs = n.NumProcs - len(n.Jobs)
				if n.FreeCPUs < 0 {
					n.FreeCPUs = 0
				}
			}
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
		}
	}
	return q
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
