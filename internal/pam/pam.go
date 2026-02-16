// Package pam provides a PAM (Pluggable Authentication Module) helper
// for PBS job access control on compute nodes. When enabled, it restricts
// SSH access to nodes so that only users with running jobs on that node
// can log in.
//
// This is a Go implementation of the pbs_pam module. It checks whether
// the authenticating user has any running jobs on the local node by
// querying the MOM daemon's job list.
//
// Configuration in /etc/pam.d/sshd:
//
//	account required pam_pbs.so
//
// Since Go cannot produce a native PAM .so module directly, this package
// provides the logic as a library and a helper binary that can be called
// from a PAM exec module:
//
//	account required pam_exec.so /usr/local/sbin/pbs_pam_check
package pam

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultPBSHome = "/var/spool/torque"
)

// CheckAccess verifies whether the given user has a running job on this node.
// It checks the MOM's job directory for active jobs owned by the user.
// Returns nil if access is allowed, or an error if denied.
func CheckAccess(username string) error {
	return CheckAccessWithHome(username, defaultPBSHome)
}

// CheckAccessWithHome checks access using the specified PBS home directory.
func CheckAccessWithHome(username, pbsHome string) error {
	// Root always has access
	if username == "root" {
		return nil
	}

	// Check MOM's active jobs directory
	jobDir := filepath.Join(pbsHome, "mom_priv", "jobs")
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		// If we can't read the job directory, allow access (fail-open)
		return nil
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".JB") {
			continue
		}

		// Read job file to check owner
		path := filepath.Join(jobDir, entry.Name())
		owner, state := readJobOwnerAndState(path)

		// Only running jobs grant access (state 4 = Running in our Go implementation)
		if owner == username && (state == "4" || state == "running") {
			return nil // User has a running job on this node
		}
	}

	return fmt.Errorf("access denied: user %s has no running jobs on this node", username)
}

// readJobOwnerAndState reads the owner and state from a job file.
func readJobOwnerAndState(path string) (owner, state string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "owner=") {
			owner = strings.TrimPrefix(line, "owner=")
			// Strip @hostname if present
			if idx := strings.Index(owner, "@"); idx >= 0 {
				owner = owner[:idx]
			}
		}
		if strings.HasPrefix(line, "state=") {
			state = strings.TrimPrefix(line, "state=")
		}
	}
	return owner, state
}
