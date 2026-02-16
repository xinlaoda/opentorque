// Command tracejob traces a PBS job across server, MOM, and scheduler logs.
// It searches log files for entries related to the specified job ID.
//
// Usage:
//
//	tracejob [-n days] [-s] [-m] [-l] job_id
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type logEntry struct {
	Source string // "Server", "MOM", "Sched"
	Time   string
	Line   string
}

func main() {
	var (
		days      = flag.Int("n", 1, "Number of days of logs to search")
		showSvr   = flag.Bool("s", false, "Search server logs only")
		showMom   = flag.Bool("m", false, "Search MOM logs only")
		showSched = flag.Bool("l", false, "Search scheduler logs only")
		pbsHome   = flag.String("p", "/var/spool/torque", "PBS home directory")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tracejob [-n days] [-s] [-m] [-l] [-p pbs_home] job_id\n\n")
		fmt.Fprintf(os.Stderr, "Trace a job across PBS server, MOM, and scheduler logs.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	jobID := flag.Arg(0)
	// Strip server suffix for matching (e.g., "123.server" -> also match "123")
	shortID := strings.Split(jobID, ".")[0]

	// Default: search all log sources
	searchAll := !*showSvr && !*showMom && !*showSched

	var entries []logEntry

	// Search server logs
	if searchAll || *showSvr {
		svrLogs := findLogFiles(filepath.Join(*pbsHome, "server_logs"), *days)
		for _, f := range svrLogs {
			entries = append(entries, searchLog(f, shortID, "Server")...)
		}
	}

	// Search MOM logs
	if searchAll || *showMom {
		momLogs := findLogFiles(filepath.Join(*pbsHome, "mom_logs"), *days)
		for _, f := range momLogs {
			entries = append(entries, searchLog(f, shortID, "MOM")...)
		}
	}

	// Search scheduler logs
	if searchAll || *showSched {
		schedLogs := findLogFiles(filepath.Join(*pbsHome, "sched_logs"), *days)
		for _, f := range schedLogs {
			entries = append(entries, searchLog(f, shortID, "Sched")...)
		}
	}

	// Also search common log locations
	if searchAll {
		for _, path := range []string{"/tmp/server.log", "/tmp/mom.log", "/tmp/sched.log"} {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				source := "Log"
				if strings.Contains(path, "server") {
					source = "Server"
				} else if strings.Contains(path, "mom") {
					source = "MOM"
				} else if strings.Contains(path, "sched") {
					source = "Sched"
				}
				entries = append(entries, searchLog(path, shortID, source)...)
			}
		}
	}

	if len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "tracejob: no log entries found for %s\n", jobID)
		os.Exit(1)
	}

	// Sort by time string (lexicographic works for ISO-like timestamps)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Time < entries[j].Time
	})

	// Print results
	fmt.Printf("Job: %s\n\n", jobID)
	for _, e := range entries {
		fmt.Printf("%-8s %s\n", e.Source, e.Line)
	}
}

// findLogFiles returns log files from the given directory for the last N days.
func findLogFiles(dir string, days int) []string {
	var files []string
	now := time.Now()
	for d := 0; d < days; d++ {
		date := now.AddDate(0, 0, -d)
		// PBS log files are named YYYYMMDD
		name := date.Format("20060102")
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	return files
}

// searchLog searches a log file for lines containing the job ID.
func searchLog(path, jobID, source string) []logEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []logEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, jobID) {
			// Extract timestamp (first field, typically YYYY/MM/DD HH:MM:SS)
			ts := ""
			if len(line) > 19 {
				ts = line[:19]
			}
			entries = append(entries, logEntry{
				Source: source,
				Time:   ts,
				Line:   line,
			})
		}
	}
	return entries
}
