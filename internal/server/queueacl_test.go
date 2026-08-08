package server

import (
	"testing"

	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// TestQueueAllowsSubmitHost verifies queue-level acl_hosts on submission (1.6).
func TestQueueAllowsSubmitHost(t *testing.T) {
	s := &Server{queueMgr: queue.NewManager()}
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