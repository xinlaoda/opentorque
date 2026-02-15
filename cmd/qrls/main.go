// Command qrls releases holds on PBS batch jobs, allowing execution to resume.
//
// Usage:
//
//	qrls [-h hold_type] job_id [job_id...]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/opentorque/opentorque/internal/cli/client"
)

func main() {
	var (
		holdType = flag.String("h", "u", "Hold type to release: u=user, o=other, s=system")
		server   = flag.String("s", "", "Specify server name")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qrls [-h hold_type] job_id [job_id...]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "qrls: no job id specified\n")
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qrls: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.ReleaseJob(jobID, *holdType); err != nil {
			fmt.Fprintf(os.Stderr, "qrls: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
