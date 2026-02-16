# qselect — Select PBS Jobs by Criteria

## Synopsis

```
qselect [-h hold_list] [-l resource_list] [-N name] [-p priority]
        [-q queue] [-s states] [-u user_list]
```

## Description

`qselect` outputs the job IDs of all jobs matching the specified selection criteria. Output is one job ID per line, suitable for piping to other commands.

## Options

| Flag | Argument | Description |
|------|----------|-------------|
| `-h` | hold_list | Select by hold type (u/o/s/n) |
| `-l` | resource_list | Select by resource (e.g., `walltime=1:00:00`) |
| `-N` | name | Select by job name |
| `-q` | queue | Select by queue name |
| `-s` | states | Select by state: Q (queued), R (running), H (held), C (complete) |
| `-u` | user_list | Select by job owner (comma-separated) |

## Protocol

Sends `BatchReqSelectJobs` (type 16) to pbs_server with selection attributes. Server returns a text list of matching job IDs.

## Examples

```bash
# List all queued jobs
qselect -s Q

# List all jobs from a specific user
qselect -u john

# Delete all held jobs
qselect -s H | xargs qdel

# Hold all jobs in the express queue
qselect -q express | xargs qhold

# Count running jobs
qselect -s R | wc -l
```

## Notes

- With no options, selects all jobs
- Output is one job ID per line
- Designed for use with Unix pipes and xargs

## Exit Status

- `0` — Selection completed (even if no jobs match)
- `1` — Selection failed
- `2` — Cannot connect to server
