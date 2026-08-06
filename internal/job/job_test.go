package job

import "testing"

func TestCloneForArray(t *testing.T) {
	j := NewJob("42.srv", "batch", "srv")
	j.Name = "arr"
	j.Script = "echo hi"
	j.ResourceReq["ncpus"] = "1"
	j.ResourceReq["mem"] = "512mb"
	j.VariableList["PBS_JOBNAME"] = "arr"
	j.JobArrayReq = "1-3"

	c := j.CloneForArray(2, "srv")
	if c.JobArrayReq != "2" {
		t.Fatalf("array index = %q, want 2", c.JobArrayReq)
	}
	if c.Script != "echo hi" || c.Name != "arr" {
		t.Fatalf("clone did not copy script/name")
	}
	if c.ResourceReq["ncpus"] != "1" || c.VariableList["PBS_JOBNAME"] != "arr" {
		t.Fatalf("clone did not deep copy maps")
	}
	// mutating the source must not affect the clone
	j.ResourceReq["ncpus"] = "8"
	if c.ResourceReq["ncpus"] != "1" {
		t.Fatalf("clone shares ResourceReq map with source")
	}
}
