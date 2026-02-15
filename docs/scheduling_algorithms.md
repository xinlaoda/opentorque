# TORQUE Go Scheduler — Scheduling Algorithms Reference

This document describes all scheduling algorithms available in the Go TORQUE
scheduler, their behavior, interactions, and configuration. Both the **built-in
FIFO scheduler** (embedded in pbs_server) and the **external pbs_sched process**
are covered.

---

## 1. Scheduler Modes

### 1.1 Built-in FIFO Scheduler (`scheduler_mode: builtin`)

The built-in scheduler runs as a goroutine inside `pbs_server`. It uses a simple
first-in-first-out policy: when a job enters the `Queued` state, the scheduler
goroutine picks the first available node with enough free CPUs and dispatches the
job immediately.

**Characteristics:**
- Zero network overhead (in-process function call)
- No sorting, no priority evaluation, no fair-share tracking
- Best suited for single-queue, single-node, or high-throughput batch workloads
- Jobs are dispatched in the order they arrive (submission timestamp)

**When to use:**
- Simple clusters with homogeneous nodes
- Workloads where all jobs have similar resource needs
- Maximum scheduling throughput is required (thousands of short jobs)
- No need for multi-queue priority or fair-share

### 1.2 External Scheduler (`scheduler_mode: external`)

The external scheduler runs as a separate process (`pbs_sched`). Every
`scheduler_interval` seconds it:

1. Connects to `pbs_server` via the DIS protocol with HMAC-SHA256 token auth
2. Queries all queues (`StatusQueue`), nodes (`StatusNode`), and queued jobs (`StatusJob`)
3. Applies the configured algorithms to order and filter jobs
4. Sends `RunJob(jobid, destination_node)` for each dispatchable job
5. Disconnects

**Characteristics:**
- Full algorithm suite (sorting, fair-share, starvation prevention, load balancing)
- Per-queue or round-robin iteration
- Configurable via `$PBS_HOME/sched_priv/sched_config`
- Slightly higher latency than built-in (network round-trip per cycle)

**When to use:**
- Multi-queue environments with different priorities
- Heterogeneous clusters where node selection matters
- Workloads requiring fair-share or starvation prevention
- Production clusters needing advanced scheduling policies

---

## 2. Job Iteration Strategies

The iteration strategy controls the order in which the scheduler considers jobs
across multiple queues. Exactly one strategy is active at a time.

### 2.1 Flat Iteration (default)

```
by_queue: false   ALL
round_robin: false ALL
```

All queued jobs from all queues are merged into a single list, then sorted by
the configured `sort_by` criterion. Queue boundaries are ignored.

**Behavior:**
- One global sorted list
- A low-priority queue's short job may run before a high-priority queue's long job
  (depending on sort criteria)

**Best for:** Single-queue clusters or when queue priority is irrelevant.

### 2.2 By-Queue Iteration

```
by_queue: true    ALL
```

Queues are processed in priority order (highest queue priority first). Within
each queue, jobs are sorted by the configured `sort_by` criterion. All runnable
jobs in queue #1 are dispatched before any jobs in queue #2 are considered.

**Behavior:**
- `sort_queues` is implicit: queues are sorted by their `Priority` attribute
- Queues with higher `Priority` values are processed first
- Within each queue, jobs are sorted and dispatched in `sort_by` order
- If `strict_fifo` is true, a blocked job stops processing for that queue

**Best for:** Multi-queue setups (e.g., `express`, `normal`, `low`) where queue
priority should determine overall scheduling order.

### 2.3 Round-Robin Iteration

```
round_robin: true  ALL
```

The scheduler alternates between queues, dispatching one job per queue per pass.
This prevents a single queue from monopolizing all resources.

**Behavior:**
- Pass 1: dispatch 1 job from queue A, 1 from queue B, 1 from queue C
- Pass 2: dispatch 1 more from A, 1 more from B, 1 more from C
- Continues until no more jobs can be dispatched
- Within each queue, jobs are in `sort_by` order

**Best for:** Environments where multiple teams share a cluster and each team
has its own queue. Ensures fairness at the queue level.

---

## 3. Job Sorting Algorithms

The `sort_by` option determines the order of jobs within each queue (or globally
in flat mode). The sort is stable: jobs with equal sort keys maintain their
original submission order (FIFO).

### 3.1 FIFO (`sort_by: fifo`)

Jobs are ordered by submission time (earliest first). This is the simplest
policy and is the default for most clusters.

```
Queue:  [Job1 t=10:00] [Job2 t=10:01] [Job3 t=10:02]
Order:   Job1 → Job2 → Job3
```

### 3.2 Shortest Job First (`sort_by: shortest_job_first`)

Jobs are sorted by their requested walltime in ascending order. Jobs that
request less wall-clock time are scheduled first.

```
Queue:  [JobA wall=4:00:00] [JobB wall=1:00:00] [JobC wall=2:00:00]
Order:   JobB (1h) → JobC (2h) → JobA (4h)
```

**Rationale:** Minimizes average turnaround time. Small jobs complete quickly,
reducing queue depth. Also known as SJF (Shortest Job First) in scheduling
theory.

**Risk:** Large jobs may starve if short jobs keep arriving. Enable
`help_starving_jobs` to mitigate.

### 3.3 Longest Job First (`sort_by: longest_job_first`)

Opposite of shortest-job-first. Jobs with the longest requested walltime run
first.

```
Queue:  [JobA wall=4:00:00] [JobB wall=1:00:00] [JobC wall=2:00:00]
Order:   JobA (4h) → JobC (2h) → JobB (1h)
```

**Rationale:** Ensures large jobs are not starved by a stream of small jobs.
Useful when large computational jobs are the primary workload.

### 3.4 High Priority First (`sort_by: high_priority_first`)

Jobs are sorted by their `Priority` attribute in descending order (highest
priority value first).

```
Queue:  [JobA pri=10] [JobB pri=50] [JobC pri=30]
Order:   JobB (50) → JobC (30) → JobA (10)
```

**Rationale:** Allows administrators or users to explicitly control job ordering
via the `qsub -p <priority>` flag or `qalter -p <priority>`.

### 3.5 Low Priority First (`sort_by: low_priority_first`)

Jobs are sorted by their `Priority` attribute in ascending order (lowest
priority value first).

**Rationale:** Uncommon, but useful for background/scavenger workloads where
the lowest-priority work should run first within a dedicated queue.

### 3.6 Smallest Memory First (`sort_by: smallest_memory_first`)

Jobs are sorted by their requested memory (`Resource_List.mem`) in ascending
order.

**Rationale:** Fills nodes with small-memory jobs first, potentially fitting
more jobs onto available nodes before needing to schedule large-memory jobs.

### 3.7 Largest Memory First (`sort_by: largest_memory_first`)

Jobs are sorted by their requested memory in descending order.

**Rationale:** Ensures memory-intensive jobs get scheduled while nodes are still
free, before small-memory jobs fragment the available memory.

### 3.8 Fair-Share Sort (`sort_by: fair_share`)

When `fair_share: true`, the sort order is determined by each user's historical
resource usage relative to their share allocation. Users who have consumed less
than their fair share are prioritized.

See Section 5 (Fair-Share Scheduling) for details.

---

## 4. Strict FIFO Mode

```
strict_fifo: true  ALL
```

When enabled, the scheduler stops processing a queue as soon as it encounters a
job that cannot run (due to insufficient resources, holds, etc.).

**Without strict_fifo:** If Job1 needs 8 CPUs and only 4 are available, the
scheduler skips Job1 and checks Job2 (which may only need 2 CPUs). This is
called "backfilling."

**With strict_fifo:** If Job1 cannot run, no more jobs from that queue are
considered. This guarantees strict ordering at the cost of potential idle
resources.

```
Example (strict_fifo=true, 4 CPUs free):
  Job1: needs 8 CPUs  → cannot run → STOP processing this queue
  Job2: needs 2 CPUs  → not considered (even though it could run)

Example (strict_fifo=false, 4 CPUs free):
  Job1: needs 8 CPUs  → skip
  Job2: needs 2 CPUs  → dispatch ✓
  Job3: needs 2 CPUs  → dispatch ✓
```

**Trade-off:** strict_fifo prevents small-job starvation of large jobs but may
leave resources idle.

---

## 5. Fair-Share Scheduling

```
fair_share: true   ALL
half_life: 24:00:00
```

Fair-share scheduling tracks each user's historical resource consumption and
adjusts job priority so that users who have used fewer resources get preference.

### 5.1 Usage Tracking

The scheduler maintains a per-user usage counter. Each time a job runs, the
user's usage is incremented by:

```
usage += requested_CPUs × walltime_seconds
```

### 5.2 Exponential Decay

To prevent historical usage from permanently penalizing a user, usage decays
exponentially with a configurable `half_life`:

```
effective_usage = raw_usage × 2^(-time_since_update / half_life)
```

With `half_life: 24:00:00`, a user's usage counter halves every 24 hours. After
48 hours of inactivity, only 25% of the original usage remains.

### 5.3 Priority Calculation

Jobs are sorted by their owner's effective usage in ascending order. Users with
the lowest effective usage get highest scheduling priority.

```
User Alice: effective_usage = 100 CPU·hours
User Bob:   effective_usage = 500 CPU·hours
User Carol: effective_usage = 50 CPU·hours

Order: Carol's jobs → Alice's jobs → Bob's jobs
```

### 5.4 Share Groups

In a future enhancement, share groups can define target usage percentages for
organizational units. The `unknown_shares` parameter sets the default share
allocation for users not in any group.

---

## 6. Starvation Prevention

```
help_starving_jobs: true  ALL
max_starve: 24:00:00
```

Starvation prevention addresses the scenario where a large job waits
indefinitely because smaller jobs keep consuming all available resources.

### 6.1 Mechanism

A job is considered "starving" if it has been in the `Queued` state for longer
than `max_starve`. When starving jobs exist:

1. Starving jobs are promoted to the front of the sorted job list
2. Among starving jobs, the longest-waiting job goes first
3. Non-starving jobs are still sorted by the normal `sort_by` criterion

### 6.2 Interaction with strict_fifo

If both `strict_fifo` and `help_starving_jobs` are enabled, a starving job that
cannot run will block all subsequent jobs in its queue (strict_fifo takes
precedence). This ensures the starving job gets the next available resources.

### 6.3 Configuration Examples

```
# Aggressive starvation prevention (jobs waiting > 1 hour get priority)
help_starving_jobs: true  ALL
max_starve: 01:00:00

# Relaxed (jobs must wait 48 hours before starvation kicks in)
help_starving_jobs: true  ALL
max_starve: 48:00:00

# Disabled (no starvation prevention)
help_starving_jobs: false ALL
```

---

## 7. Load Balancing

```
load_balancing: true  ALL
```

Controls how the scheduler selects a target node when multiple nodes can run a
job.

### 7.1 Packing (load_balancing: false)

The scheduler fills nodes sequentially. Jobs are assigned to the first node
that has enough free CPUs. This concentrates jobs on fewer nodes.

**Advantages:**
- Leaves some nodes completely idle (good for large jobs arriving later)
- Reduces inter-node communication for tightly coupled workloads
- Easier to power down unused nodes for energy savings

### 7.2 Spreading (load_balancing: true)

The scheduler picks the node with the most free CPUs (lowest load). Jobs are
distributed evenly across all available nodes.

**Advantages:**
- Better for interactive/timesharing workloads
- Avoids overloading any single node
- More predictable per-job performance

```
Example (3 nodes, each with 8 CPUs):

Packing (load_balancing: false):
  Node1: 7 jobs  Node2: 1 job   Node3: 0 jobs

Spreading (load_balancing: true):
  Node1: 3 jobs  Node2: 3 jobs  Node3: 2 jobs
```

---

## 8. Configuration Reference

All options are set in `$PBS_HOME/sched_priv/sched_config`. The format is:

```
option: value  [prime_option]
```

Where `prime_option` is one of:
- `ALL` — applies to both prime and non-prime time
- `prime` — applies only during prime time (business hours)
- `non_prime` — applies only during non-prime time

### 8.1 Complete Option Table

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `scheduler_mode` | string | `builtin` | `builtin` or `external` |
| `scheduler_interval` | integer | `10` | Seconds between scheduling cycles (external only) |
| `round_robin` | boolean | `false` | Enable round-robin queue iteration |
| `by_queue` | boolean | `true` | Enable by-queue priority iteration |
| `strict_fifo` | boolean | `false` | Stop at first unrunnable job in each queue |
| `sort_by` | string | `shortest_job_first` | Job sorting criterion |
| `fair_share` | boolean | `false` | Enable fair-share usage tracking |
| `half_life` | duration | `24:00:00` | Usage decay half-life (fair-share) |
| `help_starving_jobs` | boolean | `true` | Promote long-waiting jobs |
| `max_starve` | duration | `24:00:00` | Time before a job is starving |
| `load_balancing` | boolean | `false` | Spread jobs across nodes evenly |

### 8.2 sort_by Values

| Value | Description |
|-------|-------------|
| `fifo` | Submission order (earliest first) |
| `shortest_job_first` | Ascending walltime |
| `longest_job_first` | Descending walltime |
| `high_priority_first` | Descending job priority |
| `low_priority_first` | Ascending job priority |
| `smallest_memory_first` | Ascending memory request |
| `largest_memory_first` | Descending memory request |
| `fair_share` | Ascending user usage (requires `fair_share: true`) |

### 8.3 Example Configurations

#### High-Throughput Batch (simple FIFO)
```
scheduler_mode: builtin
```
No external scheduler needed. Server's built-in FIFO handles everything.

#### Multi-Queue Priority Cluster
```
scheduler_mode: external
scheduler_interval: 10
by_queue: true          ALL
strict_fifo: false      ALL
sort_by: high_priority_first ALL
help_starving_jobs: true ALL
max_starve: 12:00:00
load_balancing: true    ALL
```

#### Research Lab with Fair-Share
```
scheduler_mode: external
scheduler_interval: 15
by_queue: false         ALL
fair_share: true        ALL
sort_by: fair_share     ALL
half_life: 48:00:00
help_starving_jobs: true ALL
max_starve: 24:00:00
load_balancing: true    ALL
```

#### Mixed Workload with Round-Robin Queues
```
scheduler_mode: external
scheduler_interval: 10
round_robin: true       ALL
strict_fifo: false      ALL
sort_by: shortest_job_first ALL
help_starving_jobs: true ALL
max_starve: 06:00:00
load_balancing: false   ALL
```

---

## 9. Algorithm Interaction Summary

The scheduling algorithms compose in a defined order:

```
1. Select iteration strategy (flat / by_queue / round_robin)
2. For each queue (or globally):
   a. Collect queued jobs
   b. Apply sort_by criterion
   c. If help_starving_jobs: promote starving jobs to front
   d. For each job in sorted order:
      i.   Find a node with sufficient resources
      ii.  If load_balancing: pick least-loaded node
      iii. If no node available and strict_fifo: stop this queue
      iv.  If node found: dispatch job via RunJob
3. Report cycle results
```

### Priority of Options

| Condition | Winner |
|-----------|--------|
| `by_queue` and `round_robin` both true | `round_robin` takes precedence |
| `fair_share` and explicit `sort_by` | `fair_share` overrides `sort_by` |
| `strict_fifo` and `help_starving_jobs` | Both active: starving jobs go first, then strict ordering resumes |

---

## 10. Monitoring and Troubleshooting

### Scheduler Logs

The external scheduler logs to stderr (redirect to file as needed):

```bash
pbs_sched -D > /var/log/pbs_sched.log 2>&1
```

Key log messages:
- `Dispatched <jobid> to <node>` — job successfully sent to node
- `Failed to dispatch <jobid>` — RunJob request failed
- `Cannot connect to server` — server unreachable
- `Cycle complete: dispatched N job(s)` — summary per cycle

### Verifying Configuration

```bash
# Check which mode is active
grep scheduler_mode /var/spool/torque/sched_priv/sched_config

# Server log confirms mode on startup
grep "scheduler mode" /var/log/pbs_server.log
```

### Switching Modes

To switch from external to built-in:
1. Edit `sched_priv/sched_config`: set `scheduler_mode: builtin`
2. Restart `pbs_server`
3. Stop `pbs_sched` process (no longer needed)

To switch from built-in to external:
1. Edit `sched_priv/sched_config`: set `scheduler_mode: external`
2. Restart `pbs_server`
3. Start `pbs_sched`
