// Package config parses the pbs_sched configuration file (sched_config).
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the scheduler configuration parsed from sched_config.
type Config struct {
	PBSHome  string
	Server   string
	LogLevel int

	// Scheduling policy flags
	SchedulerMode     string        // "builtin" or "external"
	StrictFIFO        bool          // Block all jobs behind first unrunnable
	RoundRobin        bool          // Cycle through queues
	ByQueue           bool          // Process queues in priority order
	FairShare         bool          // Enable fair-share scheduling
	HelpStarvingJobs  bool          // Boost priority of long-waiting jobs
	SortQueues        bool          // Sort queues by priority
	LoadBalancing     bool          // Distribute jobs evenly across nodes
	SortBy            string        // Job sorting criterion
	MaxStarve         time.Duration // Maximum wait before starvation boost
	HalfLife          time.Duration // Fair-share usage decay half-life
	UnknownShares     int           // Default shares for ungrouped users
	SyncTime          time.Duration // Fair-share usage persistence interval
	DedicatedPrefix   string        // Queue prefix for dedicated-time jobs
	SchedulerInterval int           // Seconds between cycles (default 10)
}

// DefaultConfig returns a Config with default values.
func DefaultConfig(pbsHome string) *Config {
	return &Config{
		PBSHome:           pbsHome,
		SchedulerMode:     "external",
		StrictFIFO:        false,
		RoundRobin:        false,
		ByQueue:           true,
		FairShare:         false,
		HelpStarvingJobs:  true,
		SortQueues:        true,
		LoadBalancing:     false,
		SortBy:            "fifo",
		MaxStarve:         24 * time.Hour,
		HalfLife:          24 * time.Hour,
		UnknownShares:     10,
		SyncTime:          1 * time.Hour,
		DedicatedPrefix:   "ded",
		SchedulerInterval: 10,
	}
}

// Load reads and parses the sched_config file from sched_priv/.
func Load(pbsHome string) *Config {
	cfg := DefaultConfig(pbsHome)
	configPath := filepath.Join(pbsHome, "sched_priv", "sched_config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		// Parse "key: value [scope]" or "key value [scope]"
		var key, val string
		if idx := strings.Index(line, ":"); idx > 0 {
			key = strings.TrimSpace(line[:idx])
			rest := strings.TrimSpace(line[idx+1:])
			parts := strings.Fields(rest)
			if len(parts) > 0 {
				val = parts[0]
			}
		} else {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				key, val = parts[0], parts[1]
			}
		}
		if key == "" {
			continue
		}

		switch strings.ToLower(key) {
		case "scheduler_mode":
			cfg.SchedulerMode = val
		case "strict_fifo":
			cfg.StrictFIFO = parseBool(val)
		case "round_robin":
			cfg.RoundRobin = parseBool(val)
		case "by_queue":
			cfg.ByQueue = parseBool(val)
		case "fair_share":
			cfg.FairShare = parseBool(val)
		case "help_starving_jobs":
			cfg.HelpStarvingJobs = parseBool(val)
		case "sort_queues":
			cfg.SortQueues = parseBool(val)
		case "load_balancing":
			cfg.LoadBalancing = parseBool(val)
		case "sort_by":
			cfg.SortBy = val
		case "max_starve":
			cfg.MaxStarve = parseDuration(val)
		case "half_life":
			cfg.HalfLife = parseDuration(val)
		case "unknown_shares":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.UnknownShares = n
			}
		case "sync_time":
			cfg.SyncTime = parseDuration(val)
		case "dedicated_prefix":
			cfg.DedicatedPrefix = val
		case "scheduler_interval":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.SchedulerInterval = n
			}
		}
	}

	// Read server_name for connection
	serverNameFile := filepath.Join(pbsHome, "server_name")
	if data, err := os.ReadFile(serverNameFile); err == nil {
		cfg.Server = strings.TrimSpace(string(data))
	}

	return cfg
}

func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "true" || s == "1" || s == "yes"
}

// parseDuration parses "HH:MM:SS" format into a time.Duration.
func parseDuration(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 24 * time.Hour
}
