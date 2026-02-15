//go:build linux
// +build linux

package resource

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// LinuxMonitor implements Monitor for Linux systems using /proc.
type LinuxMonitor struct{}

func NewMonitor() Monitor {
	return &LinuxMonitor{}
}

func (m *LinuxMonitor) GetNodeStatus() (*NodeStatus, error) {
	status := &NodeStatus{
		Arch:   runtime.GOARCH,
		Ncpus:  runtime.NumCPU(),
	}

	// OS info from /proc/sys/kernel
	if data, err := os.ReadFile("/proc/sys/kernel/ostype"); err == nil {
		status.OSName = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		status.OSRelease = strings.TrimSpace(string(data))
	}

	// Load average
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			status.LoadAvg, _ = strconv.ParseFloat(fields[0], 64)
		}
	}

	// Memory info
	if err := m.parseMeminfo(status); err != nil {
		return nil, err
	}

	// Uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			secs, _ := strconv.ParseFloat(fields[0], 64)
			status.Uptime = time.Duration(secs * float64(time.Second))

			if len(fields) > 1 {
				idle, _ := strconv.ParseFloat(fields[1], 64)
				status.IdleTime = int64(idle)
			}
		}
	}

	return status, nil
}

func (m *LinuxMonitor) parseMeminfo(status *NodeStatus) error {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseInt(fields[1], 10, 64)
		key := strings.TrimSuffix(fields[0], ":")

		switch key {
		case "MemTotal":
			status.TotalMem = val
			status.PhysMem = val
		case "MemAvailable":
			status.AvailMem = val
		case "SwapTotal":
			status.TotalSwap = val
		case "SwapFree":
			status.AvailSwap = val
		}
	}
	return nil
}

func (m *LinuxMonitor) GetProcessResources(sid int) (*ProcessResources, error) {
	res := &ProcessResources{}

	// Walk /proc to find all processes in this session
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		// Check if this process belongs to our session
		procSid, err := getProcessSession(pid)
		if err != nil || procSid != sid {
			continue
		}

		// Read process stats
		pres, err := readProcessStat(pid)
		if err != nil {
			continue
		}

		res.CPUTime += pres.CPUTime
		res.Memory += pres.Memory
		res.VMem += pres.VMem
	}

	return res, nil
}

func getProcessSession(pid int) (int, error) {
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}

	// /proc/[pid]/stat format: pid (comm) state ppid pgrp session ...
	// The comm field may contain spaces and parens, so find the last ')'
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return 0, fmt.Errorf("malformed stat file")
	}
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 4 {
		return 0, fmt.Errorf("insufficient fields in stat")
	}

	// fields[3] is session (field index 5 in full stat, but index 3 after comm)
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return 0, err
	}
	return session, nil
}

func readProcessStat(pid int) (*ProcessResources, error) {
	res := &ProcessResources{}

	// Read /proc/[pid]/stat for CPU time
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return nil, err
	}

	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return nil, fmt.Errorf("malformed stat")
	}
	fields := strings.Fields(s[idx+2:])
	// fields[11] = utime (ticks), fields[12] = stime (ticks)
	if len(fields) > 12 {
		utime, _ := strconv.ParseInt(fields[11], 10, 64)
		stime, _ := strconv.ParseInt(fields[12], 10, 64)
		ticksPerSec := int64(100) // usually 100 Hz (sysconf(_SC_CLK_TCK))
		totalTicks := utime + stime
		res.CPUTime = time.Duration(totalTicks * int64(time.Second) / ticksPerSec)
	}

	// Read /proc/[pid]/status for memory
	statusPath := filepath.Join("/proc", strconv.Itoa(pid), "status")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		return res, nil // CPU info is still valid
	}

	for _, line := range strings.Split(string(statusData), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "VmRSS":
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			res.Memory = val * 1024 // kB to bytes
		case "VmSize":
			val, _ := strconv.ParseInt(fields[1], 10, 64)
			res.VMem = val * 1024
		}
	}

	return res, nil
}
