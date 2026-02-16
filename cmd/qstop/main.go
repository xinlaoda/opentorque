// Command qstop stops (disables scheduling for) one or more PBS queues.
// Queued jobs remain in the queue but will not be dispatched.
//
// Usage:
//
//	qstop queue[@server]...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/opentorque/opentorque/internal/cli/client"
	"github.com/opentorque/opentorque/internal/cli/dis"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qstop queue[@server]...\n\nStop scheduling for the specified queue(s).\n")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qstop: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	attrs := []dis.SvrAttrl{{Name: "started", Value: "False"}}

	exitCode := 0
	for _, queue := range flag.Args() {
		if err := conn.Manager(dis.MgrCmdSet, dis.MgrObjQueue, queue, attrs); err != nil {
			fmt.Fprintf(os.Stderr, "qstop: %s: %v\n", queue, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
