# OpenTorque Accounting System Reference

## Overview

OpenTorque maintains TORQUE-compatible accounting records that track the complete
lifecycle of every batch job. These records are essential for:

- **Usage reporting** — track resource consumption by user, group, or account
- **Chargeback** — calculate costs based on CPU time, memory, and walltime
- **Auditing** — maintain a tamper-evident history of all job activity
- **Debugging** — trace job issues by correlating timestamps across components
- **Capacity planning** — analyze historical workload patterns

The accounting system is fully compatible with the C TORQUE accounting format,
so existing reporting tools (e.g., `pbsacct`, custom scripts) work without
modification.

## File Location and Naming

Accounting files are stored in:

```
/var/spool/torque/server_priv/accounting/
```

Each file is named using the **YYYYMMDD** date format, matching the date the
records were written. A new file is automatically created at midnight:

```
/var/spool/torque/server_priv/accounting/20260216
/var/spool/torque/server_priv/accounting/20260217
/var/spool/torque/server_priv/accounting/20260218
```

### Directory Permissions

The accounting directory is owned by root and created with mode `0750`.
To allow non-root users to read accounting records (e.g., via `tracejob`),
administrators may choose to set:

```bash
chmod 755 /var/spool/torque/server_priv/accounting
```

## Record Format

Each accounting record is a single line with four semicolon-delimited fields:

```
MM/DD/YYYY HH:MM:SS;TYPE;JOB_ID;key=value key=value ...
```

| Field       | Description                                      | Example              |
|-------------|--------------------------------------------------|----------------------|
| Timestamp   | Local time when the event occurred               | `02/16/2026 14:30:45`|
| TYPE        | Single-character record type (see below)          | `E`                  |
| JOB_ID      | Full job identifier including server suffix       | `42.server1`         |
| Message     | Space-separated key=value pairs with job metadata | `user=alice ...`     |

## Record Types

### Q — Job Queued

Written when a job is submitted and committed to a queue (via `qsub`).

**Fields:**

| Key       | Description                    | Example           |
|-----------|--------------------------------|--------------------|
| user      | Job owner (username only)      | `alice`            |
| group     | Effective group                | `staff`            |
| jobname   | Job name (`-N` option)         | `my_simulation`    |
| queue     | Destination queue              | `batch`            |
| ctime     | Creation time (Unix epoch)     | `1771268064`       |
| qtime     | Queue time (Unix epoch)        | `1771268064`       |

Plus any `Resource_List.*` entries for requested resources.

**Example:**
```
02/16/2026 18:54:24;Q;7.DevBox;user=xxin group=staff jobname=acct_test queue=batch ctime=1771268064 qtime=1771268064 Resource_List.walltime=01:00:00 Resource_List.nodes=1
```

### S — Job Started

Written when a job begins execution on a compute node.

**Fields:**

| Key        | Description                         | Example          |
|------------|-------------------------------------|-------------------|
| user       | Job owner                           | `alice`           |
| group      | Effective group                     | `staff`           |
| jobname    | Job name                            | `my_simulation`   |
| queue      | Queue name                          | `batch`           |
| ctime      | Creation time (Unix epoch)          | `1771268064`      |
| qtime      | Queue time (Unix epoch)             | `1771268064`      |
| etime      | Eligible time (Unix epoch)          | `1771268064`      |
| start      | Start time (Unix epoch)             | `1771268114`      |
| exec_host  | Execution host (node/slot format)   | `node1/0`         |
| account    | Account string (if set)             | `project_abc`     |

Plus any `Resource_List.*` entries.

**Example:**
```
02/16/2026 18:54:24;S;7.DevBox;user=xxin group=staff jobname=acct_test queue=batch ctime=1771268064 qtime=1771268064 etime=1771268064 start=1771268114 exec_host=DevBox/0
```

### E — Job Ended

Written when a job completes execution normally. This is the most detailed
record type, containing all resource usage data.

**Fields:**

| Key                      | Description                              | Example        |
|--------------------------|------------------------------------------|-----------------|
| user                     | Job owner                                | `alice`         |
| group                    | Effective group                          | `staff`         |
| jobname                  | Job name                                 | `my_simulation` |
| queue                    | Queue name                               | `batch`         |
| ctime                    | Creation time (Unix epoch)               | `1771268064`    |
| qtime                    | Queue time (Unix epoch)                  | `1771268064`    |
| etime                    | Eligible time (Unix epoch)               | `1771268064`    |
| start                    | Start time (Unix epoch)                  | `1771268114`    |
| end                      | End time (Unix epoch)                    | `1771268200`    |
| Exit_status              | Process exit code                        | `0`             |
| exec_host                | Execution host                           | `node1/0`       |
| session                  | Session ID on compute node               | `12345`         |
| account                  | Account string (if set)                  | `project_abc`   |
| resources_used.walltime  | Actual wall clock time (HH:MM:SS)        | `00:01:26`      |
| resources_used.cput      | Actual CPU time (HH:MM:SS)               | `00:01:20`      |
| resources_used.mem       | Peak memory usage                        | `524288kb`      |
| resources_used.vmem      | Peak virtual memory usage                | `1048576kb`     |
| Resource_List.walltime   | Requested walltime                       | `01:00:00`      |
| Resource_List.nodes      | Requested nodes                          | `1`             |
| Resource_List.mem        | Requested memory                         | `4gb`           |

**Example:**
```
02/16/2026 18:54:42;E;7.DevBox;user=xxin group=staff jobname=acct_test queue=batch ctime=1771268064 qtime=1771268064 etime=1771268064 start=1771268114 end=1771268200 Exit_status=0 exec_host=DevBox/0 session=3839047 resources_used.walltime=00:01:26 resources_used.mem=2048kb resources_used.vmem=4096kb resources_used.cput=00:01:20
```

### D — Job Deleted

Written when a job is deleted via `qdel`.

**Fields:**

| Key       | Description                  | Example          |
|-----------|------------------------------|-------------------|
| user      | Job owner                    | `alice`           |
| group     | Effective group              | `staff`           |
| jobname   | Job name                     | `my_simulation`   |
| queue     | Queue name                   | `batch`           |
| ctime     | Creation time (Unix epoch)   | `1771268064`      |
| qtime     | Queue time (Unix epoch)      | `1771268064`      |
| exec_host | Execution host (if running)  | `node1/0`         |

**Example:**
```
02/16/2026 18:55:30;D;8.DevBox;user=xxin group=staff jobname=del_test queue=batch ctime=1771268113 qtime=1771268113 exec_host=DevBox/0
```

### A — Job Aborted

Written when a job terminates abnormally (signal, negative exit status, or
exit code > 128 indicating signal-killed).

**Fields:** Same as E record.

**Example:**
```
02/16/2026 19:00:15;A;9.DevBox;user=xxin group=staff jobname=fail_test queue=batch ctime=1771268415 qtime=1771268415 Exit_status=137 exec_host=DevBox/0
```

### R — Job Rerun

Written when a job is requeued for re-execution (via `qrerun`).

**Fields:** Same as Q record.

## Server Configuration

The following `pbs_server` attributes control accounting behavior. These can be
set via `qmgr`:

| Attribute              | Type   | Default | Description                                  |
|------------------------|--------|---------|----------------------------------------------|
| `accounting_keep_days` | int    | 0       | Days to keep accounting files (0 = forever)  |

### Setting Accounting Retention

```bash
# Keep accounting records for 90 days
qmgr -c "set server accounting_keep_days = 90"

# Keep indefinitely (default)
qmgr -c "set server accounting_keep_days = 0"
```

## File Management

### Automatic Rotation

Accounting files rotate automatically at midnight. Each day gets a new
`YYYYMMDD` file. No manual rotation is needed.

### Manual Cleanup

For systems without `accounting_keep_days` configured, old files can be
cleaned up with standard Unix tools:

```bash
# Remove accounting files older than 90 days
find /var/spool/torque/server_priv/accounting -type f -mtime +90 -delete

# Archive old files
cd /var/spool/torque/server_priv/accounting
tar czf /backup/accounting-$(date +%Y%m).tar.gz 202601* 202602*
```

### Viewing Records

Use `tracejob` to view accounting records along with server/MOM/scheduler logs:

```bash
# Show all records for job 42
tracejob 42

# Show last 7 days of records
tracejob -n 7 42

# Accounting records appear with "Acct" source label:
#   Acct     [Queued] user=alice group=staff jobname=sim ...
#   Acct     [Started] user=alice group=staff jobname=sim ...
#   Acct     [Ended] user=alice group=staff jobname=sim ... Exit_status=0
```

### Parsing with Scripts

Accounting records can be parsed with standard text tools:

```bash
# List all completed jobs today
grep ';E;' /var/spool/torque/server_priv/accounting/$(date +%Y%m%d)

# Sum walltime for a user
grep ';E;' /var/spool/torque/server_priv/accounting/20260216 | \
  grep 'user=alice' | \
  grep -oP 'resources_used.walltime=\K[^ ]+'

# Count jobs per user today
grep ';Q;' /var/spool/torque/server_priv/accounting/$(date +%Y%m%d) | \
  grep -oP 'user=\K\w+' | sort | uniq -c | sort -rn

# Find all failed jobs (exit status != 0)
grep ';E;' /var/spool/torque/server_priv/accounting/$(date +%Y%m%d) | \
  grep -v 'Exit_status=0'

# Extract job data as CSV
grep ';E;' /var/spool/torque/server_priv/accounting/20260216 | \
  awk -F';' '{print $1","$3","$4}' | \
  sed 's/ /,/g'
```

## Compatibility with C TORQUE

The OpenTorque accounting format is **fully compatible** with C TORQUE:

| Feature                          | C TORQUE | OpenTorque |
|----------------------------------|----------|------------|
| File naming (YYYYMMDD)           | ✅       | ✅         |
| Timestamp format (MM/DD/YYYY)    | ✅       | ✅         |
| Q record (queued)                | ✅       | ✅         |
| S record (started)               | ✅       | ✅         |
| E record (ended)                 | ✅       | ✅         |
| D record (deleted)               | ✅       | ✅         |
| A record (aborted)               | ✅       | ✅         |
| R record (rerun)                 | ✅       | ✅         |
| resources_used.* fields          | ✅       | ✅         |
| Resource_List.* fields           | ✅       | ✅         |
| session ID                       | ✅       | ✅         |
| exec_host                        | ✅       | ✅         |
| account string                   | ✅       | ✅         |
| accounting_keep_days             | ✅       | ✅         |

Existing tools that parse C TORQUE accounting files (e.g., `pbsacct`, Gold,
XDMOD) will work with OpenTorque accounting data without modification.

## Architecture

The accounting subsystem is implemented in the `internal/acct` package:

```
internal/acct/acct.go      — Logger, record formatting, JobInfo struct
pkg/pbslog/pbslog.go       — YYYYMMDD dated log file writer (shared with server/MOM/sched logs)
```

The server initializes an `acct.Logger` at startup pointing to the
`server_priv/accounting/` directory. Accounting records are written at four
lifecycle points:

1. **handleQueueJob / handleCommit** → Q record
2. **handleRunJob / scheduleJob** → S record
3. **handleJobObit** → E or A record (A if exit status > 128 or negative)
4. **handleDeleteJob** → D record

The logger uses `pbslog.DatedLog` which checks the current date on every write
and automatically rotates to a new file at midnight.
