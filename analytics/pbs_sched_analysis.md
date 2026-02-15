# PBS Scheduler (pbs_sched) Module Analysis

## Overview

The PBS scheduler (`pbs_sched`) is a **separate daemon process** that runs alongside `pbs_server`. It is responsible for deciding which queued jobs should run, when, and on which compute nodes. The scheduler connects to the server via TCP, queries the current state, makes scheduling decisions, and instructs the server to run jobs on specific nodes.

TORQUE provides a pluggable scheduler architecture with multiple algorithms (FIFO, round-robin, fair-share, priority-based) configurable via `sched_config`.

**Important**: The Go `pbs_server` implementation has a **built-in scheduler** that replaces the need for the external `pbs_sched` daemon. The C implementation uses the external daemon.

## Architecture

### Process Topology

```
                    ┌──────────────┐
                    │  pbs_sched   │  (External scheduler daemon)
                    │              │
                    │  Algorithms: │
                    │  - FIFO      │
                    │  - Round-Robin│
                    │  - Fair-Share│
                    │  - Priority  │
                    └──────┬───────┘
                           │ TCP connection (PBS IFL API)
                           │ pbs_connect() / pbs_disconnect()
                           │
                    ┌──────▼───────┐
                    │  pbs_server   │
                    │              │
                    │  Jobs/Queues │  ← qsub, qstat, qdel (CLI tools)
                    │  Nodes       │
                    └──────┬───────┘
                           │ TCP (DIS protocol)
                           │ RunJob → QueueJob + JobScript + Commit
                           │
                    ┌──────▼───────┐
                    │  pbs_mom      │  (Compute node)
                    │              │
                    │  Execute job │
                    │  Send obit   │
                    └──────────────┘
```

### Key Design Principle

**The scheduler does NOT communicate with pbs_mom directly.** All communication flows through pbs_server:
- Scheduler queries node status from the server (not from MOMs)
- Scheduler tells the server to run a job; the server dispatches it to MOM
- Job completion notifications go from MOM → server; the server triggers the next scheduling cycle

## Communication Protocol

### Scheduler ↔ Server Communication

The scheduler uses the **PBS IFL (Interface Library) API** to communicate with the server over TCP. This is the same API that CLI tools (qstat, qsub) use.

#### Connection Lifecycle

```
Each scheduling cycle:
  1. pbs_connect(server_name)      → Open TCP connection
  2. pbs_statserver_err()          → Query server status
  3. pbs_statque_err("")           → Query all queues and their jobs
  4. pbs_statnode_err("")          → Query all node status
  5. [for each job to run]:
     pbs_runjob_err(job_id, node)  → Tell server to run job on node
  6. pbs_disconnect()              → Close TCP connection
```

#### API Calls Used by Scheduler

| API Call | Direction | Purpose |
|----------|-----------|---------|
| `pbs_connect()` | Sched → Server | Open authenticated TCP connection |
| `pbs_statserver_err()` | Sched → Server | Get server resource limits, total jobs, state |
| `pbs_statque_err()` | Sched → Server | Get all queues with job lists and limits |
| `pbs_statnode_err()` | Sched → Server | Get all nodes with available resources |
| `pbs_runjob_err()` | Sched → Server | Request server to dispatch job to a specific node |
| `pbs_deljob_err()` | Sched → Server | Delete a job that can never run |
| `pbs_disconnect()` | Sched → Server | Close connection |

### Server → Scheduler Triggering

The server notifies the scheduler to run a cycle by sending a **command code** over a dedicated TCP socket:

| Command | Code | Trigger |
|---------|------|---------|
| `SCH_SCHEDULE_NEW` | 1 | New job submitted or becomes eligible |
| `SCH_SCHEDULE_TERM` | 2 | A running job terminated |
| `SCH_SCHEDULE_TIME` | 3 | Periodic timer interval reached (`scheduler_iteration`) |
| `SCH_SCHEDULE_RECYC` | 4 | Re-run scheduler after completing one cycle |
| `SCH_SCHEDULE_CMD` | 5 | Administrator command (e.g., `qmgr: set server scheduling = True`) |
| `SCH_CONFIGURE` | 7 | Re-read configuration files |
| `SCH_QUIT` | 8 | Shutdown scheduler |
| `SCH_SCHEDULE_FIRST` | 10 | Initial cycle after server startup |

The server sends a 4-byte integer command code via `contact_sched()` in `run_sched.c`. The scheduler receives it via `select()` on its listening socket and calls `schedule(cmd)`.

### Server → MOM Job Dispatch

When the scheduler calls `pbs_runjob_err()`, the server:
1. Validates the job exists and is in Queued state
2. Updates job state to Running
3. Opens a TCP connection to MOM on the target node
4. Sends: `QueueJob` (job attributes) → `JobScript` (script content) → `Commit`
5. MOM executes the job and sends `JobObit` back to server on completion

## Scheduling Algorithms

### Configuration File

The scheduler reads its configuration from `$PBS_HOME/sched_priv/sched_config`:

```
round_robin: False          all
by_queue: True              prime
by_queue: True              non_prime
strict_fifo: false          ALL
fair_share: false           ALL
help_starving_jobs: true    ALL
sort_queues: true           ALL
load_balancing: false       ALL
sort_by: shortest_job_first ALL
max_starve: 24:00:00
half_life: 24:00:00
unknown_shares: 10
sync_time: 1:00:00
```

Each setting can be applied to `prime` time (business hours), `non_prime` time (off-hours), or `ALL`.

### 1. FIFO (First In, First Out)

**Config**: `strict_fifo: true`

The simplest algorithm. Jobs are scheduled in the order they were submitted (by queue time). If the first job cannot run (insufficient resources), **no subsequent jobs are scheduled** — they are blocked until the first job runs.

```
Job Queue:  [J1: 8 cores] [J2: 1 core] [J3: 2 cores]
Available:  4 cores

strict_fifo=true:   J1 cannot run → J2 and J3 are BLOCKED
strict_fifo=false:  J1 cannot run → try J2 (runs!) → try J3 (runs!)
```

### 2. Round-Robin

**Config**: `round_robin: true`

Jobs are selected from queues in a round-robin fashion — one job from queue A, then one from queue B, then back to A. This prevents a single busy queue from starving other queues.

```
Queue A: [A1] [A2] [A3]
Queue B: [B1] [B2]

Execution order: A1 → B1 → A2 → B2 → A3
```

### 3. By-Queue Priority

**Config**: `by_queue: true`

Queues are processed in priority order (sorted by queue attributes). All eligible jobs in the highest-priority queue are scheduled before moving to the next queue.

### 4. Fair-Share

**Config**: `fair_share: true`

Allocates resources proportionally based on user/group shares defined in `resource_group`. Users who have consumed less than their fair share are prioritized.

**Key parameters:**
- `half_life: 24:00:00` — Usage decay half-life (older usage counts less)
- `unknown_shares: 10` — Default shares for users not in resource_group
- `sync_time: 1:00:00` — How often fair-share usage data is written to disk

**Algorithm:**
1. Each user/group is assigned a share percentage from `resource_group`
2. Actual usage is tracked and decayed exponentially over time
3. `fair_share_perc = assigned_shares / total_shares`
4. Users with `actual_usage < fair_share_perc` are prioritized
5. `extract_fairshare()` picks the job from the user with the lowest usage-to-share ratio

### 5. Priority-Based Sorting

**Config**: `sort_by: <criterion>`

Jobs are sorted before scheduling. Available sort criteria:

| Sort Criterion | Description |
|---------------|-------------|
| `shortest_job_first` | Sort by ascending requested walltime |
| `longest_job_first` | Sort by descending requested walltime |
| `smallest_memory_first` | Sort by ascending requested memory |
| `largest_memory_first` | Sort by descending requested memory |
| `high_priority_first` | Sort by descending job priority |
| `low_priority_first` | Sort by ascending job priority |
| `fair_share` | Sort by fair-share usage ratio |

Multiple sort keys can be chained — the first key is primary, subsequent keys break ties.

### 6. Starvation Prevention

**Config**: `help_starving_jobs: true`, `max_starve: 24:00:00`

If a job has been waiting longer than `max_starve`, it is flagged as "starving" and given scheduling priority regardless of other sorting criteria. This prevents large jobs from waiting indefinitely while small jobs keep running.

### 7. Dedicated Time

**Config file**: `sched_priv/dedicated_time`

Administrators can reserve time windows where only specific jobs (with the `dedicated_prefix`) can run. Format:

```
# FROM                TO
04/15/2024 12:00      04/15/2024 15:30
```

### 8. Prime/Non-Prime Time

**Config file**: `sched_priv/holidays`

Different scheduling policies can apply during business hours (prime) vs. off-hours (non-prime). Each config setting can specify `prime`, `non_prime`, or `ALL`.

## Resource Checking

Before running a job, the scheduler verifies multiple resource constraints:

### Check Order (is_ok_to_run_job)

```
1. Queue checks:
   ├── Is queue started?
   ├── Is queue type = Execution?
   ├── Queue job limit reached?
   ├── Queue user limit reached?
   ├── Queue group limit reached?
   └── Queue available resources sufficient?

2. Server checks:
   ├── Server job limit reached?
   ├── Server user limit reached?
   ├── Server group limit reached?
   └── Server available resources sufficient?

3. Node checks:
   ├── Enough nodes available?
   ├── Node resources (CPU, memory) sufficient?
   └── Node in schedulable state (free, not offline/down)?

4. Dedicated time check:
   └── Is current time within a dedicated window?
```

### Failure Codes

| Code | Meaning |
|------|---------|
| `QUEUE_NOT_STARTED` | Queue is not started |
| `QUEUE_NOT_EXEC` | Queue is not an execution queue |
| `QUEUE_JOB_LIMIT_REACHED` | Queue has max running jobs |
| `QUEUE_USER_LIMIT_REACHED` | User has max jobs in queue |
| `QUEUE_GROUP_LIMIT_REACHED` | Group has max jobs in queue |
| `SERVER_JOB_LIMIT_REACHED` | Server has max total running jobs |
| `SERVER_USER_LIMIT_REACHED` | User has max total running jobs |
| `NOT_ENOUGH_NODES_AVAIL` | Insufficient compute nodes |
| `JOB_STARVING` | Job has been waiting too long |

## Go Server Built-in Scheduler

The Go `pbs_server` implementation includes a **built-in FIFO scheduler** that replaces the external `pbs_sched` daemon:

### Implementation

```go
// schedulerLoop runs on a timer (scheduler_iteration seconds)
func (s *Server) schedulerLoop() {
    for {
        select {
        case <-s.schedTicker.C:
            if s.cfg.Scheduling {
                s.runScheduler()
            }
        }
    }
}

// runScheduler iterates queued jobs in FIFO order
func (s *Server) runScheduler() {
    queued := s.jobMgr.QueuedJobs()
    sort.Slice(queued, func(i, k int) bool {
        return queued[i].QueueTime.Before(queued[k].QueueTime)
    })
    for _, j := range queued {
        s.scheduleJob(j)   // Find free node, dispatch
    }
}
```

### Comparison: C External vs Go Built-in

| Feature | C pbs_sched (External) | Go Built-in Scheduler |
|---------|----------------------|---------------------|
| Process | Separate daemon | Goroutine in server |
| Communication | TCP + IFL API | Direct in-memory access |
| Algorithms | FIFO, round-robin, fair-share, priority | FIFO only |
| Configuration | sched_config file | Server config parameters |
| Prime/Non-Prime | Yes | No |
| Fair-Share | Yes (with usage tracking) | No |
| Starvation Prevention | Yes (max_starve) | No |
| Dedicated Time | Yes | No |
| Resource Checking | CPU, memory, nodes, limits | Node slot availability only |
| Latency | ~10-50ms per cycle (network round-trip) | ~0.1ms per cycle (in-process) |
| Pluggability | Fully replaceable binary | Compiled into server |

## Configuration Files

| File | Location | Purpose |
|------|----------|---------|
| `sched_config` | `$PBS_HOME/sched_priv/` | Main scheduling policy configuration |
| `resource_group` | `$PBS_HOME/sched_priv/` | Fair-share group tree and share allocations |
| `holidays` | `$PBS_HOME/sched_priv/` | Prime/non-prime time definitions |
| `dedicated_time` | `$PBS_HOME/sched_priv/` | Reserved time windows |
| `usage` | `$PBS_HOME/sched_priv/` | Fair-share historical usage data (auto-generated) |
| `sched_out` | `$PBS_HOME/sched_priv/` | Scheduler stdout/stderr output |
| `sched.lock` | `$PBS_HOME/sched_priv/` | PID lock file |

### sched_config Reference

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `strict_fifo` | boolean | false | Block all jobs behind first unrunnable job |
| `round_robin` | boolean | false | Cycle through queues taking one job each |
| `by_queue` | boolean | true | Process queues in priority order |
| `fair_share` | boolean | false | Enable fair-share scheduling |
| `help_starving_jobs` | boolean | true | Boost priority of long-waiting jobs |
| `sort_queues` | boolean | true | Sort queues by priority |
| `load_balancing` | boolean | false | Distribute jobs evenly across nodes |
| `sort_by` | enum | shortest_job_first | Job sorting criterion |
| `max_starve` | time | 24:00:00 | Maximum wait before starvation boost |
| `half_life` | time | 24:00:00 | Fair-share usage decay half-life |
| `unknown_shares` | integer | 10 | Default shares for ungrouped users |
| `sync_time` | time | 1:00:00 | Fair-share usage persistence interval |
| `dedicated_prefix` | string | ded | Queue prefix for dedicated-time jobs |
| `log_filter` | integer | 256 | Log verbosity filter |

## Scheduling Cycle Flowchart

```
Server sends SCH_SCHEDULE_* command
            │
            ▼
    ┌───────────────┐
    │ pbs_connect()  │  Connect to server
    └───────┬───────┘
            │
            ▼
    ┌───────────────┐
    │ query_server() │  Build in-memory data structures
    │  - statserver  │  (server, queues, jobs, nodes)
    │  - statque     │
    │  - statnode    │
    └───────┬───────┘
            │
            ▼
    ┌───────────────────────┐
    │ init_scheduling_cycle()│
    │  - sort jobs           │
    │  - decay fair-share    │
    │  - detect starving     │
    └───────┬───────────────┘
            │
            ▼
    ┌───────────────────┐
    │ next_job(sinfo)    │◄─────────────┐
    │  - FIFO order      │              │
    │  - round-robin     │              │
    │  - fair-share pick │              │
    └───────┬───────────┘              │
            │                           │
            ▼                           │
    ┌───────────────────────┐          │
    │ is_ok_to_run_job()     │          │
    │  - check queue limits  │          │
    │  - check server limits │          │
    │  - check node resources│          │
    └───┬───────────┬───────┘          │
        │           │                   │
     SUCCESS      FAIL                  │
        │           │                   │
        ▼           ▼                   │
  ┌──────────┐ ┌──────────┐           │
  │run_update│ │ set comment│           │
  │  _job()  │ │ log reason │           │
  │pbs_runjob│ │ (can_never │───────────┘
  └──────────┘ │  →pbs_del) │
               └──────────┘
            │
            ▼
    ┌───────────────┐
    │pbs_disconnect()│  Close connection
    └───────────────┘
```
