// Command qsig sends a signal to a running PBS job.
//
// Usage:
//
//	qsig [-s signal] job_id...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/opentorque/opentorque/internal/cli/client"
)

func main() {
	signal := flag.String("s", "SIGTERM", "Signal to send (e.g., SIGKILL, SIGUSR1, 9)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qsig [-s signal] job_id...\n\nSend a signal to a running job.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsig: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.SignalJob(jobID, *signal); err != nil {
			fmt.Fprintf(os.Stderr, "qsig: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
