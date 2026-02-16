// Package drmaa provides a Go implementation of the DRMAA v1 API
// (Distributed Resource Management Application API).
//
// DRMAA is an OGF (Open Grid Forum) standard that provides a uniform
// interface for submitting and controlling jobs on batch systems.
// This implementation connects to a PBS/TORQUE-compatible server
// using the DIS protocol with token authentication.
//
// Basic usage:
//
//	session, err := drmaa.NewSession("")
//	if err != nil { log.Fatal(err) }
//	defer session.Close()
//
//	jt, _ := session.AllocateJobTemplate()
//	jt.SetRemoteCommand("/bin/sleep")
//	jt.SetArgs([]string{"60"})
//	jt.SetJobName("my_job")
//
//	jobID, err := session.RunJob(jt)
//	fmt.Println("Submitted:", jobID)
//
//	status, _ := session.Wait(jobID, drmaa.TimeoutWaitForever)
//	fmt.Println("Exit code:", status.ExitStatus)
package drmaa

import (
	"fmt"
	"sync"
	"time"

	"github.com/xinlaoda/opentorque/internal/cli/client"
	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

// DRMAA error codes per the OGF specification.
const (
	ErrSuccess              = 0
	ErrInternalError        = 1
	ErrDrmCommunicationFail = 2
	ErrAuthFailure          = 3
	ErrInvalidArgument      = 4
	ErrNoActiveSession      = 5
	ErrNoMemory             = 6
	ErrInvalidContactString = 7
	ErrDefaultContactString = 8
	ErrDrmSystemInit        = 9
	ErrAlreadyActiveSession = 10
	ErrInvalidJobTemplate   = 11
	ErrInvalidJob           = 12
	ErrExitTimeout          = 13
	ErrNoRusage             = 14
	ErrNoMoreElements       = 15
)

// Job state constants.
const (
	JobStateUndetermined    = 0x00
	JobStateQueued          = 0x10
	JobStateSystemOnHold    = 0x11
	JobStateUserOnHold      = 0x12
	JobStateUserSystemOnHold = 0x13
	JobStateRunning         = 0x20
	JobStateSystemSuspended = 0x21
	JobStateUserSuspended   = 0x22
	JobStateDone            = 0x30
	JobStateFailed          = 0x40
)

const (
	TimeoutWaitForever = -1
	TimeoutNoWait      = 0
)

// Session represents an active DRMAA session connected to a PBS server.
type Session struct {
	mu     sync.Mutex
	conn   *client.Conn
	active bool
}

// JobTemplate holds job submission parameters.
type JobTemplate struct {
	remoteCommand string
	args          []string
	jobName       string
	queue         string
	workingDir    string
	outputPath    string
	errorPath     string
	email         []string
	nativeSpec    string
	env           map[string]string
	resources     map[string]string
}

// JobStatus contains the result of waiting for a job to complete.
type JobStatus struct {
	JobID      string
	HasExited  bool
	ExitStatus int
	HasSignal  bool
	Signal     string
	WasAborted bool
}

// NewSession creates a new DRMAA session and connects to the PBS server.
// Pass an empty contact string to use the default server.
func NewSession(contact string) (*Session, error) {
	server := contact
	conn, err := client.Connect(server)
	if err != nil {
		return nil, fmt.Errorf("drmaa: cannot connect to server: %w", err)
	}
	return &Session{conn: conn, active: true}, nil
}

// Close terminates the DRMAA session and releases resources.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil
	}
	s.active = false
	return s.conn.Close()
}

// AllocateJobTemplate creates a new empty job template.
func (s *Session) AllocateJobTemplate() (*JobTemplate, error) {
	if !s.active {
		return nil, fmt.Errorf("drmaa: no active session")
	}
	return &JobTemplate{
		env:       make(map[string]string),
		resources: make(map[string]string),
	}, nil
}

// SetRemoteCommand sets the executable path for the job.
func (jt *JobTemplate) SetRemoteCommand(cmd string) { jt.remoteCommand = cmd }

// SetArgs sets the command-line arguments for the job.
func (jt *JobTemplate) SetArgs(args []string) { jt.args = args }

// SetJobName sets the name of the job.
func (jt *JobTemplate) SetJobName(name string) { jt.jobName = name }

// SetQueue sets the destination queue.
func (jt *JobTemplate) SetQueue(queue string) { jt.queue = queue }

// SetWorkingDir sets the working directory for the job.
func (jt *JobTemplate) SetWorkingDir(dir string) { jt.workingDir = dir }

// SetOutputPath sets the path for stdout.
func (jt *JobTemplate) SetOutputPath(path string) { jt.outputPath = path }

// SetErrorPath sets the path for stderr.
func (jt *JobTemplate) SetErrorPath(path string) { jt.errorPath = path }

// SetNativeSpec sets PBS-specific native specification string.
func (jt *JobTemplate) SetNativeSpec(spec string) { jt.nativeSpec = spec }

// SetResource sets a resource requirement (e.g., "walltime", "1:00:00").
func (jt *JobTemplate) SetResource(name, value string) { jt.resources[name] = value }

// SetEnv sets an environment variable for the job.
func (jt *JobTemplate) SetEnv(key, value string) { jt.env[key] = value }

// RunJob submits a job based on the template and returns the job ID.
func (s *Session) RunJob(jt *JobTemplate) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return "", fmt.Errorf("drmaa: no active session")
	}

	// Build job script from command and args
	script := "#!/bin/bash\n"
	if jt.workingDir != "" {
		script += fmt.Sprintf("cd %s\n", jt.workingDir)
	}
	for k, v := range jt.env {
		script += fmt.Sprintf("export %s=%q\n", k, v)
	}
	script += jt.remoteCommand
	for _, arg := range jt.args {
		script += " " + arg
	}
	script += "\n"

	// Build attributes
	var attrs []dis.SvrAttrl
	if jt.jobName != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Job_Name", Value: jt.jobName})
	}
	if jt.outputPath != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Output_Path", Value: jt.outputPath})
	}
	if jt.errorPath != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Error_Path", Value: jt.errorPath})
	}
	for resc, val := range jt.resources {
		attrs = append(attrs, dis.SvrAttrl{Name: "Resource_List", Resc: resc, HasResc: true, Value: val})
	}

	queue := jt.queue
	if queue == "" {
		queue = "batch"
	}

	return s.conn.SubmitJob(queue, attrs, script)
}

// JobStatus queries the status of a job.
func (s *Session) GetJobStatus(jobID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return JobStateUndetermined, fmt.Errorf("drmaa: no active session")
	}

	objs, err := s.conn.StatusJob(jobID)
	if err != nil {
		return JobStateUndetermined, err
	}

	if len(objs) == 0 {
		return JobStateDone, nil // Job no longer exists, assume done
	}

	for _, attr := range objs[0].Attrs {
		if attr.Name == "job_state" {
			switch attr.Value {
			case "Q":
				return JobStateQueued, nil
			case "H":
				return JobStateUserOnHold, nil
			case "R":
				return JobStateRunning, nil
			case "C":
				return JobStateDone, nil
			}
		}
	}
	return JobStateUndetermined, nil
}

// Wait waits for a job to complete, up to the specified timeout in seconds.
// Use TimeoutWaitForever (-1) to wait indefinitely.
func (s *Session) Wait(jobID string, timeout int) (*JobStatus, error) {
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(time.Duration(timeout) * time.Second)
	}

	for {
		state, err := s.GetJobStatus(jobID)
		if err != nil {
			return nil, err
		}

		if state == JobStateDone || state == JobStateFailed {
			return &JobStatus{
				JobID:      jobID,
				HasExited:  true,
				ExitStatus: 0,
			}, nil
		}

		if timeout == TimeoutNoWait {
			return nil, fmt.Errorf("drmaa: job %s not finished (state=%d)", jobID, state)
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf("drmaa: timeout waiting for job %s", jobID)
		}

		time.Sleep(2 * time.Second)
	}
}

// Control sends a control action to a job (hold, release, terminate, suspend, resume).
func (s *Session) Control(jobID string, action int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return fmt.Errorf("drmaa: no active session")
	}

	switch action {
	case ActionSuspend:
		return s.conn.SignalJob(jobID, "SIGSTOP")
	case ActionResume:
		return s.conn.SignalJob(jobID, "SIGCONT")
	case ActionHold:
		return s.conn.HoldJob(jobID, "u")
	case ActionRelease:
		return s.conn.ReleaseJob(jobID, "u")
	case ActionTerminate:
		return s.conn.DeleteJob(jobID)
	default:
		return fmt.Errorf("drmaa: unknown action %d", action)
	}
}

// Control action constants.
const (
	ActionSuspend   = 0
	ActionResume    = 1
	ActionHold      = 2
	ActionRelease   = 3
	ActionTerminate = 4
)
