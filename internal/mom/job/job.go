package job

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// State represents the overall job state.
type State int

const (
	StateTransit  State = 0
	StateQueued   State = 1
	StateHeld     State = 2
	StateWaiting  State = 3
	StateRunning  State = 4
	StateExiting  State = 5
	StateComplete State = 6
)

func (s State) String() string {
	names := map[State]string{
		StateTransit:  "TRANSIT",
		StateQueued:   "QUEUED",
		StateHeld:     "HELD",
		StateWaiting:  "WAITING",
		StateRunning:  "RUNNING",
		StateExiting:  "EXITING",
		StateComplete: "COMPLETE",
	}
	if name, ok := names[s]; ok {
		return name
	}
	return "UNKNOWN"
}

// Substate provides fine-grained job state.
type Substate int

const (
	SubstateTransit     Substate = 0
	SubstateQueued      Substate = 10
	SubstateHeld        Substate = 20
	SubstateWaiting     Substate = 30
	SubstatePrerun      Substate = 41
	SubstateStarting    Substate = 42
	SubstateRunning     Substate = 42
	SubstateExiting     Substate = 50
	SubstateStageOut    Substate = 51
	SubstateStagedOut   Substate = 52
	SubstateReturnFiles Substate = 54
	SubstatePreObit     Substate = 57
	SubstateObit        Substate = 58
	SubstateComplete    Substate = 59
)

// Task represents a process within a job.
type Task struct {
	ID       int
	ParentID string // job ID
	State    TaskState
	Pid      int // OS process ID
	Sid      int // Session ID
	ExitCode int
	Started  time.Time
	Ended    time.Time
}

// TaskState represents a task's lifecycle state.
type TaskState int

const (
	TaskEmbryo  TaskState = 0
	TaskRunning TaskState = 1
	TaskExited  TaskState = 2
	TaskDead    TaskState = 3
)

// Resources tracks resource usage for a job.
type Resources struct {
	CPUTime    time.Duration // cumulative CPU time
	WallTime   time.Duration // wall clock time
	Memory     int64         // current memory usage (bytes)
	MaxMemory  int64         // peak memory usage (bytes)
	VMem       int64         // virtual memory (bytes)
	MaxVMem    int64         // peak virtual memory (bytes)
}

// ResourceLimits are the requested resource limits.
type ResourceLimits struct {
	CPUTime  time.Duration
	WallTime time.Duration
	Memory   int64 // bytes
	VMem     int64 // bytes
	Ncpus    int
	Nodes    int
}

// Job represents a PBS job on this MOM.
type Job struct {
	Mu sync.RWMutex

	// Identity
	ID       string // e.g., "123.server"
	Name     string // user-specified job name
	Queue    string

	// Owner
	Owner    string // user@host
	User     string // Unix username
	Group    string // Unix group
	JobOwner string // submitting user

	// State
	State    State
	Substate Substate

	// Execution
	Shell    string
	Script   string // job script content
	ScriptFile string // path to script file on disk
	ExecHost string   // execution host list
	JoinPath string   // stdout/stderr join mode

	// File paths
	StdoutPath  string // spool path during execution
	StderrPath  string // spool path during execution
	FinalStdout string // final delivery destination
	FinalStderr string // final delivery destination
	CheckpointDir string
	SpoolDir   string

	// Environment
	VariableList map[string]string

	// Resources
	ResourceReq ResourceLimits // requested
	ResourceUse Resources      // actual usage

	// Tasks (processes)
	Tasks []*Task

	// Timing
	QueueTime  time.Time
	StartTime  time.Time
	EndTime    time.Time
	MTime      time.Time // last modified

	// Exit info
	ExitStatus int
	ExitSignal int

	// Job flags
	Interactive bool
	Rerunnable  bool
	Checkpoint  string // checkpoint mode

	// Internal
	Process    *os.Process
	SessionID  int
}

// NewJob creates a new job with the given ID.
func NewJob(id string) *Job {
	return &Job{
		ID:           id,
		State:        StateQueued,
		Substate:     SubstateQueued,
		QueueTime:    time.Now(),
		MTime:        time.Now(),
		VariableList: make(map[string]string),
		Tasks:        make([]*Task, 0),
	}
}

// SetState updates job state.
func (j *Job) SetState(state State, substate Substate) {
	j.Mu.Lock()
	defer j.Mu.Unlock()
	j.State = state
	j.Substate = substate
	j.MTime = time.Now()
}

// GetState returns current state.
func (j *Job) GetState() (State, Substate) {
	j.Mu.RLock()
	defer j.Mu.RUnlock()
	return j.State, j.Substate
}

// AddTask adds a task to the job.
func (j *Job) AddTask(pid, sid int) *Task {
	j.Mu.Lock()
	defer j.Mu.Unlock()
	t := &Task{
		ID:       len(j.Tasks),
		ParentID: j.ID,
		State:    TaskRunning,
		Pid:      pid,
		Sid:      sid,
		Started:  time.Now(),
	}
	j.Tasks = append(j.Tasks, t)
	return t
}

// IsRunning returns true if the job is in running state.
func (j *Job) IsRunning() bool {
	j.Mu.RLock()
	defer j.Mu.RUnlock()
	return j.State == StateRunning
}

// IsExiting returns true if the job is in exiting state (finished but not yet processed).
func (j *Job) IsExiting() bool {
	j.Mu.RLock()
	defer j.Mu.RUnlock()
	return j.State == StateExiting
}

// StatusString returns a single-character status.
func (j *Job) StatusString() string {
	j.Mu.RLock()
	defer j.Mu.RUnlock()
	switch j.State {
	case StateQueued:
		return "Q"
	case StateRunning:
		return "R"
	case StateHeld:
		return "H"
	case StateExiting:
		return "E"
	case StateComplete:
		return "C"
	default:
		return "?"
	}
}

// FormatResourceUsed formats resource usage for reporting.
func (j *Job) FormatResourceUsed() map[string]string {
	j.Mu.RLock()
	defer j.Mu.RUnlock()

	res := make(map[string]string)
	res["cput"] = formatDuration(j.ResourceUse.CPUTime)
	res["walltime"] = formatDuration(j.ResourceUse.WallTime)
	res["mem"] = fmt.Sprintf("%dkb", j.ResourceUse.MaxMemory/1024)
	res["vmem"] = fmt.Sprintf("%dkb", j.ResourceUse.MaxVMem/1024)
	return res
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
