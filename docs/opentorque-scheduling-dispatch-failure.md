# OpenTorque — Job Scheduling, Dispatch, and Failure Handling

This document describes how OpenTorque schedules queued jobs, dispatches them to
compute nodes, how the server hands a job off to a MOM, and — critically — what
happens when things fail in the gap between a job being Queued (Q) and Running (R).
It also documents how (and when) the scheduler is triggered, since that determines
end-to-end scheduling latency.

The content is grounded in the actual Go implementation (not the reference C
TORQUE architecture). Where behavior differs from the classic TORQUE design, the
divergence is called out explicitly, because several hand-written docs in this
repo describe the reference C design rather than this codebase.

---

## 1. Job states and the Q → R transition

Relevant job states (see `internal/job`) are:

| State | Meaning |
|-------|---------|
| `Queued` (Q) | Accepted, waiting for a compute node. |
| `Held` (H) | Held; not eligible for scheduling until released. |
| `Waiting` (W) | Deferred execution (`-a` start time not yet reached). |
| `Running` (R) | Dispatched to a node; the MOM has (or is about to) start it. |
| `Complete` (C) | Finished; MOM reported `JobObit`. |
| `Exiting` (E) | Cleanup in progress. |

The interesting window is **Q → R**: the server must pick a node, reserve it,
transition the job, and hand the job to the node's MOM. If the MOM hand-off
fails, the server must undo everything and return the job to Q.

---

## 2. How a job is scheduled and dispatched

There are two topological paths depending on the scheduler mode
(`scheduler_mode` in `sched_priv/sched_config`): `builtin` or `external`.
Only one runs at a time.

### 2.1 Built-in scheduler (inside `pbs_server`)

When `scheduler_mode: builtin`, `pbs_server` runs `schedulerLoop()`
(`internal/server/server.go`). On every tick it calls `runScheduler()`, which:

1. Calls `promoteWaitingJobs()` — promotes any `Waiting` job whose `-a` time has
   passed to `Queued`.
2. Collects `jobMgr.QueuedJobs()`, sorts them by submit time (FIFO).
3. For each job still in `Queued`, calls `scheduleJob(j)`.

`scheduleJob(j)` (`server.go:2879`) is the built-in placement routine. It:

1. Re-checks that the job is `Queued`.
2. Checks dependencies (`checkDependencies`).
3. Checks run limits (`enforceRunLimits`).
4. Asks `nodeMgr.FindNodeForJob(neededSlots)` for a free node. If none, it
   returns silently — the job simply stays `Queued` and is retried on the next
   tick (there is no wake-up; see §4).
5. On a free node it calls `n.AssignJob(j.ID, slots)` (records job→node),
   sets `j.ExecHost = "nodename/0"` and `j.ExecPort`, then transitions the job
   to `Running` via `jobMgr.UpdateJobState`.
6. Updates queue state counters, persists the job, writes an accounting "started"
   record.
7. Finally spawns `go s.dispatchJobToMOM(j, n)` — the actual MOM hand-off runs in
   a background goroutine.

### 2.2 External scheduler (`pbs_sched`)

When `scheduler_mode: external`, the built-in loop is disabled
(`[SERVER] External scheduler mode — built-in scheduler disabled`). Instead the
separate `pbs_sched` daemon runs the cycle (`cmd/pbs_sched/main.go` +
`internal/sched/scheduler/scheduler.go`):

1. `RunCycle` calls `queryServer(conn)` which pulls **all** queues+their jobs
   (`StatusQueue`) and **all** nodes (`StatusNode`) — a full snapshot.
2. `initCycle` sorts each queue's jobs per `sort_by` and applies fair-share
   decay / starvation promotion.
3. A `jobIterator` (flat / by-queue / round-robin) yields one eligible job at a
   time.
4. For each job it calls `findNodeForJob()` locally against the snapshot:
   - filters nodes that are `free` or `job-*` (not down/offline/unknown),
   - requires `n.FreeCPUs >= CPUReq`,
   - packs (`load_balancing: false`) or spreads (`true`).
   - If **no** node qualifies, it sets `jinfo.CanNotRun = true` and continues.
     In `strict_fifo` mode it stops the whole cycle at the first blocked job.
5. On a match it calls `conn.RunJob(jobID, "nodename/0")` — a real dispatch
   command to the server, not a recommendation.

### 2.3 `RunJob` on the server (the "destination" path)

`handleRunJob` (`server.go:999`) receives the scheduler's `RunJob` request:

- If the request carries a `dest`, the server:
  1. Validates the job is `Queued`, dependency/limits pass, and the node exists.
  2. `n.AssignJob(j.ID, 1)` — records job→node.
  3. Sets `ExecHost`, `ExecPort`, and transitions to `Running`.
  4. Updates queue counters, persists, writes accounting "started".
  5. `go s.dispatchJobToMOM(j, n)`.
- If the request has **no** destination, it falls back to built-in placement
  (`go s.scheduleJob(j)`).

So the external scheduler's decision is authoritative: the server records the
job-node relationship (same `AssignJob` used by the built-in path) and proceeds
to dispatch.

---

## 3. The server → MOM hand-off and its failure handling

`dispatchJobToMOM` (`server.go:2946`) is the last hop before a job actually runs.
It opens a fresh privileged TCP connection to `momAddr = nodename:momport` and
walks a three-step protocol:

1. `QueueJob` — send job attributes.
2. `JobScript` — send the script body.
3. `Commit` — tell the MOM to start the job; the reply carries the session ID.

### 3.1 Where Q→R fails and how it is undone

The critical detail: **the job is already `Running` before the MOM is contacted.**
`dispatchJobToMOM` runs in a goroutine, and **every failure point** in it calls
`undoJobDispatch(j, n)` (`server.go:3163`):

| Failure point | Result |
|---------------|--------|
| Cannot connect to MOM (`dialPrivileged` fails) | `undoJobDispatch` |
| `QueueJob` send fails / non-zero reply | `undoJobDispatch` |
| `JobScript` send fails / non-zero reply | `undoJobDispatch` |
| `Commit` send fails / non-zero reply | `undoJobDispatch` |

`undoJobDispatch` reverts the transition completely:

1. `j.SetState(Queued, SubstateQueued)` — **R → Q**.
2. Clears `ExecHost` / `ExecPort`.
3. `n.ReleaseJob(jobID, 1)` — frees the node slot the job had reserved.
4. `queue.TransferJobState(Running, Queued)` — fixes the queue's counters.
5. Logs `[SCHED] Reverted dispatch for job <id>`.

After a revert the job is simply back in Q and will be retried on the next
scheduling cycle. There is no "stuck halfway" state: Commit is the final step,
and a non-zero commit reply means the whole thing unwinds.

### 3.2 MOM-side failures surfaced through Commit

On the node, `handleCommit` (`internal/mom/mom/daemon.go:696`) is where the job
actually starts. If any of these fail, the MOM sends back an **error** reply
(`PbsErrSystem`), which the server reads as a non-zero Commit reply and reverts:

- stage-in file transfer fails,
- prologue (`RunProlog`) fails,
- `executor.StartJob` fails (e.g. cannot write the script, create stdout/stderr,
  or `cmd.Start()` fails).

Only after the MOM returns a **successful** Commit reply with a session ID does
the job count as truly running.

### 3.3 The boundary: failures after Commit succeeds

Once the MOM replies OK to Commit, the job is genuinely `Running` and **no longer
reverts**. Failures after this point — the MOM process crashes, the node dies,
or the job process aborts — are *runtime* failures, not Q→R failures. They are
handled differently:

- On normal job completion the MOM sends a `JobObit` to the server, which
  transitions the job to `Complete`, releases the node's slots
  (`releaseNodeResources`), and resolves dependent jobs.
- A crashed MOM / dead node is detected only by the server's **node health
  poll** (`checkNodes`, `nodeCheckLoop`), which marks a node down after ~5
  minutes of silence. See §6 for the gap: jobs on a dead node are **not**
  automatically requeued.

---

## 4. Scheduler triggering: poll-based, not event-driven

**Both** schedulers are poll/timer driven. In the Go codebase there is **no**
event socket, no `SCH_SCHEDULE_*` command codes, and no wake-up when a job
arrives, a job ends, or node state changes. Jobs and node changes are only picked
up on the next timer tick. The default period is **10 seconds** in both modes.

### 4.1 External scheduler (`pbs_sched`)

`cmd/pbs_sched/main.go`:

- Builds `interval = SchedulerInterval seconds` (default 10, minimum 5).
- Loops on `time.NewTicker(interval)`.
- On each tick it opens a **fresh connection**, runs one `RunCycle`, and closes it.

Every cycle therefore sees a full, current snapshot of queues and nodes. The cost
is that a job submitted just after a tick waits up to ~10s before it is even
considered.

### 4.2 Built-in scheduler (`pbs_server`)

`startBackgroundTasks` (`server.go`) sets up a `schedTicker` with
`scheduler_iteration` seconds (default 10, minimum 5) and, **only in builtin
mode**, runs `schedulerLoop()`. Same tick semantics as above.

### 4.3 What is NOT a trigger in this codebase

There is **no** immediate dispatch on:
- job arrival (submission only transitions to Q and persists — it never calls
  `scheduleJob`),
- job completion (`handleJobObit` frees the node but does not trigger a cycle),
- node status change / heartbeat (`handleISMessage` only updates node state),
- an admin `qmgr set server scheduling=True` (no wake-up exists).

The existing `docs/pbs_sched_analysis.md` contains a **"Server → Scheduler
Triggering"** table with `SCH_SCHEDULE_NEW`/`SCH_SCHEDULE_TERM`/`SCH_SCHEDULE_TIME`
etc. **That table describes the reference C TORQUE `run_sched.c` design, not this
Go implementation.** A `git`-wide search finds none of those constants in Go.
Treat that section of `pbs_sched_analysis.md` as documentation of the intended C
architecture, and see §6 for the recommended event-driven enhancement.

Also note: `handleRunJob` with an explicit destination is *de facto* an on-demand
dispatch path for the external scheduler — but the external scheduler still only
invokes it on its own tick.

---

## 5. Node selection / placement summary

| Setting | Behavior |
|---------|----------|
| Default (packing) | Pick the candidate node with the **fewest free CPUs** first (best-fit/pack). |
| `load_balancing: true` | Pick the candidate node with the **lowest load average** (spread). |
| Eligibility | Node state must be `free` or `job-*` (excludes down/offline/unknown); `FreeCPUs >= CPUReq`. |
| `strict_fifo: true` | Stop the entire cycle at the first job that cannot run. |
| `FairShare` | Usage decays by half-life; `sort_by: fair_share` orders by usage. |
| `sort_by` | fifo, shortest/longest_job_first, high/low_priority_first, smallest/largest_memory_first, fair_share. |

---

## 6. Implementation gaps (specific to scheduling & dispatch)

These are the concrete, code-verified gaps found during this analysis. They
are also trackable in `TODO.md` (section 2 / section 4).

### 6.1 No event-driven scheduling (poll-only, ≥5s latency) — [GAP]
Both schedulers only react to a fixed timer (default 10s, floor 5s). There is no
server→scheduler notification when a job is submitted, completes, or a node
changes state, so end-to-end latency is at least one full tick and frequently
~10s. Recommended: add an in-process event channel (builtin) and an
event/trigger socket (external) mirroring `SCH_SCHEDULE_NEW` / `SCH_SCHEDULE_TERM`
from the reference design, plus an on-submit and on-complete dispatch attempt.

### 6.2 Node-down does NOT auto-requeue its running jobs — [GAP/BUG → DONE]
Originally `nodeMgr.MarkNodeDown` only flipped the node to `StateDown` and
incremented a fail count. It did **not** requeue or fail the jobs that were
running on that node: if a MOM crashed or a node died, jobs on it stayed in `R`
forever (orphaned) until an operator ran `qrerun`, and the scheduler simply
stopped placing new work on that node. `checkNodes` only marked it down after
~5 minutes of silence regardless of current state.

**Resolved (TODO 2.10):** `checkNodes` now calls `Server.requeueNodeJobs(name)`
after marking a node down. That method requeues every job that was
`StateRunning` on the node — freeing its CPU slots
(`n.ReleaseJob(j.ID, jobRequestedCPUs(j))`), clearing `ExecHost`/`ExecPort`,
setting the job back to `StateQueued` (with `TransferJobState(R → Q)` on its
queue), persisting it, and triggering a scheduling pass so it can be
re-dispatched onto a healthy node. Honors `disable_automatic_requeue`
(`automatic_requeue_exit_code` is not consulted — that knob applies to a job's
exit code at completion, which a dead node has no exit code for).

### 6.3 Existing docs describe the C reference, not this Go build — [DOC]
`docs/pbs_sched_analysis.md` (§"Server → Scheduler Triggering") documents the
event-driven `SCH_SCHEDULE_*` design that is **not implemented in Go**. This
document is the corrected, code-grounded reference for the Go build.

### 6.4 `dispatchJobToMOM` has no retry/requeue escalation — [GAP]
`undoJobDispatch` cleanly reverts, but the job is merely returned to Q to wait
for the next tick. There is no bounded retry, no notification to the external
scheduler that a dispatch failed (so the scheduler's local `node.Jobs`/`FreeCPUs`
snapshot can drift from server truth), and no exponential backoff. The
scheduler's next full snapshot reconciles the drift, but a persistent MOM outage
causes repeated dispatch attempts every cycle with no backoff.

### 6.5 Weak node CPU accounting (see also TODO 2.6) — [GAP]
Node selection uses a rolling `FreeCPUs = NumProcs - len(node.Jobs)` (one count
per job) rather than an aggregate of each job's `-l ncpus`. Jobs with large
`-l ncpus` requests still dispatch in parallel, under-pack/inter-leave badly, and
`findNodeForJob`'s CPU check is approximate.

---

## 7. Event-driven scheduling with SLURM-style control knobs (design)

This section replaces the earlier sketch with a concrete design informed by
Slurm's `SchedulerParameters` controls (see `slurm.conf(5)`). The goal is to cut
scheduling latency from a fixed ~10s tick toward near-zero while keeping the
existing periodic tick as a **safety-net floor** so nothing is missed even when
an event notification is dropped.

### 7.1 Reference: Slurm `SchedulerParameters` controls

Slurm exposes its scheduler-tuning knobs through `SchedulerParameters` in
`slurm.conf`, visible via `scontrol show config`. The most relevant ones for
OpenTorque are:

| Slurm option | What it controls | Slurm default |
|--------------|------------------|---------------|
| `sched_interval` | How often (s) the **main** (full, deep) scheduling loop runs over all pending jobs. `-1` disables it. | 60 s |
| `sched_min_interval` | Minimum time between cycles when **event-driven** (limited) scheduling runs. It runs "every time any event happens which could enable a job to start (e.g. job submit, job terminate)". 0 disables the throttle. | 2 µs |
| `default_queue_depth` | Max number of jobs attempted per **event-driven** (deferred) cycle. | 100 |
| `partition_job_depth` | Same queue-depth limit but enforced per partition/queue. | 0 (no limit) |
| `sched_max_job_start` | Max number of jobs started per **single** scheduling execution. | 0 (no limit) |
| `max_sched_time` | Time cap (s) for one scheduling loop run before yielding to other RPCs. | 2 s |
| `defer` / `defer_batch` | Don't try to schedule each job **immediately at submit time**; hold it so many jobs can be scheduled together (batching). `defer_batch` applies only to batch jobs. | off |
| `build_queue_timeout` | Max time to spend building the candidate job queue each cycle. | 2,000,000 µs |
| `bf_window` / `bf_yield_*` | Backfill scheduler look-ahead window and lock-yield tuning. | 1440 min |

Key insight from Slurm: there are **two scheduling densities** —
an event-triggered "limited" scheduler (fires on submit/terminate, bounded by
`sched_min_interval` and `default_queue_depth`) and a less frequent "main" full
scheduler (`sched_interval`) that sweeps everything. OpenTorque should adopt the
same two-tier split.

### 7.2 Proposed OpenTorque scheduler configuration knobs

Add a new `[sched]` block in `sched_config` (or extend the existing one) with
SLURM-compatible semantics. Proposed keys and defaults:

| Key | Meaning | Default |
|-----|---------|---------|
| `scheduler_mode` | `builtin` or `external` (unchanged) | `external` |
| `sched_interval` | Full "main" sweep period (s) — the safety-net floor. `0` disables periodic sweep (not recommended). | 10 s |
| `sched_min_interval` | Minimum gap (ms) between event-triggered cycles, to prevent a scheduling storm on bursts. | 100 ms |
| `default_queue_depth` | Max jobs attempted per event-triggered "limited" cycle. | 100 |
| `sched_max_job_start` | Max jobs actually started per single execution. `0` = no limit. | 0 |
| `max_sched_time` | Time cap (s) for one scheduling run before yielding. | 2 s |
| `defer` | Batch mode: don't dispatch immediately at submit; let more jobs accumulate before a cycle runs. | off |
| `event_driven` | Master switch to enable event notifications + trigger socket. Off preserves today's pure-poll behavior. | on (new default) |

Compatibility: keep reading the legacy `SchedulerInterval` (10 s) as the value of
`sched_interval` when `event_driven` is off, so existing configs keep working.

### 7.3 Hybrid trigger model (event-driven + safety-net floor)

The core change: the scheduler no longer waits for the ticker alone. It waits on
**both** an event channel and the periodic ticker, and runs a cycle when either
fires. This is exactly your requirement: if no event ever fires, the 10 s
watchdog still runs a cycle so nothing is missed.

```
for {  // scheduler main loop (both builtin & external)
    select {
    case e := <-schedEvents:      // job submitted/R, job obit/complete, node state change
        runLimitedCycle(e)        // event-triggered: bounded by sched_min_interval + default_queue_depth
    case <-safetyTicker.C:        // sched_interval (default 10 s) — the floor / full sweep
        runFullCycle()            // deep sweep over all pending jobs
    case <-s.done:
        return
    }
}
```

Design rules:

1. **Event types that trigger a limited cycle** (mirror Slurm "any event which
   could enable a job to start"):
   - job submitted and now `Queued` (`handleQueueJob`/`handleJobScript`),
   - a running job finished / `JobObit` received (frees a node),
   - a job was requeued / released from hold,
   - node state changed (free slot freed, node came online, `handleISMessage`).
   Together these reproduce the reference C `SCH_SCHEDULE_NEW` / `SCH_SCHEDULE_TERM`
   / `SCH_SCHEDULE_RECYC` semantics.
2. **Throttling (`sched_min_interval`)**: coalesce a burst of events (e.g. a
   large submission) into one cycle, at most once per `sched_min_interval`. This
   is the anti-storm control.
3. **Queue-depth (`default_queue_depth`)**: a limited cycle only attempts the
   first N jobs, so a burst doesn't starve the RPC thread; the full sweep
   (`sched_interval`) is not limited and catches everything left behind.
4. **Safety-net floor (`sched_interval`)**: the periodic ticker always remains.
   If events are lost or none ever fire, the 10 s watchdog still runs a full
   cycle — your requested "保底/fallback".
5. **`defer` (batching)**: when set, a limited cycle is *scheduled but not
   executed immediately* — it waits up to `sched_min_interval` to accumulate
   more arrivals before running, trading per-job latency for throughput on huge
   batches (Slurm `defer`/`defer_batch`).

### 7.4 Builtin vs external wiring

- **Builtin** (`schedulerLoop`): replace the `time.NewTicker`-only select with
  the hybrid select above, using an in-process event `chan`. Signal it from
  `handleQueueJob`, `handleJobScript`, `handleJobObit`, `handleRerunJob`,
  `qrls` (release hold), and `handleISMessage`/node-state transitions.
- **External** (`pbs_sched`): keep the ticker, and add a trigger socket from
  the server (mirroring `SCH_SCHEDULE_NEW` / `SCH_SCHEDULE_TERM`).
  **Implemented & tested 2026-08-02**: the server's `notifyExternalSched()`
  dials `127.0.0.1:<sched_trigger_port>` (default **25003**) and writes one
  byte on submit/complete/requeue/node-change while
  `scheduler_mode: external && event_driven: true`. `pbs_sched` listens on that
  loopback port (`acceptTriggers` goroutine feeding an unbuffered `eventCh`),
  coalesces bursts with a `sched_min_interval` anti-storm gate, and runs a
  **limited cycle** (`RunCycleLimited` — bounded by `default_queue_depth` and
  `sched_max_job_start`, respects `max_sched_time`) on the event; the ticker
  still runs a **full cycle** as the safety-net floor. If the trigger socket is
  down or `event_driven=false`, the ticker keeps the system correct.
  Live on Azure RG `xxin-opentorque-test`: single job ~15-16 ms to `R`, burst of
  4 ~168 ms, polling floor ~1.6 s on a 3 s ticker.

### 7.5 What the run-time cycle looks like

Both the limited and full cycles share the existing `RunCycle` body but differ in
inputs:

- **Limited cycle** → `queryServer` + attempt only `default_queue_depth` jobs,
  apply `sched_max_job_start` cap, respect `max_sched_time`.
- **Full cycle** → the current behavior (sweep all pending jobs), run at
  `sched_interval`.

The same `undoJobDispatch` failure handling (see §3) applies unchanged to both.

### 7.6 Rollout / compat notes

- Add the new keys to `internal/sched/config/config.go` and the server's
  `loadSchedConfig` surface (server needs `sched_interval`, `event_driven`,
  `sched_min_interval`, `default_queue_depth` so the builtin loop matches).
- Expose the effective knobs in `scontrol show config`-style output
  (e.g. extend `qstat -B` or add a `--sched-config` view) so operators can verify
  they took effect — this parallels "看到有几个参数是可以控制调度器行为的".
- Keep `SchedulerInterval` as an alias for `sched_interval` to avoid breaking
  existing deployments; mark it deprecated in the config reference.


## 8. Quick answers

- **Is scheduling fixed 10s, or also event-triggered?** Today (Go code,
  2026-08-02) both the built-in `schedulerLoop` and the external `pbs_sched`
  use the hybrid model from §7: **event-driven limited cycles pushed by a
  server->scheduler trigger** (in-process chan for builtin; a loopback TCP
  trigger socket `sched_trigger_port` for external) plus the periodic
  `scheduler_interval` ticker (default 10s, floor 5s) as a safety-net floor.
  Verified live on Azure RG `xxin-opentorque-test`.
- **Is the job-node relationship recorded?** Yes — `node.AssignJob(jobID, slots)`
  appends to `Node.AssignedJobs` and deducts free slots, and the job gets
  `ExecHost`/`ExecPort`.
- **If no node fits, does the job stay queued?** Yes. In the builtin path
  `scheduleJob` returns early and leaves the job Q; in the external path the job
  is marked `CanNotRun` for that cycle (and, under `strict_fifo`, the cycle stops),
  and it is retried next tick.
- **If server or MOM fails mid-dispatch, what happens?** If the failure happens
  before the MOM's successful Commit, `undoJobDispatch` reverts R→Q and frees the
  node slot. If the failure happens after a successful Commit (crash/death), the
  job is an orphan in R until an operator intervenes — see gap 6.2.
- **Which SLURM knobs inspired the design?** `sched_interval`, `sched_min_interval`,
  `default_queue_depth`/`partition_job_depth`, `sched_max_job_start`,
  `max_sched_time`, and `defer`/`defer_batch` (§7.1–7.2).