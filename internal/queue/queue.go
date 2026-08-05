// Package queue implements the Queue data model for pbs_server.
package queue

import (
	"log"
	"strings"
	"sync"
)

// Queue type constants matching C QUE_TYPE_* values
const (
	TypeExecution = 0 // Execution queue (jobs run from here)
	TypeRoute     = 1 // Route queue (jobs forwarded elsewhere)
)

// Queue represents a PBS job queue.
type Queue struct {
	Mu sync.RWMutex

	Name    string
	Type    int  // TypeExecution or TypeRoute
	Enabled bool // Can accept new jobs
	Started bool // Can run/route jobs

	// Limits
	MaxJobs     int // Max total jobs in queue (0 = unlimited)
	MaxRun      int // Max running jobs (0 = unlimited)
	MaxUserJobs int // Max jobs per user (0 = unlimited)
	MaxUserRun  int // Max running per user (0 = unlimited)

	// Resource limits
	ResourceMax  map[string]string // Maximum resource per job
	ResourceMin  map[string]string // Minimum resource per job
	ResourceDflt map[string]string // Default resources

	// ACLs
	ACLUserEnabled  bool
	ACLUsers        []string
	ACLGroupEnabled bool
	ACLGroups       []string
	ACLHostEnabled  bool
	ACLHosts        []string

	// Route queue settings
	RouteDestin []string // Destination queues for routing

	// Attributes (generic)
	Attrs map[string]string

	// Counters
	TotalJobs int
	StateJobs [7]int // Count per job state

	// Internal
	Modified bool

	// Cloud elasticity (queue-driven burst) attributes.
	// When CloudProvider is non-empty the queue is treated as a cloud-backed
	// pool: an external Cloud Resource Provider (CRP) provisions/deprovisions
	// worker VMs on demand (see docs/cloud-elastic-event-driven-design.md).
	CloudProvider  string // cloud name: "azure" | "aws" | "" (static pool)
	CloudVMSKU     string // VM size/family, e.g. "Standard_D8s_v3"
	CloudMinNodes  int    // floor: nodes kept scaled-in
	CloudMaxNodes        int    // cap: max nodes the queue may scale to (0 = unlimited)
	CloudIdleTime        int    // seconds idle before a node becomes a scale-in candidate
	CloudProvisionTimeout int    // seconds a provisioned-but-not-booted VM may wait before reclaim (0 = default)
	CloudReclaim         string // deallocate | hibernate
	CloudSubnetID    string // Azure subnet resource ID where worker VMs are placed
	CloudImageID   string // OS image: id, urn, or shared-image-gallery reference
	CloudDiskSize  int    // OS disk size in GB (0 = provider default)
	CloudDiskType  string // OS disk type, e.g. "Premium_LRS" (optional)
	CloudSSHKey    string // authorized SSH public key for worker VMs
	CloudLocation  string // Azure region for worker VM creation ("" = server region)
	CloudRgName    string // Azure resource group for worker VMs ("" = server rg)
}

// ParseList splits a comma/space separated configuration value into a trimmed,
// de-duplicated list (used for route_destinations, acl_users, acl_groups, ...).
func ParseList(v string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, part := range strings.Split(v, ",") {
		for _, sub := range strings.Fields(part) {
			sub = strings.TrimSpace(sub)
			if sub == "" || seen[sub] {
				continue
			}
			seen[sub] = true
			out = append(out, sub)
		}
	}
	return out
}

// NewQueue creates a new queue with defaults.
func NewQueue(name string, qtype int) *Queue {
	return &Queue{
		Name:         name,
		Type:         qtype,
		Enabled:      true,
		Started:      true,
		ResourceMax:  make(map[string]string),
		ResourceMin:  make(map[string]string),
		ResourceDflt: make(map[string]string),
		Attrs:        make(map[string]string),
	}
}

// Manager tracks all queues in the server.
type Manager struct {
	mu     sync.RWMutex
	queues map[string]*Queue
}

// NewManager creates a new queue manager.
func NewManager() *Manager {
	return &Manager{
		queues: make(map[string]*Queue),
	}
}

// AddQueue adds a queue to the manager.
func (m *Manager) AddQueue(q *Queue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queues[q.Name] = q
	log.Printf("[QUEUE] Added queue %s (type=%d)", q.Name, q.Type)
}

// RemoveQueue removes a queue by name.
func (m *Manager) RemoveQueue(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.queues[name]; ok {
		delete(m.queues, name)
		log.Printf("[QUEUE] Removed queue %s", name)
		return true
	}
	return false
}

// GetQueue returns a queue by name, or nil if not found.
func (m *Manager) GetQueue(name string) *Queue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queues[name]
}

// AllQueues returns a snapshot of all queue pointers.
func (m *Manager) AllQueues() []*Queue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Queue, 0, len(m.queues))
	for _, q := range m.queues {
		result = append(result, q)
	}
	return result
}

// DefaultQueue returns the first execution queue found (or the only queue).
func (m *Manager) DefaultQueue() *Queue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, q := range m.queues {
		if q.Type == TypeExecution {
			return q
		}
	}
	// Return any queue as fallback
	for _, q := range m.queues {
		return q
	}
	return nil
}

// QueueCount returns the total number of queues.
func (m *Manager) QueueCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.queues)
}

// IncrJobCount increments the job count for a queue and state.
func (q *Queue) IncrJobCount(state int) {
	q.Mu.Lock()
	defer q.Mu.Unlock()
	q.TotalJobs++
	if state >= 0 && state < len(q.StateJobs) {
		q.StateJobs[state]++
	}
}

// DecrJobCount decrements the job count for a queue and state. Guards against
// underflow so counters never go negative even if a decrement is repeated for
// a job that was not previously counted (see TODO 3.6).
func (q *Queue) DecrJobCount(state int) {
	q.Mu.Lock()
	defer q.Mu.Unlock()
	if q.TotalJobs > 0 {
		q.TotalJobs--
	}
	if state >= 0 && state < len(q.StateJobs) && q.StateJobs[state] > 0 {
		q.StateJobs[state]--
	}
}

// TransferJobState adjusts counts when a job changes state within a queue.
// The per-state counters are guarded against underflow so they never go
// negative under churn (repeated qdel of running jobs, state transitions,
// qmove) — see TODO 3.6. TotalJobs is intentionally unchanged (a transfer
// does not change the number of jobs in the queue).
func (q *Queue) TransferJobState(oldState, newState int) {
	q.Mu.Lock()
	defer q.Mu.Unlock()
	if oldState >= 0 && oldState < len(q.StateJobs) && q.StateJobs[oldState] > 0 {
		q.StateJobs[oldState]--
	}
	if newState >= 0 && newState < len(q.StateJobs) {
		q.StateJobs[newState]++
	}
}
