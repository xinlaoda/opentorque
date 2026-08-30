package server

import (
	"net"
	"testing"
	"time"

	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/job"
)

// TestPromoteWaitingJobs verifies the server-side (mode-independent) watchdog
// promotes deferred (Waiting) jobs to Queued once their execution time passes,
// and leaves future or already-queued jobs untouched. With the built-in
// scheduler removed, this is the only path by which a `-a` (deferred) job is
// made visible to the external pbs_sched for placement.
func TestPromoteWaitingJobs(t *testing.T) {
	s, jm, _ := newMoveServer(t)
	now := time.Now()

	due := job.NewJob("1.srv", "batch", "srv")
	due.SetState(job.StateWaiting, job.SubstateWaiting)
	due.ExecutionTime = now.Add(-time.Minute) // already due
	jm.AddJob(due)

	future := job.NewJob("2.srv", "batch", "srv")
	future.SetState(job.StateWaiting, job.SubstateWaiting)
	future.ExecutionTime = now.Add(time.Hour) // not yet due
	jm.AddJob(future)

	queued := job.NewJob("3.srv", "batch", "srv")
	queued.SetState(job.StateQueued, job.SubstateQueued)
	queued.ExecutionTime = now.Add(-time.Minute) // past, but already queued: must stay
	jm.AddJob(queued)

	s.promoteWaitingJobs()

	if due.State != job.StateQueued {
		t.Fatalf("due Waiting job 1: state = %d, want Queued", due.State)
	}
	if future.State != job.StateWaiting {
		t.Fatalf("future Waiting job 2: state = %d, want Waiting", future.State)
	}
	if queued.State != job.StateQueued {
		t.Fatalf("already-queued job 3: state = %d, want Queued", queued.State)
	}
}

// TestSchedulerDefaultsExternal asserts that pbs_server defaults to the external
// scheduler when there is no sched_config, and that the trigger port matches
// pbs_sched's default so event-driven scheduling + the health warning work
// out of the box.
func TestSchedulerDefaultsExternal(t *testing.T) {
	cfg := config.NewConfig("/var/spool/torque")
	if cfg.SchedulerMode != "external" {
		t.Fatalf("SchedulerMode = %q, want external", cfg.SchedulerMode)
	}
	if cfg.SchedTriggerPort != 25003 {
		t.Fatalf("SchedTriggerPort = %d, want 25003", cfg.SchedTriggerPort)
	}
	if !cfg.EventDriven {
		t.Fatal("EventDriven should default to true")
	}
}

// TestExternalSchedReachableTrue verifies externalSchedReachable reports a live
// listener (used by the "external scheduler is running" path). A plain connect
// probe succeeds against a listening socket without needing an accept.
func TestExternalSchedReachableTrue(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	s, _, _ := newMoveServer(t)
	s.cfg.SchedTriggerPort = port
	if !s.externalSchedReachable() {
		t.Fatal("externalSchedReachable = false, want true for a live listener")
	}
}

// TestTriggerSchedNotifiesExternalScheduler verifies that a job/node event
// (triggerSched) writes the 1-byte marker pbs_sched listens for on the trigger
// port, making external scheduling event-driven rather than polling-only.
func TestTriggerSchedNotifiesExternalScheduler(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	s, _, _ := newMoveServer(t)
	s.cfg.SchedulerMode = "external"
	s.cfg.EventDriven = true
	s.cfg.SchedTriggerPort = port

	got := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer conn.Close()
		buf := make([]byte, 1)
		n, _ := conn.Read(buf)
		got <- buf[:n]
	}()

	s.triggerSched()
	select {
	case marker := <-got:
		if len(marker) != 1 || marker[0] != 1 {
			t.Fatalf("trigger marker = %v, want [1]", marker)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the external-scheduler trigger marker")
	}
}

// TestExternalSchedReachableFalseWhenDown verifies the health warning path: a
// port with no listener reports the scheduler is NOT reachable.
func TestExternalSchedReachableFalseWhenDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // free the port so nothing is listening

	s, _, _ := newMoveServer(t)
	s.cfg.SchedTriggerPort = port
	if s.externalSchedReachable() {
		t.Fatal("externalSchedReachable = true for a closed port, want false")
	}
}
