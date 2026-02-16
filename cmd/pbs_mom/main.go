package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/xinlaoda/opentorque/internal/mom/config"
	"github.com/xinlaoda/opentorque/internal/mom/mom"
	"github.com/xinlaoda/opentorque/pkg/pbslog"
)

const version = "7.0.0-go"

func main() {
	var (
		pbsHome    = flag.String("d", config.DefaultPBSHome, "PBS home directory")
		configFile = flag.String("c", "", "Path to config file")
		momPort    = flag.Int("M", config.DefaultMomPort, "MOM service port")
		rmPort     = flag.Int("R", config.DefaultRMPort, "Resource manager port")
		debug      = flag.Bool("D", false, "Debug mode (don't daemonize)")
		showVer    = flag.Bool("version", false, "Show version")
		logFile    = flag.String("L", "", "Log file path")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("pbs_mom (Go) version %s\n", version)
		fmt.Printf("Go version: %s\n", runtime.Version())
		fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	// Set up logging with YYYYMMDD dated files
	setupLogging(*logFile, *pbsHome, *debug)

	log.Printf("pbs_mom (Go) version %s starting", version)
	log.Printf("Go %s on %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	// Load configuration
	cfg := config.NewDefaultConfig()
	cfg.PBSHome = *pbsHome
	cfg.MomPort = *momPort
	cfg.RMPort = *rmPort

	// Update paths based on PBSHome
	cfg.PathJobs = filepath.Join(cfg.PBSHome, "mom_priv", "jobs")
	cfg.PathSpool = filepath.Join(cfg.PBSHome, "spool")
	cfg.PathMomPriv = filepath.Join(cfg.PBSHome, "mom_priv")
	cfg.PathLogs = filepath.Join(cfg.PBSHome, "mom_logs")
	cfg.PathProlog = filepath.Join(cfg.PBSHome, "mom_priv", "prologue")
	cfg.PathEpilog = filepath.Join(cfg.PBSHome, "mom_priv", "epilogue")
	cfg.PathPrologUser = filepath.Join(cfg.PBSHome, "mom_priv", "prologue.user")
	cfg.PathEpilogUser = filepath.Join(cfg.PBSHome, "mom_priv", "epilogue.user")
	cfg.PathUndeliv = filepath.Join(cfg.PBSHome, "undelivered")
	cfg.PathAux = filepath.Join(cfg.PBSHome, "aux")

	// Load config file
	cfgPath := *configFile
	if cfgPath == "" {
		cfgPath = cfg.ConfigFile()
	}

	if _, err := os.Stat(cfgPath); err == nil {
		log.Printf("Loading config from %s", cfgPath)
		if err := cfg.Load(cfgPath); err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
		log.Printf("Config file %s loaded successfully", cfgPath)
	} else {
		log.Printf("No config file found at %s, using defaults", cfgPath)
	}

	// Apply config-driven log directory override (re-open log if needed)
	if cfg.LogDirectory != "" && *logFile == "" {
		logDir := cfg.LogDirectory
		dl, err := pbslog.Setup(logDir, *debug)
		if err == nil {
			_ = dl // kept open for process lifetime
			log.Printf("Log redirected to %s (from $log_directory config)", logDir)
		}
	}

	// Apply log level from config
	if cfg.LogLevel > 0 {
		log.Printf("Log level set to %d", cfg.LogLevel)
	}

	// Load server name if no servers configured
	if len(cfg.PBSServers) == 0 {
		if err := cfg.LoadServerName(); err != nil {
			log.Printf("Warning: could not load server_name: %v", err)
		}
	}

	if len(cfg.PBSServers) == 0 {
		log.Fatal("No PBS server configured. Set $pbsserver in config or create server_name file.")
	}

	// Override ports from command line
	if *momPort != config.DefaultMomPort {
		cfg.MomPort = *momPort
	}
	if *rmPort != config.DefaultRMPort {
		cfg.RMPort = *rmPort
	}

	// Check if we're root (required for job management on Unix)
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		log.Printf("Warning: pbs_mom should run as root for proper job management")
	}

	// Create and run daemon
	daemon := mom.New(cfg)
	if err := daemon.Run(); err != nil {
		log.Fatalf("pbs_mom failed: %v", err)
	}
}

func setupLogging(logFile, pbsHome string, debug bool) {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	// Explicit log file overrides everything
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot open log file %s: %v\n", logFile, err)
			os.Exit(1)
		}
		log.SetOutput(f)
		return
	}

	if !debug {
		// Use YYYYMMDD dated log files in mom_logs/
		logDir := filepath.Join(pbsHome, "mom_logs")
		dl, err := pbslog.Setup(logDir, false)
		if err != nil {
			log.SetOutput(os.Stderr)
			return
		}
		_ = dl // kept open for process lifetime
	}
}
