// Package node implements the compute Node data model for pbs_server.
package node

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Node state flags matching C INUSE_* constants
const (
	StateFree    = 0x00 // Available for jobs
	StateDown    = 0x01 // Unreachable
	StateOffline = 0x02 // Administratively disabled
	StateReserve = 0x04 // Reserved for a job
	StateJob     = 0x08 // Running a job
	StateBusy    = 0x10 // Load too high
	StateUnknown = 0x20 // Initial state before first contact
)

// StateNames maps state flags to display strings for pbsnodes output.
var StateNames = map[int]string{
	StateFree:    "free",
	StateDown:    "down",
	StateOffline: "offline",
	StateReserve: "reserve",
	StateJob:     "job-exclusive",
	StateBusy:    "busy",
	StateUnknown: "state-unknown",
}

// Node represents a compute node (MOM) in the cluster.
type Node struct {
	Mu sync.RWMutex

	Name     string // Hostname
	ID       int    // Node index
	State    int    // State flags (can be combined)
	NumProcs int    // Number of processors (np)
	MomPort  int    // MOM service port (default 15002)
	RmPort   int    // Resource manager port (default 15003)

	// Status attributes received from MOM via IS protocol
	Status map[string]string

	// Resource tracking
	AssignedJobs []string // Job IDs currently on this node
	SlotsTotal   int      // Total execution slots
	SlotsUsed    int      // Currently used slots

	// Health tracking
	LastUpdate      time.Time // Last status update received
	LastHeardFrom   time.Time // Last successful contact
	FailCount       int       // Consecutive failure count
	PowerState      string    // Power state string

	// Properties and features
	Properties []string // Node properties (for node selection)
	Note       string   // Administrator note

	// Attributes (generic)
	Attrs map[string]string

	Modified bool
}

// NewNode creates a new Node with defaults.
func NewNode(name string, id int) *Node {
	return &Node{
		Name:       name,
		ID:         id,
		State:      StateDown, // Start as down until first status update
		NumProcs:   1,
		MomPort:    15002,
		RmPort:     15003,
		Status:     make(map[string]string),
		Attrs:      make(map[string]string),
		SlotsTotal: 1,
		PowerState: "Running",
	}
}

// StateName returns a human-readable state string for display.
func (n *Node) StateName() string {
	if n.State == StateFree {
		return "free"
	}
	// Check combined states in priority order
	result := ""
	checkFlags := []struct {
		flag int
		name string
	}{
		{StateDown, "down"},
		{StateOffline, "offline"},
		{StateBusy, "busy"},
		{StateJob, "job-exclusive"},
		{StateReserve, "reserve"},
		{StateUnknown, "state-unknown"},
	}
	for _, cf := range checkFlags {
		if n.State&cf.flag != 0 {
			if result != "" {
				result += ","
			}
			result += cf.name
		}
	}
	if result == "" {
		return "free"
	}
	return result
}

// IsFree returns true if the node can accept new jobs.
func (n *Node) IsFree() bool {
	return n.State == StateFree && n.SlotsUsed < n.SlotsTotal
}

// IsDown returns true if the node is unreachable.
func (n *Node) IsDown() bool {
	return n.State&StateDown != 0
}

// AvailableSlots returns the number of free execution slots.
func (n *Node) AvailableSlots() int {
	avail := n.SlotsTotal - n.SlotsUsed
	if avail < 0 {
		return 0
	}
	return avail
}

// AssignJob marks a job as running on this node, consuming slots.
func (n *Node) AssignJob(jobID string, slots int) {
	n.AssignedJobs = append(n.AssignedJobs, jobID)
	n.SlotsUsed += slots
	if n.SlotsUsed >= n.SlotsTotal {
		n.State |= StateJob
	}
}

// ReleaseJob removes a job from this node, freeing slots.
func (n *Node) ReleaseJob(jobID string, slots int) {
	for i, jid := range n.AssignedJobs {
		if jid == jobID {
			n.AssignedJobs = append(n.AssignedJobs[:i], n.AssignedJobs[i+1:]...)
			break
		}
	}
	n.SlotsUsed -= slots
	if n.SlotsUsed < 0 {
		n.SlotsUsed = 0
	}
	// Clear job-exclusive flag if no jobs remain
	if len(n.AssignedJobs) == 0 {
		n.State &^= StateJob
	}
}

// UpdateFromStatus processes key=value status strings from a MOM IS update.
func (n *Node) UpdateFromStatus(items []string) {
	for _, item := range items {
		// Each item is "key=value" format
		for i := 0; i < len(item); i++ {
			if item[i] == '=' {
				key := item[:i]
				val := item[i+1:]
				n.Status[key] = val
				// Handle specific keys that affect node state
				switch key {
				case "state":
					n.applyStateString(val)
				case "ncpus":
					fmt.Sscanf(val, "%d", &n.NumProcs)
					n.SlotsTotal = n.NumProcs
				case "np":
					fmt.Sscanf(val, "%d", &n.SlotsTotal)
				}
				break
			}
		}
	}
	n.LastUpdate = time.Now()
	n.LastHeardFrom = time.Now()
	n.FailCount = 0
	// If we heard from node and it was down (not offline), mark it up
	if n.State&StateDown != 0 && n.State&StateOffline == 0 {
		n.State &^= StateDown
	}
}

// applyStateString parses a state string from MOM (e.g., "free", "busy").
func (n *Node) applyStateString(s string) {
	// Only apply MOM-reported state changes, preserve admin flags
	adminFlags := n.State & (StateOffline | StateReserve)
	switch s {
	case "free":
		n.State = StateFree | adminFlags
	case "busy":
		n.State = StateBusy | adminFlags
	case "down":
		n.State = StateDown | adminFlags
	}
	// Re-apply job-exclusive if node has assigned jobs
	if len(n.AssignedJobs) > 0 {
		n.State |= StateJob
	}
}

// Manager tracks all compute nodes in the cluster.
type Manager struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	byID  map[int]*Node
	nextID int
}

// NewManager creates a new node manager.
func NewManager() *Manager {
	return &Manager{
		nodes:  make(map[string]*Node),
		byID:   make(map[int]*Node),
		nextID: 0,
	}
}

// AddNode registers a new compute node.
func (m *Manager) AddNode(name string, numProcs int) *Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.nodes[name]; ok {
		return existing
	}
	n := NewNode(name, m.nextID)
	n.NumProcs = numProcs
	n.SlotsTotal = numProcs
	m.nodes[name] = n
	m.byID[m.nextID] = n
	m.nextID++
	log.Printf("[NODE] Added node %s (np=%d, id=%d)", name, numProcs, n.ID)
	return n
}

// RemoveNode removes a node from the manager.
func (m *Manager) RemoveNode(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.nodes[name]; ok {
		delete(m.byID, n.ID)
		delete(m.nodes, name)
		log.Printf("[NODE] Removed node %s", name)
		return true
	}
	return false
}

// GetNode returns a node by name, or nil if not found.
func (m *Manager) GetNode(name string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes[name]
}

// AllNodes returns a snapshot of all nodes.
func (m *Manager) AllNodes() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		result = append(result, n)
	}
	return result
}

// FreeNodes returns nodes with available execution slots.
func (m *Manager) FreeNodes() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Node
	for _, n := range m.nodes {
		n.Mu.RLock()
		if n.IsFree() {
			result = append(result, n)
		}
		n.Mu.RUnlock()
	}
	return result
}

// NodeCount returns the total number of nodes.
func (m *Manager) NodeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.nodes)
}

// FindNodeForJob finds a node with enough free slots for the given requirement.
// Returns the node and how many slots to allocate, or nil if no node available.
func (m *Manager) FindNodeForJob(neededSlots int) (*Node, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, n := range m.nodes {
		n.Mu.RLock()
		avail := n.AvailableSlots()
		isFree := n.IsFree()
		n.Mu.RUnlock()
		if isFree && avail >= neededSlots {
			return n, neededSlots
		}
	}
	return nil, 0
}

// MarkNodeDown sets a node to Down state (called when health check fails).
func (m *Manager) MarkNodeDown(name string) {
	m.mu.RLock()
	n := m.nodes[name]
	m.mu.RUnlock()
	if n != nil {
		n.Mu.Lock()
		n.State |= StateDown
		n.FailCount++
		n.Mu.Unlock()
	}
}
