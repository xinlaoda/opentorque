// Command qorder swaps the scheduling order of two PBS jobs.
//
// Usage:
//
//	qorder job_id1 job_id2
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qorder job_id1 job_id2\n\nSwap the scheduling order of two jobs in a queue.\n")
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qorder: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	if err := conn.OrderJob(flag.Arg(0), flag.Arg(1)); err != nil {
		fmt.Fprintf(os.Stderr, "qorder: %v\n", err)
		os.Exit(1)
	}
}
