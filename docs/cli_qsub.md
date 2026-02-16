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
| `-a` | date_time | Deferred execution time. Job will be held in `W` (Waiting) state until the specified time. Format: `[[[[CC]YY]MM]DD]hhmm[.SS]`. |
| `-A` | account | Account string for job accounting and chargeback. Recorded in accounting records. |
| `-c` | checkpoint | Checkpoint options: `none`, `enabled`, `shutdown`, `periodic`, or an interval value. |
| `-C` | prefix | Directive prefix in script (default `#PBS`). |
| `-d` | directory | Working directory for the job. Sets `PBS_O_WORKDIR`. |
| `-D` | path | Root directory path (chroot) for the job. |
| `-e` | path | Path for the job's standard error file. Default: `<job_name>.e<job_id>` in the submission directory. |
| `-f` | | Mark job as fault tolerant. |
| `-F` | arguments | Arguments passed to the job script as positional parameters (`$1`, `$2`, ...). |
| `-h` | | Place a user hold on the job at submission. Job enters `H` (Held) state and will not run until released with `qrls`. |
| `-j` | join_type | Join stdout and stderr into a single file. Values: `oe` (merge stderr into stdout), `eo` (merge stdout into stderr). |
| `-k` | keep | Keep stdout/stderr files on the execution host. Values: `n` (none), `o` (stdout), `e` (stderr), `oe`/`eo` (both). |
| `-l` | resource_list | Resource requirements as comma-separated `key=value` pairs. Examples: `nodes=1:ppn=4`, `walltime=01:00:00`, `mem=4gb`. |
| `-m` | mail_options | Mail notification events. Values: `a` (abort), `b` (begin), `e` (end), `n` (none). Can be combined: `abe`. |
| `-M` | user_list | Comma-separated list of email recipients for job notifications. |
| `-N` | name | Job name. Defaults to the script filename or `STDIN` if reading from stdin. |
| `-o` | path | Path for the job's standard output file. Default: `<job_name>.o<job_id>` in the submission directory. |
| `-p` | priority | Job priority, range -1024 to 1023. Higher values give higher scheduling priority. Default: 0. |
| `-q` | queue | Destination queue name. If not specified, the server's default queue is used. |
| `-r` | y\|n | Whether the job is rerunnable (`y`) or not (`n`). Default is server-dependent. |
| `-S` | shell | Full path to the shell used to execute the job script (e.g., `/bin/bash`). |
| `-s` | server | Connect to the specified PBS server instead of the default. |
| `-t` | array_range | Job array specification. Format: `start-end[%max_simultaneous]`. Example: `0-99%5`. |
| `-u` | user_list | User list for job ownership mapping. |
| `-v` | variable_list | Comma-separated list of environment variables to export to the job. Format: `VAR=value,VAR2=value2`. |
| `-V` | | Export all current environment variables to the job. |
| `-W` | attributes | Additional job attributes as comma-separated `key=value` pairs. Supports: `depend=`, `stagein=`, `stageout=`, `group_list=`, and any custom attribute. |
| `-z` | | Quiet mode. Do not print the job identifier after submission. |

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

# Submit to a specific queue with priority
qsub -q high_priority -p 500 -N urgent_job job.sh

# Submit with account for chargeback
qsub -A project_alpha -N simulation job.sh

# Submit with mail notification
qsub -m abe -M user@example.com job.sh

# Submit a held job (release later with qrls)
qsub -h -N staged_job job.sh

# Submit with deferred execution (run at 2am tomorrow)
qsub -a 202602170200 -N nightly_job job.sh

# Submit with checkpoint enabled
qsub -c enabled -N long_job job.sh

# Keep output files on execution host
qsub -k oe -N local_output job.sh

# Submit with dependencies (run after job 42 succeeds)
qsub -W depend=afterok:42.server job.sh

# Submit with script arguments
qsub -F "input.dat output.dat" -N process job.sh

# Submit a job array (100 tasks, 5 concurrent)
qsub -t 0-99%5 -N array_job job.sh

# Submit with joined output and custom shell
qsub -j oe -S /bin/zsh -N custom_shell job.sh

# Combined: account, priority, mail, keepfiles, rerunnable, resources
qsub -A billing -p 200 -m ae -M admin@co.com -k oe -r y \
  -l walltime=04:00:00,mem=8gb,nodes=4:ppn=8 -N big_job job.sh

# Quiet mode (no job ID printed)
echo 'echo test' | qsub -z
```

## Environment Variables

The following PBS environment variables are automatically set in the job:

| Variable | Description |
|----------|-------------|
| `PBS_O_HOME` | Submitting user's home directory |
| `PBS_O_LOGNAME` | Submitting user's login name |
| `PBS_O_WORKDIR` | Submission working directory |
| `PBS_O_SHELL` | Submitting user's login shell |
| `PBS_O_HOST` | Submission host name |
| `PBS_O_PATH` | Submitting user's PATH |

Additional environment:
- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qstat(1), qdel(1), qhold(1), qrls(1), qrerun(1)
