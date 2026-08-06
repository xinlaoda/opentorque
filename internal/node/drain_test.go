package node

import (
	"strings"
	"testing"
)

// TestDrainState verifies a drained node is not dispatchable, reports a
// "drain" state, and that a MOM free status does not clear the admin drain
// flag (4.2 node drain/roll-out primitive).
func TestDrainState(t *testing.T) {
	n := NewNode("n1", 2)
	n.State = StateFree
	n.SlotsUsed = 0
	n.SlotsTotal = 2

	if !n.IsFree() {
		t.Fatalf("clean node should be free")
	}

	n.State |= StateDrain
	if n.IsFree() {
		t.Fatalf("drained node must not accept new jobs")
	}
	if !strings.Contains(n.StateName(), "drain") {
		t.Fatalf("StateName=%q want drain", n.StateName())
	}

	// A MOM "free" status must preserve the admin drain flag.
	n.applyStateString("free")
	if n.State&StateDrain == 0 {
		t.Fatalf("MOM free status cleared drain flag")
	}
	if n.IsFree() {
		t.Fatalf("drained node still free after MOM free report")
	}

	// Clearing drain restores schedulability.
	n.State &^= StateDrain
	if n.State == StateFree && !n.IsFree() {
		t.Fatalf("node should be free after drain cleared")
	}
}

// TestExclState verifies exclusive nodes also block new jobs but report excl.
func TestExclState(t *testing.T) {
	n := NewNode("n2", 4)
	n.State = StateFree
	n.SlotsUsed = 0
	n.SlotsTotal = 4
	n.State |= StateExcl
	if n.IsFree() {
		t.Fatalf("excl node must not accept new jobs")
	}
	if !strings.Contains(n.StateName(), "excl") {
		t.Fatalf("StateName=%q want excl", n.StateName())
	}
}
