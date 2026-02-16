// Command qdel deletes (cancels) PBS batch jobs.
//
// Usage:
//
//	qdel [options] job_id [job_id...]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

func main() {
	var (
		purge      = flag.Bool("p", false, "Purge the job (force delete)")
		msg        = flag.String("m", "", "Add a message to the job delete")
		server     = flag.String("s", "", "Specify server name")
		arrayRange = flag.String("t", "", "Array range for job array delete")
		delay      = flag.String("W", "", "Delay before delete (seconds)")
	)
	_, _ = arrayRange, delay
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qdel [options] job_id [job_id...]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "qdel: no job id specified\n")
		flag.Usage()
		os.Exit(1)
	}

	// Build extension string from flags
	extend := ""
	if *purge {
		extend = "purge"
	}

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qdel: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if *msg != "" {
			fmt.Fprintf(os.Stderr, "qdel: deleting %s: %s\n", jobID, *msg)
		}
		if err := conn.DeleteJobExtend(jobID, extend); err != nil {
			fmt.Fprintf(os.Stderr, "qdel: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
