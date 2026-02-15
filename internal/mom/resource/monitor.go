package resource

import "time"

// NodeStatus holds the current status of this compute node.
type NodeStatus struct {
	Arch       string
	OSName     string
	OSRelease  string
	Ncpus      int
	Nthreads   int
	LoadAvg    float64
	TotalMem   int64 // KB
	AvailMem   int64 // KB
	PhysMem    int64 // KB
	TotalSwap  int64 // KB
	AvailSwap  int64 // KB
	NetLoad    int64 // bytes
	IdleTime   int64 // seconds
	Uptime     time.Duration
	NumJobs    int
}

// ProcessResources holds resource usage for a single process tree.
type ProcessResources struct {
	CPUTime  time.Duration
	Memory   int64 // bytes (RSS)
	VMem     int64 // bytes (virtual)
}

// Monitor is the interface for platform-specific resource monitoring.
type Monitor interface {
	// GetNodeStatus returns current node status.
	GetNodeStatus() (*NodeStatus, error)

	// GetProcessResources returns resource usage for a process session.
	GetProcessResources(sid int) (*ProcessResources, error)
}
