package server

import (
	"testing"

	"github.com/xinlaoda/opentorque/internal/mom/resource"
)

func TestBuildMomStatusAttrs(t *testing.T) {
	st := &resource.NodeStatus{
		Arch:      "amd64",
		OSName:    "Linux",
		OSRelease: "6.8.0-azure",
		Ncpus:     2,
		LoadAvg:   0.25,
		TotalMem:  8000000,
		AvailMem:  7000000,
		PhysMem:   8000000,
		TotalSwap: 1000000,
		AvailSwap: 900000,
		IdleTime:  42,
	}
	attrs := BuildMomStatusAttrs(st, "mom1")

	checks := map[string]string{
		"opsys":     "Linux",
		"ncpus":     "2",
		"loadave":   "0.25",
		"totmem":    "8000000kb",
		"availmem":  "7000000kb",
		"physmem":   "8000000kb",
		"totswap":   "1000000kb",
		"availswap": "900000kb",
		"idletime":  "42",
		"arch":      "amd64",
		"state":     "free",
		"version":   "7.0.0-go",
		"gres":      "",
		"jobs":      "",
	}
	for k, want := range checks {
		if got := attrs[k]; got != want {
			t.Errorf("attr %q = %q, want %q", k, got, want)
		}
	}
	// uname should embed hostname and release
	if got := attrs["uname"]; got != "Linux mom1 6.8.0-azure" {
		t.Errorf("uname = %q", got)
	}
	// every key from buildStatusString must be present
	for _, required := range []string{"sessions", "nsessions", "nusers", "netload", "varattr", "cpuclock", "rectime", "opsys", "uname"} {
		if _, ok := attrs[required]; !ok {
			t.Errorf("missing attr %q", required)
		}
	}
}

func TestBuildMomStatusAttrsNoSwap(t *testing.T) {
	st := &resource.NodeStatus{Arch: "x86_64", OSName: "linux"}
	attrs := BuildMomStatusAttrs(st, "n1")
	if _, ok := attrs["totswap"]; ok {
		t.Errorf("totswap should be omitted when TotalSwap==0, got %q", attrs["totswap"])
	}
}
