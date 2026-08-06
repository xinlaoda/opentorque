package cec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xinlaoda/opentorque/internal/crp"
)

type fakeProvider struct {
	mu         sync.Mutex
	ensures    int
	lastReq    crp.EnsureRequest
	reclaimErr error
	resumes    []string
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Ensure(req crp.EnsureRequest) ([]crp.VM, error) {
	f.mu.Lock()
	f.ensures++
	f.lastReq = req
	f.mu.Unlock()
	vms := make([]crp.VM, req.Count)
	for i := 0; i < req.Count; i++ {
		vms[i] = crp.VM{ID: fmt.Sprintf("vm-%d", i)}
	}
	return vms, nil
}
func (f *fakeProvider) Describe(ref crp.VMRef) (crp.VM, error) { return crp.VM{}, nil }
func (f *fakeProvider) Reclaim(ref crp.VMRef, policy string, destroy bool) error {
	return f.reclaimErr
}
func (f *fakeProvider) Resume(ref crp.VMRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes = append(f.resumes, ref.VMID)
	return nil
}
func (f *fakeProvider) Health(ref crp.VMRef) error { return nil }

type fakeNodes struct {
	mu       sync.Mutex
	drained  []string
	dereg    []string
	drainErr error
}

func (n *fakeNodes) DrainNode(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.drained = append(n.drained, name)
	return n.drainErr
}
func (n *fakeNodes) DeregisterNode(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dereg = append(n.dereg, name)
	return nil
}

func capEvent(queue string, nodes, cores, blocked int, jobs ...JobDemand) Event {
	return Event{
		Kind:      EventCapacity,
		Queue:     queue,
		Provider:  "azure",
		SKU:       "D2",
		MaxNodes:  10,
		Shortfall: Shortfall{Nodes: nodes, Cores: cores, Blocked: blocked},
		Jobs:      jobs,
	}
}

func TestDesiredSizeHeadroom(t *testing.T) {
	p := &Pool{Running: 2, MinNodes: 1, MaxNodes: 10, Headroom: 2}
	ev := capEvent("q", 3, 6, 3)
	if got := p.desiredSize(ev); got != 7 {
		t.Fatalf("desiredSize with headroom = %d, want 7", got)
	}
	// capped by MaxNodes
	p2 := &Pool{Running: 4, MinNodes: 1, MaxNodes: 5, Headroom: 3}
	if got := p2.desiredSize(ev); got != 5 {
		t.Fatalf("desiredSize capped = %d, want 5", got)
	}
	// floored by MinNodes
	p3 := &Pool{Running: 0, MinNodes: 2, MaxNodes: 10, Headroom: 0}
	ev0 := capEvent("q", 0, 0, 0)
	if got := p3.desiredSize(ev0); got != 2 {
		t.Fatalf("desiredSize min floor = %d, want 2", got)
	}
}

func TestPerPoolCooldown(t *testing.T) {
	fp := &fakeProvider{}
	c := New(fp)
	c.Cooldown = 30 * time.Second
	ev := capEvent("cq", 2, 4, 2)
	ev.Cooldown = time.Hour // per-pool override

	c.handle(ev)
	fp.mu.Lock()
	if fp.ensures != 1 {
		t.Fatalf("first scale-out ensures = %d, want 1", fp.ensures)
	}
	fp.mu.Unlock()

	// second immediate capacity event must be suppressed by the 1h cooldown
	c.handle(ev)
	fp.mu.Lock()
	if fp.ensures != 1 {
		t.Fatalf("second scale-out within cooldown should be suppressed, ensures=%d", fp.ensures)
	}
	fp.mu.Unlock()
}

func TestCoalesceCapacity(t *testing.T) {
	fp := &fakeProvider{}
	c := New(fp)
	c.Cooldown = time.Minute

	// two additional capacity events already queued for the same pool
	c.Events <- capEvent("cq", 3, 6, 3, JobDemand{ID: "j2"})
	c.Events <- capEvent("cq", 4, 8, 4, JobDemand{ID: "j3"})

	// handle a capacity event for that pool (as if it came off the channel)
	ev := capEvent("cq", 2, 4, 2, JobDemand{ID: "j1"})
	c.handle(ev)

	fp.mu.Lock()
	n := fp.ensures
	cnt := fp.lastReq.Count
	fp.mu.Unlock()
	if n != 1 {
		t.Fatalf("coalescing should yield a single Ensure, got %d", n)
	}
	// max shortfall nodes across the merged events = 4
	if cnt != 4 {
		t.Fatalf("coalesced Ensure count = %d, want 4", cnt)
	}
	// channel should be drained of the two queued events
	if len(c.Events) != 0 {
		t.Fatalf("capacity events not drained, %d left", len(c.Events))
	}
}

func TestDrainTimeoutRateLimit(t *testing.T) {
	fp := &fakeProvider{reclaimErr: errors.New("boom")}
	fn := &fakeNodes{drainErr: errors.New("drain-failed")}
	c := New(fp)
	c.nodes = fn

	now := time.Now()
	c.mu.Lock()
	c.pools["q"] = &Pool{
		IdleTime:     time.Second,
		Running:      2,
		MinNodes:     0,
		DrainTimeout: 10 * time.Second,
		Owned:        map[string]bool{"n1": true},
		IdleSince:    map[string]time.Time{"n1": now.Add(-5 * time.Second)},
		LastReclaim:  map[string]time.Time{},
	}
	c.mu.Unlock()

	c.reclaimIdle()
	fn.mu.Lock()
	d1 := len(fn.drained)
	fn.mu.Unlock()
	if d1 != 1 {
		t.Fatalf("first reclaim attempt drained = %d, want 1", d1)
	}

	// immediate second sweep within DrainTimeout must be rate-limited (drain failed
	// so node stays Owned; LastReclaim is set and within timeout)
	c.reclaimIdle()
	fn.mu.Lock()
	d2 := len(fn.drained)
	fn.mu.Unlock()
	if d2 != 1 {
		t.Fatalf("second sweep within drain timeout should not re-attempt, drained=%d", d2)
	}
}

// TestHibernateReclaimKeepsVM verifies that reclaiming an idle node with the
// "hibernate" policy keeps the VM for fast resume (added to Pool.Hibernated)
// instead of destroying it, and decrements Running.
func TestHibernateReclaimKeepsVM(t *testing.T) {
	fp := &fakeProvider{}
	fn := &fakeNodes{}
	c := New(fp)
	c.nodes = fn
	now := time.Now()
	c.mu.Lock()
	c.pools["q"] = &Pool{
		IdleTime:    time.Second,
		Reclaim:     "hibernate",
		Running:     2,
		MinNodes:    0,
		Owned:       map[string]bool{"hv": true},
		IdleSince:   map[string]time.Time{"hv": now.Add(-5 * time.Second)},
		Hibernated:  map[string]time.Time{},
		LastReclaim: map[string]time.Time{},
	}
	c.mu.Unlock()

	c.reclaimIdle()

	c.mu.Lock()
	_, kept := c.pools["q"].Hibernated["hv"]
	running := c.pools["q"].Running
	c.mu.Unlock()
	if !kept {
		t.Fatalf("hibernate-reclaimed VM not kept in Hibernated")
	}
	if running != 1 {
		t.Fatalf("running after hibernate reclaim = %d, want 1", running)
	}
}

// TestHibernateFastResume verifies a capacity event resumes a hibernated VM
// (fast path) instead of provisioning a brand-new one, so no Ensure call is
// made when hibernated capacity covers the shortfall.
func TestHibernateFastResume(t *testing.T) {
	fp := &fakeProvider{}
	fn := &fakeNodes{}
	c := New(fp)
	c.nodes = fn
	now := time.Now()
	c.mu.Lock()
	c.pools["q"] = &Pool{
		Reclaim:       "hibernate",
		MaxNodes:      5,
		Provisioning:  map[string]string{},
		ProvisionedAt: map[string]time.Time{},
		Hibernated:    map[string]time.Time{"hv": now.Add(-time.Minute)},
	}
	c.mu.Unlock()

	ev := capEvent("q", 1, 2, 1, JobDemand{ID: "j1"})
	ev.Reclaim = "hibernate"
	c.handle(ev)

	fp.mu.Lock()
	nres := len(fp.resumes)
	nes := fp.ensures
	fp.mu.Unlock()
	if nres != 1 {
		t.Fatalf("resumes = %d, want 1", nres)
	}
	if nes != 0 {
		t.Fatalf("ensures = %d, want 0 (resume-first should cover shortfall)", nes)
	}
	fp.mu.Lock()
	resumedID := fp.resumes[0]
	fp.mu.Unlock()
	if resumedID != "hv" {
		t.Fatalf("resumed vm = %q, want hv", resumedID)
	}
}

// TestProvisioningTimeout verifies a still-booting VM that exceeds the pool
// provisioning timeout is destroyed and removed from Provisioning.
func TestProvisioningTimeout(t *testing.T) {
	fp := &fakeProvider{}
	fn := &fakeNodes{}
	c := New(fp)
	c.nodes = fn
	now := time.Now()
	c.mu.Lock()
	c.pools["q"] = &Pool{
		Provisioning:     map[string]string{"pv": "j1"},
		ProvisionedAt:    map[string]time.Time{"pv": now.Add(-time.Hour)},
		ProvisionTimeout: 10 * time.Minute,
		Inflight:         1,
		Hibernated:       map[string]time.Time{},
	}
	c.mu.Unlock()

	c.reclaimIdle()

	c.mu.Lock()
	_, still := c.pools["q"].Provisioning["pv"]
	inf := c.pools["q"].Inflight
	c.mu.Unlock()
	if still {
		t.Fatalf("timed-out provisioning VM still in Provisioning")
	}
	if inf != 0 {
		t.Fatalf("inflight after timeout destroy = %d, want 0", inf)
	}
}
