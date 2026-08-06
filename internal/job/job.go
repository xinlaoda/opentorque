// Package job implements the Job data model and state machine for pbs_server.
package job

import (
	"fmt"
	"strconv"
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
	StateProvisioning = 7 // Cloud-provisioning (VM booting, between Q and R)
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
	StateProvisioning: "D", // D = dispatching/provisioning
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
	CreateTime    time.Time
	QueueTime     time.Time
	StartTime     time.Time
	CompTime      time.Time
	ModifyTime    time.Time
	MTime         time.Time // Last modification
	ExecutionTime time.Time // -a: deferred execution time

	// Script
	Script     string // Script content
	ScriptFile string // Path on disk
	ScriptArgs string // -F: arguments passed to script

	// Paths
	StdoutPath string // Output_Path
	StderrPath string // Error_Path
	JoinPath   string // Join_Path (oe, eo, n)
	Checkpoint string // Checkpoint attribute

	// Resources
	ResourceReq  map[string]string // Resource_List (requested)
	ResourceUsed map[string]string // resources_used (actual)

	// User info
	EUser     string // Effective user
	EGroup    string // Effective group
	Shell     string // Job shell (-S)
	UserList  string // -u: user list for job ownership
	Account   string // -A: account string

	// Attributes (generic key-value for extensibility)
	Attrs map[string]string

	// Variable list (environment)
	VariableList map[string]string

	// Scheduling
	KeepFiles     string // Keep_Files (n, o, e, oe)
	FaultTolerant string
	JobRadix      string
	ReqVersion    string
	Priority      int    // -p: job priority (-1024 to 1023)
	Rerunnable    string // -r: y or n
	MailPoints    string // -m: mail event flags
	MailUsers     string // -M: mail recipient list
	Comment       string // job comment
	DependList    string // -W depend=: job dependencies
	StageinList   string // -W stagein=: file staging
	StageoutList  string // -W stageout=: file staging
	GroupList     string // -W group_list=: group list
	JobArrayReq   string // -t: job array range
	InitWorkDir   string // -d: initial working directory
	RootDir       string // -D: root (chroot) directory

	// Node assignment
	NodeCount  int
	TaskCount  int
	NeedNodes  string // neednodes spec

	// Cloud provisioning binding (cloud elasticity)
	// ProvisionVM holds the stable cloud VM id the job is bound to while its
	// VM boots; ProvisionNode is the provisional node name. Persisted in .JB.
	ProvisionVM   string
	ProvisionNode string

	// Flags
	Modified  bool
	FromRoute bool // Job came via routing
	HoldTypes string // -h: hold types (u=user, o=other, s=system)
	Interactive bool // -I: interactive job
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

// IsProvisioning returns true if the job is in the cloud-provisioning state.
func (j *Job) IsProvisioning() bool {
	return j.State == StateProvisioning
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


// CloneForArray returns a copy of the job for one element of a job array
// (TODO 3.3). The copy carries the same script/attrs/resources but a fresh
// identity/state and a single-index JobArrayReq so it behaves like a normal
// queued job elsewhere. The returned job is NOT registered with any manager.
func (j *Job) CloneForArray(index int, server string) *Job {
	c := &Job{
		ID:           j.ID,
		Name:         j.Name,
		Owner:        j.Owner,
		Queue:        j.Queue,
		Server:       j.Server,
		HashName:     j.HashName,
		State:        StateTransit,
		Substate:     SubstateTransitQ,
		ExecHost:     j.ExecHost,
		ExecPort:     j.ExecPort,
		SessionID:    j.SessionID,
		ExitStatus:   j.ExitStatus,
		CreateTime:   time.Now(),
		Script:       j.Script,
		ScriptFile:   j.ScriptFile,
		ScriptArgs:   j.ScriptArgs,
		StdoutPath:   j.StdoutPath,
		StderrPath:   j.StderrPath,
		JoinPath:     j.JoinPath,
		Checkpoint:   j.Checkpoint,
		EUser:        j.EUser,
		EGroup:       j.EGroup,
		Shell:        j.Shell,
		UserList:     j.UserList,
		Account:      j.Account,
		KeepFiles:    j.KeepFiles,
		FaultTolerant: j.FaultTolerant,
		JobRadix:     j.JobRadix,
		ReqVersion:   j.ReqVersion,
		Priority:     j.Priority,
		Rerunnable:   j.Rerunnable,
		MailPoints:   j.MailPoints,
		MailUsers:    j.MailUsers,
		Comment:      j.Comment,
		DependList:   j.DependList,
		StageinList:  j.StageinList,
		StageoutList: j.StageoutList,
		GroupList:    j.GroupList,
		JobArrayReq:  strconv.Itoa(index),
		InitWorkDir:  j.InitWorkDir,
		RootDir:      j.RootDir,
		NodeCount:    j.NodeCount,
		TaskCount:    j.TaskCount,
		NeedNodes:    j.NeedNodes,
		ProvisionVM:  j.ProvisionVM,
		ProvisionNode: j.ProvisionNode,
		Modified:     true,
		FromRoute:    j.FromRoute,
		HoldTypes:    j.HoldTypes,
		Interactive:  j.Interactive,
	}
	c.ResourceReq = make(map[string]string, len(j.ResourceReq))
	for k, v := range j.ResourceReq {
		c.ResourceReq[k] = v
	}
	c.ResourceUsed = make(map[string]string, len(j.ResourceUsed))
	for k, v := range j.ResourceUsed {
		c.ResourceUsed[k] = v
	}
	c.Attrs = make(map[string]string, len(j.Attrs))
	for k, v := range j.Attrs {
		c.Attrs[k] = v
	}
	c.VariableList = make(map[string]string, len(j.VariableList))
	for k, v := range j.VariableList {
		c.VariableList[k] = v
	}
	c.Server = server
	return c
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
