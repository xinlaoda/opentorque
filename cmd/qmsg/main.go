// Command qmsg sends a message string to one or more PBS jobs.
// The message is appended to the job's stdout or stderr output file.
//
// Usage:
//
//	qmsg [-E] [-O] message job_id...
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

const (
	msgErr = 1 // Append to stderr
	msgOut = 2 // Append to stdout
)

func main() {
	toErr := flag.Bool("E", false, "Send message to stderr file")
	toOut := flag.Bool("O", false, "Send message to stdout file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qmsg [-E] [-O] message job_id...\n\nSend a message to a job's output file.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}

	fileOpt := msgErr // default to stderr
	if *toOut {
		fileOpt = msgOut
	}
	if *toErr {
		fileOpt = msgErr
	}

	message := flag.Arg(0)
	jobIDs := flag.Args()[1:]

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qmsg: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range jobIDs {
		if err := conn.MessJob(jobID, fileOpt, message); err != nil {
			fmt.Fprintf(os.Stderr, "qmsg: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	_ = strings.TrimSpace // avoid unused import
	os.Exit(exitCode)
}
