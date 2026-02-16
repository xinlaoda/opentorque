# qalter — Modify PBS Job Attributes

## Synopsis

```
qalter [options] job_id...
```

## Description

`qalter` modifies the attributes of one or more PBS jobs. Jobs can be altered while in the Queued (Q) or Held (H) state. Some attributes can also be modified while a job is Running (R).

## Options

| Flag | Argument | Description |
|------|----------|-------------|
| `-a` | datetime | Execution time (YYMMDDHHMM format) |
| `-e` | path | Path for stderr output file |
| `-h` | hold_list | Hold types: `u` (user), `o` (other), `s` (system), `n` (none) |
| `-l` | resource_list | Resource requirements (e.g., `walltime=1:00:00,mem=1gb`) |
| `-m` | mail_events | Mail notification: `a` (abort), `b` (begin), `e` (end), `n` (none) |
| `-M` | mail_list | Comma-separated email addresses for notifications |
| `-N` | name | Job name (up to 15 characters) |
| `-o` | path | Path for stdout output file |
| `-p` | priority | Job priority (-1024 to +1023) |
| `-r` | y/n | Whether the job is rerunable |
| `-S` | shell | Shell path for job script |
| `-A` | account | Set account name (sets the `Account_Name` attribute). |
| `-c` | checkpoint | Set checkpoint interval: `none`, `enabled`, `interval`, `shutdown`, `periodic`. |
| `-j` | join | Join stdout/stderr streams: `oe` (merge into stdout), `eo` (merge into stderr), `n` (do not join). |
| `-k` | keep | Keep output files on execution host: `o` (stdout), `e` (stderr), `oe` (both), `n` (return to server). |
| `-q` | queue | Move job to a different queue. |
| `-W` | attrs | Extended attributes as comma-separated key=value pairs. |

## Protocol

Sends `BatchReqModifyJob` (type 11) to pbs_server with the modified attribute list.

## Examples

```bash
# Change job name
qalter -N new_name 123.server

# Increase walltime
qalter -l walltime=4:00:00 123.server

# Change priority
qalter -p 100 123.server

# Modify multiple jobs
qalter -N batch_job 123.server 124.server 125.server
```

## Exit Status

- `0` — All jobs modified successfully
- `1` — One or more jobs could not be modified
- `2` — Cannot connect to server
