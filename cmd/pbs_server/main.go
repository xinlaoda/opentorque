// Package main implements the pbs_server daemon entry point.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/opentorque/opentorque/internal/server"
)

var version = "dev"

func main() {
	// Parse command-line flags matching C pbs_server interface
	pbsHome := flag.String("d", "/var/spool/torque", "PBS home directory")
	port := flag.Int("p", 15001, "Server port (DIS protocol)")
	startType := flag.String("t", "warm", "Start type: create, warm, hot, cold")
	debug := flag.Bool("D", false, "Debug mode (foreground, verbose logging)")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("pbs_server (Go) version %s\n", version)
		os.Exit(0)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("pbs_server (Go) version %s starting", version)

	// Build server configuration from flags
	cfg := &server.Config{
		PBSHome:   *pbsHome,
		Port:      *port,
		StartType: *startType,
		Debug:     *debug,
	}

	// Create and initialize the server daemon
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Start the server (begins listening and processing)
	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// Wait for shutdown signal (SIGINT, SIGTERM)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for sig := range sigCh {
		switch sig {
		case syscall.SIGHUP:
			log.Printf("[SERVER] Received SIGHUP, reloading config")
			// Reload could be implemented here
		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("[SERVER] Received %s, shutting down", sig)
			srv.Shutdown()
			os.Exit(0)
		}
	}
}
