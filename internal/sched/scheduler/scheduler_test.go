package scheduler

import (
	"testing"
	"time"

	"github.com/xinlaoda/opentorque/internal/sched/config"
)

func newTestScheduler() *Scheduler {
	return New(config.DefaultConfig(""))
}

func TestFindNodeForJobSkipsDrain(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "free1", State: "free", FreeCPUs: 2},
		{Name: "draining", State: "drain", FreeCPUs: 2},
		{Name: "drainjob", State: "drain,job-exclusive", FreeCPUs: 2},
		{Name: "offline", State: "offline", FreeCPUs: 2},
		{Name: "excl", State: "excl", FreeCPUs: 2},
	}}
	j := &JobInfo{ID: "1", CPUReq: 2}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "free1" {
		t.Fatalf("expected only free1 schedulable, got %+v", got)
	}
}

func TestNodeSchedulable(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{"free", true},
		{"job-exclusive", true},
		{"drain", false},
		{"drain,job-exclusive", false},
		{"excl", false},
		{"offline", false},
		{"down", false},
		{"busy", false},
	}
	for _, c := range cases {
		if got := nodeSchedulable(&NodeInfo{State: c.state}); got != c.want {
			t.Errorf("nodeSchedulable(%q)=%v want %v", c.state, got, c.want)
		}
	}
}

func TestFindNodeForJobBestFit(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "big", State: "free", FreeCPUs: 8},
		{Name: "small", State: "free", FreeCPUs: 2},
		{Name: "med", State: "free", FreeCPUs: 4},
	}}
	j := &JobInfo{ID: "1", CPUReq: 2}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "small" {
		t.Fatalf("best-fit expected small, got %+v", got)
	}
}

func TestFindNodeForJobHostPinning(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "n1", State: "free", FreeCPUs: 2},
		{Name: "n2", State: "free", FreeCPUs: 2},
	}}
	j := &JobInfo{ID: "1", CPUReq: 1, Host: "n2"}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "n2" {
		t.Fatalf("host pin expected n2, got %+v", got)
	}
}

func TestFindNodeForJobFeatures(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "plain", State: "free", FreeCPUs: 4},
		{Name: "gpu1", State: "free", FreeCPUs: 4, Properties: []string{"gpu"}},
	}}
	j := &JobInfo{ID: "1", CPUReq: 1, Features: []string{"gpu"}}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "gpu1" {
		t.Fatalf("feature select expected gpu1, got %+v", got)
	}
}

func TestFindNodeForJobNoFit(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "n1", State: "free", FreeCPUs: 1},
	}}
	j := &JobInfo{ID: "1", CPUReq: 4}
	if got := s.findNodeForJob(sinfo, j); got != nil {
		t.Fatalf("expected no node, got %+v", got)
	}
}

func TestNodeHasAllFeatures(t *testing.T) {
	n := &NodeInfo{Properties: []string{"gpu", "a100"}}
	if !nodeHasAllFeatures(n, []string{"gpu"}) {
		t.Fatal("expected gpu match")
	}
	if nodeHasAllFeatures(n, []string{"gpu", "nonexistent"}) {
		t.Fatal("expected missing feature to fail")
	}
	if !nodeHasAllFeatures(n, []string{"GPU"}) {
		t.Fatal("expected case-insensitive match for GPU")
	}
}

func TestSortJobsFIFO(t *testing.T) {
	s := newTestScheduler()
	base := time.Unix(1000, 0)
	jobs := []*JobInfo{
		{ID: "late", QueueTime: base.Add(2 * time.Minute)},
		{ID: "early", QueueTime: base},
	}
	s.sortJobs(jobs, time.Now())
	if jobs[0].ID != "early" {
		t.Fatalf("fifo sort expected early first, got %s", jobs[0].ID)
	}
}

func TestSortJobsShortestFirst(t *testing.T) {
	cfg := config.DefaultConfig("")
	cfg.SortBy = "shortest_job_first"
	s := New(cfg)
	jobs := []*JobInfo{
		{ID: "long", Walltime: 10 * time.Minute},
		{ID: "short", Walltime: 1 * time.Minute},
	}
	s.sortJobs(jobs, time.Now())
	if jobs[0].ID != "short" {
		t.Fatalf("shortest first expected short, got %s", jobs[0].ID)
	}
}

func TestIsRouteQueue(t *testing.T) {
	if !isRouteQueue(&QueueInfo{Type: "Route"}) {
		t.Fatal("expected Route to be route queue")
	}
	if isRouteQueue(&QueueInfo{Type: "Execution"}) {
		t.Fatal("expected Execution to not be route queue")
	}
	if isRouteQueue(&QueueInfo{Type: ""}) {
		t.Fatal("expected empty type to not be route queue")
	}
}

// TestFindNodeForJobLocalFirst: a free local (static) node is always preferred
// over an auto-registered cloud/dynamic node, even when the cloud node has more
// free CPUs -- so cloud burst only kicks in when local capacity is exhausted
// and a cloud VM is not kept alive while local sits idle.
func TestFindNodeForJobLocalFirst(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "cloud1", State: "free", FreeCPUs: 8, Dynamic: true},
		{Name: "local1", State: "free", FreeCPUs: 1, Dynamic: false},
	}}
	j := &JobInfo{ID: "1", CPUReq: 1}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "local1" {
		t.Fatalf("local-first expected local1, got %+v", got)
	}
}

// TestFindNodeForJobCloudFallback: once no local node can take the job, the
// scheduler falls back to the cloud/dynamic node.
func TestFindNodeForJobCloudFallback(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "local1", State: "free", FreeCPUs: 0, Dynamic: false},
		{Name: "cloud1", State: "free", FreeCPUs: 4, Dynamic: true},
	}}
	j := &JobInfo{ID: "1", CPUReq: 1}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "cloud1" {
		t.Fatalf("expected fallback to cloud1, got %+v", got)
	}
}


func TestFindNodeForJobHostGroup(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "gpu1", State: "free", FreeCPUs: 4, Groups: []string{"gpu"}},
		{Name: "gpu2", State: "free", FreeCPUs: 8, Groups: []string{"gpu", "fast"}},
		{Name: "cpu1", State: "free", FreeCPUs: 16, Groups: []string{"cpu"}},
		{Name: "none", State: "free", FreeCPUs: 32},
	}}
	// Pin to @gpu: only gpu1/gpu2 qualify, best fit picks gpu1.
	j := &JobInfo{ID: "1", CPUReq: 2, HostGroup: "gpu"}
	got := s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "gpu1" {
		t.Fatalf("expected gpu1 for @gpu, got %+v", got)
	}
	// Pin to @fast: only gpu2 qualifies even though cpu1 has more free cpus.
	j.HostGroup = "fast"
	got = s.findNodeForJob(sinfo, j)
	if got == nil || got.Name != "gpu2" {
		t.Fatalf("expected gpu2 for @fast, got %+v", got)
	}
	// Group pin must not match a node outside the group despite free capacity.
	j.HostGroup = "missing"
	if got := s.findNodeForJob(sinfo, j); got != nil {
		t.Fatalf("expected nil for @missing, got %+v", got)
	}
}

func TestParseNodeSelectSpec(t *testing.T) {
	cases := []struct {
		spec        string
		wantNodes   int
		wantPPN     int
	}{
		{"", 1, 1},
		{"1", 1, 1},
		{"4", 4, 1},
		{"4:ppn=2", 4, 2},
		{"8:ppn=4", 8, 4},
		{"2:ppn=2+4:ppn=1", 2, 2}, // heterogeneous: first chunk wins
	}
	for _, c := range cases {
		n, p := parseNodeSelectSpec(c.spec)
		if n != c.wantNodes || p != c.wantPPN {
			t.Errorf("parseNodeSelectSpec(%q)=(%d,%d) want (%d,%d)", c.spec, n, p, c.wantNodes, c.wantPPN)
		}
	}
}

func TestFindNodeForJobMultiNode(t *testing.T) {
	s := newTestScheduler()
	sinfo := &ServerInfo{Nodes: []*NodeInfo{
		{Name: "n1", State: "free", FreeCPUs: 4},
		{Name: "n2", State: "free", FreeCPUs: 4},
	}}
	// Nodes=2 ppn=2 -> enough distinct nodes, returns an anchor node.
	j := &JobInfo{ID: "1", CPUReq: 2, Nodes: 2, PPN: 2}
	if got := s.findNodeForJob(sinfo, j); got == nil {
		t.Fatalf("expected an anchor node for 2-node request, got nil")
	}
	// Nodes=3 distinct requested but only 2 free -> blocked (nil).
	j.Nodes = 3
	if got := s.findNodeForJob(sinfo, j); got != nil {
		t.Fatalf("expected nil when fewer distinct nodes than requested, got %+v", got)
	}
	// A greedy node alone cannot satisfy N>1 even if it has the aggregate cpus.
	sinfo2 := &ServerInfo{Nodes: []*NodeInfo{{Name: "big", State: "free", FreeCPUs: 8}}}
	j.Nodes = 2
	j.PPN = 4
	if got := s.findNodeForJob(sinfo2, j); got != nil {
		t.Fatalf("expected nil when one node holds all cpus but 2 distinct nodes needed, got %+v", got)
	}
}

func TestQueueNodeOKHostList(t *testing.T) {
	q := &QueueInfo{HostList: []string{"worker1", "@gpu"}}
	nodeA := &NodeInfo{Name: "worker1", Groups: nil}
	nodeG := &NodeInfo{Name: "any", Groups: []string{"gpu"}}
	nodeX := &NodeInfo{Name: "other", Groups: []string{"cpu"}}
	if !queueNodeOK(q, nodeA) || !queueNodeOK(q, nodeG) {
		t.Fatalf("hostlist worker1/@gpu should allow worker1 and gpu-group node")
	}
	if queueNodeOK(q, nodeX) {
		t.Fatalf("hostlist should reject node outside list/groups")
	}
	// nil queue allows everything.
	if !queueNodeOK(nil, nodeX) {
		t.Fatalf("nil queue should allow any node")
	}
}

func TestQueueNodeOKExclusive(t *testing.T) {
	q := &QueueInfo{NaccessPolicy: "exclusive"}
	idle := &NodeInfo{Name: "n1", Jobs: nil}
	busy := &NodeInfo{Name: "n2", Jobs: []string{"j1"}}
	if !queueNodeOK(q, idle) {
		t.Fatalf("exclusive should allow an idle node")
	}
	if queueNodeOK(q, busy) {
		t.Fatalf("exclusive should reject a node already running a job")
	}
	// shared (default) allows both.
	q2 := &QueueInfo{NaccessPolicy: "shared"}
	if !queueNodeOK(q2, idle) || !queueNodeOK(q2, busy) {
		t.Fatalf("shared should allow both idle and busy nodes")
	}
}