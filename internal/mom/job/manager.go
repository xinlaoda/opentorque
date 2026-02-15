package job

import (
	"fmt"
	"log"
	"sync"
)

// Manager manages all jobs on this MOM.
type Manager struct {
	mu   sync.RWMutex
	jobs map[string]*Job // keyed by job ID

	// Callbacks
	onJobComplete func(j *Job) // called when a job finishes
}

// NewManager creates a new job manager.
func NewManager() *Manager {
	return &Manager{
		jobs: make(map[string]*Job),
	}
}

// SetOnJobComplete registers a callback for job completion.
func (m *Manager) SetOnJobComplete(fn func(j *Job)) {
	m.onJobComplete = fn
}

// AddJob adds a job to the manager.
func (m *Manager) AddJob(j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.jobs[j.ID]; exists {
		return fmt.Errorf("job %s already exists", j.ID)
	}
	m.jobs[j.ID] = j
	log.Printf("[JOB] Added job %s (owner=%s)", j.ID, j.Owner)
	return nil
}

// GetJob returns a job by ID.
func (m *Manager) GetJob(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

// RemoveJob removes a job.
func (m *Manager) RemoveJob(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)
	log.Printf("[JOB] Removed job %s", id)
}

// AllJobs returns a snapshot of all jobs.
func (m *Manager) AllJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	jobs := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	return jobs
}

// RunningJobs returns all jobs that are running or exiting (need processing).
func (m *Manager) RunningJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	jobs := make([]*Job, 0)
	for _, j := range m.jobs {
		if j.IsRunning() || j.IsExiting() {
			jobs = append(jobs, j)
		}
	}
	return jobs
}

// JobCount returns the total number of jobs.
func (m *Manager) JobCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.jobs)
}

// MarkJobExiting marks a job as exiting.
func (m *Manager) MarkJobExiting(id string, exitStatus int) {
	m.mu.RLock()
	j, ok := m.jobs[id]
	m.mu.RUnlock()

	if !ok {
		return
	}

	j.SetState(StateExiting, SubstateExiting)
	j.Mu.Lock()
	j.ExitStatus = exitStatus
	j.Mu.Unlock()

	log.Printf("[JOB] Job %s exiting with status %d", id, exitStatus)
}

// MarkJobComplete marks a job as complete and triggers callback.
func (m *Manager) MarkJobComplete(id string) {
	m.mu.RLock()
	j, ok := m.jobs[id]
	m.mu.RUnlock()

	if !ok {
		return
	}

	j.SetState(StateComplete, SubstateComplete)
	log.Printf("[JOB] Job %s complete (exit=%d)", id, j.ExitStatus)

	if m.onJobComplete != nil {
		m.onJobComplete(j)
	}
}
