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

	"github.com/opentorque/opentorque/internal/cli/client"
)

func main() {
	var (
		purge  = flag.Bool("p", false, "Purge the job (force delete)")
		msg    = flag.String("m", "", "Add a message to the job delete")
		server = flag.String("s", "", "Specify server name")
	)
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

	// Acknowledge flags (purge/msg may be used in future extension)
	_ = purge
	_ = msg

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qdel: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.DeleteJob(jobID); err != nil {
			fmt.Fprintf(os.Stderr, "qdel: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
