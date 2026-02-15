// Package config holds the server configuration structure.
package config

// Config holds runtime configuration for pbs_server.
type Config struct {
	PBSHome   string // PBS home directory (default /var/spool/torque)
	Port      int    // Server port (default 15001)
	StartType string // create, warm, hot, cold
	Debug     bool   // Debug mode

	// Derived paths
	ServerPriv string // server_priv directory
	JobsDir    string // server_priv/jobs
	QueuesDir  string // server_priv/queues
	NodesFile  string // server_priv/nodes
	ServerDB   string // server_priv/serverdb
	LogDir     string // server_logs
	AcctDir    string // server_priv/accounting

	// Server attributes (configurable via qmgr)
	ServerName          string
	DefaultQueue        string
	Scheduling          bool
	SchedulerMode       string // "builtin" (default) or "external"
	SchedulerIteration  int    // seconds between scheduling cycles
	NodeCheckRate       int    // seconds between node health checks
	TCPTimeout          int
	MaxRunning          int // Max concurrent running jobs (0=unlimited)
	LogLevel            int
	KeepCompleted       int // Seconds to keep completed jobs (default 300)
}

// NewConfig creates a Config with defaults for the given PBS home.
func NewConfig(pbsHome string) *Config {
	return &Config{
		PBSHome:            pbsHome,
		Port:               15001,
		ServerPriv:         pbsHome + "/server_priv",
		JobsDir:            pbsHome + "/server_priv/jobs",
		QueuesDir:          pbsHome + "/server_priv/queues",
		NodesFile:          pbsHome + "/server_priv/nodes",
		ServerDB:           pbsHome + "/server_priv/serverdb",
		LogDir:             pbsHome + "/server_logs",
		AcctDir:            pbsHome + "/server_priv/accounting",
		Scheduling:         true,
		SchedulerMode:      "builtin",
		SchedulerIteration: 10,
		NodeCheckRate:      600,
		TCPTimeout:         300,
		KeepCompleted:      300,
		LogLevel:           511, // 0x1ff
	}
}
