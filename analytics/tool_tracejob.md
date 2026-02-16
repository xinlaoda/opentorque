# tracejob — Trace a PBS Job Across Logs

## Synopsis

```
tracejob [-n days] [-s] [-m] [-l] [-p pbs_home] job_id
```

## Description

`tracejob` searches server, MOM, and scheduler log files for entries related to a specific job ID. It collects and displays log entries in chronological order, providing a complete timeline of the job's lifecycle across all PBS components.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-n` | days | 1 | Number of days of logs to search |
| `-s` | — | — | Search server logs only |
| `-m` | — | — | Search MOM logs only |
| `-l` | — | — | Search scheduler logs only |
| `-p` | path | /var/spool/torque | PBS home directory |

## Log Sources

| Source | Directory | Content |
|--------|-----------|---------|
| Server | `$PBS_HOME/server_logs/` | Job submission, scheduling, state changes |
| MOM | `$PBS_HOME/mom_logs/` | Job execution, resource usage, completion |
| Scheduler | `$PBS_HOME/sched_logs/` | Scheduling decisions, dispatch events |

Also searches `/tmp/{server,mom,sched}.log` for Go daemon logs.

## Examples

```bash
# Trace a job (last 24 hours)
tracejob 123.server

# Search last 7 days
tracejob -n 7 123.server

# Server logs only
tracejob -s 123.server
```

## Exit Status

- `0` — Log entries found
- `1` — No log entries found for the job
