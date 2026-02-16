# OpenTorque MOM Configuration Reference

## Overview

The MOM (Machine Oriented Miniserver) daemon reads its configuration from the
`mom_priv/config` file, typically located at `/var/spool/torque/mom_priv/config`.

Each configuration line uses one of two formats:

- **Directives** — lines starting with `$` followed by a parameter name and value:
  ```
  $pbsserver headnode.example.com
  ```
- **Static resources** — plain `name value` pairs that define custom node resources:
  ```
  ncpus 8
  ```

Lines starting with `#` are comments. Blank lines are ignored.

---

## Parameters

### Server & Connection

#### `$pbsserver`
PBS server hostname(s). Can appear multiple times to specify multiple servers.
Port suffixes (e.g., `host:15001`) are accepted but stripped internally.

- **Type:** string (hostname)
- **Default:** none (read from `server_name` file if unset)
- **Example:**
  ```
  $pbsserver headnode.example.com
  $pbsserver backup-server
  ```

#### `$clienthost`
Deprecated alias for `$pbsserver`. Adds the host to the server list.

- **Type:** string (hostname)
- **Default:** none
- **Example:**
  ```
  $clienthost headnode.example.com
  ```

#### `$restricted`
Hosts allowed to connect to this MOM. Can appear multiple times to allow
multiple hosts.

- **Type:** string (hostname)
- **Default:** none
- **Example:**
  ```
  $restricted headnode.example.com
  $restricted admin-server
  ```

#### `$timeout`
DIS TCP protocol timeout in seconds.

- **Type:** integer
- **Default:** `300`
- **Example:**
  ```
  $timeout 600
  ```

#### `$max_conn_timeout_micro_sec`
Maximum connection timeout in microseconds.

- **Type:** integer
- **Default:** `0`
- **Example:**
  ```
  $max_conn_timeout_micro_sec 5000000
  ```

#### `$alias_server_name`
Alias name for the PBS server.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $alias_server_name pbs-alias
  ```

#### `$pbsclient`
Hosts authorized to submit jobs to this MOM. Can appear multiple times.

- **Type:** string (hostname)
- **Default:** none
- **Example:**
  ```
  $pbsclient submit-node.example.com
  ```

#### `$remote_reconfig`
Allow remote reconfiguration of MOM via `qmgr`.

- **Type:** boolean (`true`/`false`/`yes`/`no`/`on`/`off`/`1`/`0`)
- **Default:** `false`
- **Example:**
  ```
  $remote_reconfig true
  ```

---

### Load Management

#### `$ideal_load`
Ideal system load threshold. When the system load drops below this value, the
node is considered available for new work.

- **Type:** float
- **Default:** `-1.0` (disabled)
- **Example:**
  ```
  $ideal_load 3.0
  ```

#### `$max_load`
Maximum system load threshold. When the system load exceeds this value, the
node is considered overloaded.

- **Type:** float
- **Default:** `-1.0` (disabled)
- **Example:**
  ```
  $max_load 6.0
  ```

#### `$auto_ideal_load`
Path to a script that dynamically computes the ideal load value.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $auto_ideal_load /usr/local/scripts/compute_ideal_load.sh
  ```

#### `$auto_max_load`
Path to a script that dynamically computes the maximum load value.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $auto_max_load /usr/local/scripts/compute_max_load.sh
  ```

---

### Resource Enforcement

#### `$ignwalltime`
Ignore walltime limit violations. Jobs will not be killed for exceeding
walltime.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $ignwalltime true
  ```

#### `$ignmem`
Ignore physical memory limit violations.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $ignmem true
  ```

#### `$igncput`
Ignore CPU time limit violations.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $igncput true
  ```

#### `$ignvmem`
Ignore virtual memory limit violations.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $ignvmem true
  ```

#### `$cputmult`
Multiplier applied to CPU time accounting. Useful for normalizing CPU time
across heterogeneous hardware.

- **Type:** float
- **Default:** `1.0`
- **Example:**
  ```
  $cputmult 1.5
  ```

#### `$wallmult`
Multiplier applied to walltime accounting. Useful for normalizing walltime
across heterogeneous hardware.

- **Type:** float
- **Default:** `1.0`
- **Example:**
  ```
  $wallmult 0.8
  ```

---

### Logging

#### `$logevent`
Bitmask controlling which events are logged. Accepts hexadecimal or decimal
values.

- **Type:** integer (hex or decimal)
- **Default:** `0x1ff` (all events)
- **Example:**
  ```
  $logevent 0x1ff
  $logevent 255
  ```

#### `$loglevel`
Log verbosity level. Higher values produce more detailed output.

- **Type:** integer
- **Default:** `0`
- **Example:**
  ```
  $loglevel 3
  ```

#### `$log_directory`
Custom directory path for log files. Overrides the default log location.

- **Type:** string (directory path)
- **Default:** `$PBS_HOME/mom_logs`
- **Example:**
  ```
  $log_directory /var/log/torque/mom
  ```

#### `$log_file_max_size`
Maximum log file size in bytes before rotation.

- **Type:** integer
- **Default:** `0` (unlimited)
- **Example:**
  ```
  $log_file_max_size 104857600
  ```

#### `$log_file_roll_depth`
Number of rolled (rotated) log files to keep.

- **Type:** integer
- **Default:** `1`
- **Example:**
  ```
  $log_file_roll_depth 5
  ```

#### `$log_file_suffix`
Suffix appended to log file names.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $log_file_suffix .log
  ```

#### `$log_keep_days`
Number of days to retain log files before automatic deletion.

- **Type:** integer
- **Default:** `0` (keep forever)
- **Example:**
  ```
  $log_keep_days 30
  ```

---

### Job Execution

#### `$prologalarm`
Timeout in seconds for prologue and epilogue scripts. If a prolog or epilog
exceeds this time, it is killed.

- **Type:** integer
- **Default:** `300`
- **Example:**
  ```
  $prologalarm 600
  ```

#### `$job_starter`
Path to a custom job starter executable. When set, all jobs are launched through
this program.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $job_starter /usr/local/bin/job_wrapper.sh
  ```

#### `$job_starter_run_privileged`
Run the job starter executable with elevated (root) privileges.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $job_starter_run_privileged true
  ```

#### `$preexec`
Pre-execution command run before prologue.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $preexec /usr/local/scripts/preexec.sh
  ```

#### `$source_login_batch`
Source the user's login environment files for batch jobs.

- **Type:** boolean
- **Default:** `true`
- **Example:**
  ```
  $source_login_batch false
  ```

#### `$source_login_interactive`
Source the user's login environment files for interactive jobs.

- **Type:** boolean
- **Default:** `true`
- **Example:**
  ```
  $source_login_interactive false
  ```

#### `$job_output_file_umask`
Umask applied to job output files. Specified in octal.

- **Type:** integer (octal)
- **Default:** `0077`
- **Example:**
  ```
  $job_output_file_umask 0022
  ```

#### `$job_start_block_time`
Seconds to wait before backgrounding a job launch.

- **Type:** integer
- **Default:** `5`
- **Example:**
  ```
  $job_start_block_time 10
  ```

#### `$job_exit_wait_time`
Seconds to wait for a job exit notification before cleanup.

- **Type:** integer
- **Default:** `600`
- **Example:**
  ```
  $job_exit_wait_time 300
  ```

#### `$job_oom_score_adjust`
OOM killer score adjustment for job processes. Range: -1000 to 1000.

- **Type:** integer
- **Default:** `0`
- **Example:**
  ```
  $job_oom_score_adjust 500
  ```

#### `$attempttomakedirectory`
Create job directories if they do not exist.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $attempttomakedirectory true
  ```

#### `$exec_with_exec`
Use `exec()` system call for job execution instead of fork+exec.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $exec_with_exec true
  ```

#### `$presetup_prologue`
Path to a pre-setup prologue script that runs before the standard prologue.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $presetup_prologue /usr/local/scripts/presetup.sh
  ```

#### `$ext_pwd_retry`
Number of retries for external password validation checks.

- **Type:** integer
- **Default:** `3`
- **Example:**
  ```
  $ext_pwd_retry 5
  ```

---

### File & Directory

#### `$usecp`
Maps remote file paths to local paths, enabling direct file copies instead of
network transfers. Format: `host:remote_prefix local_path`.

- **Type:** string (mapping)
- **Default:** none
- **Example:**
  ```
  $usecp headnode:/home /home
  $usecp *:/shared /shared
  ```

#### `$tmpdir`
Temporary directory for job scratch space.

- **Type:** string (directory path)
- **Default:** none
- **Example:**
  ```
  $tmpdir /scratch/tmp
  ```

#### `$nodefile_suffix`
Suffix appended to node names in generated hostfiles.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $nodefile_suffix .internal
  ```

#### `$nospool_dir_list`
Comma-separated list of directories that should not be spooled.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $nospool_dir_list /home,/scratch
  ```

#### `$rcpcmd`
Path to the RCP or SCP command used for file transfers.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $rcpcmd /usr/bin/scp
  ```

#### `$xauthpath`
Path to the `xauth` executable for X11 forwarding.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $xauthpath /usr/bin/xauth
  ```

#### `$spool_as_final_name`
Use the spool file name as the final output file name.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $spool_as_final_name true
  ```

#### `$remote_checkpoint_dirs`
Directories for remote checkpoint storage.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $remote_checkpoint_dirs /shared/checkpoints
  ```

---

### Status & Polling

#### `$check_poll_time`
Interval in seconds between resource monitoring checks on running jobs.

- **Type:** integer
- **Default:** `45`
- **Example:**
  ```
  $check_poll_time 30
  ```

#### `$status_update_time`
Interval in seconds between status updates sent to the PBS server.

- **Type:** integer
- **Default:** `45`
- **Example:**
  ```
  $status_update_time 60
  ```

#### `$max_updates_before_sending`
Number of job updates to batch before sending to the PBS server.

- **Type:** integer
- **Default:** `0`
- **Example:**
  ```
  $max_updates_before_sending 10
  ```

---

### Node Health

#### `$node_check_script`
Path to a node health-check script. The script should exit 0 for healthy
and non-zero for unhealthy.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $node_check_script /usr/local/scripts/health_check.sh
  ```

#### `$node_check_interval`
Interval for running the health-check script. Can be a number of seconds
or the special values `jobstart` or `jobend`.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $node_check_interval 300
  $node_check_interval jobstart
  ```

---

### Configuration Management

#### `$configversion`
Version string for tracking configuration changes.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $configversion 2024-01-15
  ```

#### `$enablemomrestart`
Allow MOM to restart itself when the configuration changes.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $enablemomrestart true
  ```

#### `$down_on_error`
Mark MOM as down when a critical error occurs.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $down_on_error true
  ```

#### `$force_overwrite`
Force overwriting of files during transfers.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $force_overwrite true
  ```

#### `$mom_host`
Override the MOM hostname. Useful when the system hostname differs from
the name known to the PBS server.

- **Type:** string (hostname)
- **Default:** none (uses system hostname)
- **Example:**
  ```
  $mom_host compute-001.internal
  ```

#### `$reject_job_submission`
Reject new job submissions to this node.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $reject_job_submission true
  ```

---

### Checkpoint

#### `$checkpoint_interval`
Default checkpoint interval in seconds.

- **Type:** integer
- **Default:** `0` (disabled)
- **Example:**
  ```
  $checkpoint_interval 600
  ```

#### `$checkpoint_script`
Path to the script executed to create a checkpoint.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $checkpoint_script /usr/local/scripts/checkpoint.sh
  ```

#### `$restart_script`
Path to the script executed to restart a job from a checkpoint.

- **Type:** string (file path)
- **Default:** none
- **Example:**
  ```
  $restart_script /usr/local/scripts/restart.sh
  ```

#### `$checkpoint_run_exe`
Name or path of the checkpoint executable.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $checkpoint_run_exe blcr_checkpoint
  ```

---

### Variable Attributes

#### `$varattr`
Define a variable attribute with a TTL and script path. The script is run
periodically and its output is reported as a node attribute.

- **Type:** string (format: `<TTL> <PATH>`)
- **Default:** none
- **Example:**
  ```
  $varattr 60 /usr/local/scripts/gpu_status.sh
  ```

---

### Memory & CPU

#### `$use_smt`
Enable simultaneous multi-threading (SMT / HyperThreading) awareness.

- **Type:** boolean
- **Default:** `true`
- **Example:**
  ```
  $use_smt false
  ```

#### `$memory_pressure_threshold`
Memory pressure percentage threshold at which jobs may be killed.

- **Type:** integer
- **Default:** `0` (disabled)
- **Example:**
  ```
  $memory_pressure_threshold 90
  ```

#### `$memory_pressure_duration`
Duration in seconds that memory pressure must exceed the threshold before
action is taken.

- **Type:** integer
- **Default:** `0` (disabled)
- **Example:**
  ```
  $memory_pressure_duration 30
  ```

#### `$mom_oom_immunize`
Make the MOM process immune to the Linux OOM killer.

- **Type:** boolean
- **Default:** `true`
- **Example:**
  ```
  $mom_oom_immunize false
  ```

---

### Job Hierarchy

#### `$max_join_job_wait_time`
Maximum time in seconds to wait for sister MOMs to join a multi-node job.

- **Type:** integer
- **Default:** `600`
- **Example:**
  ```
  $max_join_job_wait_time 900
  ```

#### `$resend_join_job_wait_time`
Time in seconds before resending a join-job request to sister MOMs.

- **Type:** integer
- **Default:** `300`
- **Example:**
  ```
  $resend_join_job_wait_time 120
  ```

#### `$mom_hierarchy_retry_time`
Retry interval in seconds for MOM hierarchy communication.

- **Type:** integer
- **Default:** `0`
- **Example:**
  ```
  $mom_hierarchy_retry_time 60
  ```

#### `$job_directory_sticky`
Keep job directories across MOM restarts instead of cleaning them up.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $job_directory_sticky true
  ```

---

### Hardware

#### `$cuda_visible_devices`
Automatically set `CUDA_VISIBLE_DEVICES` for jobs based on assigned GPUs.

- **Type:** boolean
- **Default:** `true`
- **Example:**
  ```
  $cuda_visible_devices false
  ```

---

### Advanced

#### `$reduce_prolog_checks`
Skip redundant prolog state checks to improve job startup performance.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $reduce_prolog_checks true
  ```

#### `$thread_unlink_calls`
Use a background thread for file unlink operations to reduce blocking.

- **Type:** boolean
- **Default:** `true`
- **Example:**
  ```
  $thread_unlink_calls false
  ```

#### `$reporter_mom`
Designate this MOM as a reporter node in a hierarchical configuration.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $reporter_mom true
  ```

#### `$login_node`
Designate this MOM as running on a login node.

- **Type:** boolean
- **Default:** `false`
- **Example:**
  ```
  $login_node true
  ```

#### `$alloc_par_cmd`
Command used for allocation parallel operations.

- **Type:** string
- **Default:** none
- **Example:**
  ```
  $alloc_par_cmd /usr/local/bin/alloc_parallel
  ```

---

### Static Resources

Static resources are defined as plain `name value` lines (without a `$` prefix).
These are reported to the PBS server as node attributes and can be used in job
resource requests.

- **Example:**
  ```
  ncpus 8
  gpus 2
  mem 64gb
  arch linux
  ```

---

## Example Configuration

A comprehensive `mom_priv/config` example for a typical compute node:

```
# ---- Server & Connection ----
$pbsserver       headnode.example.com
$restricted      headnode.example.com
$timeout         300
$mom_host        compute-001.internal

# ---- Logging ----
$logevent            0x1ff
$loglevel            3
$log_directory       /var/log/torque/mom
$log_file_roll_depth 5
$log_keep_days       30

# ---- Status & Polling ----
$check_poll_time     30
$status_update_time  60

# ---- Load Management ----
$ideal_load  4.0
$max_load    8.0

# ---- Resource Enforcement ----
$cputmult    1.0
$wallmult    1.0
$ignwalltime false

# ---- Job Execution ----
$prologalarm              600
$source_login_batch       true
$source_login_interactive true
$job_output_file_umask    0022
$job_exit_wait_time       600
$attempttomakedirectory   true

# ---- File & Directory ----
$usecp  headnode:/home /home
$usecp  *:/shared      /shared
$tmpdir /scratch/tmp
$rcpcmd /usr/bin/scp

# ---- Node Health ----
$node_check_script   /usr/local/scripts/health_check.sh
$node_check_interval 300

# ---- Configuration Management ----
$enablemomrestart true
$down_on_error    true
$configversion    2024-01-15

# ---- Memory & CPU ----
$use_smt          true
$mom_oom_immunize true

# ---- Hardware ----
$cuda_visible_devices true

# ---- Static Resources ----
ncpus 16
gpus  2
mem   128gb
arch  linux
```
