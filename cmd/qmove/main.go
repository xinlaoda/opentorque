// Command qmove moves a PBS job to a different queue.
//
// Usage:
//
//	qmove destination job_id...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/opentorque/opentorque/internal/cli/client"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qmove destination job_id...\n\nMove a job to a different queue.\n\n  destination: queue[@server]\n")
	}
	flag.Parse()

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}

	destination := flag.Arg(0)
	jobIDs := flag.Args()[1:]

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qmove: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range jobIDs {
		if err := conn.MoveJob(jobID, destination); err != nil {
			fmt.Fprintf(os.Stderr, "qmove: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
