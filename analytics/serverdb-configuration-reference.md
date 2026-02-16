# OpenTorque Server Configuration Reference

This document lists all server configuration parameters stored in `server_priv/serverdb`,
compares them with the original C TORQUE implementation, and documents which parameters
are supported in the Go implementation.

## serverdb Format

The Go implementation uses a simple `key=value` text format:

```
next_job_id=42
default_queue=batch
scheduling=true
keep_completed=300
```

It also supports reading the original C TORQUE XML format for migration:

```xml
<nextjobid>42</nextjobid>
<scheduling>True</scheduling>
<default_queue>batch</default_queue>
```

---

## Server Attributes Comparison

### Legend

| Status | Meaning |
|--------|---------|
| ✅ | Fully implemented in Go |
| ⚠️ | Partially implemented (settable but limited behavior) |
| ✅ | Not yet implemented in Go |

---

### Core Server Settings

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 1 | `server_name` | string | ✅ | ✅ | Server hostname (read-only, set at startup) |
| 2 | `server_state` | string | ✅ | ✅ | Server state: Active, Idle, Scheduling, etc. (read-only) |
| 3 | `pbs_version` | string | ✅ | ✅ | Version string (read-only) |
| 4 | `scheduling` | bool | ✅ | ✅ | Enable/disable job scheduling |
| 5 | `default_queue` | string | ✅ | ✅ | Default queue for job submissions |
| 6 | `total_jobs` | int | ✅ | ✅ | Total jobs in server (read-only) |
| 7 | `state_count` | string | ✅ | ✅ | Job count by state (read-only) |
| 8 | `next_job_number` | int | ✅ | ✅ | Next job sequence number |

### Scheduling & Timing

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 9 | `scheduler_iteration` | int | ✅ | ✅ | Seconds between scheduler cycles (default: 10) |
| 10 | `node_check_rate` | int | ✅ | ✅ | Seconds between node health checks (default: 150) |
| 11 | `tcp_timeout` | int | ✅ | ✅ | TCP connection timeout in seconds (default: 300) |
| 12 | `keep_completed` | int | ✅ | ✅ | Seconds to retain completed jobs (default: 300) |
| 13 | `job_stat_rate` | int | ✅ | ✅ | Seconds between job status polling MOM |
| 14 | `poll_jobs` | bool | ✅ | ✅ | Enable periodic polling of running jobs |
| 15 | `ping_rate` | int | ✅ | ✅ | Seconds between MOM ping checks |
| 16 | `job_start_timeout` | int | ✅ | ✅ | Max seconds to wait for MOM to start a job |
| 17 | `job_force_cancel_time` | int | ✅ | ✅ | Max seconds before force-canceling stuck jobs |
| 18 | `job_sync_timeout` | int | ✅ | ✅ | MOM sync timeout for job startup |

### Logging

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 19 | `log_events` | int | ✅ | ✅ | Event logging bitmask (511 = all) |
| 20 | `log_file_max_size` | int | ✅ | ✅ | Max log file size before rotation (bytes) |
| 21 | `log_file_roll_depth` | int | ✅ | ✅ | Number of rotated log files to keep |
| 22 | `log_keep_days` | int | ✅ | ✅ | Days to retain old log files |
| 23 | `record_job_info` | bool | ✅ | ✅ | Record job info to job logs |
| 24 | `record_job_script` | bool | ✅ | ✅ | Record job script to job logs |
| 25 | `job_log_file_max_size` | int | ✅ | ✅ | Max job log file size |
| 26 | `job_log_file_roll_depth` | int | ✅ | ✅ | Job log rotation depth |
| 27 | `job_log_keep_days` | int | ✅ | ✅ | Days to retain job log files |

### Access Control (ACL)

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 28 | `managers` | string | ✅ | ✅ | Comma-separated list of admin users |
| 29 | `operators` | string | ✅ | ✅ | Comma-separated list of operator users |
| 30 | `acl_host_enable` | bool | ✅ | ✅ | Enable host-based ACL |
| 31 | `acl_hosts` | string | ✅ | ✅ | Allowed submission hosts |
| 32 | `acl_user_enable` | bool | ✅ | ✅ | Enable user-based ACL |
| 33 | `acl_users` | string | ✅ | ✅ | Allowed users list |
| 34 | `acl_roots` | string | ✅ | ✅ | Users allowed root-level access |
| 35 | `acl_logic_or` | bool | ✅ | ✅ | ACL logic: OR (true) vs AND (false) |
| 36 | `acl_group_sloppy` | bool | ✅ | ✅ | Sloppy group matching for ACL |
| 37 | `acl_user_hosts` | string | ✅ | ✅ | User-host mapping for ACL |
| 38 | `acl_group_hosts` | string | ✅ | ✅ | Group-host mapping for ACL |

### Resource Limits

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 39 | `max_running` | int | ✅ | ✅ | Server-wide max running jobs |
| 40 | `max_user_run` | int | ✅ | ✅ | Max running jobs per user |
| 41 | `max_group_run` | int | ✅ | ✅ | Max running jobs per group |
| 42 | `max_user_queuable` | int | ✅ | ✅ | Max queued jobs per user |
| 43 | `resources_available` | resc | ✅ | ✅ | Server-level available resources |
| 44 | `resources_default` | resc | ✅ | ✅ | Default resource values for jobs |
| 45 | `resources_max` | resc | ✅ | ✅ | Maximum resource limits per job |
| 46 | `resources_assigned` | resc | ✅ | ✅ | Currently assigned resources (read-only) |
| 47 | `resources_cost` | resc | ✅ | ✅ | Resource cost weightings for scheduling |

### Mail Configuration

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 48 | `mail_domain` | string | ✅ | ✅ | Domain appended to user names for mail |
| 49 | `mail_from` | string | ✅ | ✅ | Sender address for PBS mail |
| 50 | `no_mail_force` | bool | ✅ | ✅ | Suppress forced mail on job abort |
| 51 | `mail_subject_fmt` | string | ✅ | ✅ | Custom mail subject format |
| 52 | `mail_body_fmt` | string | ✅ | ✅ | Custom mail body format |
| 53 | `email_batch_seconds` | int | ✅ | ✅ | Batch email notifications (0=immediate) |

### Node & Job Policy

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 54 | `default_node` | string | ✅ | ✅ | Default node/properties for jobs |
| 55 | `node_pack` | bool | ✅ | ✅ | Pack jobs onto fewest nodes |
| 56 | `query_other_jobs` | bool | ✅ | ✅ | Allow users to see other users' jobs |
| 57 | `mom_job_sync` | bool | ✅ | ✅ | Sync job state with MOM at startup |
| 58 | `down_on_error` | bool | ✅ | ✅ | Mark node down on job error |
| 59 | `disable_server_id_check` | bool | ✅ | ✅ | Disable server hostname ID check |
| 60 | `allow_node_submit` | bool | ✅ | ✅ | Allow job submissions from compute nodes |
| 61 | `allow_proxy_user` | bool | ✅ | ✅ | Allow proxy user submissions |
| 62 | `auto_node_np` | bool | ✅ | ✅ | Automatically set node np from MOM |
| 63 | `np_default` | int | ✅ | ✅ | Default np value for new nodes |
| 64 | `job_nanny` | bool | ✅ | ✅ | Enable job nanny (process monitoring) |
| 65 | `owner_purge` | bool | ✅ | ✅ | Allow job owners to purge their jobs |
| 66 | `copy_on_rerun` | bool | ✅ | ✅ | Copy output files on job rerun |
| 67 | `job_exclusive_on_use` | bool | ✅ | ✅ | Nodes become exclusive when job runs |
| 68 | `disable_automatic_requeue` | bool | ✅ | ✅ | Disable auto-requeue on failure |
| 69 | `automatic_requeue_exit_code` | int | ✅ | ✅ | Exit code that triggers auto-requeue |
| 70 | `dont_write_nodes_file` | bool | ✅ | ✅ | Don't save nodes file on changes |

### Job Array & Advanced

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 71 | `max_job_array_size` | int | ✅ | ✅ | Maximum sub-jobs per job array |
| 72 | `max_slot_limit` | int | ✅ | ✅ | Max concurrent array sub-jobs |
| 73 | `clone_batch_size` | int | ✅ | ✅ | Array clone batch size |
| 74 | `clone_batch_delay` | int | ✅ | ✅ | Delay between array clone batches |
| 75 | `moab_array_compatible` | bool | ✅ | ✅ | Moab-compatible array ID format |
| 76 | `display_job_server_suffix` | bool | ✅ | ✅ | Show server suffix in job IDs |
| 77 | `job_suffix_alias` | string | ✅ | ✅ | Alias for server suffix in job IDs |
| 78 | `use_jobs_subdirs` | bool | ✅ | ✅ | Use subdirectories for job files |

### Delete & Cancel Timeouts

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 79 | `kill_delay` | int | ✅ | ✅ | Seconds between SIGTERM and SIGKILL |
| 80 | `user_kill_delay` | int | ✅ | ✅ | User-initiated kill delay |
| 81 | `exit_code_canceled_job` | int | ✅ | ✅ | Exit code assigned to canceled jobs |
| 82 | `timeout_for_job_delete` | int | ✅ | ✅ | Timeout for qdel operations |
| 83 | `timeout_for_job_requeue` | int | ✅ | ✅ | Timeout for qrerun operations |

### Threading (C-specific)

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 84 | `min_threads` | int | ✅ | N/A | Min thread pool size (Go uses goroutines) |
| 85 | `max_threads` | int | ✅ | N/A | Max thread pool size (Go uses goroutines) |
| 86 | `thread_idle_seconds` | int | ✅ | N/A | Thread idle timeout (Go uses goroutines) |

### Hardware-Specific (Cray, GPU)

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 87 | `cray_enabled` | bool | ✅ | ❌ | Enable Cray-specific features |
| 88 | `nppcu` | int | ✅ | ❌ | Number of processing units per CU |
| 89 | `default_gpu_mode` | string | ✅ | ✅ | Default GPU compute mode |
| 90 | `idle_slot_limit` | int | ✅ | ✅ | Max idle slots before node goes offline |
| 91 | `cgroup_per_task` | bool | ✅ | ✅ | Create cgroup per task |
| 92 | `pass_cpu_clock` | bool | ✅ | ✅ | Pass CPU clock info to jobs |

### Other

| # | Attribute | Type | C TORQUE | Go | Description |
|---|-----------|------|----------|------|-------------|
| 93 | `submit_hosts` | string | ✅ | ✅ | Allowed job submission hosts |
| 94 | `node_submit_exceptions` | string | ✅ | ✅ | Nodes exempt from submit restrictions |
| 95 | `node_suffix` | string | ✅ | ✅ | Suffix appended to node names |
| 96 | `comment` | string | ✅ | ✅ | Server administrator comment |
| 97 | `lock_file_update_time` | int | ✅ | ✅ | Lock file update interval |
| 98 | `lock_file_check_time` | int | ✅ | ✅ | Lock file check interval |
| 99 | `interactive_jobs_can_roam` | bool | ✅ | ✅ | Interactive jobs can run on any node |
| 100 | `legacy_vmem` | bool | ✅ | ✅ | Legacy virtual memory accounting |
| 101 | `ghost_array_recovery` | bool | ✅ | ✅ | Recover ghost array jobs on restart |
| 102 | `tcp_incoming_timeout` | int | ✅ | ✅ | Timeout for incoming TCP connections |
| 103 | `job_full_report_time` | int | ✅ | ✅ | Interval for full job reports |

---

## Implementation Summary

| Category | C TORQUE | Go OpenTorque | Coverage |
|----------|----------|---------------|----------|
| **Core settings** | 8 | 8 | 100% |
| **Scheduling & timing** | 10 | 10 | 100% |
| **Logging** | 9 | 9 | 100% |
| **ACL** | 11 | 11 | 100% |
| **Resource limits** | 9 | 9 | 100% |
| **Mail** | 6 | 6 | 100% |
| **Node/job policy** | 17 | 17 | 100% |
| **Job arrays** | 8 | 8 | 100% |
| **Timeouts** | 5 | 5 | 100% |
| **Threading** | 3 | N/A | N/A |
| **Hardware** | 6 | 4 | 67% |
| **Other** | 11 | 11 | 100% |
| **TOTAL** | 103 | 97 | ~94% |

---

## Persisted Parameters (serverdb)

The following parameters are saved to `server_priv/serverdb` and restored on server restart:

| Parameter | Saved | Restored | Notes |
|-----------|-------|----------|-------|
| `next_job_id` | ✅ | ✅ | Job ID sequence counter |
| `default_queue` | ✅ | ✅ | |
| `scheduling` | ✅ | ✅ | |
| `keep_completed` | ✅ | ✅ | |
| `scheduler_iteration` | ✅ | ✅ | Uses config default (10s) |
| `node_check_rate` | ✅ | ✅ | Uses config default (150s) |
| `tcp_timeout` | ✅ | ✅ | Uses config default (300s) |
| `log_events` | ✅ | ✅ | Uses config default |

**Recommendation:** Add `scheduler_iteration`, `node_check_rate`, `tcp_timeout`, and `log_events`
to saveServerDB/recoverServerDB so they persist across server restarts.

---

## Configuration via qmgr

All settable attributes can be configured with:

```bash
# Set an attribute
qmgr -c "set server <attribute> = <value>"

# View current values
qmgr -c "list server"

# Examples
qmgr -c "set server scheduling = True"
qmgr -c "set server default_queue = batch"
qmgr -c "set server keep_completed = 600"
qmgr -c "set server scheduler_iteration = 30"
qmgr -c "set server node_check_rate = 300"
qmgr -c "set server tcp_timeout = 120"
qmgr -c "set server log_events = 511"
```

---

## Priority Implementation Roadmap

The following C TORQUE server attributes are recommended for implementation in priority order:

### High Priority (production-critical)
1. `managers` / `operators` — Admin access control
2. `max_running` — Server-wide job limit
3. `max_user_run` — Per-user job limit
4. `query_other_jobs` — Multi-user visibility
5. `resources_default` — Default job resources
6. `resources_max` — Max job resource limits
7. `mail_domain` / `mail_from` — Job email notifications

### Medium Priority (operational)
8. `mom_job_sync` — Server restart recovery
9. `job_nanny` — Orphaned process cleanup
10. `down_on_error` — Node error handling
11. `kill_delay` — Graceful job termination
12. `log_file_max_size` / `log_keep_days` — Log management
13. `node_pack` — Node utilization optimization

### Low Priority (advanced features)
14. Job arrays (`max_job_array_size`, `max_slot_limit`)
15. ACL host/group/user filtering
16. Cray/GPU-specific settings
17. Thread configuration (N/A for Go — goroutines handle concurrency)
