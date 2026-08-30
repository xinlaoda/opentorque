package server

import (
	"testing"

	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// TestQueueAllowsSubmitHost verifies queue-level acl_hosts on submission (1.6).
func TestQueueAllowsSubmitHost(t *testing.T) {
	s := &Server{queueMgr: queue.NewManager(), store: NewStore(config.NewConfig("/tmp"))}
	q := queue.NewQueue("batch", queue.TypeExecution)
	q.ACLHostEnabled = true
	q.ACLHosts = []string{"submit01", "submit02"}
	s.queueMgr.AddQueue(q)

	j := job.NewJob("1.srv", "batch", "srv")
	j.Owner = "alice@submit01"
	if !s.queueAllowsSubmitHost("batch", j) {
		t.Fatalf("expected submit01 host allowed")
	}
	j2 := job.NewJob("2.srv", "batch", "srv")
	j2.Owner = "bob@evil.host"
	if s.queueAllowsSubmitHost("batch", j2) {
		t.Fatalf("expected evil.host rejected")
	}
	// disabled ACL -> allow.
	q.ACLHostEnabled = false
	if !s.queueAllowsSubmitHost("batch", j2) {
		t.Fatalf("expected allow when acl disabled")
	}
}

// TestAdmitToQueueACLHosts verifies the submission-host ACL at the admission
// gate (route.go admitToQueue, 1.6). The submit host must come from PBS_O_HOST
// (falling back to the host part of Job_Owner) so allow-listed hosts pass even
// though the client sends Job_Owner without a "@host" suffix.
func TestAdmitToQueueACLHosts(t *testing.T) {
	jm := job.NewManager("srv", 1)
	q := queue.NewQueue("exec", queue.TypeExecution)
	q.ACLHostEnabled = true
	q.ACLHosts = []string{"allow01"}

	rj := job.NewJob("1.srv", "exec", "srv")
	rj.Owner = "root"
	rj.VariableList["PBS_O_HOST"] = "allow01"
	if err := admitToQueue(jm, q, rj, "root", false); err != nil {
		t.Fatalf("expected allow-listed PBS_O_HOST accepted, got: %v", err)
	}

	rj2 := job.NewJob("2.srv", "exec", "srv")
	rj2.Owner = "root"
	rj2.VariableList["PBS_O_HOST"] = "evil.host"
	if err := admitToQueue(jm, q, rj2, "root", false); err == nil {
		t.Fatalf("expected non-listed host rejected")
	}

	rj3 := job.NewJob("3.srv", "exec", "srv")
	rj3.Owner = "root@allow01"
	if err := admitToQueue(jm, q, rj3, "root@allow01", false); err != nil {
		t.Fatalf("expected host from Job_Owner accepted, got: %v", err)
	}

	q.ACLHostEnabled = false
	if err := admitToQueue(jm, q, rj2, "root", false); err != nil {
		t.Fatalf("expected allow when acl disabled, got: %v", err)
	}
}
