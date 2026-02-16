// Command pbs_pam_check is a helper binary for PAM access control.
// It is invoked by pam_exec.so to check whether a user has running jobs
// on the current compute node.
//
// Exit codes:
//   - 0: Access allowed (user has running jobs)
//   - 1: Access denied (no running jobs)
//
// Configuration in /etc/pam.d/sshd:
//
//	account required pam_exec.so /usr/local/sbin/pbs_pam_check
package main

import (
	"fmt"
	"os"
	"os/user"

	"github.com/opentorque/opentorque/internal/pam"
)

func main() {
	// PAM_USER is set by pam_exec
	username := os.Getenv("PAM_USER")
	if username == "" {
		// Fallback to current user
		u, err := user.Current()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pbs_pam_check: cannot determine user: %v\n", err)
			os.Exit(0) // Fail-open
		}
		username = u.Username
	}

	if err := pam.CheckAccess(username); err != nil {
		fmt.Fprintf(os.Stderr, "pbs_pam_check: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
