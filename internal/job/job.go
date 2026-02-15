// Package job implements the Job data model and state machine for pbs_server.
package job

import (
	"fmt"
	"sync"
	"time"
)

// Job states matching C pbs_server JOB_STATE_* constants
const (
	StateTransit  = 0 // Job is in transit (being moved)
	StateQueued   = 1 // Queued, waiting to run
	StateHeld     = 2 // Held by user or system
	StateWaiting  = 3 // Waiting for scheduled time
	StateRunning  = 4 // Currently executing on a node
	StateExiting  = 5 // Exiting, cleanup in progress
	StateComplete = 6 // Completed (kept for history)
)

// Job substates for more granular tracking
const (
	SubstateTransitQ  = 0  // Transit to queue
	SubstateQueued    = 10 // Simply queued
	SubstateHeld      = 20 // Job held
	SubstateWaiting   = 30 // Waiting on dependency
	SubstateStagedIn  = 37 // Files staged in
	SubstateRunning   = 42 // Running
	SubstateExiting   = 50 // Exiting
	SubstateComplete  = 59 // Complete, ready for purge
	SubstateObitSent  = 53 // Obit received and processed
)

// StateNames maps state codes to human-readable names for logging and status output.
var StateNames = map[int]string{
	StateTransit:  "T",
	StateQueued:   "Q",
	StateHeld:     "H",
	StateWaiting:  "W",
	StateRunning:  "R",
	StateExiting:  "E",
	StateComplete: "C",
}

// Job represents a batch job in the server.
type Job struct {
	Mu sync.RWMutex

	// Identity
	ID       string // e.g., "42.servername"
	Name     string // Job_Name
	Owner    string // user@host
	Queue    string // Queue name
	Server   string // Server name
	HashName string // Hash name for file storage

	// State machine
	State    int
	Substate int

	// Execution info
	ExecHost   string // e.g., "node1/0+node1/1"
	ExecPort   int
	SessionID  int
	ExitStatus int

	// Timing
	CreateTime time.Time
	QueueTime  time.Time
	StartTime  time.Time
	CompTime   time.Time
	ModifyTime time.Time
	MTime      time.Time // Last modification

	// Script
	Script     string // Script content
	ScriptFile string // Path on disk

	// Paths
	StdoutPath string // Output_Path
	StderrPath string // Error_Path
	JoinPath   string // Join_Path (oe, eo, n)
	Checkpoint string // Checkpoint attribute

	// Resources
	ResourceReq  map[string]string // Resource_List (requested)
	ResourceUsed map[string]string // resources_used (actual)

	// User info
	EUser   string // Effective user
	EGroup  string // Effective group
	Shell   string // Job shell

	// Attributes (generic key-value for extensibility)
	Attrs map[string]string

	// Variable list (environment)
	VariableList map[string]string

	// Scheduling
	KeepFiles     string // Keep_Files (n, o, e, oe)
	FaultTolerant string
	JobRadix      string
	ReqVersion    string

	// Node assignment
	NodeCount  int
	TaskCount  int
	NeedNodes  string // neednodes spec

	// Flags
	Modified  bool
	FromRoute bool // Job came via routing
}

// NewJob creates a new Job with initialized maps and default state.
func NewJob(id, queue, server string) *Job {
	return &Job{
		ID:           id,
		Queue:        queue,
		Server:       server,
		State:        StateTransit,
		Substate:     SubstateTransitQ,
		CreateTime:   time.Now(),
		ResourceReq:  make(map[string]string),
		ResourceUsed: make(map[string]string),
		Attrs:        make(map[string]string),
		VariableList: make(map[string]string),
	}
}

// StateName returns the single-character state code for display (e.g., "Q", "R", "C").
func (j *Job) StateName() string {
	if name, ok := StateNames[j.State]; ok {
		return name
	}
	return "?"
}

// IsRunning returns true if the job is in Running state.
func (j *Job) IsRunning() bool {
	return j.State == StateRunning
}

// IsQueued returns true if the job is in Queued state.
func (j *Job) IsQueued() bool {
	return j.State == StateQueued
}

// IsComplete returns true if the job is in Complete state.
func (j *Job) IsComplete() bool {
	return j.State == StateComplete
}

// FormatResourceUsed returns resource usage as a map suitable for accounting.
func (j *Job) FormatResourceUsed() map[string]string {
	result := make(map[string]string)
	for k, v := range j.ResourceUsed {
		result[k] = v
	}
	if _, ok := result["walltime"]; !ok && !j.StartTime.IsZero() {
		end := j.CompTime
		if end.IsZero() {
			end = time.Now()
		}
		dur := end.Sub(j.StartTime)
		h := int(dur.Hours())
		m := int(dur.Minutes()) % 60
		s := int(dur.Seconds()) % 60
		result["walltime"] = fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return result
}

// SetState transitions the job to a new state, updating timestamps accordingly.
func (j *Job) SetState(state, substate int) {
	j.State = state
	j.Substate = substate
	j.MTime = time.Now()
	j.Modified = true

	switch state {
	case StateQueued:
		if j.QueueTime.IsZero() {
			j.QueueTime = time.Now()
		}
	case StateRunning:
		j.StartTime = time.Now()
	case StateComplete:
		j.CompTime = time.Now()
	}
}
