// Command qselect selects PBS jobs matching specified criteria and prints their IDs.
// Output can be piped to other commands like qdel or qhold.
//
// Usage:
//
//	qselect [-a [op]date_time] [-h hold_list] [-l resource_list]
//	        [-N name] [-p [op]priority] [-q queue] [-s states]
//	        [-u user_list]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/xinlaoda/opentorque/internal/cli/client"
	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

func main() {
	var (
		holdList  = flag.String("h", "", "Select by hold type (u/o/s/n)")
		resources = flag.String("l", "", "Select by resource list (resource=value,...)")
		jobName   = flag.String("N", "", "Select by job name")
		queue     = flag.String("q", "", "Select by queue name")
		states    = flag.String("s", "", "Select by state (Q=queued, R=running, H=held, C=complete)")
		userList  = flag.String("u", "", "Select by owner (user1,user2,...)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qselect [options]\n\nSelect jobs matching criteria and print job IDs.\n\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n  qselect -s Q -u john | xargs qdel\n")
	}
	flag.Parse()

	var attrs []dis.SvrAttrl
	if *holdList != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Hold_Types", Value: *holdList})
	}
	if *resources != "" {
		for _, rv := range strings.Split(*resources, ",") {
			parts := strings.SplitN(rv, "=", 2)
			if len(parts) == 2 {
				attrs = append(attrs, dis.SvrAttrl{Name: "Resource_List", Resc: parts[0], HasResc: true, Value: parts[1]})
			}
		}
	}
	if *jobName != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Job_Name", Value: *jobName})
	}
	if *queue != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "queue", Value: *queue})
	}
	if *states != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "job_state", Value: *states})
	}
	if *userList != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Job_Owner", Value: *userList})
	}

	conn, err := client.Connect("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qselect: cannot connect to server: %v\n", err)
		os.Exit(2)
	}
	defer conn.Close()

	ids, err := conn.SelectJobs(attrs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qselect: %v\n", err)
		os.Exit(1)
	}

	for _, id := range ids {
		fmt.Println(id)
	}
}
