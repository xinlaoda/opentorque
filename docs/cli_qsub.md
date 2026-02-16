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

## Resource List (`-l`) Detailed Reference

The `-l` option specifies resource requirements for the job. The scheduler uses
these to select appropriate compute nodes, and the MOM daemon enforces limits
during execution.

**Format**: `-l key=value,key=value,...`

### Available Resource Keywords

| Resource | Type | Description | Example |
|----------|------|-------------|---------|
| `nodes` | string | Node count and processors per node | `nodes=2:ppn=4` |
| `walltime` | HH:MM:SS | Maximum wall clock time | `walltime=04:00:00` |
| `cput` | HH:MM:SS | Maximum CPU time | `cput=02:00:00` |
| `mem` | size | Maximum physical memory | `mem=8gb` |
| `vmem` | size | Maximum virtual memory | `vmem=16gb` |
| `ncpus` | int | Number of CPU cores | `ncpus=8` |
| `pmem` | size | Per-process memory limit | `pmem=2gb` |
| `pvmem` | size | Per-process virtual memory | `pvmem=4gb` |
| `file` | size | Maximum file size | `file=10gb` |

Size units: `b` (bytes), `kb`, `mb`, `gb`, `tb`.

### Interaction with MOM Configuration

Resource enforcement on compute nodes is controlled by MOM config (`mom_priv/config`):

| MOM Parameter | Default | Effect on `-l` |
|---------------|---------|----------------|
| `$ignwalltime` | false | If true, MOM won't kill jobs exceeding `-l walltime` |
| `$ignmem` | false | If true, MOM won't kill jobs exceeding `-l mem` |
| `$igncput` | false | If true, MOM won't kill jobs exceeding `-l cput` |
| `$ignvmem` | false | If true, MOM won't kill jobs exceeding `-l vmem` |
| `$cputmult` | 1.0 | Multiplier applied to CPU time limit (compensate for CPU speed) |
| `$wallmult` | 1.0 | Multiplier applied to walltime limit |

### Examples

```bash
# Single node, 4 cores, 8GB RAM, 2-hour walltime
qsub -l nodes=1:ppn=4,walltime=02:00:00,mem=8gb job.sh

# Two nodes with 8 cores each, large memory
qsub -l nodes=2:ppn=8,mem=64gb,walltime=24:00:00 job.sh

# CPU time limit (job killed if CPU time exceeds 4 hours)
qsub -l cput=04:00:00,walltime=08:00:00 job.sh
```

## Additional Attributes (`-W`) Detailed Reference

The `-W` option passes advanced job attributes. Multiple attributes are
separated by commas: `-W key1=value1,key2=value2`.

### Job Dependencies (`depend`)

Control execution order between jobs. Jobs with unsatisfied dependencies
remain in Queued (Q) state until conditions are met.

**Format**: `-W depend=type:jobid[:jobid][,type:jobid...]`

| Dependency Type | Condition to Run |
|----------------|-----------------|
| `afterok:jobid` | Run after jobid completes with exit status 0 |
| `afternotok:jobid` | Run after jobid completes with non-zero exit |
| `afterany:jobid` | Run after jobid completes (any exit status) |
| `before:jobid` | This job must start before jobid can start |
| `beforeok:jobid` | jobid runs only after this job succeeds |
| `beforenotok:jobid` | jobid runs only after this job fails |
| `beforeany:jobid` | jobid runs after this job completes (any exit) |

**Examples**:
```bash
# Job B runs only if Job A succeeds
JOB_A=$(echo 'echo step1' | qsub -N step1)
echo 'echo step2' | qsub -N step2 -W depend=afterok:$JOB_A

# Job C runs after Job A finishes, regardless of exit status
echo 'echo cleanup' | qsub -N cleanup -W depend=afterany:$JOB_A

# Job D runs only if Job A fails (e.g., error handler)
echo 'echo error_handler' | qsub -N errhandler -W depend=afternotok:$JOB_A

# Chain: A -> B -> C
JOB_B=$(echo 'echo step2' | qsub -N step2 -W depend=afterok:$JOB_A)
echo 'echo step3' | qsub -N step3 -W depend=afterok:$JOB_B

# Multiple dependencies: run after both A and B succeed
echo 'echo merge' | qsub -N merge -W depend=afterok:$JOB_A:$JOB_B
```

**Behavior**:
- If a dependency job has been purged (removed from server), it is treated as
  completed successfully for `afterok`/`afterany`.
- The dependency check runs every scheduling cycle, so there may be a brief
  delay after the parent completes.
- Dependencies are checked by both the built-in scheduler and external
  scheduler (pbs_sched).

### File Staging (`stagein` / `stageout`)

Transfer files to/from compute nodes before and after job execution.

**Format**: `local_file@remote_host:remote_path`

Multiple files separated by commas.

**stagein** — Files copied TO the compute node BEFORE job starts:
```bash
# Copy input data from a file server before the job runs
qsub -W stagein=input.dat@fileserver:/data/experiment/input.dat job.sh

# Multiple files
qsub -W stagein=data1.csv@storage:/csv/data1.csv,data2.csv@storage:/csv/data2.csv job.sh
```

**stageout** — Files copied FROM the compute node AFTER job finishes:
```bash
# Copy results back to a file server after the job completes
qsub -W stageout=results.tar@fileserver:/results/output.tar job.sh

# Both stagein and stageout
qsub -W stagein=input.dat@storage:/in/input.dat,stageout=output.dat@storage:/out/output.dat job.sh
```

**Interaction with MOM Configuration**:
- File transfers use `scp` by default
- The MOM `$rcpcmd` config parameter can override the remote copy command
- The MOM `$usecp` config maps remote paths to local paths for direct copy
  (avoids network transfer for shared filesystems)
- Stagein failures abort the job (before prologue runs)
- Stageout failures are logged but don't change the job exit status

### Group List (`group_list`)

Specifies the Unix group(s) for the job.

```bash
# Run job under the 'research' group
qsub -W group_list=research job.sh
```

### Combined `-W` Examples

```bash
# Complex workflow: dependencies + staging + group
qsub -W depend=afterok:100.server,stagein=model.bin@storage:/models/v2.bin,group_list=ml_team job.sh

# Pipeline with staging
JOB1=$(echo 'process input.dat > output.dat' | qsub -N process \
  -W stagein=input.dat@storage:/raw/data.dat)
echo 'analyze output.dat' | qsub -N analyze \
  -W depend=afterok:$JOB1,stageout=report.pdf@storage:/reports/report.pdf
```

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
