package server

import (
	"testing"

	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/queue"
)

func TestParseList(t *testing.T) {
	if got := queue.ParseList("a, a, b ,b"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ParseList dedupe = %v", got)
	}
	if got := queue.ParseList("  x y ,z "); len(got) != 3 {
		t.Fatalf("ParseList space split = %v", got)
	}
	if got := queue.ParseList(" , "); len(got) != 0 {
		t.Fatalf("ParseList empty = %v", got)
	}
}

func testAdmitQueue(t *testing.T) *queue.Queue {
	return queue.NewQueue("batch", queue.TypeExecution)
}

func TestAdmitQueueDisabled(t *testing.T) {
	q := testAdmitQueue(t)
	q.Enabled = false
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "batch", "srv")
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("expected disabled queue to reject")
	}
}

func TestAdmitQueueNotStarted(t *testing.T) {
	q := testAdmitQueue(t)
	q.Started = false
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "batch", "srv")
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("expected not-started queue to reject")
	}
}

func TestAdmitQueueMaxQueuable(t *testing.T) {
	q := testAdmitQueue(t)
	q.MaxJobs = 1
	jm := job.NewManager("srv", 1)
	// one job already queued in this queue
	existing := job.NewJob("1.srv", "batch", "srv")
	existing.SetState(job.StateQueued, job.SubstateQueued)
	jm.AddJob(existing)
	rj := job.NewJob("2.srv", "batch", "srv")
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("expected max_queuable to reject")
	}
}

func TestAdmitQueueFromRouteOnly(t *testing.T) {
	q := testAdmitQueue(t)
	q.Attrs["from_route_only"] = "True"
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "batch", "srv")
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("expected from_route_only to reject direct submission")
	}
	if err := admitToQueue(jm, q, rj, "alice", true); err != nil {
		t.Fatalf("expected routed job to pass from_route_only, got %v", err)
	}
}

func TestAdmitQueueACLUser(t *testing.T) {
	q := testAdmitQueue(t)
	q.ACLUserEnabled = true
	q.ACLUsers = []string{"alice"}
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "batch", "srv")
	if err := admitToQueue(jm, q, rj, "bob@host", false); err == nil {
		t.Fatal("expected ACL to reject bob")
	}
	if err := admitToQueue(jm, q, rj, "alice@host", false); err != nil {
		t.Fatalf("expected ACL to accept alice, got %v", err)
	}
}

func TestAdmitQueueResourceMax(t *testing.T) {
	q := testAdmitQueue(t)
	q.ResourceMax["ncpus"] = "2"
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "batch", "srv")
	rj.ResourceReq["ncpus"] = "4"
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("expected ncpus=4 to exceed max 2")
	}
	rj.ResourceReq["ncpus"] = "1"
	if err := admitToQueue(jm, q, rj, "alice", false); err != nil {
		t.Fatalf("expected ncpus=1 to pass, got %v", err)
	}
}

func TestAdmitQueueResourceMinWalltime(t *testing.T) {
	q := testAdmitQueue(t)
	q.ResourceMin["walltime"] = "00:30:00"
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "batch", "srv")
	rj.ResourceReq["walltime"] = "00:10:00"
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("expected walltime below min to reject")
	}
	rj.ResourceReq["walltime"] = "01:00:00"
	if err := admitToQueue(jm, q, rj, "alice", false); err != nil {
		t.Fatalf("expected walltime above min to pass, got %v", err)
	}
}

func TestRouteJobPicksFirstAccepting(t *testing.T) {
	qm := queue.NewManager()
	qm.AddQueue(queue.NewQueue("route_q", queue.TypeRoute))
	// set route destinations
	rq := qm.GetQueue("route_q")
	rq.RouteDestin = []string{"full_q", "ok_q"}
	qm.AddQueue(queue.NewQueue("full_q", queue.TypeExecution))
	qm.AddQueue(queue.NewQueue("ok_q", queue.TypeExecution))
	qm.GetQueue("full_q").MaxJobs = 0
	qm.GetQueue("full_q").Enabled = false // disabled -> skipped

	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "route_q", "srv")

	dest, routed, err := routeJob(qm, jm, rj, "alice")
	if err != nil {
		t.Fatalf("routeJob error: %v", err)
	}
	if !routed {
		t.Fatal("expected routing to occur")
	}
	if dest != "ok_q" {
		t.Fatalf("expected dest ok_q, got %s", dest)
	}
	if rj.Queue != "route_q" {
		t.Fatalf("routeJob should not mutate job queue, got %s", rj.Queue)
	}
}

func TestRouteJobNoAcceptingDest(t *testing.T) {
	qm := queue.NewManager()
	rq := queue.NewQueue("route_q", queue.TypeRoute)
	rq.RouteDestin = []string{"only_q"}
	qm.AddQueue(rq)
	qm.AddQueue(queue.NewQueue("only_q", queue.TypeExecution))
	qm.GetQueue("only_q").Enabled = false

	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "route_q", "srv")
	if _, _, err := routeJob(qm, jm, rj, "alice"); err == nil {
		t.Fatal("expected no-accepting-dest error")
	}
}

func TestRouteJobNotRouteQueue(t *testing.T) {
	qm := queue.NewManager()
	qm.AddQueue(queue.NewQueue("exec_q", queue.TypeExecution))
	jm := job.NewManager("srv", 1)
	rj := job.NewJob("1.srv", "exec_q", "srv")
	dest, routed, err := routeJob(qm, jm, rj, "alice")
	if err != nil || routed || dest != "exec_q" {
		t.Fatalf("expected passthrough, got dest=%s routed=%v err=%v", dest, routed, err)
	}
}

func TestJobTypes(t *testing.T) {
	rj := job.NewJob("1.srv", "batch", "srv")
	if got := jobTypes(rj); len(got) != 1 || got[0] != "batch" {
		t.Fatalf("plain job types = %v", got)
	}
	rj.Interactive = true
	if got := jobTypes(rj); len(got) != 2 {
		t.Fatalf("interactive types = %v", got)
	}
	rj = job.NewJob("2.srv", "batch", "srv")
	rj.JobArrayReq = "1-3"
	if got := jobTypes(rj); len(got) != 2 || got[1] != "job_array" {
		t.Fatalf("array types = %v", got)
	}
	rj = job.NewJob("3.srv", "batch", "srv")
	rj.Rerunnable = "y"
	if got := jobTypes(rj); len(got) != 2 || got[1] != "rerunable" {
		t.Fatalf("rerunable types = %v", got)
	}
}

func TestAdmitQueueDisallowedTypes(t *testing.T) {
	q := testAdmitQueue(t)
	q.DisallowedTypes = []string{"interactive"}
	jm := job.NewManager("srv", 1)

	// batch job passes
	rj := job.NewJob("1.srv", "batch", "srv")
	if err := admitToQueue(jm, q, rj, "alice", false); err != nil {
		t.Fatalf("batch should pass, got %v", err)
	}
	// interactive job rejected
	rj = job.NewJob("1.srv", "batch", "srv")
	rj.Interactive = true
	if err := admitToQueue(jm, q, rj, "alice", false); err == nil {
		t.Fatal("interactive should be rejected by disallowed_types")
	}
}
