// Command qstat displays the status of PBS jobs, queues, and server.
//
// Usage:
//
//	qstat [options] [job_id...]
//	qstat -Q [queue_name...]
//	qstat -B
//	qstat -f [job_id]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/opentorque/opentorque/internal/cli/client"
)

func main() {
	var (
		showAll    = flag.Bool("a", false, "Display all jobs")
		showQueues = flag.Bool("Q", false, "Display queue status")
		showServer = flag.Bool("B", false, "Display server status")
		showFull   = flag.Bool("f", false, "Display full (detailed) status")
		showIdle   = flag.Bool("i", false, "Display idle/queued jobs only")
		showRun    = flag.Bool("r", false, "Display running jobs only")
		showNode   = flag.Bool("n", false, "Display node assigned to jobs")
		userFilter = flag.String("u", "", "Display jobs owned by user")
		server     = flag.String("s", "", "Specify server name")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qstat [options] [job_id...]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qstat: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	switch {
	case *showQueues:
		queueName := ""
		if flag.NArg() > 0 {
			queueName = flag.Arg(0)
		}
		displayQueueStatus(conn, queueName)
	case *showServer:
		displayServerStatus(conn, *showFull)
	default:
		jobID := ""
		if flag.NArg() > 0 {
			jobID = flag.Arg(0)
		}
		displayJobStatus(conn, jobID, *showAll, *showFull, *showIdle, *showRun, *showNode, *userFilter)
	}
}

func displayJobStatus(conn *client.Conn, jobID string, showAll, showFull, showIdle, showRun, showNode bool, userFilter string) {
	objects, err := conn.StatusJob(jobID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qstat: %v\n", err)
		os.Exit(1)
	}

	if len(objects) == 0 {
		return
	}

	if showFull {
		// Full format: one attribute per line
		for _, obj := range objects {
			if !matchesFilter(obj, showIdle, showRun, userFilter) {
				continue
			}
			fmt.Printf("Job Id: %s\n", obj.Name)
			for _, attr := range obj.Attrs {
				if attr.HasResc && attr.Resc != "" {
					fmt.Printf("    %s.%s = %s\n", attr.Name, attr.Resc, attr.Value)
				} else {
					fmt.Printf("    %s = %s\n", attr.Name, attr.Value)
				}
			}
			fmt.Println()
		}
		return
	}

	// Tabular format
	fmt.Printf("%-25s %-16s %-15s %-8s %s %s\n",
		"Job ID", "Name", "User", "Time Use", "S", "Queue")
	fmt.Printf("%-25s %-16s %-15s %-8s %s %s\n",
		"-------------------------", "----------------", "---------------",
		"--------", "-", "-----")

	for _, obj := range objects {
		if !matchesFilter(obj, showIdle, showRun, userFilter) {
			continue
		}
		attrs := attrMap(obj.Attrs)
		name := truncate(attrs["Job_Name"], 16)
		owner := attrs["Job_Owner"]
		if idx := strings.Index(owner, "@"); idx > 0 {
			owner = owner[:idx]
		}
		owner = truncate(owner, 15)
		timeUse := attrs["resources_used.walltime"]
		if timeUse == "" {
			timeUse = "0"
		}
		state := attrs["job_state"]
		queue := attrs["queue"]

		line := fmt.Sprintf("%-25s %-16s %-15s %8s %s %-5s",
			truncate(obj.Name, 25), name, owner, timeUse, state, queue)

		if showNode {
			execHost := attrs["exec_host"]
			if execHost != "" {
				line += " " + execHost
			}
		}
		fmt.Println(line)
	}
}

func displayQueueStatus(conn *client.Conn, queueName string) {
	objects, err := conn.StatusQueue(queueName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qstat: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%-18s %3s %6s %5s %5s %5s %5s %5s %5s %5s %5s %s %5s\n",
		"Queue", "Max", "Tot", "Ena", "Str",
		"Que", "Run", "Hld", "Wat", "Trn", "Ext", "T", "Cpt")
	fmt.Printf("%-18s %3s %6s %5s %5s %5s %5s %5s %5s %5s %5s %s %5s\n",
		"----------------", "---", "----", "--", "--",
		"---", "---", "---", "---", "---", "---", "-", "---")

	for _, obj := range objects {
		attrs := attrMap(obj.Attrs)
		qtype := "E"
		if attrs["queue_type"] == "Route" {
			qtype = "R"
		}
		fmt.Printf("%-18s %3s %6s %5s %5s %5s %5s %5s %5s %5s %5s %s %5s\n",
			obj.Name,
			zeroDefault(attrs["max_running"]),
			zeroDefault(attrs["total_jobs"]),
			yesNo(attrs["enabled"]),
			yesNo(attrs["started"]),
			zeroDefault(attrs["state_count_queued"]),
			zeroDefault(attrs["state_count_running"]),
			zeroDefault(attrs["state_count_held"]),
			zeroDefault(attrs["state_count_waiting"]),
			zeroDefault(attrs["state_count_transit"]),
			zeroDefault(attrs["state_count_exiting"]),
			qtype,
			zeroDefault(attrs["state_count_complete"]),
		)
	}
}

func displayServerStatus(conn *client.Conn, showFull bool) {
	objects, err := conn.StatusServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qstat: %v\n", err)
		os.Exit(1)
	}

	if showFull {
		for _, obj := range objects {
			fmt.Printf("Server: %s\n", obj.Name)
			for _, attr := range obj.Attrs {
				if attr.HasResc && attr.Resc != "" {
					fmt.Printf("    %s.%s = %s\n", attr.Name, attr.Resc, attr.Value)
				} else {
					fmt.Printf("    %s = %s\n", attr.Name, attr.Value)
				}
			}
		}
		return
	}

	fmt.Printf("%-18s %5s %5s %5s %5s %5s %5s %5s %5s %5s %s\n",
		"Server", "Max", "Tot", "Que", "Run", "Hld", "Wat", "Trn", "Ext", "Com", "Status")
	fmt.Printf("%-18s %5s %5s %5s %5s %5s %5s %5s %5s %5s %s\n",
		"----------------", "---", "---", "---", "---", "---", "---", "---", "---", "---",
		"----------")

	for _, obj := range objects {
		attrs := attrMap(obj.Attrs)
		fmt.Printf("%-18s %5s %5s %5s %5s %5s %5s %5s %5s %5s %s\n",
			obj.Name,
			zeroDefault(attrs["max_running"]),
			zeroDefault(attrs["total_jobs"]),
			zeroDefault(attrs["state_count_queued"]),
			zeroDefault(attrs["state_count_running"]),
			zeroDefault(attrs["state_count_held"]),
			zeroDefault(attrs["state_count_waiting"]),
			zeroDefault(attrs["state_count_transit"]),
			zeroDefault(attrs["state_count_exiting"]),
			zeroDefault(attrs["state_count_complete"]),
			attrs["server_state"],
		)
	}
}

// matchesFilter checks if a job object matches the given display filters.
func matchesFilter(obj client.StatusObject, showIdle, showRun bool, userFilter string) bool {
	attrs := attrMap(obj.Attrs)
	state := attrs["job_state"]

	if showIdle && state != "Q" && state != "H" && state != "W" {
		return false
	}
	if showRun && state != "R" {
		return false
	}
	if userFilter != "" {
		owner := attrs["Job_Owner"]
		if idx := strings.Index(owner, "@"); idx > 0 {
			owner = owner[:idx]
		}
		if owner != userFilter {
			return false
		}
	}
	return true
}

func attrMap(attrs []client.SvrAttrl) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		key := a.Name
		if a.HasResc && a.Resc != "" {
			key = a.Name + "." + a.Resc
		}
		m[key] = a.Value
	}
	return m
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

func zeroDefault(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func yesNo(s string) string {
	if s == "True" || s == "true" || s == "1" || s == "yes" {
		return "yes"
	}
	if s == "" {
		return "no"
	}
	return s
}
