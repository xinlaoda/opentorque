package server

import (
	"testing"
	"time"

	"github.com/xinlaoda/opentorque/internal/job"
)

// TestSerializeDeserializeJobCompleted verifies the .JB persistence round-trip
// keeps the full attribute set (resource request, timing, execution, multi-node
// layout) for a completed job, enabling read-back after a restart (TODO 5.2).
func TestSerializeDeserializeJobCompleted(t *testing.T) {
	j := job.NewJob("99.srv", "batch", "srv")
	j.SetState(job.StateComplete, job.SubstateComplete)
	j.Name = "htest"
	j.EUser = "alice"
	j.ExecHost = "n1/0+n2/0"
	j.ResourceReq = map[string]string{"ncpus": "2", "nodes": "2:ppn=2", "walltime": "01:00:00"}
	j.NodeCount = 2
	j.TaskCount = 4
	j.ExitStatus = 7
	j.CompTime = time.Unix(1600000000, 0)
	j.StartTime = time.Unix(1599999000, 0)

	data := serializeJob(j)
	got := deserializeJob(data, "99.srv", "srv")

	if got.State != job.StateComplete {
		t.Fatalf("state=%d want Complete", got.State)
	}
	if got.ResourceReq["nodes"] != "2:ppn=2" || got.ResourceReq["ncpus"] != "2" || got.ResourceReq["walltime"] != "01:00:00" {
		t.Fatalf("resource list not restored: %v", got.ResourceReq)
	}
	if got.NodeCount != 2 || got.TaskCount != 4 {
		t.Fatalf("multi-node (nodes=%d, tasks=%d) not restored", got.NodeCount, got.TaskCount)
	}
	if got.ExecHost != j.ExecHost || got.ExitStatus != 7 {
		t.Fatalf("exec_host=%q exit=%d want %q/7", got.ExecHost, got.ExitStatus, j.ExecHost)
	}
	if !got.CompTime.Equal(j.CompTime) || !got.StartTime.Equal(j.StartTime) {
		t.Fatalf("timing not restored: comp=%v start=%v", got.CompTime, got.StartTime)
	}
}