// Command qchkpt requests a checkpoint of a running PBS job.
//
// Usage:
//
//	qchkpt job_id...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qchkpt job_id...\n\nRequest checkpoint of running job(s).\n")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qchkpt: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.CheckpointJob(jobID); err != nil {
			fmt.Fprintf(os.Stderr, "qchkpt: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
