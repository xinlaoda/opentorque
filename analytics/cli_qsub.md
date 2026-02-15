# qsub - Submit PBS Batch Jobs

## Synopsis

```
qsub [options] [script_file]
echo "command" | qsub [options]
```

## Description

`qsub` submits a batch job script to the PBS server for execution. The job is placed in the specified queue (or the default queue) and waits until resources become available.

If a script file is provided as an argument, it is read and submitted. Otherwise, `qsub` reads the job script from standard input. The command prints the assigned job identifier to stdout on success.

The Go implementation uses HMAC-SHA256 token authentication instead of the legacy trqauthd daemon, enabling cross-platform operation.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-q` | queue | Destination queue name. If not specified, the server's default queue is used. |
| `-N` | name | Job name. Defaults to the script filename or `STDIN` if reading from stdin. |
| `-l` | resource_list | Resource requirements as comma-separated `key=value` pairs. Examples: `nodes=1:ppn=4`, `walltime=01:00:00`, `mem=4gb`. |
| `-o` | path | Path for the job's standard output file. Default: `<job_name>.o<job_id>` in the submission directory. |
| `-e` | path | Path for the job's standard error file. Default: `<job_name>.e<job_id>` in the submission directory. |
| `-j` | join_type | Join stdout and stderr into a single file. Values: `oe` (merge stderr into stdout), `eo` (merge stdout into stderr). |
| `-m` | mail_options | Mail notification events. Values: `a` (abort), `b` (begin), `e` (end), `n` (none). Can be combined: `abe`. |
| `-M` | user_list | Comma-separated list of email recipients for job notifications. |
| `-d` | directory | Working directory for the job. Sets `PBS_O_WORKDIR`. |
| `-v` | variable_list | Comma-separated list of environment variables to export to the job. Format: `VAR=value,VAR2=value2`. |
| `-V` | | Export all current environment variables to the job. |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Exit Status

- `0` — Job submitted successfully
- `1` — Error (connection failure, authentication error, or submission rejected)

## Examples

```bash
# Submit a script file
qsub job.sh

# Submit from stdin
echo '#!/bin/bash
echo "Hello World"' | qsub

# Submit with resource requirements
qsub -l nodes=2:ppn=4,walltime=02:00:00 -N my_job job.sh

# Submit to a specific queue
qsub -q high_priority job.sh

# Submit with mail notification
qsub -m abe -M user@example.com job.sh
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qstat(1), qdel(1), qhold(1), qrls(1)
