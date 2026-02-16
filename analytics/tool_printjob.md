# printjob — Print PBS Job File Contents

## Synopsis

```
printjob [-a] [-p pbs_home] {job_file | job_id}...
```

## Description

`printjob` reads and displays the contents of PBS job files from the server's job database. It is a debugging tool for inspecting job attributes as stored on disk.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-a` | — | — | Show all attributes including internal ones |
| `-p` | path | /var/spool/torque | PBS home directory |

## Arguments

| Argument | Description |
|----------|-------------|
| `job_id` | Job ID (e.g., `123.server`) — looks for `$PBS_HOME/server_priv/jobs/ID.JB` |
| `job_file` | Full path to a job file |

## Examples

```bash
# Print job by ID
sudo printjob 123.server

# Print all attributes
sudo printjob -a 123.server

# Print from a specific file
sudo printjob /var/spool/torque/server_priv/jobs/123.server.JB
```

## Notes

- Requires read access to `$PBS_HOME/server_priv/jobs/` (typically root)
- Job files use key=value format, one attribute per line
- Attributes starting with `_` are internal (shown only with `-a`)

## Exit Status

- `0` — Job file(s) displayed
- `1` — Cannot read job file
