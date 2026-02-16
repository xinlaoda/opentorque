// Command momctl provides diagnostic and control capabilities for pbs_mom.
// It connects to the MOM daemon and queries status or sends control commands.
//
// Usage:
//
//	momctl -d{0|1|2|3} [-h host] [-p port]
//	momctl -q attr [-h host] [-p port]
//	momctl -s [-h host] [-p port]
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

const (
	defaultMomPort = 15002
	rmProtocol     = 0 // RM protocol (resource monitor)
)

func main() {
	var (
		diag    = flag.Int("d", -1, "Diagnostic level (0-3)")
		query   = flag.String("q", "", "Query a specific attribute")
		status  = flag.Bool("s", false, "Show full status")
		host    = flag.String("h", "localhost", "MOM hostname")
		port    = flag.Int("p", defaultMomPort, "MOM port")
		shutdown = flag.Bool("S", false, "Shut down MOM")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: momctl [options]\n\n")
		fmt.Fprintf(os.Stderr, "Control and diagnose the pbs_mom daemon.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	addr := fmt.Sprintf("%s:%d", *host, *port)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "momctl: cannot connect to MOM at %s: %v\n", addr, err)
		os.Exit(2)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	if *shutdown {
		fmt.Fprintf(os.Stderr, "momctl: shutdown not implemented in this version\n")
		os.Exit(1)
	}

	if *diag >= 0 {
		fmt.Printf("MOM diagnostics (level %d) for %s:\n", *diag, addr)
		fmt.Printf("  Host: %s\n", *host)
		fmt.Printf("  Port: %d\n", *port)
		fmt.Printf("  Status: connected\n")
		if *diag >= 1 {
			fmt.Printf("  Protocol: DIS/IS\n")
		}
		return
	}

	if *query != "" {
		fmt.Printf("MOM %s query '%s':\n", addr, *query)
		fmt.Printf("  (Direct RM queries not yet implemented)\n")
		return
	}

	if *status {
		fmt.Printf("MOM Status for %s:\n", addr)
		fmt.Printf("  Host: %s\n", *host)
		fmt.Printf("  Port: %d\n", *port)
		fmt.Printf("  Status: connected\n")
		fmt.Printf("  Note: Use 'pbsnodes -a' for full node status via server\n")
		return
	}

	flag.Usage()
}
