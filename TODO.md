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

### 1.1 `-l host=<node>` — pin a job to a specific node  [GAP]
A job cannot request a particular node. `qsub -l host=nodeX` is silently
ignored: on a live cluster, a job requesting a non-existent host (`-l
host=nonexistent-host`) ran immediately on the available node.
- Where: `internal/sched/scheduler/scheduler.go` (`parseJobInfo`,
  `findNodeForJob`); `internal/server/server.go` (`handleQueueJob`).
- Expected: parse `Resource_List.host`, store it on the job, and have
  `findNodeForJob` only consider nodes whose name matches.

### 1.2 `-l feature=<prop>` / node `properties` — schedule by node property  [STUB]
`Node` has a `Properties []string` field (settable via `qmgr set node ...
properties=...`, shown by `pbsnodes`), but the scheduler **never reads it**.
`qsub -l feature=special` is ignored even when no node has that property.
The only code touching `Properties` is `formatNodeStatus` (output) and
`mgrSetNode` (parse). 
- Where: `internal/sched/scheduler/scheduler.go` (`parseJobInfo`,
  `findNodeForJob`); `internal/node/node.go` (data model exists).
- Expected: match job `-l feature=/properties=` against node `Properties`.

### 1.3 Host groups / host tags / node pools  [GAP]
No notion of grouping nodes (equivalent of PBS `hostgroup`, host tags, or node
pools) exists at the job or queue level. Only the flat `ServerInfo.Nodes` list
is considered.

### 1.4 `nodes=N:ppn=M` / `select=` / `place=` true multi-node placement  [GAP]
`-l nodes=` is parsed only as an approximation of `CPUReq` (a single integer)
and `ncpus=` likewise. There is no real multi-node allocation, no per-node
processor request (`ppn`), and no `select`/`place` chunk model. Jobs never span
more than one node.

### 1.5 Queue node-affinity (queue `naccesspolicy`)  [GAP]
No equivalent of PBS `naccesspolicy` (shared / sharing-exclusive / exclhost) or
queue-to-node-pool binding. A route/execution queue has no node-selection policy.

### 1.6 Queue node-pool (`hostlist`) vs. submission-host ACL (`acl_hosts`)  [GAP]
Two TORQUE attributes are easily confused; both are unimplemented/mis-wired in
OpenTorque. Their semantics (per `docs/job_queue_analysis.md` §6.2, §11.3) and
status:
- **`hostlist`** (queue attribute) — binds the queue to a set of **compute
  nodes** (a node pool). Jobs in the queue may only run on those nodes.
  Supports wildcards, e.g. `set queue gpu hostlist=gpu01,gpu02` or
  `hostlist=node[001-100]`. This is the core mechanism for queue→node-pool
  isolation. **OpenTorque: not implemented at all** — no queue field/logic; the
  scheduler iterates all nodes regardless of queue (see 1.3/1.5, and scheduler
  `findNodeForJob`).
- **`acl_hosts`** (with `acl_host_enable`) — restricts which **submission
  (client/login) hosts** may submit jobs *into* the queue. Its values are
  client hostnames/IPs (e.g. `submit.cluster.local`), **NOT compute nodes**. It
  is a submit-side access control, parallel to `acl_users`/`acl_groups`.
- Key difference: `hostlist` controls **where jobs run** (compute nodes);
  `acl_hosts` controls **who/where may submit** (clients).
- **OpenTorque status**: `acl_hosts` exists only at server config level
  (`s.cfg.ACLHosts`, "allowed submission hosts") and is **never enforced**; the
  queue-level `ACLHosts` field is never read (see 3.5). Implement both as
  specified so the two roles are not conflated.

------

## 2. Scheduler & resource management

### 2.1 GPU / accelerator, license, and generic resource constraints  [GAP]
Only `ncpus`, `mem`, `walltime`, and `nodes` are understood by the scheduler.
No GPU (`ngpus`), license, or arbitrary named-resource accounting/constraints.
Resource requests beyond these are effectively ignored (and, with multiple `-l`
flags, dropped — see 2.3).

### 2.2 Backfill / reservation / preemption  [GAP]
No backfill, no job reservations, no preemption. These are core to throughput
on heterogeneous/multi-queue clusters.

### 2.3 [BUG] `qsub` multiple `-l` flags overwrite each other
`qsub -l ncpus=4 -l walltime=01:00:00` keeps only the **last** `-l` value —
`ncpus` is silently dropped. Root cause: `cmd/qsub/main.go` uses
`flag.String("l", ...)`, and Go's `flag` package overwrites repeated flags.
- Workaround today: `qsub -l "ncpus=4,walltime=01:00:00"` (single `-l`).
- Expected (TORQUE-compatible): accumulate all `-l` occurrences, or reject
  malformed input.

### 2.4 [BUG] `qstat -f <completed-job-id>` returns `server error 15001`
`qstat -f` works for queued jobs but fails with `PbseUnkjobid (15001)` for
**completed** jobs, even though `qstat -a` lists them. Server log shows
`Read proto error ... ReadUint: EOF`. Likely in `handleStatusJob` /
`formatJobStatus` shutdown path for jobs already removed from in-memory store.

### 2.5 [BUG] `qrun <node> <job>` returns 15001
The only way to force a job onto a specific node (`handleRunJob` with a `dest`)
currently errors with `15001` on the deployed build, preventing manual node
pinning. Needs root-cause (see 1.1/5.2 `qstat -f` for a possibly related cause).

### 2.6 Node CPU-accounting strength  [GAP]
Node selection uses `FreeCPUs = NumProcs - len(node.Jobs)` (one count per job),
not a per-job `CPUReq` aggregate, so CPU-based packing is weak and jobs
effectively dispatch in parallel regardless of `-l ncpus`. See 1.4.

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

### 2.10 Node-down does not auto-requeue its running jobs  [GAP/BUG]
`nodeMgr.MarkNodeDown` only sets `StateDown` and bumps `FailCount`; it does **not**
requeue or fail the jobs running on that node. If a MOM crashes or a node dies,
its jobs stay in `R` forever (orphaned) until an operator runs `qrerun`, and the
scheduler just stops placing new work there. `checkNodes` marks it down after
~5 min of silence regardless of state. Recommended: on node-down, requeue its
jobs (honoring `disable_automatic_requeue` / `automatic_requeue_exit_code`) and
free slots. See `docs/opentorque-scheduling-dispatch-failure.md` §6.2.

---

## 3. Queue routing & job control

### 3.1 Automatic queue routing (`queue_type=R`)  [STUB/GAP]
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

### 3.2 `qmove` of non-queued jobs  [GAP]
`handleMoveJob` only allows `Queued` jobs; moving running/held jobs is rejected
(`15004`). Standard `qmove` can move a broader set of states (operator
permission permitting).

### 3.3 Job arrays (`qsub -t`)  [GAP]
`-t 1-3` is accepted but never expanded into sub-jobs; it runs as a single job.

### 3.4 `momctl` direct attribute query  [STUB]
`momctl -q` is a placeholder; the underlying protocol query is unimplemented.

---



### 3.5 Queue attributes: model vs. configured vs. used mismatch  [GAP]
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

### 3.6 [BUG] Negative queue/server job counters
Queue and server job counters go stale and can go **negative** under churn
(repeated `qdel` of running jobs, state transitions, `qmove`). Observed on a
live node: `qmgr p q` showed `debug total_jobs = -1`, and server
`state_count` reported `Running:-4 Complete:-29`. Root cause is a
multiple-decrement path in `TransferJobState` / `DecrJobCount` /
`handleMoveJob` (or the delete path) — a counter is decremented even when the
job was not previously counted, or a move double-counts.
- Where: `internal/queue/queue.go` (`TransferJobState`), `internal/server/
  server.go` (`handleDeleteJob`, `handleMoveJob`).
- Expected: counters must never go negative; treat them as derived from the
  live job set (recompute on query) or guard all decrements.

### 3.7 Target-queue admission control (gatekeeping) for routing  [GAP]
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

## 4. Cloud / platform integration


### 4.1 Cloud elasticity (dynamic node up/down by queue demand)  [GAP]
No built-in cloud autoscaling: OpenTorque schedules against nodes that already
exist. No burst/elastic node support (cf. Slurm `resume`/`suspend`, CycleCloud
autoscale). See `docs/` analysis and the external design described in
`opentorque-dynamic-scheduling-azure.md` (out of repo). Recommended as a new
external "Fleet/Autoscale controller" plus drain/offline hooks.

### 4.2 Node drain/roll-out primitive  [GAP]
No convenience for draining a node before maintenance/scale-in (only manual
`pbsnodes -o` + wait; no graceful `excl`/drain state).

### 4.3 Cloud elastic node scaling (queue-driven)  [GAP] -- DESIGNED, NOT implemented
Queue-defined burst/scale-in of cloud VMs, per the design in
`docs/cloud-elastic-node-scaling-design.md`. When a queue (or node group)
carries cloud attributes, an external "Cloud Elastic Controller" (CEC) talks to a
per-cloud "Cloud Resource Provider" (CRP) process to provision/deprovision worker
VMs on demand. Not implemented; the design is the authoritative reference.
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

---

## 5. High availability & robustness

### 5.1 HA / failover  [GAP]
Single `pbs_server`; no active/passive failover, no job migration, no server
redundancy used in production.

### 5.2 Completed-job status read-back  [GAP/BUG]
Beyond the `qstat -f` 15001 bug (2.4), there is no retention/query path that
reliably returns full attributes of finished jobs.

### 5.3 No Go unit tests  [GAP]
`go test ./...` finds no test files; correctness is validated only by manual
integration testing. Adding at least unit tests for the scheduler sort
policies and node selection would help prevent regressions.

---

## Suggested triage order
1. Fix the data-loss bugs: 2.3 (`-l` overwrite), 2.4 (`qstat -f` 15001), 2.5 (`qrun`).
2. Wire the existing scaffolding: 1.1/1.2 host+feature selection, 3.1 route queues,
   3.3 job arrays.
3. Add real resource/multi-node support: 1.4, 2.1, 2.6.
4. Add node groups/affinity + queue policy: 1.3, 1.5, 2.2.
5. Cloud elasticity and HA: section 4, 5.1.
