// Command qenable enables one or more PBS queues to accept new job submissions.
//
// Usage:
//
//	qenable queue[@server]...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qenable queue[@server]...\n\nEnable job submission for the specified queue(s).\n")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qenable: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	attrs := []dis.SvrAttrl{{Name: "enabled", Value: "True"}}

	exitCode := 0
	for _, queue := range flag.Args() {
		if err := conn.Manager(dis.MgrCmdSet, dis.MgrObjQueue, queue, attrs); err != nil {
			fmt.Fprintf(os.Stderr, "qenable: %s: %v\n", queue, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
