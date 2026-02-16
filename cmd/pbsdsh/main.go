// Command pbsdsh executes a command on all nodes allocated to a PBS job.
// It reads the PBS_NODEFILE to determine which nodes to target and runs
// the command on each node via SSH.
//
// Usage:
//
//	pbsdsh [-c copies] [-o] [-s] [-v] [command [args...]]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

func main() {
	var (
		copies  = flag.Int("c", 0, "Number of copies to run (0 = one per node)")
		oneNode = flag.Bool("o", false, "Run on first node only")
		stdin   = flag.Bool("s", false, "Read command from stdin")
		verbose = flag.Bool("v", false, "Verbose output")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pbsdsh [-c copies] [-o] [-s] [-v] [command [args...]]\n\n")
		fmt.Fprintf(os.Stderr, "Execute a command on all nodes allocated to a PBS job.\n\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n  PBS_NODEFILE  File containing allocated node names (one per line)\n  PBS_JOBID     Current job ID\n")
	}
	flag.Parse()

	// Read nodes from PBS_NODEFILE
	nodeFile := os.Getenv("PBS_NODEFILE")
	if nodeFile == "" {
		fmt.Fprintf(os.Stderr, "pbsdsh: PBS_NODEFILE not set (not running inside a PBS job?)\n")
		os.Exit(1)
	}

	nodes, err := readNodeFile(nodeFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pbsdsh: cannot read nodefile %s: %v\n", nodeFile, err)
		os.Exit(1)
	}

	if len(nodes) == 0 {
		fmt.Fprintf(os.Stderr, "pbsdsh: no nodes found in %s\n", nodeFile)
		os.Exit(1)
	}

	// Determine command to run
	var cmdArgs []string
	if *stdin {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			cmdArgs = strings.Fields(scanner.Text())
		}
	} else {
		cmdArgs = flag.Args()
	}

	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "pbsdsh: no command specified\n")
		os.Exit(1)
	}

	// Determine target nodes
	targetNodes := uniqueNodes(nodes)
	if *oneNode {
		targetNodes = targetNodes[:1]
	}
	if *copies > 0 && *copies < len(targetNodes) {
		targetNodes = targetNodes[:*copies]
	}

	if *verbose {
		fmt.Fprintf(os.Stderr, "pbsdsh: running on %d node(s): %s\n", len(targetNodes), strings.Join(targetNodes, ", "))
	}

	// Execute on each node in parallel
	var wg sync.WaitGroup
	exitCode := 0
	var mu sync.Mutex

	for _, node := range targetNodes {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()

			// Build SSH command
			sshArgs := append([]string{host, "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes"}, cmdArgs...)
			cmd := exec.Command("ssh", sshArgs...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if *verbose {
				fmt.Fprintf(os.Stderr, "pbsdsh: %s: running %s\n", host, strings.Join(cmdArgs, " "))
			}

			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "pbsdsh: %s: %v\n", host, err)
				mu.Lock()
				exitCode = 1
				mu.Unlock()
			}
		}(node)
	}

	wg.Wait()
	os.Exit(exitCode)
}

// readNodeFile reads node names from the PBS nodefile.
func readNodeFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var nodes []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		node := strings.TrimSpace(scanner.Text())
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes, scanner.Err()
}

// uniqueNodes returns unique node names preserving order.
func uniqueNodes(nodes []string) []string {
	seen := make(map[string]bool)
	var unique []string
	for _, n := range nodes {
		if !seen[n] {
			seen[n] = true
			unique = append(unique, n)
		}
	}
	return unique
}
