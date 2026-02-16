# PAM Module — PBS Job Access Control

## Overview

The `pam` package provides PBS-aware access control for compute nodes.
When configured, it restricts SSH access so that only users with running
jobs on a node can log in. This prevents unauthorized users from consuming
resources on compute nodes.

## Architecture

Since Go cannot produce native PAM `.so` modules, the implementation uses
a two-part approach:

1. **Go library** (`internal/pam/`): Core logic for checking job ownership
2. **Helper binary** (`pbs_pam_check`): Called by `pam_exec.so`

## Configuration

### Enable on Compute Nodes

Add to `/etc/pam.d/sshd`:

```
account required pam_exec.so /usr/local/bin/pbs_pam_check
```

### How It Works

1. User attempts SSH to a compute node
2. PAM invokes `pbs_pam_check` via `pam_exec.so`
3. `pbs_pam_check` reads `$PBS_HOME/mom_priv/jobs/*.JB`
4. If any running job is owned by the user → **access allowed** (exit 0)
5. If no running jobs for the user → **access denied** (exit 1)
6. Root always has access

### Access Rules

| User | Running Job on Node? | Result |
|------|---------------------|--------|
| root | N/A | Allowed (always) |
| alice | Yes | Allowed |
| bob | No | Denied |
| alice | Job completed | Denied |

## API

```go
import "github.com/opentorque/opentorque/internal/pam"

// Check if user has access to this node
err := pam.CheckAccess("username")
if err != nil {
    // Access denied
}

// With custom PBS home
err := pam.CheckAccessWithHome("username", "/opt/pbs")
```

## Security

- **Fail-open**: If the job directory cannot be read, access is allowed
  (prevents lockout due to misconfiguration)
- Root bypass prevents admin lockout
- Only checks MOM job files on the local node (no network calls)
