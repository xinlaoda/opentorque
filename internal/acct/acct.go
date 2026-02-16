// Package acct implements TORQUE-compatible accounting records.
//
// Accounting records are written to YYYYMMDD-named files in the
// server_priv/accounting/ directory. Each line follows the format:
//
//	MM/DD/YYYY HH:MM:SS;TYPE;JOB_ID;key=value key=value ...
//
// Record types:
//   - Q  Job queued (submitted to a queue)
//   - S  Job started (began execution on a compute node)
//   - E  Job ended (execution completed, resources released)
//   - D  Job deleted (removed by qdel or administrator)
//   - A  Job aborted (terminated abnormally)
//   - R  Job rerun (requeued for another execution)
package acct

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/xinlaoda/opentorque/pkg/pbslog"
)

// Record types matching C TORQUE PBS_ACCT_* constants.
const (
	RecordQueue  = "Q" // Job queued
	RecordStart  = "S" // Job started
	RecordEnd    = "E" // Job ended
	RecordDelete = "D" // Job deleted
	RecordAbort  = "A" // Job aborted
	RecordRerun  = "R" // Job rerun
)

// Logger writes TORQUE-format accounting records to dated files.
type Logger struct {
	dl *pbslog.DatedLog
}

// NewLogger creates an accounting logger that writes to dir/YYYYMMDD files.
func NewLogger(dir string) (*Logger, error) {
	dl, err := pbslog.New(dir)
	if err != nil {
		return nil, fmt.Errorf("acct: %w", err)
	}
	return &Logger{dl: dl}, nil
}

// Close closes the underlying log file.
func (l *Logger) Close() error {
	if l.dl != nil {
		return l.dl.Close()
	}
	return nil
}

// Record writes a single accounting record.
// Format: MM/DD/YYYY HH:MM:SS;TYPE;JOB_ID;message
func (l *Logger) Record(recType, jobID, message string) {
	now := time.Now()
	ts := now.Format("01/02/2006 15:04:05")
	line := fmt.Sprintf("%s;%s;%s;%s\n", ts, recType, jobID, message)
	if _, err := l.dl.Write([]byte(line)); err != nil {
		log.Printf("[ACCT] Error writing accounting record: %v", err)
	}
}

// JobInfo holds job metadata used to build accounting record messages.
type JobInfo struct {
	User       string // Job owner (user@host -> user)
	Group      string // Effective group
	Account    string // Account string (optional)
	JobName    string // Job_Name
	Queue      string // Queue name
	CreateTime int64  // ctime (Unix timestamp)
	QueueTime  int64  // qtime (Unix timestamp)
	EligTime   int64  // etime (eligible time, Unix timestamp)
	StartTime  int64  // start (Unix timestamp)
	EndTime    int64  // end (Unix timestamp, for E records)
	ExitStatus int    // Exit_status
	ExecHost   string // exec_host (node/slot assignments)
	SessionID  int    // session (session ID on compute node)

	// Requested resources (Resource_List.*)
	ResourceReq map[string]string
	// Used resources (resources_used.*)
	ResourceUsed map[string]string
}

// extractUser strips "@host" from "user@host" format.
func extractUser(owner string) string {
	if idx := strings.Index(owner, "@"); idx >= 0 {
		return owner[:idx]
	}
	return owner
}

// RecordQueued writes a Q record when a job is submitted to a queue.
func (l *Logger) RecordQueued(jobID string, info *JobInfo) {
	msg := fmt.Sprintf("user=%s group=%s jobname=%s queue=%s ctime=%d qtime=%d",
		extractUser(info.User), info.Group, info.JobName, info.Queue,
		info.CreateTime, info.QueueTime)

	// Append requested resources
	msg += formatResourceReq(info.ResourceReq)

	l.Record(RecordQueue, jobID, msg)
}

// RecordStarted writes an S record when a job begins execution.
func (l *Logger) RecordStarted(jobID string, info *JobInfo) {
	msg := fmt.Sprintf("user=%s group=%s jobname=%s queue=%s ctime=%d qtime=%d etime=%d start=%d exec_host=%s",
		extractUser(info.User), info.Group, info.JobName, info.Queue,
		info.CreateTime, info.QueueTime, info.EligTime, info.StartTime,
		info.ExecHost)

	if info.Account != "" {
		msg += fmt.Sprintf(" account=%s", info.Account)
	}

	// Append requested resources
	msg += formatResourceReq(info.ResourceReq)

	l.Record(RecordStart, jobID, msg)
}

// RecordEnded writes an E record when a job completes execution.
// This is the most detailed record, including all resource usage.
func (l *Logger) RecordEnded(jobID string, info *JobInfo) {
	msg := fmt.Sprintf("user=%s group=%s jobname=%s queue=%s ctime=%d qtime=%d etime=%d start=%d end=%d Exit_status=%d",
		extractUser(info.User), info.Group, info.JobName, info.Queue,
		info.CreateTime, info.QueueTime, info.EligTime, info.StartTime,
		info.EndTime, info.ExitStatus)

	if info.ExecHost != "" {
		msg += fmt.Sprintf(" exec_host=%s", info.ExecHost)
	}
	if info.SessionID != 0 {
		msg += fmt.Sprintf(" session=%d", info.SessionID)
	}
	if info.Account != "" {
		msg += fmt.Sprintf(" account=%s", info.Account)
	}

	// Append requested resources (Resource_List.*)
	msg += formatResourceReq(info.ResourceReq)

	// Append used resources (resources_used.*)
	msg += formatResourceUsed(info.ResourceUsed)

	l.Record(RecordEnd, jobID, msg)
}

// RecordDeleted writes a D record when a job is deleted via qdel.
func (l *Logger) RecordDeleted(jobID string, info *JobInfo) {
	msg := fmt.Sprintf("user=%s group=%s jobname=%s queue=%s ctime=%d qtime=%d",
		extractUser(info.User), info.Group, info.JobName, info.Queue,
		info.CreateTime, info.QueueTime)

	if info.ExecHost != "" {
		msg += fmt.Sprintf(" exec_host=%s", info.ExecHost)
	}

	l.Record(RecordDelete, jobID, msg)
}

// RecordAborted writes an A record when a job is terminated abnormally.
func (l *Logger) RecordAborted(jobID string, info *JobInfo) {
	msg := fmt.Sprintf("user=%s group=%s jobname=%s queue=%s ctime=%d qtime=%d Exit_status=%d",
		extractUser(info.User), info.Group, info.JobName, info.Queue,
		info.CreateTime, info.QueueTime, info.ExitStatus)

	if info.ExecHost != "" {
		msg += fmt.Sprintf(" exec_host=%s", info.ExecHost)
	}

	l.Record(RecordAbort, jobID, msg)
}

// RecordRerun writes an R record when a job is requeued for execution.
func (l *Logger) RecordRerun(jobID string, info *JobInfo) {
	msg := fmt.Sprintf("user=%s group=%s jobname=%s queue=%s ctime=%d qtime=%d",
		extractUser(info.User), info.Group, info.JobName, info.Queue,
		info.CreateTime, info.QueueTime)

	l.Record(RecordRerun, jobID, msg)
}

// formatResourceReq formats requested resources as " Resource_List.key=value" pairs.
func formatResourceReq(req map[string]string) string {
	if len(req) == 0 {
		return ""
	}
	var sb strings.Builder
	for k, v := range req {
		sb.WriteString(fmt.Sprintf(" Resource_List.%s=%s", k, v))
	}
	return sb.String()
}

// formatResourceUsed formats used resources as " resources_used.key=value" pairs.
func formatResourceUsed(used map[string]string) string {
	if len(used) == 0 {
		return ""
	}
	var sb strings.Builder
	for k, v := range used {
		sb.WriteString(fmt.Sprintf(" resources_used.%s=%s", k, v))
	}
	return sb.String()
}
