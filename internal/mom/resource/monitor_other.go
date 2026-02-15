//go:build !linux
// +build !linux

package resource

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GenericMonitor provides basic resource monitoring for non-Linux platforms.
type GenericMonitor struct{}

func NewMonitor() Monitor {
	return &GenericMonitor{}
}

func (m *GenericMonitor) GetNodeStatus() (*NodeStatus, error) {
	status := &NodeStatus{
		Arch:     runtime.GOARCH,
		OSName:   runtime.GOOS,
		Ncpus:    runtime.NumCPU(),
		Nthreads: runtime.NumCPU(),
	}

	// Try to get memory info on various platforms
	switch runtime.GOOS {
	case "darwin":
		m.getDarwinInfo(status)
	case "windows":
		m.getWindowsInfo(status)
	}

	return status, nil
}

func (m *GenericMonitor) GetProcessResources(sid int) (*ProcessResources, error) {
	return &ProcessResources{}, nil
}

func (m *GenericMonitor) getDarwinInfo(status *NodeStatus) {
	// Use sysctl on macOS
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		val, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		status.TotalMem = val / 1024
		status.PhysMem = val / 1024
	}
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		fields := strings.Fields(strings.Trim(string(out), "{ }"))
		if len(fields) > 0 {
			status.LoadAvg, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	if out, err := exec.Command("sysctl", "-n", "kern.osrelease").Output(); err == nil {
		status.OSRelease = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("uptime").Output(); err == nil {
		_ = out
		status.Uptime = 0
	}
}

func (m *GenericMonitor) getWindowsInfo(status *NodeStatus) {
	// Windows: use wmic or other tools
	if out, err := exec.Command("wmic", "OS", "get", "TotalVisibleMemorySize", "/value").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "TotalVisibleMemorySize=") {
				val, _ := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(line), "TotalVisibleMemorySize="), 10, 64)
				status.TotalMem = val
				status.PhysMem = val
			}
		}
	}
	status.Uptime = time.Duration(0)
}
