// Command qterm shuts down the PBS server.
//
// Usage:
//
//	qterm [-t type] [server]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinlaoda/opentorque/internal/cli/client"
)

const (
	shutImmediate = 0 // Shut down immediately
	shutQuick     = 2 // Wait for running jobs to finish, then shut down
)

func main() {
	shutType := flag.String("t", "quick", "Shutdown type: quick (default) or immediate")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qterm [-t type] [server]\n\nShut down the PBS server.\n\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nShutdown types:\n  quick       Wait for running jobs, then shut down (default)\n  immediate   Shut down immediately\n")
	}
	flag.Parse()

	manner := shutQuick
	if *shutType == "immediate" {
		manner = shutImmediate
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qterm: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	if err := conn.Shutdown(manner); err != nil {
		fmt.Fprintf(os.Stderr, "qterm: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Server shutdown initiated")
}
