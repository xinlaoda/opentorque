// Command qrun forces a queued PBS job to start execution immediately.
//
// Usage:
//
//	qrun [-H host] job_id...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

func main() {
	host := flag.String("H", "", "Destination host/node to run job on")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qrun [-H host] job_id...\n\nForce a queued job to run immediately.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qrun: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.RunJob(jobID, *host); err != nil {
			fmt.Fprintf(os.Stderr, "qrun: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
