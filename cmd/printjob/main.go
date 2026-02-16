// Command printjob reads and displays the contents of a PBS job file
// from the server's job database directory. Used for debugging.
//
// Usage:
//
//	printjob [-a] job_file...
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var (
		showAll = flag.Bool("a", false, "Show all attributes including internal")
		pbsHome = flag.String("p", "/var/spool/torque", "PBS home directory")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: printjob [-a] [-p pbs_home] {job_file | job_id}...\n\n")
		fmt.Fprintf(os.Stderr, "Display contents of PBS job files for debugging.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	for _, arg := range flag.Args() {
		path := arg
		// If not a path, look in server_priv/jobs/
		if !strings.Contains(arg, "/") {
			// Try as job ID — look for ID.JB file
			id := arg
			path = filepath.Join(*pbsHome, "server_priv", "jobs", id+".JB")
			if _, err := os.Stat(path); err != nil {
				// Try with server suffix stripped
				jobNum := strings.Split(arg, ".")[0]
				path = filepath.Join(*pbsHome, "server_priv", "jobs", jobNum+".JB")
			}
			if _, err := os.Stat(path); err != nil {
				path = filepath.Join(*pbsHome, "server_priv", "jobs", arg)
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "printjob: cannot read %s: %v\n", path, err)
			continue
		}

		fmt.Printf("--- Job File: %s ---\n", path)

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Skip internal attributes unless -a flag
			if !*showAll && strings.HasPrefix(line, "_") {
				continue
			}
			fmt.Println(line)
		}
		fmt.Println()
	}
}
