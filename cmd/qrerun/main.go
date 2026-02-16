// Command qrerun requeues a running PBS job back to the queued state.
//
// Usage:
//
//	qrerun [-f] job_id...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/opentorque/opentorque/internal/cli/client"
)

func main() {
	force := flag.Bool("f", false, "Force rerun even if job is not rerunable")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qrerun [-f] job_id...\n\nRequeue a running job back to queued state.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qrerun: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.RerunJob(jobID, *force); err != nil {
			fmt.Fprintf(os.Stderr, "qrerun: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
