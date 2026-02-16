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

	// Core settings
	ServerName    string
	DefaultQueue  string
	Scheduling    bool
	SchedulerMode string // "builtin" (default) or "external"

	// Scheduling & timing
	SchedulerIteration int // seconds between scheduler cycles (default 10)
	NodeCheckRate      int // seconds between node health checks (default 600)
	TCPTimeout         int // TCP connection timeout seconds (default 300)
	KeepCompleted      int // seconds to keep completed jobs (default 300)
	JobStatRate        int // seconds between job stat polling (default 0=off)
	PollJobs           bool
	PingRate           int // seconds between MOM pings (default 300)
	JobStartTimeout    int // max seconds for MOM to start a job (default 300)
	JobForceCancelTime int // max seconds before force-cancel stuck jobs (default 0=off)
	JobSyncTimeout     int // MOM sync timeout at startup (default 120)

	// Logging
	LogLevel            int // event bitmask (default 511)
	LogFileMaxSize      int // max server log size in bytes (default 0=unlimited)
	LogFileRollDepth    int // rotated log files to keep (default 1)
	LogKeepDays         int // days to retain old logs (default 0=unlimited)
	RecordJobInfo       bool
	RecordJobScript     bool
	JobLogFileMaxSize   int
	JobLogFileRollDepth int
	JobLogKeepDays      int

	// Access control
	Managers         string // comma-sep list of admin users (user@host)
	Operators        string // comma-sep list of operator users
	ACLHostEnable    bool
	ACLHosts         string // comma-sep allowed submission hosts
	ACLUserEnable    bool
	ACLUsers         string // comma-sep allowed users
	ACLRoots         string // comma-sep users with root access
	ACLLogicOr       bool   // true=OR, false=AND for ACL evaluation
	ACLGroupSloppy   bool
	ACLUserHosts     string
	ACLGroupHosts    string

	// Resource limits
	MaxRunning       int // server-wide max running jobs (0=unlimited)
	MaxUserRun       int // per-user max running jobs (0=unlimited)
	MaxGroupRun      int // per-group max running jobs (0=unlimited)
	MaxUserQueuable  int // per-user max queued jobs (0=unlimited)
	ResourcesAvail   map[string]string // server-level available resources
	ResourcesDefault map[string]string // default resource values for jobs
	ResourcesMax     map[string]string // max resource limits per job
	ResourcesCost    map[string]string // resource cost weights

	// Mail
	MailDomain        string
	MailFrom          string
	NoMailForce       bool
	MailSubjectFmt    string
	MailBodyFmt       string
	EmailBatchSeconds int

	// Node & job policy
	DefaultNode          string
	NodePack             bool
	QueryOtherJobs       bool
	MOMJobSync           bool
	DownOnError          bool
	DisableServerIdCheck bool
	AllowNodeSubmit      bool
	AllowProxyUser       bool
	AutoNodeNP           bool
	NPDefault            int
	JobNanny             bool
	OwnerPurge           bool
	CopyOnRerun          bool
	JobExclusiveOnUse    bool
	DisableAutoRequeue   bool
	AutoRequeueExitCode  int // exit code that triggers auto-requeue (default -1=off)
	DontWriteNodesFile   bool

	// Job array & display
	MaxJobArraySize        int // max sub-jobs per array (default 10000)
	MaxSlotLimit           int // max concurrent array sub-jobs (default 0=unlimited)
	CloneBatchSize         int // array clone batch size (default 256)
	CloneBatchDelay        int // delay between batches in seconds (default 2)
	MoabArrayCompatible    bool
	DisplayJobServerSuffix bool // show server suffix in job IDs (default true)
	JobSuffixAlias         string
	UseJobsSubdirs         bool

	// Kill & cancel timeouts
	KillDelay            int // seconds between SIGTERM and SIGKILL (default 2)
	UserKillDelay        int // user-initiated kill delay (default 0=use KillDelay)
	ExitCodeCanceledJob  int // exit code for canceled jobs (default 271)
	TimeoutForJobDelete  int // qdel timeout (default 120)
	TimeoutForJobRequeue int // qrerun timeout (default 120)

	// Hardware-specific (excluding Cray)
	DefaultGpuMode string
	IdleSlotLimit  int // max idle slots (0=unlimited)
	CgroupPerTask  bool
	PassCpuClock   bool

	// Other
	SubmitHosts             string
	NodeSubmitExceptions    string
	NodeSuffix              string
	Comment                 string
	LockFileUpdateTime      int
	LockFileCheckTime       int
	InteractiveJobsCanRoam  bool
	LegacyVmem              bool
	GhostArrayRecovery      bool
	TCPIncomingTimeout      int
	JobFullReportTime       int
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
		PingRate:           300,
		JobStartTimeout:    300,
		JobSyncTimeout:     120,
		LogLevel:           511,
		LogFileRollDepth:   1,
		QueryOtherJobs:     true,
		MOMJobSync:         true,
		MaxJobArraySize:    10000,
		CloneBatchSize:     256,
		CloneBatchDelay:    2,
		DisplayJobServerSuffix: true,
		KillDelay:          2,
		ExitCodeCanceledJob: 271,
		TimeoutForJobDelete: 120,
		TimeoutForJobRequeue: 120,
		AutoRequeueExitCode: -1,
		ResourcesAvail:   make(map[string]string),
		ResourcesDefault:  make(map[string]string),
		ResourcesMax:      make(map[string]string),
		ResourcesCost:     make(map[string]string),
	}
}
