package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultMomPort      = 15002
	DefaultRMPort       = 15003
	DefaultServerPort   = 15001
	DefaultPBSHome      = "/var/spool/torque"
	DefaultCheckPoll    = 45
	DefaultStatusUpdate = 45
	DefaultPrologAlarm  = 300
	DefaultLogEvents    = 0x1ff
)

// Config holds the pbs_mom configuration.
type Config struct {
	// PBS home directory
	PBSHome string

	// Server configuration
	PBSServers []string
	ServerPort int

	// MOM ports
	MomPort int
	RMPort  int

	// Logging
	LogEvents int
	LogLevel  int

	// Timing
	CheckPollTime    int // seconds between resource checks
	StatusUpdateTime int // seconds between status updates to server
	PrologAlarm      int // seconds before prolog timeout

	// Paths
	PathJobs       string
	PathSpool      string
	PathMomPriv    string
	PathLogs       string
	PathProlog     string
	PathEpilog     string
	PathPrologUser string
	PathEpilogUser string
	PathUndeliv    string
	PathAux        string

	// Behavior
	EnableMomRestart bool
	DownOnError      bool
	MomHost          string

	// usecp directives: prefix -> local path mapping
	UseCp map[string]string

	// Static resources
	StaticResources map[string]string

	// Server & Connection
	ClientHost         string   // alias for pbsserver, deprecated
	Restricted         []string // hosts allowed to connect
	Timeout            int      // DIS TCP timeout
	MaxConnTimeoutUsec int
	AliasServerName    string
	PBSClient          []string // authorized client hosts
	RemoteReconfig     bool

	// Load Management
	IdealLoad    float64 // -1.0 = disabled
	MaxLoad      float64 // -1.0 = disabled
	AutoIdealLoad string  // script path
	AutoMaxLoad   string  // script path

	// Resource Enforcement
	IgnWalltime bool
	IgnMem      bool
	IgnCput     bool
	IgnVmem     bool
	CputMult    float64 // multiplier for cput
	WallMult    float64 // multiplier for walltime

	// Logging (extended)
	LogDirectory    string
	LogFileMaxSize  int
	LogFileRollDepth int
	LogFileSuffix   string
	LogKeepDays     int

	// Job Execution
	JobStarter              string
	JobStarterRunPrivileged bool
	PreExec                 string
	SourceLoginBatch        bool
	SourceLoginInteractive  bool
	JobOutputFileUmask      int
	JobStartBlockTime       int
	JobExitWaitTime         int
	JobOomScoreAdjust       int
	AttemptToMakeDir        bool
	ExecWithExec            bool
	PresetupPrologue        string
	ExtPwdRetry             int

	// File & Directory
	TmpDir                string
	NodefileSuffix        string
	NospoolDirList        string
	RcpCmd                string
	XauthPath             string
	SpoolAsFinalName      bool
	RemoteCheckpointDirs  string

	// Status & Polling
	MaxUpdatesBeforeSending int

	// Node Health
	NodeCheckScript   string
	NodeCheckInterval string // "jobstart", "jobend", or seconds

	// Configuration Management
	ConfigVersion    string
	ForceOverwrite   bool
	RejectJobSubmission bool

	// Checkpoint
	CheckpointInterval int
	CheckpointScript   string
	RestartScript      string
	CheckpointRunExe   string

	// Variable Attributes
	VarAttr string

	// Memory & CPU
	UseSMT                  bool
	MemoryPressureThreshold int
	MemoryPressureDuration  int
	MomOomImmunize          bool

	// Job Hierarchy
	MaxJoinJobWaitTime    int
	ResendJoinJobWaitTime int
	MomHierarchyRetryTime int
	JobDirectorySticky    bool

	// Hardware
	CudaVisibleDevices bool

	// Advanced
	ReducePrologChecks bool
	ThreadUnlinkCalls  bool
	ReporterMom        bool
	LoginNode          bool
	AllocParCmd        string
}

func NewDefaultConfig() *Config {
	home := DefaultPBSHome
	return &Config{
		PBSHome:          home,
		PBSServers:       []string{},
		ServerPort:       DefaultServerPort,
		MomPort:          DefaultMomPort,
		RMPort:           DefaultRMPort,
		LogEvents:        DefaultLogEvents,
		CheckPollTime:    DefaultCheckPoll,
		StatusUpdateTime: DefaultStatusUpdate,
		PrologAlarm:      DefaultPrologAlarm,
		PathJobs:         filepath.Join(home, "mom_priv", "jobs"),
		PathSpool:        filepath.Join(home, "spool"),
		PathMomPriv:      filepath.Join(home, "mom_priv"),
		PathLogs:         filepath.Join(home, "mom_logs"),
		PathProlog:       filepath.Join(home, "mom_priv", "prologue"),
		PathEpilog:       filepath.Join(home, "mom_priv", "epilogue"),
		PathPrologUser:   filepath.Join(home, "mom_priv", "prologue.user"),
		PathEpilogUser:   filepath.Join(home, "mom_priv", "epilogue.user"),
		PathUndeliv:      filepath.Join(home, "undelivered"),
		PathAux:          filepath.Join(home, "aux"),
		UseCp:            make(map[string]string),
		StaticResources:  make(map[string]string),

		// Server & Connection defaults
		Timeout: 300,

		// Load Management defaults
		IdealLoad: -1.0,
		MaxLoad:   -1.0,

		// Resource Enforcement defaults
		CputMult: 1.0,
		WallMult: 1.0,

		// Logging defaults
		LogFileRollDepth: 1,

		// Job Execution defaults
		SourceLoginBatch:       true,
		SourceLoginInteractive: true,
		JobOutputFileUmask:     0077,
		JobStartBlockTime:      5,
		JobExitWaitTime:        600,
		ExtPwdRetry:            3,

		// Memory & CPU defaults
		UseSMT:         true,
		MomOomImmunize: true,

		// Job Hierarchy defaults
		MaxJoinJobWaitTime:    600,
		ResendJoinJobWaitTime: 300,

		// Hardware defaults
		CudaVisibleDevices: true,

		// Advanced defaults
		ThreadUnlinkCalls: true,
	}
}

// Load reads the mom config file.
func (c *Config) Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c.parseLine(line)
	}
	return scanner.Err()
}

func (c *Config) parseLine(line string) {
	// Handle $variable directives
	if strings.HasPrefix(line, "$") {
		c.parseDirective(line[1:])
		return
	}

	// Handle resource definitions: name value
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 2 {
		c.StaticResources[parts[0]] = strings.TrimSpace(parts[1])
	}
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

func (c *Config) parseDirective(line string) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return
	}

	key := strings.ToLower(parts[0])
	var val string
	if len(parts) > 1 {
		val = strings.Join(parts[1:], " ")
	}

	switch key {
	case "pbsserver":
		servers := strings.Fields(val)
		for _, s := range servers {
			// Strip port if present
			host := s
			if h, _, err := net.SplitHostPort(s); err == nil {
				host = h
			}
			c.PBSServers = append(c.PBSServers, host)
		}

	case "logevent":
		if v, err := strconv.ParseInt(val, 0, 64); err == nil {
			c.LogEvents = int(v)
		}

	case "check_poll_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.CheckPollTime = v
		}

	case "status_update_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.StatusUpdateTime = v
		}

	case "prologalarm":
		if v, err := strconv.Atoi(val); err == nil {
			c.PrologAlarm = v
		}

	case "enablemomrestart":
		c.EnableMomRestart = parseBool(val)

	case "down_on_error":
		c.DownOnError = parseBool(val)

	case "mom_host":
		c.MomHost = val

	case "usecp":
		// $usecp host:path localpath
		fields := strings.Fields(val)
		if len(fields) >= 2 {
			c.UseCp[fields[0]] = fields[1]
		}

	// Server & Connection
	case "clienthost":
		c.ClientHost = val
		// clienthost is alias for pbsserver (deprecated)
		host := val
		if h, _, err := net.SplitHostPort(val); err == nil {
			host = h
		}
		c.PBSServers = append(c.PBSServers, host)

	case "restricted":
		c.Restricted = append(c.Restricted, strings.Fields(val)...)

	case "timeout":
		if v, err := strconv.Atoi(val); err == nil {
			c.Timeout = v
		}

	case "max_conn_timeout_micro_sec":
		if v, err := strconv.Atoi(val); err == nil {
			c.MaxConnTimeoutUsec = v
		}

	case "alias_server_name":
		c.AliasServerName = val

	case "pbsclient":
		c.PBSClient = append(c.PBSClient, strings.Fields(val)...)

	case "remote_reconfig":
		c.RemoteReconfig = parseBool(val)

	// Load Management
	case "ideal_load":
		if v, err := strconv.ParseFloat(val, 64); err == nil {
			c.IdealLoad = v
		}

	case "max_load":
		if v, err := strconv.ParseFloat(val, 64); err == nil {
			c.MaxLoad = v
		}

	case "auto_ideal_load":
		c.AutoIdealLoad = val

	case "auto_max_load":
		c.AutoMaxLoad = val

	// Resource Enforcement
	case "ignwalltime":
		c.IgnWalltime = parseBool(val)

	case "ignmem":
		c.IgnMem = parseBool(val)

	case "igncput":
		c.IgnCput = parseBool(val)

	case "ignvmem":
		c.IgnVmem = parseBool(val)

	case "cputmult":
		if v, err := strconv.ParseFloat(val, 64); err == nil {
			c.CputMult = v
		}

	case "wallmult":
		if v, err := strconv.ParseFloat(val, 64); err == nil {
			c.WallMult = v
		}

	// Logging (extended)
	case "loglevel":
		if v, err := strconv.Atoi(val); err == nil {
			c.LogLevel = v
		}

	case "log_directory":
		c.LogDirectory = val

	case "log_file_max_size":
		if v, err := strconv.Atoi(val); err == nil {
			c.LogFileMaxSize = v
		}

	case "log_file_roll_depth":
		if v, err := strconv.Atoi(val); err == nil {
			c.LogFileRollDepth = v
		}

	case "log_file_suffix":
		c.LogFileSuffix = val

	case "log_keep_days":
		if v, err := strconv.Atoi(val); err == nil {
			c.LogKeepDays = v
		}

	// Job Execution
	case "job_starter":
		c.JobStarter = val

	case "job_starter_run_privileged":
		c.JobStarterRunPrivileged = parseBool(val)

	case "preexec":
		c.PreExec = val

	case "source_login_batch":
		c.SourceLoginBatch = parseBool(val)

	case "source_login_interactive":
		c.SourceLoginInteractive = parseBool(val)

	case "job_output_file_umask":
		if v, err := strconv.ParseInt(val, 8, 64); err == nil {
			c.JobOutputFileUmask = int(v)
		}

	case "job_start_block_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.JobStartBlockTime = v
		}

	case "job_exit_wait_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.JobExitWaitTime = v
		}

	case "job_oom_score_adjust":
		if v, err := strconv.Atoi(val); err == nil {
			c.JobOomScoreAdjust = v
		}

	case "attempttomakedirectory":
		c.AttemptToMakeDir = parseBool(val)

	case "exec_with_exec":
		c.ExecWithExec = parseBool(val)

	case "presetup_prologue":
		c.PresetupPrologue = val

	case "ext_pwd_retry":
		if v, err := strconv.Atoi(val); err == nil {
			c.ExtPwdRetry = v
		}

	// File & Directory
	case "tmpdir":
		c.TmpDir = val

	case "nodefile_suffix":
		c.NodefileSuffix = val

	case "nospool_dir_list":
		c.NospoolDirList = val

	case "rcpcmd":
		c.RcpCmd = val

	case "xauthpath":
		c.XauthPath = val

	case "spool_as_final_name":
		c.SpoolAsFinalName = parseBool(val)

	case "remote_checkpoint_dirs":
		c.RemoteCheckpointDirs = val

	// Status & Polling
	case "max_updates_before_sending":
		if v, err := strconv.Atoi(val); err == nil {
			c.MaxUpdatesBeforeSending = v
		}

	// Node Health
	case "node_check_script":
		c.NodeCheckScript = val

	case "node_check_interval":
		c.NodeCheckInterval = val

	// Configuration Management
	case "configversion":
		c.ConfigVersion = val

	case "force_overwrite":
		c.ForceOverwrite = parseBool(val)

	case "reject_job_submission":
		c.RejectJobSubmission = parseBool(val)

	// Checkpoint
	case "checkpoint_interval":
		if v, err := strconv.Atoi(val); err == nil {
			c.CheckpointInterval = v
		}

	case "checkpoint_script":
		c.CheckpointScript = val

	case "restart_script":
		c.RestartScript = val

	case "checkpoint_run_exe":
		c.CheckpointRunExe = val

	// Variable Attributes
	case "varattr":
		c.VarAttr = val

	// Memory & CPU
	case "use_smt":
		c.UseSMT = parseBool(val)

	case "memory_pressure_threshold":
		if v, err := strconv.Atoi(val); err == nil {
			c.MemoryPressureThreshold = v
		}

	case "memory_pressure_duration":
		if v, err := strconv.Atoi(val); err == nil {
			c.MemoryPressureDuration = v
		}

	case "mom_oom_immunize":
		c.MomOomImmunize = parseBool(val)

	// Job Hierarchy
	case "max_join_job_wait_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.MaxJoinJobWaitTime = v
		}

	case "resend_join_job_wait_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.ResendJoinJobWaitTime = v
		}

	case "mom_hierarchy_retry_time":
		if v, err := strconv.Atoi(val); err == nil {
			c.MomHierarchyRetryTime = v
		}

	case "job_directory_sticky":
		c.JobDirectorySticky = parseBool(val)

	// Hardware
	case "cuda_visible_devices":
		c.CudaVisibleDevices = parseBool(val)

	// Advanced
	case "reduce_prolog_checks":
		c.ReducePrologChecks = parseBool(val)

	case "thread_unlink_calls":
		c.ThreadUnlinkCalls = parseBool(val)

	case "reporter_mom":
		c.ReporterMom = parseBool(val)

	case "login_node":
		c.LoginNode = parseBool(val)

	case "alloc_par_cmd":
		c.AllocParCmd = val
	}
}

// LoadServerName reads the server_name file.
func (c *Config) LoadServerName() error {
	path := filepath.Join(c.PBSHome, "server_name")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(string(data))
	if name != "" && len(c.PBSServers) == 0 {
		c.PBSServers = append(c.PBSServers, name)
	}
	return nil
}

// EnsureDirs creates required directories.
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.PathJobs,
		c.PathSpool,
		c.PathMomPriv,
		c.PathLogs,
		c.PathUndeliv,
		c.PathAux,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("config: mkdir %s: %w", d, err)
		}
	}
	return nil
}

// ConfigFile returns the default config file path.
func (c *Config) ConfigFile() string {
	return filepath.Join(c.PathMomPriv, "config")
}
