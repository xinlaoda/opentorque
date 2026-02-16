// Package job provides the JobManager for tracking all jobs in the server.
package job

import (
	"fmt"
	"log"
	"sync"
)

// Manager tracks all jobs in the server and assigns job IDs.
type Manager struct {
	mu          sync.RWMutex
	jobs        map[string]*Job
	nextJobID   int
	serverName  string

	// Job state counters for quick stats
	stateCounts [7]int // indexed by job state
}

// NewManager creates a new job manager.
func NewManager(serverName string, startingJobID int) *Manager {
	return &Manager{
		jobs:       make(map[string]*Job),
		nextJobID:  startingJobID,
		serverName: serverName,
	}
}

// NextJobID allocates the next job ID and returns it as a formatted string.
// Format: "seqnum.servername" (e.g., "42.DevBox")
func (m *Manager) NextJobID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextJobID
	m.nextJobID++
	return fmt.Sprintf("%d.%s", id, m.serverName)
}

// GetNextJobIDNum returns the current next job ID number (for persistence).
func (m *Manager) GetNextJobIDNum() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nextJobID
}

// SetNextJobIDNum sets the next job ID (used during recovery).
func (m *Manager) SetNextJobIDNum(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextJobID = n
}

// AddJob registers a job in the manager.
func (m *Manager) AddJob(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
	if j.State >= 0 && j.State < len(m.stateCounts) {
		m.stateCounts[j.State]++
	}
	log.Printf("[JOB] Added job %s (state=%s, queue=%s)", j.ID, j.StateName(), j.Queue)
}

// RemoveJob removes a job from the manager.
func (m *Manager) RemoveJob(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[id]; ok {
		if j.State >= 0 && j.State < len(m.stateCounts) {
			m.stateCounts[j.State]--
		}
		delete(m.jobs, id)
		log.Printf("[JOB] Removed job %s", id)
	}
}

// GetJob returns a job by ID, or nil if not found.
func (m *Manager) GetJob(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
}

// UpdateJobState changes a job's state and updates counters.
func (m *Manager) UpdateJobState(id string, newState, newSubstate int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return
	}
	// Adjust state counters
	oldState := j.State
	if oldState >= 0 && oldState < len(m.stateCounts) {
		m.stateCounts[oldState]--
	}
	j.SetState(newState, newSubstate)
	if newState >= 0 && newState < len(m.stateCounts) {
		m.stateCounts[newState]++
	}
}

// AllJobs returns a snapshot of all job pointers (for iteration).
func (m *Manager) AllJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		result = append(result, j)
	}
	return result
}

// QueuedJobs returns jobs in Queued state, ordered by creation time.
func (m *Manager) QueuedJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Job
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.State == StateQueued {
			result = append(result, j)
		}
		j.Mu.RUnlock()
	}
	return result
}

// RunningJobs returns jobs in Running or Exiting state.
func (m *Manager) RunningJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Job
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.State == StateRunning || j.State == StateExiting {
			result = append(result, j)
		}
		j.Mu.RUnlock()
	}
	return result
}

// JobCount returns the total number of jobs.
func (m *Manager) JobCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.jobs)
}

// StateCount returns the number of jobs in a given state.
func (m *Manager) StateCount(state int) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if state >= 0 && state < len(m.stateCounts) {
		return m.stateCounts[state]
	}
	return 0
}

// CountByState counts jobs by iterating all jobs and checking actual state.
// More accurate than StateCount during scheduling when states are in flux.
func (m *Manager) CountByState(state int) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.State == state {
			count++
		}
		j.Mu.RUnlock()
	}
	return count
}

// JobsInQueue returns all jobs belonging to a specific queue.
func (m *Manager) JobsInQueue(queueName string) []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Job
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.Queue == queueName {
			result = append(result, j)
		}
		j.Mu.RUnlock()
	}
	return result
}

// CompletedJobs returns jobs in Complete state (for purging).
func (m *Manager) CompletedJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Job
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.State == StateComplete {
			result = append(result, j)
		}
		j.Mu.RUnlock()
	}
	return result
}

// CountJobsByOwner returns total jobs owned by a given user.
func (m *Manager) CountJobsByOwner(owner string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.Owner == owner {
			count++
		}
		j.Mu.RUnlock()
	}
	return count
}

// CountRunningByOwner returns running jobs owned by a given user.
func (m *Manager) CountRunningByOwner(owner string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.State == StateRunning && j.Owner == owner {
			count++
		}
		j.Mu.RUnlock()
	}
	return count
}

// CountRunningByGroup returns running jobs belonging to a given group.
func (m *Manager) CountRunningByGroup(group string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, j := range m.jobs {
		j.Mu.RLock()
		if j.State == StateRunning && j.EGroup == group {
			count++
		}
		j.Mu.RUnlock()
	}
	return count
}
