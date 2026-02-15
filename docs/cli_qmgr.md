# qmgr - PBS Batch System Manager

## Synopsis

```
qmgr [-c command] [-s server]
qmgr                          (interactive mode)
```

## Description

`qmgr` is the administrative interface for managing the PBS batch system. It can create, delete, and configure queues, modify server attributes, and manage nodes.

`qmgr` operates in two modes:
- **Command mode** (`-c`): Execute a single command and exit.
- **Interactive mode**: Present a `Qmgr:` prompt and accept commands until `quit` or EOF.

The Go implementation uses HMAC-SHA256 token authentication.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-c` | command | Execute the specified command and exit. The command string should be quoted. |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Commands

### Syntax

```
<verb> <object_type> [object_name] [attribute = value [, ...]]
```

### Verbs

| Verb | Abbreviation | Description |
|------|-------------|-------------|
| `create` | `c` | Create a new object (queue, node) |
| `delete` | `d` | Delete an existing object |
| `set` | `s` | Set attributes on an object |
| `unset` | `u` | Remove (unset) attributes from an object |
| `list` | `l` | Display current attributes of an object |
| `print` | `p` | Display attributes in a format suitable for recreating the configuration |

### Object Types

| Type | Abbreviation | Description |
|------|-------------|-------------|
| `server` | `s` | The PBS server configuration |
| `queue` | `q`, `que` | A job queue |
| `node` | `n` | A compute node |

### Queue Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `queue_type` | string | Queue type: `execution` or `route` |
| `enabled` | boolean | Whether the queue accepts job submissions (`True`/`False`) |
| `started` | boolean | Whether the queue schedules jobs for execution (`True`/`False`) |
| `max_running` | integer | Maximum number of concurrently running jobs |
| `max_queuable` | integer | Maximum number of jobs allowed in the queue |
| `resources_max.walltime` | time | Maximum walltime for jobs |
| `resources_max.mem` | size | Maximum memory for jobs |
| `resources_max.ncpus` | integer | Maximum CPUs per job |
| `resources_default.walltime` | time | Default walltime for jobs |
| `resources_default.nodes` | string | Default node specification |

### Server Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `scheduling` | boolean | Enable/disable job scheduling (`True`/`False`) |
| `default_queue` | string | Default destination queue for job submissions |
| `scheduler_iteration` | integer | Scheduling cycle interval in seconds |
| `node_check_rate` | integer | Node health check interval in seconds |
| `tcp_timeout` | integer | TCP connection timeout in seconds |
| `keep_completed` | integer | Time to keep completed jobs (seconds) |

## Exit Status

- `0` — All commands executed successfully
- `1` — One or more commands failed

## Examples

```bash
# Create a new execution queue
qmgr -c "create queue batch"
qmgr -c "set queue batch queue_type = execution"
qmgr -c "set queue batch enabled = True"
qmgr -c "set queue batch started = True"

# Set resource limits on a queue
qmgr -c "set queue batch resources_max.walltime = 24:00:00"
qmgr -c "set queue batch resources_max.mem = 64gb"

# Set the default queue
qmgr -c "set server default_queue = batch"

# Enable scheduling
qmgr -c "set server scheduling = True"

# List server configuration
qmgr -c "list server"

# List queue configuration
qmgr -c "list queue batch"

# Delete a queue
qmgr -c "delete queue test"

# Interactive mode
qmgr
Qmgr: list server
Qmgr: set queue batch enabled = True
Qmgr: quit
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qsub(1), qstat(1), qdel(1), pbsnodes(1)
