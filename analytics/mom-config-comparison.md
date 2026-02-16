# MOM Configuration Parameter Comparison: Go vs C TORQUE

This document compares the `mom_priv/config` parameters supported by the
Go OpenTorque implementation versus the original C TORQUE implementation.

## Config File Format

Both implementations use the same format:
```
$pbsserver  headnode
$logevent   0x1ff
$usecp      headnode:/home  /home
ncpus  8
```
- Lines starting with `$` are directives
- Lines starting with `#` are comments
- Plain `name value` pairs define static resources

---

## Parameter Comparison

### Legend

| Status | Meaning |
|--------|---------|
| ✅ | Implemented |
| ❌ | Not implemented |
| N/A | Not applicable |

---

### Server & Connection

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 1 | `$pbsserver` | ✅ | ✅ | PBS server hostname (can appear multiple times) |
| 2 | `$clienthost` | ✅ | ✅ | Deprecated alias for `$pbsserver` |
| 3 | `$restricted` | ✅ | ✅ | Hosts allowed to connect to MOM (wildcard support) |
| 4 | `$timeout` | ✅ | ✅ | DIS TCP protocol timeout (seconds) |
| 5 | `$max_conn_timeout_micro_sec` | ✅ | ✅ | Max connection timeout (microseconds) |
| 6 | `$alias_server_name` | ✅ | ✅ | Alias name for the server |
| 7 | `$pbsclient` | ✅ | ✅ | Client hosts authorized to submit jobs to MOM |
| 8 | `$remote_reconfig` | ✅ | ✅ | Allow remote reconfiguration via qmgr |

### Load Management

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 9 | `$ideal_load` | ✅ | ✅ | Ideal system load threshold (float) |
| 10 | `$max_load` | ✅ | ✅ | Max system load threshold (float) |
| 11 | `$auto_ideal_load` | ✅ | ✅ | Script to auto-compute ideal load |
| 12 | `$auto_max_load` | ✅ | ✅ | Script to auto-compute max load |

### Resource Enforcement

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 13 | `$ignwalltime` | ✅ | ✅ | Ignore walltime limit violations |
| 14 | `$ignmem` | ✅ | ✅ | Ignore memory limit violations |
| 15 | `$igncput` | ✅ | ✅ | Ignore CPU time violations |
| 16 | `$ignvmem` | ✅ | ✅ | Ignore virtual memory violations |
| 17 | `$cputmult` | ✅ | ✅ | CPU time multiplier factor (float) |
| 18 | `$wallmult` | ✅ | ✅ | Walltime multiplier factor (float) |

### Logging

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 19 | `$logevent` | ✅ | ✅ | Event logging bitmask (hex/decimal) |
| 20 | `$loglevel` | ✅ | ✅ | Log verbosity level (integer) |
| 21 | `$log_directory` | ✅ | ✅ | Custom log directory path |
| 22 | `$log_file_max_size` | ✅ | ✅ | Max log file size (bytes) |
| 23 | `$log_file_roll_depth` | ✅ | ✅ | Number of rolled log files to keep |
| 24 | `$log_file_suffix` | ✅ | ✅ | Log file naming suffix |
| 25 | `$log_keep_days` | ✅ | ✅ | Days to keep log files |

### Job Execution

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 26 | `$prologalarm` | ✅ | ✅ | Prolog/epilog timeout (seconds, default 300) |
| 27 | `$job_starter` | ✅ | ✅ | Custom job starter executable |
| 28 | `$job_starter_run_privileged` | ✅ | ✅ | Run job starter with elevated privileges |
| 29 | `$preexec` | ✅ | ✅ | Prolog pre-execution config |
| 30 | `$source_login_batch` | ✅ | ✅ | Source login env for batch jobs (default: true) |
| 31 | `$source_login_interactive` | ✅ | ✅ | Source login env for interactive jobs (default: true) |
| 32 | `$job_output_file_umask` | ✅ | ✅ | Umask for job output files |
| 33 | `$job_start_block_time` | ✅ | ✅ | Seconds to wait before backgrounding job launch (default: 5) |
| 34 | `$job_exit_wait_time` | ✅ | ✅ | Wait time for job exit notification (default: 600) |
| 35 | `$job_oom_score_adjust` | ✅ | ✅ | OOM killer score adjustment (-1000 to 1000) |
| 36 | `$attempttomakedirectory` | ✅ | ✅ | Create job directories if missing |
| 37 | `$exec_with_exec` | ✅ | ✅ | Use exec() for job execution |
| 38 | `$presetup_prologue` | ✅ | ✅ | Pre-setup prologue script path |
| 39 | `$ext_pwd_retry` | ✅ | ✅ | External password check retries (default: 3) |

### File & Directory

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 40 | `$usecp` | ✅ | ✅ | File copy mapping (host:from → local) |
| 41 | `$tmpdir` | ✅ | ✅ | Temporary directory for jobs |
| 42 | `$nodefile_suffix` | ✅ | ✅ | Suffix for node names in hostfiles |
| 43 | `$nospool_dir_list` | ✅ | ✅ | Directories not to spool |
| 44 | `$rcpcmd` | ✅ | ✅ | Path to RCP/SCP command for file transfer |
| 45 | `$xauthpath` | ✅ | ✅ | Path to xauth executable |
| 46 | `$spool_as_final_name` | ✅ | ✅ | Use spool filename as final output name |
| 47 | `$remote_checkpoint_dirs` | ✅ | ✅ | Remote checkpoint directories |

### Status & Polling

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 48 | `$check_poll_time` | ✅ | ✅ | Resource monitoring check interval (default: 45s) |
| 49 | `$status_update_time` | ✅ | ✅ | Status update interval to server (default: 45s) |
| 50 | `$max_updates_before_sending` | ✅ | ✅ | Batch job updates before server sync |

### Node Health

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 51 | `$node_check_script` | ✅ | ✅ | Node health check script |
| 52 | `$node_check_interval` | ✅ | ✅ | Health check interval; supports "jobstart"/"jobend" |

### Configuration Management

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 53 | `$configversion` | ✅ | ✅ | Config version string for tracking |
| 54 | `$enablemomrestart` | ✅ | ✅ | Allow MOM to restart on config change |
| 55 | `$down_on_error` | ✅ | ✅ | Set MOM down on critical errors |
| 56 | `$force_overwrite` | ✅ | ✅ | Force file overwriting |
| 57 | `$mom_host` | ✅ | ✅ | Override MOM hostname |
| 58 | `$reject_job_submission` | ✅ | ✅ | Reject new job submissions |

### Checkpoint

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 59 | `$checkpoint_interval` | ✅ | ✅ | Checkpoint frequency |
| 60 | `$checkpoint_script` | ✅ | ✅ | Checkpoint script path |
| 61 | `$restart_script` | ✅ | ✅ | Restart from checkpoint script path |
| 62 | `$checkpoint_run_exe` | ✅ | ✅ | Checkpoint executable name |

### Variable Attributes

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 63 | `$varattr` | ✅ | ✅ | Variable attribute (format: `<TTL> <PATH>`) |

### Memory & CPU (Linux-specific)

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 64 | `$use_smt` | ✅ | ✅ | Use simultaneous multi-threading (default: 1) |
| 65 | `$memory_pressure_threshold` | ✅ | ✅ | Memory pressure kill threshold |
| 66 | `$memory_pressure_duration` | ✅ | ✅ | Memory pressure monitoring duration |
| 67 | `$mom_oom_immunize` | ✅ | ✅ | Make MOM process OOM-immune (default: 1) |

### Job Hierarchy & Clustering

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 68 | `$max_join_job_wait_time` | ✅ | ✅ | Max wait for job join (default: 600s) |
| 69 | `$resend_join_job_wait_time` | ✅ | ✅ | Resend wait for job join (default: 300s) |
| 70 | `$mom_hierarchy_retry_time` | ✅ | ✅ | Hierarchy retry interval |
| 71 | `$job_directory_sticky` | ✅ | ✅ | Keep job directory across restarts |

### Hardware-specific

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 72 | `$cuda_visible_devices` | ✅ | ✅ | CUDA GPU visibility control (default: 1) |
| 73 | `$cray_check_rur` | ✅ | ❌ | Cray Resource Utilization Record check |
| 74 | `$apbasil_path` | ✅ | ❌ | ALPS BASIL command path (Cray) |
| 75 | `$apbasil_protocol` | ✅ | ❌ | ALPS BASIL protocol version (Cray) |
| 76 | `$alloc_par_cmd` | ✅ | ✅ | Allocation parallel command |

### Advanced

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 77 | `$reduce_prolog_checks` | ✅ | ✅ | Skip redundant prolog checks |
| 78 | `$thread_unlink_calls` | ✅ | ✅ | Threaded file unlink (default: true) |
| 79 | `$reporter_mom` | ✅ | ✅ | MOM is a reporter node |
| 80 | `$login_node` | ✅ | ✅ | MOM is a login node |

### Static Resources

| # | Parameter | C TORQUE | Go | Description |
|---|-----------|----------|-----|-------------|
| 81 | `<name> <value>` | ✅ | ✅ | Custom static resources (e.g., `ncpus 8`, `gpus 2`) |

---

## Summary

| Category | C TORQUE | Go OpenTorque | Coverage |
|----------|----------|---------------|----------|
| Server & connection | 8 | 8 | 100% |
| Load management | 4 | 4 | 100% |
| Resource enforcement | 6 | 6 | 100% |
| Logging | 7 | 7 | 100% |
| Job execution | 14 | 14 | 100% |
| File & directory | 8 | 8 | 100% |
| Status & polling | 3 | 3 | 100% |
| Node health | 2 | 2 | 100% |
| Configuration mgmt | 6 | 6 | 100% |
| Checkpoint | 4 | 4 | 100% |
| Variable attrs | 1 | 1 | 100% |
| Memory & CPU | 4 | 4 | 100% |
| Job hierarchy | 4 | 4 | 100% |
| Hardware-specific | 5 | 2 | 40% |
| Advanced | 4 | 4 | 100% |
| Static resources | ✅ | ✅ | 100% |
| **TOTAL directives** | **80** | **77** | **~96%** |

---

## Remaining Gaps

The only unimplemented directives are Cray-specific parameters that are not
applicable to standard Linux clusters:

1. **`$cray_check_rur`** — Cray Resource Utilization Record check
2. **`$apbasil_path`** — ALPS BASIL command path (Cray)
3. **`$apbasil_protocol`** — ALPS BASIL protocol version (Cray)
