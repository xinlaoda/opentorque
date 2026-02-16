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
	// Extract numeric sequence for boundary matching (e.g., "123.server" -> "123")
	shortID := strings.Split(jobID, ".")[0]

	// Default: search all log sources
	searchAll := !*showSvr && !*showMom && !*showSched

	var entries []logEntry

	// Search server logs
	if searchAll || *showSvr {
		svrLogs := findLogFiles(filepath.Join(*pbsHome, "server_logs"), *days)
		for _, f := range svrLogs {
			entries = append(entries, searchLog(f, jobID, shortID, "Server")...)
		}
	}

	// Search MOM logs
	if searchAll || *showMom {
		momLogs := findLogFiles(filepath.Join(*pbsHome, "mom_logs"), *days)
		for _, f := range momLogs {
			entries = append(entries, searchLog(f, jobID, shortID, "MOM")...)
		}
	}

	// Search scheduler logs
	if searchAll || *showSched {
		schedLogs := findLogFiles(filepath.Join(*pbsHome, "sched_logs"), *days)
		for _, f := range schedLogs {
			entries = append(entries, searchLog(f, jobID, shortID, "Sched")...)
		}
	}

	// Also search common log locations
	if searchAll {
		// Search /tmp for any server, mom, sched log files
		for _, pattern := range []string{"/tmp/*server*.log", "/tmp/*mom*.log", "/tmp/*sched*.log"} {
			matches, _ := filepath.Glob(pattern)
			for _, path := range matches {
				source := "Log"
				if strings.Contains(path, "server") {
					source = "Server"
				} else if strings.Contains(path, "mom") {
					source = "MOM"
				} else if strings.Contains(path, "sched") {
					source = "Sched"
				}
				entries = append(entries, searchLog(path, jobID, shortID, source)...)
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

// searchLog searches a log file for lines referring to the given job.
func searchLog(path, fullID, shortID, source string) []logEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []logEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !matchJobLine(line, fullID, shortID) {
			continue
		}
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
	return entries
}

// matchJobLine checks if a log line refers to the given job.
// Matches the full job ID or the short numeric ID with boundary delimiters.
func matchJobLine(line, fullID, shortID string) bool {
	// Try full ID with boundary check
	if matchWithBoundary(line, fullID) {
		return true
	}
	// Try short numeric ID with boundary check
	if shortID != fullID {
		return matchWithBoundary(line, shortID)
	}
	return false
}

// matchWithBoundary checks if pattern appears in line with non-ID chars on both sides.
func matchWithBoundary(line, pattern string) bool {
	idx := 0
	for {
		pos := strings.Index(line[idx:], pattern)
		if pos < 0 {
			return false
		}
		pos += idx
		start := pos - 1
		end := pos + len(pattern)
		validBefore := start < 0 || !isIDChar(line[start])
		validAfter := end >= len(line) || !isIDChar(line[end])
		if validBefore && validAfter {
			return true
		}
		idx = pos + 1
		if idx >= len(line) {
			return false
		}
	}
}

func isIDChar(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}
