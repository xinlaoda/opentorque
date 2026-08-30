// Command pbstool is a small admin utility for OpenTorque. Currently it
// provides:
//
//	pbstool migrate -d <pbs_home> -dsn <postgres_dsn>
//
// which migrates an existing file-backed server_priv into PostgreSQL before the
// server is switched to the PostgreSQL state store (PBS_PG_DSN).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "migrate":
		fs := flag.NewFlagSet("migrate", flag.ExitOnError)
		home := fs.String("d", "/var/spool/torque", "PBS home directory")
		dsn := fs.String("dsn", "", "PostgreSQL DSN (postgres://user:pass@host:5432/db)")
		fs.Parse(os.Args[2:])
		if *dsn == "" {
			log.Fatal("migrate: -dsn is required")
		}
		cfg := config.NewConfig(*home)
		if err := server.MigrateFilesToPostgres(cfg, *dsn); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		log.Printf("migrated file store (%s) to PostgreSQL", *home)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `pbstool - OpenTorque admin utility

usage:
  pbstool migrate -d <pbs_home> -dsn <postgres_dsn>
      one-time migration of the file-backed server state into PostgreSQL
`)
}
