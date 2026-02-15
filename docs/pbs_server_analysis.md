# PBS Server (pbs_server) Module Analysis

## Overview

`pbs_server` is the central daemon of the TORQUE resource management system. It is responsible for:
- Accepting and managing batch jobs from clients (`qsub`, `qstat`, `qdel`, etc.)
- Managing compute node inventory and health
- Scheduling job execution on available nodes
- Communicating with `pbs_mom` daemons on compute nodes
- Persisting all state (jobs, queues, nodes, server config) to disk
- Enforcing resource limits, ACLs, and authorization policies

**Source location**: `src/server/` (~81,000 lines of C code across 133 files)

---

## 1. Architecture

### 1.1 Process Model
- Single-process, multi-threaded daemon
- Main thread runs event loop (250ms cycle)
- Thread pool (`task_pool`) processes requests and timed tasks
- Dedicated threads for: connection acceptance, routing retries, exiting job inspection, job cleanup

### 1.2 Network Ports
| Port | Purpose | Default |
|------|---------|---------|
| `pbs_server_port_dis` | Client requests (DIS protocol) | 15001 |
| `pbs_scheduler_port` | Scheduler communication | 15004 |
| `pbs_mom_port` | MOM communication | 15002 |
| `pbs_rm_port` | Resource manager | 15003 |

### 1.3 Key Data Structures
- **Server** (`server` global): Configuration, job counters, next job ID, ACLs
- **Job** (`job` struct): Full job state, attributes, execution info, dependencies
- **Queue** (`pbs_queue`): Job container, limits, ACLs, routing rules
- **Node** (`pbsnode`): Compute node state, resources, health, GPU/MIC info

### 1.4 Protocol
- **DIS (Data Is Strings)**: Wire protocol for all client/server/MOM communication
- **IS (Information Status)**: Sub-protocol used by MOMs for status updates (protocol type=4)
- **Batch Protocol**: Request/reply over DIS (protocol type=2, version=2)

---

## 2. Core Subsystems

### 2.1 Request Processing Pipeline

```
TCP Accept → Read DIS Header → Authentication → ACL Check → Dispatch → Handler → Reply
```

**Files**: `incoming_request.c`, `process_request.c`

#### Authentication Flow:
1. **Privileged port** (< 1024): Trusted as server/MOM, granted full permissions
2. **Unix socket**: Credentials extracted via `get_creds()`
3. **Inet socket**: 
   - `ENABLE_TRUSTED_AUTH=TRUE`: bypass auth
   - `MUNGE_AUTH`: validate via munge
   - Otherwise: reject with `PBSE_BADCRED`

#### Request Dispatch Table (35+ request types):
| Type Code | Name | Handler | Description |
|-----------|------|---------|-------------|
| 0 | Connect | special | Initial connection handshake |
| 1 | QueueJob | `req_quejob()` | Submit job to queue |
| 2 | JobCred | `req_jobcredential()` | Job credential transfer |
| 3 | JobScript | `req_jobscript()` | Job script transfer |
| 4 | RdytoCommit | `req_rdycommit()` | Ready-to-commit signal |
| 5 | Commit | `req_commit()` | Commit job to queue |
| 6 | DeleteJob | `req_deletejob()` | Delete/cancel job |
| 7 | HoldJob | `req_holdjob()` | Place hold on job |
| 8 | LocateJob | `req_locatejob()` | Find job location |
| 9 | Manager | `req_manager()` | Admin commands (qmgr) |
| 10 | Message | `req_messagejob()` | Send message to job |
| 11 | ModifyJob | `req_modifyjob()` | Modify job attributes |
| 12 | MoveJob | `req_movejob()` | Move job between queues |
| 13 | ReleaseJob | `req_releasejob()` | Release job hold |
| 14 | RescQuery | `req_rescq()` | Resource query |
| 15 | Rescreserve | `req_rescreserve()` | Reserve resources |
| 16 | RescFree | `req_rescfree()` | Free resources |
| 17 | Shutdown | `svr_shutdown()` | Server shutdown |
| 18 | SignalJob | `req_signaljob()` | Send signal to job |
| 19 | StatusJob | `req_stat_job()` | Query job status |
| 20 | StatusQueue | `req_stat_que()` | Query queue status |
| 21 | StatusServer | `req_stat_svr()` | Query server status |
| 24 | TrackJob | `req_trackjob()` | Track job movement |
| 25 | SelectJobs | `req_selectjobs()` | Select jobs by criteria |
| 26 | Rerun | `req_rerunjob()` | Rerun completed job |
| 45 | AuthenUser | `req_authenuser()` | User authentication |
| 48 | StageIn | `req_stagein()` | Stage input files |
| 50 | RunJob | `req_runjob()` | Force-run job on node |
| 51 | AltAuthenUser | `req_altauthenuser()` | Alternative auth |
| 52 | StatusNode | `req_stat_node()` | Query node status |
| 53 | ReturnFiles | `req_returnfiles()` | Return job files |
| 56 | JobObit | `req_jobobit()` | Job completion notice |
| 58 | GpuCtrl | `req_gpuctrl()` | GPU control |
| 59 | Disconnect | special | Close connection |

### 2.2 Job Management

**Files**: `req_quejob.c`, `svr_jobfunc.c`, `job_func.c`, `job_container.c`

#### Job Submission Flow:
```
Client → QueueJob(attrs) → JobCred → JobScript → [RdytoCommit] → Commit
```
1. `req_quejob()`: Validate user, check queue ACLs/limits, create job structure
2. `req_jobcredential()`: Store job credentials
3. `req_jobscript()`: Receive and store job script
4. `req_commit()`: Finalize job, assign job ID, persist to disk

#### Job State Machine:
```
Transit → Queued → Held → Waiting → Running → Exiting → Complete
                                              ↓
                                           Suspended
```

Key state transitions:
- `svr_setjobstate()`: Central state change function with persistence
- `svr_enquejob()` / `svr_dequejob()`: Queue membership management
- `svr_evaljobstate()`: Evaluate if job is ready to run
- `chk_resc_limits()`: Validate resource requests against queue/server limits

#### Job Attributes:
- Stored as array indexed by `JOB_ATR_*` enum values
- Dynamic attribute system with encode/decode/compare/set/free operations
- Resource attributes have sub-resources (e.g., `Resource_List.walltime`)

### 2.3 Job Execution

**Files**: `req_runjob.c`, `svr_movejob.c`, `issue_request.c`

#### Dispatch to MOM Flow:
```
Scheduler → RunJob → assign_hosts() → svr_startjob() → send_job() → MOM
```
1. `req_runjob()`: Scheduler or admin requests job execution
2. `assign_hosts()`: Allocate compute nodes based on resource requirements
3. `svr_startjob()`: Set up execution, stage files if needed
4. `send_job()` / `svr_movejob()`: Transfer job (attrs + script) to MOM via DIS
5. MOM receives QueueJob → JobScript → Commit sequence

#### Node Selection:
- Parse `nodes` resource specification (e.g., `2:ppn=4`)
- Match against available nodes
- Reserve slots on selected nodes
- Create `exec_host` string (e.g., `node1/0+node1/1+node2/0+node2/1`)

### 2.4 Job Completion

**Files**: `req_jobobit.c`, `exiting_jobs.c`

#### Obit Processing Flow:
```
MOM → JobObit(exit_status, resources_used) → Server
  → release_nodes()
  → update_accounting()
  → deliver_output_files()
  → mark_complete()
```
1. `req_jobobit()`: Receive obit from MOM
2. `on_job_exit()`: Central cleanup coordinator
3. `handle_complete_first_time()`: Release nodes, update accounting
4. `handle_complete_second_time()`: File staging, final cleanup
5. `setup_cpyfiles()`: Stage output/error files back to submit host

### 2.5 Node Management

**Files**: `node_manager.c`, `node_func.c`, `process_mom_update.c`, `receive_mom_communication.c`

#### Node Lifecycle:
```
Create (qmgr) → Initialize → Health Check → Status Update → [Down/Offline]
```

#### Key Operations:
- `check_nodes()`: Periodic health check task (polls MOMs)
- `update_node_state()`: Central state transition function
- `process_status_info()`: Parse IS protocol status from MOMs
- `save_node_status()`: Persist node attributes
- `handle_auto_np()`: Auto-detect processor count from MOM reports
- `reserve_node()` / `release_node_allocation()`: Resource allocation tracking

#### Node States:
| State | Meaning |
|-------|---------|
| `INUSE_FREE` | Available for jobs |
| `INUSE_JOB` | Running a job |
| `INUSE_DOWN` | Unreachable |
| `INUSE_OFFLINE` | Administratively disabled |
| `INUSE_RESERVE` | Reserved for specific job |
| `INUSE_BUSY` | Load too high |

#### IS Protocol Message Handling:
- `svr_is_request()`: Main IS message dispatcher
- Message types: IS_NULL (keepalive), IS_UPDATE, IS_STATUS, IS_GPU_STATUS
- Status items: `state`, `availmem`, `physmem`, `ncpus`, `loadave`, `jobs`, etc.

### 2.6 Queue Management

**Files**: `queue_func.c`, `queue_recov.c`, `req_manager.c`

#### Queue Types:
- **Execution queue**: Jobs run from this queue on compute nodes
- **Route queue**: Jobs are routed to other queues/servers

#### Queue Operations:
- `que_alloc()` / `que_free()`: Create/destroy queues
- `find_queuebyname()`: Lookup with locking
- `que_save()` / `que_recov_xml()`: Persistence
- Queue attributes: max_jobs, max_run, resource limits, ACLs, enabled/started state

### 2.7 Server Administration

**Files**: `req_manager.c`

#### qmgr Commands:
- **Server**: `set/unset server attr=value`
- **Queue**: `create/delete/set/unset queue name attr=value`
- **Node**: `create/delete/set node name attr=value`

#### Key Administrative Functions:
- `mgr_server_set()` / `mgr_server_unset()`: Server config
- `mgr_queue_create()` / `mgr_queue_delete()`: Queue CRUD
- `mgr_queue_set()` / `mgr_queue_unset()`: Queue config
- `mgr_node_create()` / `mgr_node_delete()`: Node CRUD
- `mgr_node_set()`: Node config

### 2.8 Status Queries

**Files**: `req_stat.c`, `svr_format_job.c`

Handles `qstat` and related status queries:
- `req_stat_job()`: Job status (single or all)
- `req_stat_que()`: Queue status
- `req_stat_node()`: Node status
- `req_stat_svr()`: Server status

Response format: attribute list encoded in DIS `svrattrl` format.

### 2.9 Scheduler Interface

**Files**: `pbsd_main.c` (handle_scheduler_contact, schedule_jobs)

- Server contacts scheduler on configurable interval (`scheduler_iteration`)
- Schedule triggers: time-based, event-based (job submit, job complete, node change)
- Scheduler runs as separate process, connects to server via dedicated port
- `SCH_SCHEDULE_*` flags: NULL, NEW, TERM, TIME, RECYCLER, FIRST, JOBCOMPL, etc.

---

## 3. Persistence Layer

### 3.1 Data Format
- **Primary**: XML-based storage
- **Fallback**: Legacy binary format for backward compatibility
- **Atomic writes**: Write to `.new` temp file, then rename (prevents corruption)

### 3.2 Storage Locations
```
/var/spool/torque/
├── server_priv/
│   ├── serverdb          # Server state (next job ID, counters)
│   ├── jobs/             # Job files (*.JB = attributes, *.SC = script)
│   ├── queues/           # Queue definitions
│   ├── nodes             # Node inventory
│   └── acl_svr/          # Server ACLs
├── server_logs/          # Server log files (daily rotation)
└── accounting/           # Accounting records (daily files)
```

### 3.3 Key Persistence Functions
- `job_save()` / `job_recov()`: Job state
- `que_save()` / `que_recov_xml()`: Queue config
- `svr_save_xml()` / `svr_recov_xml()`: Server state
- `save_nodes_db()` / node recovery in `pbsd_init()`

### 3.4 Recovery on Startup
`pbsd_init()` orchestrates:
1. Load server database (next job ID, config)
2. Load queue definitions
3. Load node inventory
4. For each saved job: evaluate state, re-queue or re-run as appropriate
5. Re-establish MOM connections

---

## 4. Accounting System

**File**: `accounting.c`

- Daily log file rotation
- Record types: Start (S), End (E), Abort (A), Queue (Q), Checkpoint (C)
- Fields: job ID, user, group, queue, resources requested/used, timestamps
- Format: `MM/DD/YYYY HH:MM:SS;TYPE;jobid;key=value key=value ...`

---

## 5. Security Model

### 5.1 Authentication Methods
1. **Privileged port**: Connections from port < 1024 are trusted (root only)
2. **Unix domain socket**: OS-level credential passing
3. **trqauthd**: Authentication daemon for non-privileged clients
4. **Munge**: Cluster-wide authentication (optional)
5. **Trusted auth**: Bypass for trusted networks (configurable)

### 5.2 Authorization Levels
| Level | Permissions |
|-------|-------------|
| User | Read own jobs, submit jobs |
| Operator | Read all, hold/release/signal jobs |
| Manager | Full control (create queues, nodes, etc.) |
| Server | Internal server-to-server trust |

### 5.3 ACL Types
- `acl_hosts`: Allowed submission hosts
- `acl_users`: Allowed users per queue
- `acl_groups`: Allowed groups per queue
- Server-level manager/operator lists

---

## 6. Key Internal Patterns

### 6.1 Thread Safety
- All major structures protected by mutexes (`pthread_mutex_t`)
- Lock ordering: Server → Queue → Job → Node (prevents deadlocks)
- Recycler pattern for deferred destruction (avoids use-after-free)

### 6.2 Task System
- `set_task()`: Schedule callback for future execution
- `WORK_Immed`: Execute in thread pool immediately
- `WORK_Timed`: Execute at specified time
- Main loop calls `check_tasks()` every 250ms

### 6.3 Connection Management
- `svr_connect()`: Establish TCP to MOM/server with privileged port
- Connection slots tracked in global table
- Auto-reconnect on failure with retry backoff

### 6.4 Attribute System
- Generic attribute framework with type-specific handlers
- Operations: encode, decode, set, compare, free, action
- Attribute definitions in `*_attr_def.c` files
- Supports: string, long, boolean, list, resource, ACL types

---

## 7. Configuration

### 7.1 Command-Line Options
- `-t create|warm|hot|cold`: Startup type (create new or recover)
- `-p port`: Override server port
- `-d path`: PBS home directory
- `-D`: Debug mode (foreground)
- `-A acctfile`: Accounting file location
- `-L logfile`: Log file location

### 7.2 Server Attributes (set via qmgr)
- `scheduling`: Enable/disable scheduling (True/False)
- `scheduler_iteration`: Scheduling cycle interval (seconds)
- `default_queue`: Default submission queue
- `max_running`: Maximum concurrent running jobs
- `node_check_rate`: How often to ping nodes (seconds)
- `tcp_timeout`: TCP connection timeout
- `acl_hosts`: Allowed submit hosts
- `managers`: Manager user list
- `operators`: Operator user list
- `log_events`: Log event mask

---

## 8. Summary Statistics

| Metric | Value |
|--------|-------|
| Source files | 133 (.c + .h) |
| Lines of code | ~82,000 |
| Request types | 35+ |
| Major subsystems | 9 (request processing, job mgmt, execution, completion, nodes, queues, admin, status, persistence) |
| Thread model | Thread pool + dedicated threads |
| Protocol | DIS over TCP |
| Persistence | XML files with atomic writes |
| Authentication | 5 methods (privileged port, unix socket, trqauthd, munge, trusted) |

---

## 9. Go Reimplementation Scope

For a functional Go reimplementation that works with existing `pbs_mom` (C or Go), the minimum viable feature set is:

### Must Have (Phase 1):
1. DIS protocol codec (already done in go-mom)
2. TCP listener with request dispatch
3. Job submission (QueueJob → JobScript → Commit)
4. Job execution dispatch to MOM
5. Job obit processing
6. Node management (IS protocol status updates)
7. Queue management (at least one execution queue)
8. Status queries (qstat, pbsnodes)
9. State persistence (jobs, queues, server config)
10. Basic authentication (privileged port)

### Nice to Have (Phase 2):
- Multiple queue support with routing
- Full ACL enforcement
- Scheduler integration
- Job dependencies
- Job arrays
- GPU/MIC support
- Email notifications
- Accounting records

### Not Needed for Basic Testing:
- Munge authentication
- ALPS/Cray support
- Job checkpointing
- Remote server federation
