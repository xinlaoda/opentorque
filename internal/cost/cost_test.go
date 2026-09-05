package cost

import (
	"math"
	"testing"
)

func closeEnough(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAllocateSharedNodeByCoreSeconds(t *testing.T) {
	// Node n1 billed $10 for the window. Account A used 2 cores x 3600s = 7200
	// core-sec, account B used 1 core x 3600s = 3600 core-sec. A gets 2/3,
	// B gets 1/3 of the bill. Idle time is implicitly included (whole bill is
	// covered), so this satisfies "VM bill != job walltime" correctly.
	usages := []Usage{
		{Account: "A", Node: "n1", CoreSeconds: 7200},
		{Account: "B", Node: "n1", CoreSeconds: 3600},
	}
	bills := []Bill{{Node: "n1", USD: 10}}
	al := Allocate(usages, bills)
	if !closeEnough(al.Accounts["A"], 10.0*2/3) {
		t.Fatalf("A cost = %v, want %v", al.Accounts["A"], 10.0*2/3)
	}
	if !closeEnough(al.Accounts["B"], 10.0*1/3) {
		t.Fatalf("B cost = %v, want %v", al.Accounts["B"], 10.0*1/3)
	}
	if al.Overhead != 0 {
		t.Fatalf("overhead = %v, want 0", al.Overhead)
	}
}

func TestAllocateEmptyNodeGoesToOverhead(t *testing.T) {
	// A node billed $8 but with zero jobs -> pure idle / wrongly-scaled node.
	usages := []Usage{{Account: "A", Node: "n1", CoreSeconds: 100}}
	bills := []Bill{{Node: "n1", USD: 12}, {Node: "empty", USD: 8}}
	al := Allocate(usages, bills)
	if !closeEnough(al.Accounts["A"], 12) {
		t.Fatalf("A cost = %v, want 12", al.Accounts["A"])
	}
	if !closeEnough(al.Overhead, 8) {
		t.Fatalf("overhead = %v, want 8 (empty node bill)", al.Overhead)
	}
}

func TestAllocateUnknownAccountBucketed(t *testing.T) {
	usages := []Usage{{Account: "", Node: "n1", CoreSeconds: 500}}
	bills := []Bill{{Node: "n1", USD: 20}}
	al := Allocate(usages, bills)
	if !closeEnough(al.Accounts["(unknown)"], 20) {
		t.Fatalf("unknown account cost = %v, want 20", al.Accounts["(unknown)"])
	}
}

func TestAllocateCoreSecondsRollup(t *testing.T) {
	usages := []Usage{
		{Account: "A", Node: "n1", CoreSeconds: 100},
		{Account: "A", Node: "n2", CoreSeconds: 50},
	}
	al := Allocate(usages, []Bill{{Node: "n1", USD: 1}, {Node: "n2", USD: 1}})
	if !closeEnough(al.CoreSeconds["A"], 150) {
		t.Fatalf("A core-seconds = %v, want 150", al.CoreSeconds["A"])
	}
	if !closeEnough(al.Accounts["A"], 2) {
		t.Fatalf("A cost = %v, want 2", al.Accounts["A"])
	}
}
