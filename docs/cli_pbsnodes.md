# pbsnodes - Display and Manage PBS Compute Nodes

## Synopsis

```
pbsnodes [options] [node_name...]
```

## Description

`pbsnodes` displays the status of compute nodes managed by the PBS server and allows administrators to modify node states. Without arguments, it displays status for all nodes.

Node management operations (offline, clear, reset) require administrator privileges.

The Go implementation uses HMAC-SHA256 token authentication.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-a` | | List all nodes with their full attribute set. |
| `-l` | | List only nodes that are in `down` or `offline` state. |
| `-o` | | Mark the specified node(s) as `offline`. Prevents new jobs from being scheduled to the node, but running jobs continue. |
| `-c` | | Clear the `offline` or `down` state from the specified node(s). Sets the node state back to `free`. |
| `-r` | | Reset the node. Equivalent to `-c` (clear offline state). |
| `-N` | note | Set a note (comment) on the specified node(s). Useful for recording maintenance information. |
| `-q` | | Quiet mode. Suppress non-essential output. |
| `-x` | | XML output format. Wraps nodes in `<Data><Node>...</Node></Data>`. |
| `-A` | note | Append text to the existing note on the specified node(s). |
| `-n` | | Show only node notes. |
| `-d` | | Diagnostic mode (verbose output). Compatibility flag. |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Node States

| State | Description |
|-------|-------------|
| `free` | Node is available for scheduling |
| `job-exclusive` | Node is fully allocated to running jobs |
| `job-sharing` | Node has some resources allocated |
| `down` | Node is unreachable or has failed health checks |
| `offline` | Node has been manually taken offline by an administrator |
| `reserve` | Node is reserved for specific use |
| `state-unknown` | Node state cannot be determined |

## Output Format

```
DevBox
     state = free
     np = 8
     ntype = cluster
     opsys = Linux
     loadave = 0.15
     ncpus = 8
     totmem = 32863972kb
     availmem = 30440696kb
     physmem = 32863972kb
     arch = amd64
     version = 7.0.0-go
```

## Node Attributes

| Attribute | Description |
|-----------|-------------|
| `state` | Current node state |
| `np` | Number of processors (virtual CPUs) |
| `ntype` | Node type (cluster, time-shared) |
| `opsys` | Operating system name |
| `arch` | CPU architecture |
| `loadave` | Current system load average |
| `ncpus` | Number of physical CPUs |
| `totmem` | Total memory (physical + swap) |
| `availmem` | Available memory |
| `physmem` | Physical memory |
| `totswap` | Total swap space |
| `availswap` | Available swap space |
| `idletime` | System idle time in seconds |
| `netload` | Network load counter |
| `version` | TORQUE MOM version string |
| `rectime` | Last status update timestamp (Unix epoch) |
| `jobs` | List of jobs currently running on the node |

## Exit Status

- `0` — Success
- `1` — Error (connection failure, unknown node, or permission denied)

## Examples

```bash
# List all nodes
pbsnodes -a

# List only down/offline nodes
pbsnodes -l

# Show status of a specific node
pbsnodes compute01

# Mark a node offline for maintenance
pbsnodes -o compute01

# Bring a node back online
pbsnodes -c compute01

# Add a maintenance note
pbsnodes -N "Replacing disk in slot 3" compute01
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qstat(1), qmgr(1), qsub(1)
