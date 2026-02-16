// Command pbs_track registers an external process with PBS for resource tracking.
// The tracked process's resource usage (CPU, memory) is accounted to the specified job.
//
// Usage:
//
//	pbs_track -j job_id [-p pid]
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		jobID = flag.String("j", "", "Job ID to associate with the process")
		pid   = flag.Int("p", 0, "PID of the process to track (default: current process)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pbs_track -j job_id [-p pid]\n\n")
		fmt.Fprintf(os.Stderr, "Register an external process for PBS resource tracking.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *jobID == "" {
		flag.Usage()
		os.Exit(1)
	}

	trackPID := *pid
	if trackPID == 0 {
		trackPID = os.Getpid()
	}

	// In a full implementation, this would communicate with the local MOM
	// daemon to register the PID in the job's process tracking cgroup or
	// session. For now, write the tracking info to a local file.
	trackDir := "/var/spool/torque/mom_priv/tracking"
	if err := os.MkdirAll(trackDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "pbs_track: cannot create tracking directory: %v\n", err)
		os.Exit(2)
	}

	trackFile := fmt.Sprintf("%s/%d", trackDir, trackPID)
	if err := os.WriteFile(trackFile, []byte(fmt.Sprintf("job=%s\npid=%d\n", *jobID, trackPID)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "pbs_track: cannot write tracking file: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Tracking PID %d for job %s\n", trackPID, *jobID)
}
