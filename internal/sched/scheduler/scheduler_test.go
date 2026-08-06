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
