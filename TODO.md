# OpenTorque — Missing Features / TODO

This file catalogs capabilities that standard PBS/TORQUE (and mainstream HPC
schedulers such as Slurm) provide but that OpenTorque does **not** implement
yet. It is the result of code analysis plus live testing on a real deployment
(Azure single-node `xxin-opentorque-vm`, RG `xxin-opentorque-test`, scheduler
mode `external`). Item descriptions include the expected behavior and, where
helpful, the code area that would need to change.

Legend:
- **[BUG]** — existing behavior is incorrect / harmful
- **[GAP]** — functionality is missing entirely
- **[STUB]** — data model or config exists but is never wired up

---

## 1. Node selection & node groups

### 1.1 `-l host=<node>` — pin a job to a specific node  [DONE — implemented & tested]
Implemented in `internal/sched/scheduler/scheduler.go`: `parseJobInfo` reads
`Resource_List.host` into a new `JobInfo.Host`, and `findNodeForJob` filters
candidates by `strings.EqualFold(n.Name, jinfo.Host)`.
- Live test (2026-08-03, external scheduler): `qsub -l host=xxin-opentorque-srv
  ncpus=1` dispatched to `xxin-opentorque-srv/0` and reached state `R`;
  `qsub -l host=xxin-opentorque-nonexistent ncpus=1` stayed `Q` (no such
  node, never dispatched). A `-l host=xxin-opentorque-w1` job was dispatched
  only to `w1` per the sched log (w1's MOM could not exec it due to a separate
  workdir/shell environment issue, not a scheduling problem).

### 1.2 `-l feature=<prop>` / node `properties` — schedule by node property  [DONE — implemented & tested]
Implemented in `internal/sched/scheduler/scheduler.go`: `parseNodeInfo` now
populates `NodeInfo.Properties` from the node `properties`/`features` status
attrs, `parseJobInfo` reads `-l feature/features/properties` into
`JobInfo.Features`, and `findNodeForJob` requires `nodeHasAllFeatures`
(case-insensitive subset match). Properties are settable via
`qmgr set node <name> properties=a,b` and shown by `pbsnodes`.
- Live test (2026-08-03): with `properties=gpu` on `xxin-opentorque-srv`,
  `qsub -l feature=gpu ncpus=1` dispatched to `xxin-opentorque-srv/0` and
  reached `R`; `qsub -l feature=missing ncpus=1` stayed `Q` (no node has it).

### 1.3 Host groups / host tags / node pools  [DONE — implemented & tested]
Nodes now carry a named host-group / node-pool membership (`node.Node.Groups`,
stat attr `hostgroups`, persisted in `server_priv/nodes` as `groups=`). Nodes
are assigned to groups via `qmgr set node <name> hostgroups=gpu,fast`. A job
pins to a group with `-l host=@<group>`; both the built-in scheduler
(`Server.selectNodeForJob`, `internal/server/server.go`) and the external
scheduler (`findNodeForJob`, `internal/sched/scheduler/scheduler.go`) restrict
candidates to nodes in the requested group. Static (local) nodes remain
preferred over dynamic (cloud) nodes within a group.
- Unit tests: `TestFindNodeForJobHostGroup` (external) and node `HasGroup`.
- Caveat: PBS hostgroups are typically declared server-side and shared across
  nodes; here membership is stored on each node (equivalent for scheduling).

### 1.4 `nodes=N:ppn=M` / `select=` / `place=` true multi-node placement  [DONE - implemented & tested]
Real multi-node allocation is implemented in both scheduling paths.
- Parse `-l nodes=N:ppn=M` (and `select=N:ppn=M`) into a node count + ppn
  (`parseNodeSelectSpec` in both `internal/server` and
  `internal/sched/scheduler`); `nodes=` is no longer collapsed into a single
  `CPUReq`.
- Built-in scheduler: `scheduleJobMulti` allocates N distinct schedulable nodes
  (each with >= ppn free, honoring host/group/feature), records a `+`-joined
  multi-node `exec_host`, counts per-node CPU, and dispatches the job to every
  allocated node MOM. `node.Node.AssignJob` accounts ppn on each node.
- External scheduler: `findNodeForJob` gates on `countSchedulableForJob` (N
  distinct nodes each with >= ppn free); dispatch anchors on the first node and
  `Server.runJobMulti` completes the set and dispatches to all nodes.
- MOM already emits `PBS_NODEFILE`/`PBS_NODELIST` from the multi-node
  `exec_host` (each node listed once; touching every node).
- Unit tests: `TestParseNodeSelectSpec`, `TestFindNodeForJobMultiNode`.
- Caveats: `place=` (pack/scatter) is parsed for single-chunk layouts but only
  pack (fewest nodes) is honored; heterogeneous `N:ppn=M+R:ppn=S` reduces to the
  first chunk; true mother/superior task fan-out via `pbsdsh` remains a
  follow-up (each node runs the full script - sleep/independent jobs work).

### 1.5 Queue node-affinity (queue `naccesspolicy`)  [DONE - implemented & tested]
A queue can now set `naccesspolicy`: `shared` (default) packs multiple jobs
per node, while `exclusive` / `singleuser` allow only one job per node (a
node that already runs a job is not a candidate). Parsed, displayed, and
persisted on the queue, and enforced by both schedulers (`queueNodeOK` in
`internal/sched/scheduler` and `internal/server`).
- Test: `TestQueueNodeOKExclusive` (external scheduler).

### 1.6 Queue node-pool (`hostlist`) vs. submission-host ACL (`acl_hosts`)  [DONE — implemented & tested]
Both roles are now wired and kept distinct:
- **`hostlist`** (queue attr) restricts which compute nodes a queue may schedule
  onto; values are node names or `@`-prefixed host groups (1.3). Enforced at
  node selection in both schedulers (`queueNodeOK` in `internal/sched/scheduler`
  and `internal/server`).
- **`acl_hosts`** (with `acl_host_enable`) restricts which submission/client
  hosts may submit *into* the queue. Enforced at submit in
  `Server.queueAllowsSubmitHost` using `PBS_O_HOST` or the host portion of
  `Job_Owner`.
- Tests: `TestQueueNodeOKHostList` (scheduler), `TestQueueAllowsSubmitHost`
  (server).
------

## 2. Scheduler & resource management

### 2.1 GPU / accelerator, license, and generic resource constraints  [DONE — implemented & live-tested]
Nodes now carry arbitrary named-resource capacities (`resources_available.<name>`
set via `qmgr set node <name> resources_available.<name>=N`, persisted as
`gres.<name>=N` and reloaded on restart). Jobs request them with `qsub -l <name>=N`
(any non-built-in `Resource_List` integer). Both the built-in scheduler
(`Server.nodeHasGRes` / `selectNodeForJob`) and the external scheduler
(`Scheduler.nodeHasGRes` / `findNodeForJob`) only place a job on a node whose
remaining capacity (`cap − used`) covers the request; `pbsnodes` reports
`resources_available.<name>` and `gres_used.<name>` and the accounting is released
on job completion. Interoperates with `-l host=@group` host-group pinning and
CPU/feature gates; a fittable job behind an unsatisfiable gres head job still runs
under the 2.2 backfill knob.

- Live test (2026-08-10, external scheduler, Azure westus3): see
  `docs/live-azure-verification-report.md` §2.1.

### 2.2 Backfill / reservation / preemption  [DONE — backfill implemented & tested; reservations/preemption open]
**Backfill is implemented** in both schedulers. A blocked head-of-line job no
longer holds back later jobs that fit the current free capacity:
- External `pbs_sched`: new `backfill` sched-config knob (default on). With
  backfill on, even `strict_fifo` continues to later fittable jobs instead of
  stopping at the first unrunnable job (`Scheduler.strictStop`); with backfill
  off, strict FIFO halts as before. Test `TestStrictStop`.
- Built-in scheduler: `runScheduler` already iterates all queued jobs and
  dispatches any that fit, so it backfills naturally (FIFO order).
- **Still open (large follow-ups):** advance job reservations with a future
  start time, and preemption (suspend/requeue lower-priority running jobs).

### 2.3 [BUG] `qsub` multiple `-l` flags overwrite each other  [DONE — fixed & live-tested]
`cmd/qsub/main.go` now uses a repeatable flag value (`concatValue`) registered
with `flag.Var`, so every `-l` occurrence is accumulated comma-joined instead of
overwriting the previous one — matching TORQUE's merge behavior.
- Live test (2026-08-03, Azure RG `xxin-opentorque-test`): `echo 'sleep 2' |
  qsub -q testq -l ncpus=1 -l walltime=01:00:00` produced a job whose
  `qstat -f` showed **both** `Resource_List.ncpus = 1` and
  `Resource_List.walltime = 01:00:00` (previously only the last `-l` survived).

### 2.4 [BUG] `qstat -f <job-id>` returns `server error 15001` for short IDs  [DONE — fixed & live-tested]
Root cause: `jobMgr.GetJob(id)` matched only the exact full ID and there was no
short-ID (`<jobnum>` -> `<jobnum>.<server>`) resolution. Fixed centrally in
`internal/job/manager.go`: `GetJob` now trims the ID, tries the exact key, and
if there is no `.` in the ID resolves `<jobnum>` against the manager's
`serverName` (also case-insensitive on the suffix) so `qstat`/`qdel`/`qrun` all
benefit.
- Live test (2026-08-03, Azure): `qstat -f 50` (short) returned full job status
  (was 15001); `qdel <shortid>` succeeded; `qrun -H <node> <shortid>` force-ran a
  queued job (was 15001).

### 2.5 [BUG] `qrun <node> <job>` returns 15001  [DONE — fixed & live-tested]
The 15001 came from `handleRunJob` being unable to resolve a **short** job ID via
`jobMgr.GetJob`. Resolved by the 2.4 short-ID fix in `internal/job/manager.go`.
- Live test (2026-08-03, Azure): with `pbsnodes -o xxin-opentorque-srv` to hold a
  job in `Q`, `qrun -H xxin-opentorque-srv <shortid>` returned success (exit 0)
  and the job transitioned `Q -> R` on `srv/0`. The 15001 is gone; a job that is
  already running now correctly returns `15004` (not 15001).

### 2.6 Node CPU-accounting strength  [DONE — fixed & live-tested]
Capacity is now accounted per-job `CPUReq`, not per-job-count, on **both**
scheduling paths:
- **External `pbs_sched`** (`internal/sched/scheduler/scheduler.go`): the server
  now reports a node's `used_cpus` (sum of the requested `ncpus` of every job on
  the node, via `formatNodeStatus`), and `parseNodeInfo` computes
  `FreeCPUs = NumProcs - used_cpus` (falling back to `NumProcs - len(jobs)` only
  when the server did not report `used_cpus`). `findNodeForJob` then places jobs
  only on nodes with enough real free CPUs.
- **Built-in scheduler** (`internal/server/server.go`): `scheduleJob` now calls
  `FindNodeForJob(jobRequestedCPUs(j))` instead of hardcoding 1 slot, and every
  `AssignJob`/`ReleaseJob` in `handleRunJob`, `handleRerunJob`,
  `undoJobDispatch`, and `releaseNodeResources` uses the job's requested CPUs so
  slots are consumed/freed symmetrically.
- Live test (2026-08-03, RG `xxin-opentorque-test`, external scheduler): an
  `-l ncpus=4` job (no node has 4 of the 2-CPU nodes' capacity) stayed **Q**,
  while two `-l ncpus=1` jobs ran **R**; node status showed `used_cpus = 2` on
  the full node. Previously every job ran immediately regardless of `-l ncpus`.

### 2.7 Fair-share accounts / projects / QoS  [GAP]
Only a minimal per-user `fairUsage` map exists. No accounts, projects, or QoS
tiers with per-group fair-share or limits.

### 2.8 `subscription`/multi-tenancy & quotas  [GAP]
No per-user/group quota or account-based enforcement beyond basic
`max_user_jobs`/`max_user_run` (see `enforceSubmitLimits` / `enforceRunLimits`).
### 2.9 Poll-only / no event-driven scheduling trigger  [DONE — implemented & tested]
RESOLVED (avoid regression): both schedulers used to run on a fixed timer only
(default 10s, floor 5s) with **no** server->scheduler notification, so end-to-end
latency was >= one full tick.

Status: **implemented (built-in mode)** in `internal/server/server.go` — see
design `docs/opentorque-scheduling-dispatch-failure.md` §7. Behavior verified
live in RG `xxin-opentorque-test` (RG tx on Azure VM, Linux built-in mode):
- `triggerSched()` is invoked on job submit/release, completion, requeue, and
  node capacity/state change; it wakes the built-in `schedulerLoop` immediately.
- Event-driven "limited" cycles are gated by `event_driven` (default true),
  throttled by `sched_min_interval` (default 100ms), bounded by
  `default_queue_depth` and `sched_max_job_start`, and yield after
  `max_sched_time`.
- The periodic `scheduler_interval` ticker (default 10s) remains as the
  **safety-net floor**: if `event_driven=false` or no event fires, work is still
  swept every tick.

New server tunables (configurable via `sched_priv/sched_config` **and** live via
`qmgr set server ...`, persisted to the server attribute DB, surfaced by
`qmgr p s` / `print server`):
`sched_interval`, `event_driven`, `sched_min_interval`, `default_queue_depth`,
`sched_max_job_start`, `max_sched_time`, `defer`, `defer_batch`.

Measured on Azure (built-in mode, `scheduler_interval=10`, 4-CPU node):
- event_driven=true: single job submitted -> state **R in ~0.02s** (not 10s).
- event_driven=false: same job waited ~8.2s for the next 10s tick (proves the
  floor still guarantees dispatch and that event-driven is what removes latency).
- burst of 4 jobs: all dispatched to R within ~0.6s.
- `sched_max_job_start` limits starts **per cycle**; because each submission can
  trigger its own event cycle, a rapid burst still spreads across cycles (100ms
  min gap). This matches per-cycle semantics; document if a global-instant cap
  is desired.

Precedence note: on startup `loadSchedConfig()` (the `sched_priv/sched_config`
file) **overrides** values restored from the server attribute DB, so the config
file is the source of truth for these knobs; `qmgr set` changes are live but
re-apply the file value after a restart.

**External `pbs_sched` is now event-driven too (2026-08-02, fully tested on
Azure RG `xxin-opentorque-test`).** The server sends a 1-byte TCP marker to a
loopback trigger socket when a scheduling-worthy event occurs
(submit/release/completion/requeue/node-change) while
`scheduler_mode: external` and `event_driven: true`; `pbs_sched` listens on the
trigger port, coalesces bursts with a `sched_min_interval` anti-storm gate, and
runs a **limited cycle** (`RunCycleLimited`: bounded by `default_queue_depth`
and `sched_max_job_start`, respects `max_sched_time`). The periodic
`scheduler_interval` ticker remains the safety-net floor (full cycle).
- Trigger transport: `notifyExternalSched()` dials `127.0.0.1:<sched_trigger_port>`
  (default **25003**), writes 1 byte, non-blocking / ignores errors — mirrors
  PBS `SCH_SCHEDULE_NEW` / `SCH_SCHEDULE_TERM`.
- New config keys: `sched_trigger_port` (server + `pbs_sched`, default 25003),
  plus existing `event_driven`, `sched_min_interval`, `default_queue_depth`,
  `sched_max_job_start`.
- Measured on Azure (`scheduler_interval=3`, external mode, 4-CPU node):
  - single fresh submit, node free -> state **R in ~15-16 ms** (was ~one tick).
  - burst of 4 jobs (np=4) -> all **R in ~168 ms** across 2 limited cycles.
  - `event_driven=false` (polling floor) -> same job took **~1.6 s** via the 3s
    ticker (safety net proven).
  - `pbs_sched` logs `Starting limited (event) cycle` between ticker ticks;
    triggers confirmed arriving on port 25003.
- **Port fix**: the original default `15003` collided with `pbs_mom`
  (mom listens on port 15003); the trigger port default was changed to
  **25003**. After changing any sched config, restart `pbs_server` so
  `loadSchedConfig` re-reads the file, then restart `pbs_sched`.
- Remaining gap: these are strong event triggers + per-cycle limits, but not
  real channel/batch budgeting across concurrent trigger cycles at high submit
  rates; `qstat -B` does not yet surface the effective sched knobs (see §7.6).

### 2.10 Node-down does not auto-requeue its running jobs  [DONE — implemented]
Previously `nodeMgr.MarkNodeDown` only set `StateDown` and bumped `FailCount`; it
did **not** requeue or fail the jobs running on that node. If a MOM crashed or a
node died, its jobs stayed in `R` forever (orphaned) until an operator ran
`qrerun`, and the scheduler just stopped placing new work there. `checkNodes`
marked it down after ~5 min of silence regardless of state. See
`docs/opentorque-scheduling-dispatch-failure.md` §6.2.

Implemented in `internal/server/server.go`:
- `checkNodes` now calls a new `s.requeueNodeJobs(name)` right after marking a
  node down.
- `requeueNodeJobs` requeues every job that was `StateRunning` on the node:
  frees the node's CPU slots (`n.ReleaseJob(j.ID, jobRequestedCPUs(j))`),
  clears `ExecHost`/`ExecPort`, sets the job back to `StateQueued`
  (`TransferJobState(R → Q)` on its queue), `saveJob`s it, and triggers a
  scheduling pass so it can be re-dispatched onto a healthy node.
- Honors `disable_automatic_requeue`: when set, the node-down requeue is skipped
  and the jobs are left in place for manual `qrerun`.
- `automatic_requeue_exit_code` is not consulted here: that knob decides requeue
  based on a job's exit code at *completion*, a node that died is not a
  completed job and has no exit code.

### 2.11 Remove the built-in (in-process FIFO) scheduler  [DONE - removed]
`pbs_server` previously shipped an in-process FIFO scheduler that ran when
`scheduler_mode: builtin`. It duplicated (and could drift from) the external
`pbs_sched` placement logic. Removed entirely:
- `internal/server`: deleted `schedulerLoop`, `runScheduler`, `scheduleJob`,
  `scheduleJobMulti`, `selectNodeForJob`, `selectNodesForJob`, and the server-
  side `nodeHasGRes`/`nodeHasAllFeatures`/`queueNodeOK` (their only callers).
- `pbs_server` now **always** uses the external scheduler (default
  `SchedulerMode: external`); a stale `scheduler_mode: builtin` in
  `sched_priv/sched_config` is ignored with a warning.
- Deferred (`-a`/Waiting) job promotion is now server-side and mode-independent:
  the new `schedulerWatchLoop` ticker calls `promoteWaitingJobs()` so Waiting
  jobs reach Queued under the external scheduler too. `qrun` without a node
  delegates to the external scheduler instead of in-process placement.
- When `pbs_sched` is not running, the server logs a clear, rate-limited
  `WARNING` on startup and in the watch loop.
- Verified: `go vet` + `go test ./internal/...`, and `cmd/pbs_server` /
  `cmd/pbs_sched` build clean. Live test on Azure below.
- **Important default fix:** the server's `SchedTriggerPort` default was
  0 (only wired via `sched_config`), so event-driven external triggering and
  the "scheduler not running" warning were off unless configured. It now
  defaults to **25003** (matching `pbs_sched`'s default), so external
  scheduling + the health warning work with **no config**.
- Live test (2026-08-30, Azure subscription `dfcb03a2` / RG `xxin-opentorque-test` /
  `xxin-opentorque-vm`, Ubuntu 24.04, 2-core, no `sched_config`): the server
  started in external mode and logged
  `WARNING: external scheduler (pbs_sched) is NOT running on 127.0.0.1:25003;
  jobs will NOT be scheduled until it is started`; after starting `pbs_sched` it
  logged `External scheduler (pbs_sched) detected on 127.0.0.1:25003` and the
  warning stopped. A `sched_config` containing stale `scheduler_mode: builtin`
  was ignored with `WARNING: scheduler_mode "builtin" is no longer supported`.
  Jobs were dispatched by `pbs_sched` end-to-end (submit -> R -> C, exit 0),
  incl. a 4-job burst on a 2-core node (2 concurrent, 2 queued). A deferred
  `qsub -a` job sat in `W`, then auto-promoted to Queued (server
  `promoteWaitingJobs`) and was dispatched by the external scheduler -> C exit 0.
  `go vet ./...` + `go test ./...` all green on the VM.

---

## 3. Queue routing & job control

### 3.1 Automatic queue routing (`queue_type=R`)  [DONE - implemented & live-tested]
`TypeRoute` and `RouteDestin` exist in the data model and `qmgr` can create a
`queue_type=Route` queue, but **no forwarding engine exists**: `RouteDestin` is
never read anywhere. A job submitted to a route queue is treated as a normal
execution job and runs directly in that queue (verified live: job ran in
`route_q` with `route_destin=batch`, never forwarded).
- Where: `internal/queue/queue.go` (model), `internal/server/server.go`
  (`handleQueueJob`), `internal/sched/scheduler/scheduler.go` (`parseQueueInfo`
  does not even inspect `queue_type`).
- Expected: on submit to a route queue, choose a destination from
  `RouteDestin` and move/requeue the job there.

> **DONE (2026-08-05):** `internal/server/route.go` implements `routeJob` - a
> job submitted to a `queue_type=Route` queue is forwarded to the first
> passable `route_destinations` entry; the scheduler skips route queues (they
> never run as execution queues). `finalizeRoute` runs in both commit paths.
> Also fixed `cmd/qmgr` `parseAttrs`, which split comma-list values
> (`route_destinations = short_q,long_q`) into bogus separate attrs, dropping
> all but the first destination; it now keeps the comma-list as a single value.
> Live: `route_q2` (`short_q,long_q`) routed a 00:05 job to `short_q` and a
> 01:00 job to `long_q`; both ran to completion.

### 3.2 `qmove` of non-queued jobs  [DONE - implemented & live-tested]
`handleMoveJob` only allowed `Queued` jobs; moving held/waiting jobs was
rejected. Standard `qmove` can move a broader set of non-running states.

> **DONE (2026-08-06):** `handleMoveJob` now moves **Queued, Held, and Waiting**
> jobs (state preserved, only the queue changes), and rejects **Running /
> Exiting / Complete / Provisioning** jobs cleanly with `15004`. A running job
> cannot be moved directly -- it must be rerun first (`qrerun`), matching
> standard PBS/TORQUE semantics. The earlier approach that requeued running
> jobs to the destination queue released node slots out from under the still
> running MOM instance and deadlocked the server (qstat froze); that path is
> removed. Same-queue moves are a benign no-op. Unit tests
> `internal/server/movejob_test.go` (Queued / Held / Running-reject / Unknown
> queue / Unknown job / Same-queue) pass. Live on `xxin-opentorque-srv`: queued
> job moved Q mqa->mqb; held job moved H mqa->mqb; waiting job moved W mqa->mqb;
> and a genuinely running job's `qmove` was rejected (15004) while the server
> stayed responsive (`qstat` OK) -- the previous hang is gone.

### 3.3 Job arrays (`qsub -t`)  [DONE - implemented & live-tested]
`-t 1-3` is accepted but never expanded into sub-jobs; it runs as a single job.

> **DONE (2026-08-05):** `qsub -t` now expands the array spec into per-index
> task sub-jobs (`N[1].srv`, `N[2].srv`, ...). `internal/server/server.go`
> gained `parseArraySpec` (ranges, steps, comma lists), `CloneForArray` in
> `internal/job/job.go`, and `processJobArray`/`commitJobInstance` wired into
> both commit paths (auto-commit + 2-phase). Each task is routed/admitted and
> saved independently; the whole submission rolls back if any task is rejected.
> Live: `qsub -t 2-4` returned `187`, `qstat` showed `187[2]/[3]/[4]`, tasks ran
> and completed, and `qdel <full-id>` removed them.

### 3.4 `momctl` direct attribute query  [DONE - implemented & live-tested]
`momctl -q` was a placeholder; the underlying protocol query was unimplemented.

> **DONE (2026-08-06):** `momctl -q <attr>` now performs a real direct query to a
> MOM daemon over the batch DIS protocol on the MOM service port (15002). Added
> opcode `BatchMomStatus` (63) handled by a new `handleMomStatus` in the MOM,
> which builds the node status via `server.BuildMomStatusAttrs` (shared with the
> periodic IS status path) and returns the requested attribute value (text
> reply) or the full sorted `key=value` dump for `-q all`; unknown attributes
> return `15001`. Added unit tests `internal/mom/server/attrs_test.go`.
> Live on `xxin-opentorque-srv` (new `pbs_mom` binary): `momctl -q ncpus` -> `2`,
> `-q loadave` -> `0.02`, `-q state` -> `free`, `-q all` -> full sorted dump,
> `-q notarealattr` -> clean `(code 15001)` error. Full `go test ./internal/...`
> passes; job submission and cluster health unaffected.

---



### 3.5 Queue attributes: model vs. configured vs. used mismatch  [DONE - reconciled]
The `Queue` data model (`internal/queue/queue.go`), the set of attributes that
`applyQueueAttrs` in `internal/server/server.go` actually honors, and the
attributes the scheduler consumes are **three different sets**:
- **Model fields** include `MaxUserJobs`, `MaxUserRun`, `ACLUserEnabled`,
  `ACLUsers`, `ACLGroupEnabled`, `ACLGroups`, `ACLHostEnabled`, `ACLHosts`,
  and `RouteDestin`, but **none of these are wired up** by `applyQueueAttrs` —
  `qmgr set queue ...` for them falls into the generic `Attrs` map and has no
  effect (e.g. ACLs are stored but never enforced).
- **Actually settable** (handled by `applyQueueAttrs`): `queue_type`,
  `enabled`, `started`, `max_queuable`, `max_running`, `resources_max`,
  `resources_min`, `resources_default`.
- **Actually used** by the scheduler (`parseQueueInfo`): `enabled`, `started`,
  `Priority`, `max_running`, `state_count_running`, `state_count_queued`.
  `queue_type` is never parsed by the scheduler (see 3.1).
- **Displayed** by `formatQueueStatus` / `qstat -Q`: `queue_type`,
  `total_jobs`, `state_count`, `enabled`, `started`, `max_queuable`,
  `max_running`, `resources_max`, `resources_default` (`resources_min` and
  ACL/route attributes are not shown).
- Expected: reconcile these sets — implement ACL enforcement, per-user limits,
  route destinations, and queue priority end-to-end, or document them as
  unsupported and remove the misleading stub fields.

> **DONE (2026-08-05):** model / configured / used sets are now reconciled.
> ACL enforcement, per-user limits, route destinations and resource limits were
> already wired by 3.1/3.7. This pass completed the remaining gap: **queue
> `Priority`** (model field + `applyQueueAttrs` + `formatQueueStatus` + disk
> persistence; the scheduler already consumed it) and **`disallowed_types`**
> (model + parse/display/persist + admission gate enforcement via `jobTypes`:
> batch/interactive/rerunable/job_array). Live: queue `Priority=100` shows and
> persists; `disallowed_types=job_array` rejected a `qsub -t 1-2` (15007) while
> a batch job was accepted.

### 3.6 [BUG] Negative queue/server job counters  [DONE — fixed & live-tested]
Root causes fixed:
- **From live testing** the drift is driven by `handleMoveJob`:
  `TransferJobState` only moves between state slots and never touches
  `TotalJobs`, so moving a job left the old queue's `TotalJobs` permanently too
  high (observed `total_jobs = 13` with zero actual jobs after churn).
  `handleMoveJob` now uses `oq.DecrJobCount(oldState)` +
  `newQ.IncrJobCount(oldState)` which update both `TotalJobs` and the state slot.
- `queue.DecrJobCount`/`TransferJobState` and `job.Manager.RemoveJob`/
  `UpdateJobState` now guard every decrement (`> 0`) so counters never go
  negative.
- `formatQueueStatus` now calls `refreshQueueCounts` which recomputes
  `TotalJobs` and the per-state counts from the **live job set** (`JobsInQueue`)
  at query time, so `qmgr`/`qstat` always report numbers consistent with reality
  regardless of any residual in-memory drift.
- Live test (2026-08-03, Azure): after 20x submit+`qmove`+`qdel` and 15x
  `qdel`-of-running churn cycles, `testq`/`testq2` counters stayed `0`/all-zeros
  matching the (0) actual jobs, and no negative value was ever reported.

### 3.7 Target-queue admission control (gatekeeping) for routing  [DONE - implemented & live-tested]
For automatic routing (3.1) to be useful, a target execution queue must be able
to *accept or reject* a routed job. Real PBS/TORQUE runs an ordered admission
gate (`svr_chkque`) before a job enters any queue; the router tries each
`route_destinations` entry in order and the **first queue whose gate passes**
accepts the job. OpenTorque has **no working admission gate** today — most
queue attributes are stored but never enforced (see 3.5). Implement, per target
queue, the checks below (reference: `docs/job_queue_analysis.md` §7, §4):

- **enabled / started** — reject if queue is `qdisable`d or not started.
  (Partially read by the scheduler, but not enforced as an admission rule.)
- **max_queuable / max_running / per-user limits** — reject if at cap. Queue
  counts are already unreliable (3.6), so derive limits from live state.
- **resources_max / resources_min** (`ResourceMax`/`ResourceMin` maps) —
  accept only if the job's `-l walltime/ncpus/mem/...` fall within the queue's
  [min, max] resource interval. This is the key criterion for auto-routing
  (short vs. long, small vs. big) and is currently **not enforced**.
- **ACL user / group / host** (`acl_users`, `acl_groups`, `acl_hosts` + enable
  flags) — reject if the submitter/host is not allowed. Currently stored but
  never enforced.
- **disallowed_types** — reject if the job type (batch/interactive/rerunable/
  job_array/...) is disallowed. Not present at all.
- **from_route_only** — reject direct `qsub` submissions; only accept jobs that
  arrived via routing (or admin `qmove`/`qorder`). Not present.
- **unknown resource/attribute + site ACL** — reject jobs requesting resources
  or attributes the cluster does not know (site hook). Not present.

Bypass rules (from TORQUE) worth matching: admin `qmove`/`qorder` bypass
enabled/max_queuable/from_route_only/ACL/resource checks, but `disallowed_types`
is hard and cannot be bypassed.
- Where: `internal/server/server.go` (`handleQueueJob`, new admission helper),
  `internal/queue/queue.go` (wire ACL/limit/resource enforcement),
  `internal/sched/scheduler/scheduler.go` (resource-interval routing).
- Expected: a single admission function returns pass/fail (+reason); the router
  uses it to pick the first accepting destination; `qsub` also uses it for
  direct submissions.

> **DONE (2026-08-05):** `admitToQueue` in `internal/server/route.go` is the
> ordered admission gate (`svr_chkque`): enabled/started, `from_route_only`,
> `max_queuable`/`max_running`/`max_user_queuable`/`max_user_run` derived from
> live state, ACL user/group/host, and the `resources_min`/`resources_max`
> interval for ncpus/walltime/mem. Direct `qsub` validates the target execution
> queue; routed jobs validate during destination selection. `applyQueueAttrs`
> now wires the new attrs and `formatQueueStatus` displays them incl.
> `resources_min`. `qmgr` list attrs accumulate across comma-split SvrAttrl
> entries (real-TORQUE interop). Live: `from_route_only` on `batch` rejected
> direct `qsub` (15007) but accepted routed jobs; `short_q` rejected a 01:00
> walltime job at the gate, which then routed to `long_q`.
> Queue resource limits (`resources_min`/`resources_max`/`resources_default`) and
> `max_queuable`/`max_running` are now persisted in the queue file and restored on
> server restart.

## 4. Cloud / platform integration


### 4.1 Cloud elasticity (dynamic node up/down by queue demand)  [DONE - implemented & live-tested]
Dynamic node up/down by queue demand is implemented end-to-end by the
event-driven Cloud Elastic Controller (CEC) + Azure CRP (see 4.4 / 4.4a-e):
`NeedCapacity` triggers scale-out to new VMs, idle reclaim drives scale-in.
The drain/offline hooks it relies on are 4.2 below.

### 4.2 Node drain/roll-out primitive  [DONE - implemented & live-tested]
A graceful drain state now exists (see 4.4f). `pbsnodes -D <node>` sets a
`drain` state (no new jobs, running jobs finish); `pbsnodes -r <node>` resumes.
`excl` is also supported. Both schedulers and the node-capacity snapshot
honor drain/excl.

### 4.3 Cloud elastic node scaling (queue-driven)  [DONE — implemented & live-tested]
Queue-defined burst/scale-in of cloud VMs. When a queue (or node group) carries
cloud attributes, an external "Cloud Elastic Controller" (CEC) talks to a
per-cloud "Cloud Resource Provider" (CRP) process to provision/deprovision worker
VMs on demand. Not implemented; the designs are the authoritative reference:
- `docs/cloud-elastic-node-scaling-design.md` — architecture, queue attrs, CRP
  interface, dynamic node add/remove, reclaim lifecycle.
- `docs/cloud-elastic-event-driven-design.md` (**preferred**) — **event-driven**
  scale model: the scheduler emits a `NeedCapacity` event whenever jobs are left
  unplaceable (`CanNotRun`), and the CEC reacts immediately instead of polling
  on a fixed tick. Scale-in is driven by `NodeFree`/`NodeIdle`/`NodeDown` events.
  This supersedes the fixed-tick policy of the older design (see §8 note there).
- Queue `cloud_*` attributes to implement:
  - `cloud_provider`  -- cloud name (azure/aws/...) used to locate the matching
    external CRP via a provider-name -> endpoint registry.
  - `cloud_vm_sku`    -- concrete VM size/family to request from the provider.
  - `cloud_min_nodes` -- minimum number of worker nodes kept scaled in.
  - `cloud_max_nodes` -- maximum number of worker nodes the queue may scale to.
  - `cloud_idle_time` -- seconds a node stays idle with no queue assignment
    before it becomes a scale-in candidate.
  - `cloud_reclaim`   -- scale-in policy: `deallocate` (next use needs a fresh
    VM) vs `hibernate` (VM pauses, resumes quickly for the next job).
- Required Go changes (mirror 4.1/4.2):
  - `internal/queue/queue.go` -- add the new `cloud_*` fields to the queue struct.
  - `internal/server/server.go` -- parse the new attrs in `applyQueueAttrs`,
    expose them in `formatQueueStatus`, and persist them with the queue.
- Runtime coupling:
  - CEC watches queues/nodes, and calls `pbsnodes -o` (offline/drain) hooks on
    the scheduler so scale-in/scale-out transitions are job-safe.
  - Dynamic node add/remove reuses the existing node/mom registration path
    (`qmgr` + `server_priv/nodes`), so newly provisioned VMs join the cluster and
    decommissioned ones are removed cleanly.
- Open questions (see design appendix): cost/metering, capacity planning for
  `cloud_max_nodes`, cold-start latency for `deallocate` vs `hibernate`,
  and coordination with HA (section 5).

### 4.4 Event-driven elastic controller implementation  [DONE — implemented & live-tested]
Concrete work items to build the event-driven cloud elasticity (per
`docs/cloud-elastic-event-driven-design.md`, esp. the revision in §13):
- **M0** — add the `cloud_*` queue attributes end-to-end: model fields  **[DONE -- implemented & live-tested]** -- the cloud_* burst attributes are parsed, displayed, and persisted across server restart.
  (`internal/queue/queue.go`), `applyQueueAttrs` parsing, `formatQueueStatus`
  display, and persistence (server restart). Enables config only.
- **M1** — `pbs_sched` emits a JSON `NeedCapacity` event when jobs are left
  unplaceable (`findNodeForJob` returns nil → `CanNotRun`), per cloud-backed
  queue, using **lookahead accumulation** (§12.1) so strict-FIFO size
  one-or-more VMs for the backlog, not just the head job; add the new
  `PROVISIONING` job state + job<->VM (`vm_id`) binding record (§12.2/12.3);
  CEC event-loop with in-flight guard and cooldown; CRP adapter interface stubs
  (`ensure/describe/reclaim/resume/health`) that return `vmID` before boot.
  **[DONE -- implemented & integration-tested]** -- see 4.4a below.

### 4.4f Node drain/roll-out primitive (4.2) - implementation + live test
- New node admin states `drain` (no new jobs; running finish) and `excl`
  (exclusive/maintenance) in `internal/node` (flags preserved by
  `applyStateString` against MOM status updates). `pbsnodes -D <node>` drains,
  `pbsnodes -r/<c>` resumes (also `qmgr set node <name> state=drain|excl|free`).
- Both schedulers honor the state: the external scheduler's `nodeSchedulable`
  skips drain/excl/offline/down/busy and `internal/server` builtin uses
  `IsFree` (drain/excl excluded). `formatNodeStatus` now emits node state
  exactly once from `StateName()` (admin-aware) instead of re-emitting the
  raw MOM `state` (which previously masked drain and let the scheduler place
  new jobs on a drained node).
- Unit tests: `internal/node/drain_test.go` (drain/excl state, MOM free does
  not clear drain, IsFree false), `TestFindNodeForJobSkipsDrain` and
  `TestNodeSchedulable` in `internal/sched/scheduler/scheduler_test.go`.
- Live (2026-08-06, srv): `pbsnodes -D xxin-opentorque-srv` -> node state
  `drain`; submitting jobs while drained produced **zero** dispatches to srv
  (scheduler placed on w1 only, per sched log); `pbsnodes -r xxin-opentorque-srv`
  -> srv immediately accepted jobs again (212/213 dispatched to srv). During
  the shortfall the CEC auto-scaled a VM (`ot-node-8armcZ7L`), confirming 4.1
  dynamic elasticity, and destroyed the orphan when the job left the queue.

### 4.4a M1 test results (integration, RG `xxin-opentorque-test`, westus3)
Validated live against the M1 stub CRP (`azure`) running inside `pbs_sched`:
- Queue `batch` configured cloud-backed: `cloud_provider=azure`,
  `cloud_vm_sku=Standard_D2s_v3`, `cloud_max_nodes=10`, `cloud_idle_time=300`,
  `cloud_reclaim=deallocate`, `cloud_location=westus3`,
  `cloud_rg_name=xxin-opentorque-test`.
- Fill the two static nodes (4 cores) with 4 jobs, then queue 2 more: scheduler
  logs `[SCHED] Cloud queue batch: job N needs 1 cores, no node available` per
  blocked job and one merged `capacity event cores=2 nodes=1 blocked=2`.
- Event forwarded to CEC -> `[CEC] ... scaling OUT by 1`; stub VM provisioned and
  bound to the head job (`bound vm=azure-vm-N -> job=...`), `inflight=1`.
- Cooldown + in-flight guard verified: later cycles log `no-op` / `in cooldown`
  instead of over-provisioning. Static dispatch still works (jobs run on both
  nodes across all free cores). No daemon restarts; server/mom/sched PIDs stable.
- Job-state path confirmed in code: `StateProvisioning` (state 7, char `D`),
  `ProvisionVM`/`ProvisionNode` round-trip the `.JB` file and survive restart
  (PROVISIONING stays PROVISIONING for CEC reconcile); `formatJobStatus` emits
  them so `qstat -f <full-id>` shows them. The real `PROVISIONING -> R` transition
  needs a real provider + M2 node registration and is not exercised by the stub.
- Notable gap found while testing: server lacks short-job-ID resolution; use the
  full `<jobnum>.<server>` form everywhere (see 2.4).
- Next (M2/M2b/M3): real Azure CRP, cloud-init `pbs_mom` bootstrap, dynamic node
  add/remove + IP-range ACL auto-registration, event-driven scale-in reclaim.


### 4.4b M2/M2b implementation + live test results (RG `xxin-opentorque-test`, westus3)
M2 (real Azure CRP + cloud-init `pbs_mom` bootstrap) and M2b (dynamic node
auto-registration with IP-range ACL) are now **implemented and verified
end-to-end against live Azure** (subscription `a04b47d2-...`). Key results:
- **Real CRP driver** (`internal/cec` + IMDS MSI auth) creates VMs via ARM with
  no public IP; vnet/subnet `10.20.0.0/16`; cloud-init pulls `pbs_mom` +
  `auth_key` from the server's HTTP bootstrap endpoint (`http.server 8080`).
- **Scale-out:** `[AzureCRP] Ensure created VM ot-node-WhcG4fS0 sku=Standard_D2s_v3`.
- **Auto-register (M2b):** server attrs `allow_dynamic_nodes=True` +
  `node_allowed_ip_ranges=10.20.0.0/16`; on first IS contact from an allowed
  source IP the MOM is registered: `[NODE] Added node ot-node-WhcG4fS0 (np=2,
  id=2)` + `[SERVER] Auto-registered dynamic node ot-node-WhcG4fS0 (ip=10.20.0.6,
  np=2)`; the bound VM's first contact drives `PROVISIONING -> R`.
- **Job executes on the dynamic node** (proves full pipeline): jobs 30 & 31
  dispatched to `ot-node-WhcG4fS0` and ran exit=0 (`EXEC-ON ot-node-WhcG4fS0` /
  `DONE ot-node-WhcG4fS0`); output files delivered to `/tmp/fo1.txt`,
  `/tmp/fo2.txt` on the node. Scheduler log: `[SCHED] Dispatched
  30.xxin-opentorque-srv to ot-node-WhcG4fS0`.
- **Bug found + fixed (commit `49a481d`):** dynamic-node mom failed every job
  with `fork/exec /bin/sh: no such file or directory` even though `/bin/sh`
  exists. Root cause: `getWorkDir(j)` returned `PBS_O_WORKDIR` = the srv-only
  submit dir (`/var/lib/waagent/run-command/download/N`) that does not exist on
  the node; Go reports a misleading no-such-file error when a bad `cmd.Dir` is
  combined with `Setsid: true`. Fix: validate each candidate (`PBS_O_WORKDIR`,
  `HOME`, `os.Getenv`) with `os.Stat` and fall back to `/`.
- **M3 (scale-in) is now IMPLEMENTED & verified live** (commits ``7fe7cea`` +
  ``69a3723``): CEC drains, deregisters and reclaims idle cloud VMs. See 4.4c below.



- **M2** — Azure CRP driver + cloud-init bootstrap (install `pbs_mom`, point at
  server, register) + dynamic node add via `qmgr create node`/RPC; node named
  by VM ID as the stable handle (§12.3). **[DONE -- implemented & integration-tested]**
- **M2b** — dynamic node registration with IP-range ACL (§12.4): new server
  attrs `allow_dynamic_nodes` (default false) + `node_allowed_ip_ranges` (CIDR
  list); auto-register a MOM on first IS contact only if its source IP is in an
  allowed range; first contact of a bound VM drives `PROVISIONING -> R`.
  **[DONE -- implemented & integration-tested]**

- **M3** — event-driven scale-in: observe `NodeFree` → idle window →
  drain → deregister → `deallocate`/`hibernate`; fast `resume` for hibernate;
  provisioning timeout + `qdel`-during-provisioning cleanup (§12.2). **[DONE -- implemented & integration-tested]** -- see 4.4c below. (hibernate/resume fast path + provisioning timeout are also now implemented and live-verified -- see 4.4c.)

### 4.4c M3 scale-in implementation + live test results (RG xxin-opentorque-test, westus3)
M3 event-driven scale-in is now implemented and verified end-to-end (commits
7fe7cea + 69a3723) in external-scheduler mode. Scale-out, job execution on the
dynamic VM, then idle-time-based drain/deregister/deallocate all ran live:
- Code: internal/cec/cec.go gained Owned map[string]bool + per-node
  IdleSince timers and a reclaimInterval ticker (default 3s); a NodeController
  interface (DrainNode, DeregisterNode) injected from cmd/pbs_sched/main.go
  keeps CEC decoupled from the server CLI. RegisterNodesUp seeds Owned when
  consuming provisioning; RegisterNodesIdle(queue, idleNodes) starts/clears idle
  timers (static/foreign nodes ignored).
- Scale-out + run: submitted 5 -l ncpus=2 (180s sleep) jobs; 4 filled the static
  slots (w1/srv), the 5th (job 39) queued -> capacity event -> CEC created
  ot-node-4hdZDYiK (Standard_D2s_v3) -> cloud-init booted mom -> auto-registered
  -> job 39 dispatched and ran on it. Scheduler log: [SCHED] Dispatching job 39
  ... to ot-node-4hdZDYiK/0.
- Idle reclaim: after the job finished, [CEC] idle timer started, then after
  cloud_idle_time seconds [CEC] reclaiming idle node ot-node-4hdZDYiK
  (policy=deallocate, running=1 min=0). Drain via Manager set node state=offline
  (server log cmd=3 objType=4 attrs=1), deregister via Manager delete node
  (cmd=2, [NODE] Removed node), then provider.Reclaim -> VM confirmed
  PowerState/deallocated, Running decremented to 0, node dropped from Owned.
- Cleanup/state: test VM deallocated and deleted; orphaned M2 VM removed;
  cloud_idle_time restored to 300. Final node list: w1, srv only.
- cloud_reclaim=hibernate fast-resume: IMPLEMENTED + live-verified (2026-08-06,
  RG xxin-opentorque-test, westus3). CEC now keeps a deallocated ("hibernated")
  VM for fast resume instead of destroying it, and the next scale-out resumes it
  via provider.Resume (POST /start) rather than re-provisioning a fresh VM:
    - Code: Pool.Hibernated map + ensurePool init; reclaimNodeLocked adds the
      node to Hibernated under policy= hibernate (retain, not destroy);
      handleCapacity resumes hibernated VMs first (push into Provisioning,
      Inflight++, provider.Resume) before provisioning new ones. Tests
      TestHibernateReclaimKeepsVM / TestHibernateFastResume in cec_test.go.
    - Live cycle observed in sched log: scale-out created ot-node-cOG4VhDG
      (Standard_D2s_v3) -> ran jobs -> idle 45s -> [CEC] reclaiming idle node
      ... (policy=hibernate) -> hibernated node ... for fast resume (VM kept,
      PowerState/deallocated) -> new overflow -> [CEC] resuming hibernated
      vm=ot-node-cOG4VhDG -> job=... (fast path) -> VM PowerState/running ->
      job ran. No new VM was provisioned on the fast path.
    - Azure hibernation capability: createVM no longer sets the Azure
      `hibernationEnabled` hardware flag -- that requires a hibernation-
      capable SKU which the default Dsv3 pool SKU (Standard_D2s_v3) is not,
      and it caused HTTP 400 BadRequest on create. Fast resume is delivered by
      retaining the deallocated VM + POST /start, which works on any SKU.
- provisioning-timeout (qdel-during-provisioning) cleanup: IMPLEMENTED +
  live-verified (2026-08-06, same RG). Code: sweepProvisioningTimeoutLocked
  (running under the reclaim ticker) destroys any Provisioning VM whose bound
  job is no longer queued or running; SyncQueuedJobs qdel-during-provisioning
  orphan destroy. Tests TestProvisioningTimeout in cec_test.go. Live: with
  cloud_provision_timeout=15 an overflowing job triggered scale-out and the
  still-booting VM (ot-node-p5Z4iO9q) was destroyed ~15s later; zero orphan
  VMs/NICs remained in the RG.
- createVM-failure NIC cleanup: Ensure now deletes the just-created NIC when
  createVM fails, so a failed create no longer orphans network interfaces
  (found and fixed during the hibernate live test; the 7 orphaned ot-node-*-nic
  already in the RG were manually deleted).
- Destroy-path resource cleanup (verified end-to-end live 2026-08-05): when
  scale-in reclaims with destroy=true (VM deleted rather than deallocated),
  provider.Reclaim now deletes the VM's attached NICs and any public IPs so
  Azure does not leave orphaned network interfaces. Verified with a real
  scale-out -> idle-reclaim(destroy) cycle: the dynamic VM `ot-node-lthCkGOx`
  and its NIC were both removed, leaving no orphan. Two bugs were found and
  fixed along the way: (1) destroyVM deleted the VM asynchronously then
  deleted the NIC immediately, which returned HTTP 400 NicInUse and orphaned
  the NIC -- destroyVM now waits for the VM to report 404 and retries NIC/PIP
  deletion (commit 3888d55); (2) CEC SyncQueuedJobs destroyed a provisioning
  VM whenever its bound job left the queue, including when the job was running
  on that very VM -- it now only releases a provisioning VM when the bound job
  is gone (queued nor running), via an AliveJobs set (commit 3888d55). Earlier
  orphaned ot-node-* NICs in the test RG were manually cleaned up.

- **M4** — cooldown tuning, shortfall headroom, `NeedCapacity` merge/coalesce,
  drain timeout policy **[DONE -- implemented & live-tested]**, and the
  per-pool free-cores status RPC in `qstat -B` (2.9 remainder) **[DONE -- see 4.4e]**.
- Blockers (design §11): multi-node jobs (1.4) affect shortfall math; jobs
  larger than one SKU node cannot be placed.

### 4.4d M4 implementation + live test results (RG xxin-opentorque-test, westus3)
M4 adds the queue tuning knobs and CEC elasticity refinements:
- New queue attrs (parsed, displayed, persisted across server restart):
  `cloud_cooldown` (seconds between scale-out actions per pool, 0 = global
  default), `cloud_scale_headroom` (extra VMs beyond exact shortfall for burst
  cushion), `cloud_drain_timeout` (seconds a reclaim may drain a busy node
  before giving up, 0 = default). Wired through `internal/queue`,
  `internal/server` (`applyQueueAttrs` + `formatQueueStatus` + `saveQueue`/
  recover), and `internal/sched/scheduler` (`QueueInfo`/`CapacityEvent`).
- CEC: per-pool cooldown override, `desiredSize` + headroom in scale-out,
  `coalesceCapacityEvents` (drain queued `NeedCapacity` events, take max
  shortfall, union jobs), drain-timeout rate-limit in `reclaimIdle`, and
  `LastReclaim` map. `cmd/pbs_sched/main.go` wires the new fields into the CEC
  event.
- Local build + unit tests pass; 4 new CEC unit tests
  (`TestDesiredSizeHeadroom`, `TestPerPoolCooldown`, `TestCoalesceCapacity`,
  `TestDrainTimeoutRateLimit`). VM `go vet` clean, full `go test ./internal/...`
  passes.
- Live (2026-08-06, srv): set `cloud_cooldown=120 cloud_scale_headroom=2
  cloud_drain_timeout=60` on queue `batch` -> shown in-memory, and after a real
  server restart all three persisted (verified in `server_priv/queues/batch`
  and via `qmgr print queue batch`). Attributes then reset to defaults to keep
  the production `batch` config intact. All 6 changed files byte-match local
  vs VM (go1.22.12).

---

### 4.4e Per-pool free-cores snapshot in `qstat -B` (M4 follow-up)
Implemented the per-queue/per-pool capacity snapshot (design §13 'Per-queue/SKU
free cores snapshot API'; was the M4 follow-up). Nodes now carry an optional
`queue` (pool) ownership set via `qmgr set node <name> queue=<q>` and persisted in
`server_priv/nodes`. `pbs_server` aggregates each pool's running node count and
total/free cores into the server status, surfaced by `qstat -B -f`:
- New server status attrs (one entry per pool, keyed by queue name):
  `pool_nodes`, `pool_up_nodes`, `pool_total_cores`, `pool_free_cores`.
  Nodes with no ownership fall into a `default` pool; down/offline nodes are
  excluded from up/free counts.
- New code: `internal/node` `Node.Queue`; `internal/server` `applyNodeAttrs`
  (`queue`), `saveNodes`/`recoverNodes` (persist/restore), and the per-pool
  aggregation in `formatServerStatus`. Unit test `TestPoolFreeCoresSnapshot`
  (`internal/server/poolsnapshot_test.go`).
- Live (2026-08-06, srv): set `queue=batch` on `xxin-opentorque-srv`/`w1`
  (np=2 each). `qstat -B -f` showed `pool_total_cores.batch=4,
  pool_free_cores.batch=4, pool_up_nodes.batch=2`; running an `ncpus=2` job
  dropped `pool_free_cores.batch` to 2 and it returned to 4 after `qdel`.
  Ownership persisted across a real server restart. All changed files
  byte-match local vs VM (go1.22.12).

## 5. High availability & robustness

- MOM orphaned-process cleanup (fixed 2026-08-05): a crash/restart left a running
  job's process group orphaned, and a re-dispatched job started a second instance
  while the first (e.g. `sleep`) kept running and was never killed by `qdel`.
  The MOM now persists each running job's session id to `mom_priv/jobs/<id>.SID`,
  reaps any recorded session at startup (manager is empty then) and before
  starting a new instance of the same job, and removes the sidecar on cleanup
  (commits d5fe0ed + e7ef26a).

### 5.1 HA / failover  [DONE - implemented & live-tested]
Full HA is implemented, cloud-native (Azure-first), and covered end-to-end by live tests:
- **State/authority** lives in managed PostgreSQL (PostgresStore behind the Store interface), so masters are interchangeable and nothing binds to one machine's filesystem.
- **Leader election & fencing-by-lease**: PBS_HA=1 elects exactly one active via the ot_lease row (10 s TTL / 3 s renewal); standbys are gated from dispatch (no split brain / double-run). The holder is the OS hostname (unique per master). Startup + takeover reconciliation (`recoverJobs`, `reconcileRunningJobsWithMOMs`) keeps a still-running job Running and requeues only confirmed-dead ones.
- **Two supported modes, both live-verified on Azure (westus3, RG xxin-opentorque-test, managed PG otx-pg + internal LB otx-lb frontend 10.0.0.10:15001 over active-only health port 15150):
  - *Dual-master (hot standby)*: two control-plane VMs share the LB backend; measured **~16-20 s** client-visible failover (tuned 5 s probe).
  - *Single-master (auto-replace)*: one-instance VMSS from a **generalized non-TrustedLaunch (Gen1)** image; measured **~45 s** end-to-end RTO (needs a subnet **NAT gateway** so the private instance can reach managed PG at boot).
- **Floating-VIP failover** (PBS_HA_VIP): active binds the VIP and releases on loss/shutdown; the takeover rebinds it (live-verified). With an internal LB the VIP is optional - the LB follows the active via the health port.
- **Crash recovery (Phase 0)**: jobs/queues/nodes recovered from PG on takeover; running jobs never re-dispatched; orphans requeued (internal/server/reconcile_test.go).
- **Production networking best practice** documented (Private Link for managed PG + ExpressRoute/VPN for enterprise clients + hardened public edge only for external clients).
Docs: docs/opentorque-ha.md (reference), docs/ha-guide.md (user guide), docs/blog-ha.md; scripts under scripts/ha-*.sh.
**Phase 0 done (2026-08-30):** startup/takeover reconciliation so a running
job is never re-dispatched after a server crash, and orphans are requeued.
- `recoverJobs` (internal/server) now keeps Running jobs Running (instead of
  blindly requeueing them) and `restoreRecoveredRunningJobs` rebuilds the node's
  slot accounting, so a job a MOM is still executing continues and is never run
  twice.
- MOM (`internal/mom/mom/daemon.go handleMomStatus`) now reports its
  currently-running job ids (attr `jobs`).
- Server queries each MOM (`reconcileRunningJobsWithMOMs`, retried a few seconds
  after startup) and requeues a Running job ONLY when its MOM is reachable and
  confirms it is no longer running; jobs on unreachable MOMs stay Running (the
  node-down path, TODO 2.10, cleans up truly-dead nodes). Safe against double
  execution.
- Unit tests: internal/server/reconcile_test.go.
- Live test (2026-08-30, Azure): a `sleep 120` job went Running; `pbs_server`
  was killed (head-node crash) while the MOM kept the job; after server restart
  the reconcile confirmed the job was still running -> it stayed Running, was
  dispatched exactly once, and completed exit=0 at full 2:00 runtime. A leftover
  orphan (stuck Running after a MOM restart) was correctly requeued, returning
  the node to free.
- (2026-08-30) Persistence groundwork: added `Store` interface + `FileStore`
  (internal/server/store.go) as a behavior-preserving refactor of the server's
  file persistence, so a PostgreSQL backend (`PostgresStore`) can be added for
  multi-master HA later.
- (2026-08-30) Multi-master leader election implemented (PBS_HA=1): PG `ot_lease`
  lease elects the active `pbs_server`; standbys are gated from dispatching,
  and a standby taking over runs the running-job reconciliation. Live takeover
  tested (A=active runs a job; kill A -> B acquires lease and becomes active).
  Floating-VIP address failover implemented (`PBS_HA_VIP`): the active binds the
  VIP, releases it on loss/shutdown, and the taking-over standby rebinds it so
  clients/MOMs at the VIP follow the leader (verified live on Azure).

### 5.2 Completed-job status read-back  [DONE - implemented & tested]
Finished jobs now keep their full attribute set and remain queryable through the
retention window, including across a server restart.
- `saveJob`/`recoverJobs` were refactored around `serializeJob` /
  `deserializeJob`: the `.JB` file now persists the resource request
  (`req.<resc>=<val>`), timing (`qtime`/`start_time`/`comp_time`),
  execution + exit status, and the multi-node layout (`node_count` /
  `task_count`, `+`-joined `exec_host`).
- `recoverJobs` previously *skipped* completed jobs, so finished-job history
  was lost on restart and their `.JB` files leaked on disk. Completed jobs are
  now reloaded into the job manager and stay queryable via `qstat` until
  `completedJobCleanup` purges them after `keep_completed` (removing the
  leak too).
- Unit test: `TestSerializeDeserializeJobCompleted` (round-trips resource
  request, timing, exec/exit, multi-node layout).

### 5.3 No Go unit tests  [DONE - added]
`go test ./...` finds no test files; correctness is validated only by manual
integration testing. Adding at least unit tests for the scheduler sort
policies and node selection would help prevent regressions.

---

> **DONE (2026-08-05):** added Go unit tests for the routing/admission work:
> `internal/queue/queue_test.go` (`ParseList`), `internal/server/route_test.go`
> (routing + admission gate), `internal/sched/scheduler/scheduler_test.go`, and
> `cmd/qmgr/parse_attrs_test.go` (list-valued attribute parsing).
> `go test ./internal/... ./cmd/qmgr` passes on the VM.

## Suggested triage order
1. Fix the data-loss bugs: 2.3 (`-l` overwrite), 2.4 (`qstat -f` 15001), 2.5 (`qrun`).
2. Wire the existing scaffolding: 1.1/1.2 host+feature selection, 3.1 route queues,
   3.3 job arrays.
3. Add real resource/multi-node support: 1.4, 2.1, 2.6.
4. Add node groups/affinity + queue policy: 1.3, 1.5, 2.2.
5. Cloud elasticity and HA: section 4, 5.1.
