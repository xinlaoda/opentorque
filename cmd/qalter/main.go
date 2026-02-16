// Command qalter modifies attributes of a queued or running PBS job.
//
// Usage:
//
//	qalter [-a date_time] [-e path] [-h hold_list] [-l resource_list]
//	       [-m mail_events] [-M mail_list] [-N name] [-o path] [-p priority]
//	       [-r rerunable] [-S shell] [-W additional_attrs] job_id...
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/opentorque/opentorque/internal/cli/client"
	"github.com/opentorque/opentorque/internal/cli/dis"
)

func main() {
	var (
		dateTime  = flag.String("a", "", "Date/time for job eligibility (YYMMDDHHMM)")
		errPath   = flag.String("e", "", "Path for stderr output")
		holdList  = flag.String("h", "", "Hold types (u=user, o=other, s=system, n=none)")
		resources = flag.String("l", "", "Resource list (e.g., walltime=1:00:00,mem=1gb)")
		mailEvts  = flag.String("m", "", "Mail events (a=abort, b=begin, e=end, n=none)")
		mailList  = flag.String("M", "", "Mail recipient list")
		jobName   = flag.String("N", "", "Job name")
		outPath   = flag.String("o", "", "Path for stdout output")
		priority  = flag.String("p", "", "Job priority (-1024 to +1023)")
		rerun     = flag.String("r", "", "Rerunable (y/n)")
		shell     = flag.String("S", "", "Shell path")
		extra     = flag.String("W", "", "Additional attributes (key=value,...)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qalter [options] job_id...\n\nModify attributes of a PBS job.\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	// Build attribute list from flags
	var attrs []dis.SvrAttrl
	if *dateTime != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Execution_Time", Value: *dateTime})
	}
	if *errPath != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Error_Path", Value: *errPath})
	}
	if *holdList != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Hold_Types", Value: *holdList})
	}
	if *resources != "" {
		// Parse comma-separated resource=value pairs
		for _, rv := range strings.Split(*resources, ",") {
			parts := strings.SplitN(rv, "=", 2)
			if len(parts) == 2 {
				attrs = append(attrs, dis.SvrAttrl{Name: "Resource_List", Resc: parts[0], HasResc: true, Value: parts[1]})
			}
		}
	}
	if *mailEvts != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Mail_Points", Value: *mailEvts})
	}
	if *mailList != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Mail_Users", Value: *mailList})
	}
	if *jobName != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Job_Name", Value: *jobName})
	}
	if *outPath != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Output_Path", Value: *outPath})
	}
	if *priority != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Priority", Value: *priority})
	}
	if *rerun != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Rerunable", Value: *rerun})
	}
	if *shell != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Shell_Path_List", Value: *shell})
	}
	if *extra != "" {
		for _, kv := range strings.Split(*extra, ",") {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				attrs = append(attrs, dis.SvrAttrl{Name: parts[0], Value: parts[1]})
			}
		}
	}

	if len(attrs) == 0 {
		fmt.Fprintf(os.Stderr, "qalter: no attributes specified\n")
		os.Exit(1)
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qalter: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	exitCode := 0
	for _, jobID := range flag.Args() {
		if err := conn.ModifyJob(jobID, attrs); err != nil {
			fmt.Fprintf(os.Stderr, "qalter: %s: %v\n", jobID, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
