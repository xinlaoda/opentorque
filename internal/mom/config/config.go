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
	CheckPollTime       int // seconds between resource checks
	StatusUpdateTime    int // seconds between status updates to server
	PrologAlarm         int // seconds before prolog timeout

	// Paths
	PathJobs     string
	PathSpool    string
	PathMomPriv  string
	PathLogs     string
	PathProlog   string
	PathEpilog   string
	PathPrologUser string
	PathEpilogUser string
	PathUndeliv  string
	PathAux      string

	// Behavior
	EnableMomRestart bool
	DownOnError      bool
	MomHost          string

	// usecp directives: prefix -> local path mapping
	UseCp map[string]string

	// Static resources
	StaticResources map[string]string
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
		c.EnableMomRestart = strings.ToLower(val) == "true"

	case "down_on_error":
		c.DownOnError = strings.ToLower(val) == "true"

	case "mom_host":
		c.MomHost = val

	case "usecp":
		// $usecp host:path localpath
		fields := strings.Fields(val)
		if len(fields) >= 2 {
			c.UseCp[fields[0]] = fields[1]
		}
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
