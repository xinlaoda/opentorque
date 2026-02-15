# qstat - Display PBS Job, Queue, and Server Status

## Synopsis

```
qstat [options] [job_id...]
qstat -Q [queue_name...]
qstat -B
```

## Description

`qstat` displays the status of PBS batch jobs, queues, and the batch server. By default, it shows a summary of all jobs. Different modes are selected with flags:

- **Job status** (default): Shows jobs in tabular format with ID, name, user, time, state, and queue.
- **Queue status** (`-Q`): Shows queue configuration and job counts.
- **Server status** (`-B`): Shows server configuration and aggregate job counts.
- **Detailed status** (`-f`): Shows all attributes of the specified object.

The Go implementation uses HMAC-SHA256 token authentication.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-a` | | Display all jobs (equivalent to no arguments). |
| `-i` | | Display only idle (queued, held, waiting) jobs. Filters to states Q, H, W. |
| `-r` | | Display only running jobs. Filters to state R. |
| `-u` | user | Display only jobs owned by the specified user. |
| `-f` | | Full (detailed) output. Shows all attributes, one per line. |
| `-n` | | Show the execution host (node) assigned to running jobs. |
| `-Q` | | Display queue status instead of job status. |
| `-B` | | Display server status instead of job status. |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Job States

| State | Code | Description |
|-------|------|-------------|
| Transit | T | Job is being routed to another server |
| Queued | Q | Job is queued and eligible for execution |
| Held | H | Job is held (user, operator, or system hold) |
| Waiting | W | Job is waiting for its execution time |
| Running | R | Job is running on a compute node |
| Exiting | E | Job is exiting after execution |
| Complete | C | Job has completed execution |

## Output Format

### Default (Tabular)

```
Job ID                    Name             User            Time Use S Queue
------------------------- ---------------- --------------- -------- - -----
42.DevBox                 my_job           user1           00:05:32 R batch
43.DevBox                 analysis         user2                  0 Q batch
```

### Queue Status (`-Q`)

```
Queue              Max    Tot   Ena   Str   Que   Run   Hld   Wat   Trn   Ext T   Cpt
batch                0      5   yes   yes     2     1     1     0     0     0 E     1
```

### Server Status (`-B`)

```
Server             Max   Tot   Que   Run   Hld   Wat   Trn   Ext   Com Status
DevBox               0     5     2     1     1     0     0     0     1 Active
```

### Detailed (`-f`)

```
Job Id: 42.DevBox
    Job_Name = my_job
    Job_Owner = user1@hostname
    job_state = R
    queue = batch
    ...
```

## Exit Status

- `0` — Success
- `1` — Error (connection failure, authentication error, or server error)

## Examples

```bash
# Show all jobs
qstat

# Show only running jobs
qstat -r

# Show jobs for a specific user
qstat -u john

# Show detailed info for a specific job
qstat -f 42.DevBox

# Show queue status
qstat -Q

# Show server status
qstat -B

# Show detailed server status
qstat -B -f
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qsub(1), qdel(1), pbsnodes(1), qmgr(1)
