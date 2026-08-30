package server

import (
	"net"
	"testing"

	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/job"
	momdis "github.com/xinlaoda/opentorque/internal/mom/dis"
	"github.com/xinlaoda/opentorque/internal/node"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// fakeMom is an in-process MOM that answers a BatchMomStatus "jobs" query with
// the supplied jobs text, using the real MOM wire protocol (internal/mom/dis).
func fakeMom(t *testing.T, jobsText string) (port int, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeMom listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := momdis.NewReader(conn)
				if _, err := momdis.ReadRequestHeader(r); err != nil {
					return
				}
				_, _ = r.ReadString() // attribute name ("jobs")
				_, _ = r.ReadUint()   // request extension
				_ = momdis.SendTextReply(conn, jobsText)
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

// newReconcileServer builds a Server with in-memory managers for reconciliation
// tests. The node added later is addressed at 127.0.0.1:<fake mom port>.
func newReconcileServer(t *testing.T) *Server {
	return &Server{
		cfg:      &config.Config{JobsDir: t.TempDir(), ServerName: "srv"},
		jobMgr:   job.NewManager("srv", 1),
		queueMgr: queue.NewManager(),
		nodeMgr:  node.NewManager(),
	}
}

// addRecoveredRunningJob registers a job that was recovered from disk as Running
// on node "srv" (its exec host), mirroring what recoverJobs/restore produce.
func addRecoveredRunningJob(s *Server, id string) *job.Job {
	j := job.NewJob(id, "batch", "srv")
	j.Mu.Lock()
	j.State = job.StateRunning
	j.ExecHost = "srv/0"
	j.Mu.Unlock()
	s.jobMgr.AddJob(j)
	return j
}

// TestReconcileRequeuesWhenMomConfirmsNotRunning verifies a Running job is
// requeued ONLY when its MOM is reachable and explicitly reports it is NOT
// running any more (its process is gone -> it can be re-scheduled).
func TestReconcileRequeuesWhenMomConfirmsNotRunning(t *testing.T) {
	port, closeFn := fakeMom(t, "") // reachable, running nothing
	defer closeFn()

	s := newReconcileServer(t)
	n := s.nodeMgr.AddNode("srv", 2)
	n.IP = "127.0.0.1"
	n.MomPort = port
	j := addRecoveredRunningJob(s, "1.srv")

	s.reconcileRunningJobsWithMOMs()

	j.Mu.RLock()
	state := j.State
	execHost := j.ExecHost
	j.Mu.RUnlock()
	if state != job.StateQueued {
		t.Fatalf("job state = %d, want Queued after MOM confirmed not running", state)
	}
	if execHost != "" {
		t.Fatalf("ExecHost = %q, want cleared", execHost)
	}
}

// TestReconcileKeepsRunningWhenMomConfirmsRunning verifies a job that the MOM
// says is still running is left Running (never re-dispatched -> no double exec).
func TestReconcileKeepsRunningWhenMomConfirmsRunning(t *testing.T) {
	port, closeFn := fakeMom(t, "1.srv") // reachable, still running 1.srv
	defer closeFn()

	s := newReconcileServer(t)
	n := s.nodeMgr.AddNode("srv", 2)
	n.IP = "127.0.0.1"
	n.MomPort = port
	j := addRecoveredRunningJob(s, "1.srv")

	s.reconcileRunningJobsWithMOMs()

	j.Mu.RLock()
	state := j.State
	execHost := j.ExecHost
	j.Mu.RUnlock()
	if state != job.StateRunning {
		t.Fatalf("job state = %d, want Running (MOM confirmed it is running)", state)
	}
	if execHost == "" {
		t.Fatalf("ExecHost cleared for a still-running job")
	}
}

// TestReconcileKeepsRunningWhenMomUnreachable verifies the safety invariant:
// when a MOM cannot be reached we do NOT requeue (a live copy may still exist),
// leaving the node-down path (TODO 2.10) to decide later.
func TestReconcileKeepsRunningWhenMomUnreachable(t *testing.T) {
	port, closeFn := fakeMom(t, "")
	closeFn() // now nothing listens on the port

	s := newReconcileServer(t)
	n := s.nodeMgr.AddNode("srv", 2)
	n.IP = "127.0.0.1"
	n.MomPort = port
	j := addRecoveredRunningJob(s, "1.srv")

	s.reconcileRunningJobsWithMOMs()

	j.Mu.RLock()
	state := j.State
	j.Mu.RUnlock()
	if state != job.StateRunning {
		t.Fatalf("job state = %d, want Running when MOM is unreachable", state)
	}
}

// TestRestoreRecoveredRunningJobs verifies slot accounting is restored for a
// recovered Running job so the scheduler does not treat its node as free.
func TestRestoreRecoveredRunningJobs(t *testing.T) {
	s := newReconcileServer(t)
	n := s.nodeMgr.AddNode("srv", 2)
	j := addRecoveredRunningJob(s, "1.srv")

	s.restoreRecoveredRunningJobs()

	if n.SlotsUsed != 1 {
		t.Fatalf("SlotsUsed = %d, want 1 after restoring the running job", n.SlotsUsed)
	}
	found := false
	for _, jid := range n.AssignedJobs {
		if jid == j.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("node does not track recovered running job %s", j.ID)
	}
}
