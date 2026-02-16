// Command qstart starts (enables scheduling for) one or more PBS queues.
//
// Usage:
//
//	qstart queue[@server]...
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
		fmt.Fprintf(os.Stderr, "Usage: qstart queue[@server]...\n\nStart scheduling for the specified queue(s).\n")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qstart: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	attrs := []dis.SvrAttrl{{Name: "started", Value: "True"}}

	exitCode := 0
	for _, queue := range flag.Args() {
		if err := conn.Manager(dis.MgrCmdSet, dis.MgrObjQueue, queue, attrs); err != nil {
			fmt.Fprintf(os.Stderr, "qstart: %s: %v\n", queue, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
