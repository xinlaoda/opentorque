package server

import (
	"testing"

	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/dis"
	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/node"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// poolAttrs returns the values of all status attributes with the given name
// (an addResc group like pool_free_cores emits one attr per pool, keyed by
// Resc).
func poolAttrs(t *testing.T, obj dis.StatusObject, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, a := range obj.Attrs {
		if a.Name == name {
			if a.Resc == "" {
				t.Fatalf("%s attr lacks Resc key (value=%q)", name, a.Value)
			}
			out[a.Resc] = a.Value
		}
	}
	return out
}

// TestPoolFreeCoresSnapshot verifies formatServerStatus aggregates per-pool
// running node count and total/free cores by node queue ownership (M4
// follow-up: qstat -B per-pool capacity snapshot).
func TestPoolFreeCoresSnapshot(t *testing.T) {
	cfg := &config.Config{ServerName: "srv", DefaultQueue: "batch"}
	s := &Server{
		cfg:      cfg,
		nodeMgr:  node.NewManager(),
		jobMgr:   job.NewManager("srv", 1),
		queueMgr: queue.NewManager(),
		store:    NewStore(cfg),
	}

	// batch pool: w1 up 4 cores/1 used; w2 down 2 cores (down is excluded).
	w1 := s.nodeMgr.AddNode("w1", 4)
	w1.Queue = "batch"
	w1.State = node.StateFree
	w1.SlotsUsed = 1
	w2 := s.nodeMgr.AddNode("w2", 2)
	w2.Queue = "batch"
	w2.State = node.StateDown

	// default pool (no explicit queue): srv 8 cores/3 used, up.
	srv := s.nodeMgr.AddNode("srv", 8)
	srv.State = node.StateFree
	srv.SlotsUsed = 3

	obj := s.formatServerStatus()

	nodes := poolAttrs(t, obj, "pool_nodes")
	up := poolAttrs(t, obj, "pool_up_nodes")
	total := poolAttrs(t, obj, "pool_total_cores")
	free := poolAttrs(t, obj, "pool_free_cores")

	if got := nodes["batch"]; got != "2" {
		t.Errorf("pool_nodes[batch]=%q want 2", got)
	}
	if got := up["batch"]; got != "1" {
		t.Errorf("pool_up_nodes[batch]=%q want 1", got)
	}
	if got := total["batch"]; got != "4" {
		t.Errorf("pool_total_cores[batch]=%q want 4", got)
	}
	if got := free["batch"]; got != "3" {
		t.Errorf("pool_free_cores[batch]=%q want 3", got)
	}

	if got := nodes["default"]; got != "1" {
		t.Errorf("pool_nodes[default]=%q want 1", got)
	}
	if got := total["default"]; got != "8" {
		t.Errorf("pool_total_cores[default]=%q want 8", got)
	}
	if got := free["default"]; got != "5" {
		t.Errorf("pool_free_cores[default]=%q want 5", got)
	}
}
