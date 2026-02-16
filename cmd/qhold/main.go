// Command qhold places a hold on PBS batch jobs, preventing execution.
//
// Usage:
//
//	qhold [-h hold_type] job_id [job_id...]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

func main() {
	var (
		holdType = flag.String("h", "u", "Hold type: u=user, o=other, s=system")
		server   = flag.String("s", "", "Specify server name")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qhold [-h hold_type] job_id [job_id...]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "qhold: no job id specified\n")
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qhold: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.HoldJob(jobID, *holdType); err != nil {
			fmt.Fprintf(os.Stderr, "qhold: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
