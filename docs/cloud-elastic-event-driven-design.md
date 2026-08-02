# OpenTorque — Event-Driven Cloud Elastic Scaling — Design

Status: Draft proposal (event-driven revision)
Author: xin
Date: 2026-08-02
Supersedes (in part): `docs/cloud-elastic-node-scaling-design.md` §8's
fixed-tick polling policy. This document describes an **event-driven** scale
out/in model that reacts to scheduler and cluster events instead of polling on
a fixed timer.

---

## 1. Motivation: why not fixed-tick

The earlier draft (§8 of `cloud-elastic-node-scaling-design.md`) sized and
re-scaled the pool on a **periodic tick** (30–60 s). Problem: scaling latency
is bounded by the tick even when the scheduler already knows, immediately, that
a job cannot run because there are no usable nodes.

We already implemented an **event-driven scheduling trigger** for both the
built-in scheduler and the external `pbs_sched` (loopback TCP `sched_trigger_port`,
default 25003) that fires on job submit/release/completion/requeue/node-change.
The same event model extends naturally to elasticity: the scheduler already
detects "this job cannot be scheduled" per cycle (`findNodeForJob` returns nil →
`CanNotRun = true`). We use that precise signal to drive scale-out **immediately**,
and we use job-completion / node-idle events to drive scale-in.

Goal latency model:
- **Scale-out**: from "job submitted but no usable node" to "extra node is
  free and ready" is as fast as the underlying cloud can provision (seconds to
  minutes depending on SKU). Event-driven removes the scheduler-side tick; it does
  not remove cloud provisioning time, which is a separate, provider-bounded cost.
- **Scale-in**: reclaim only after `cloud_idle_time` with no new assignment, so
  the cluster does not thrash.

---

## 2. Core idea

Three cooperating components, all event-driven:

1. **`pbs_sched` (external scheduler)** — already signals when scheduling
   capacity is short. We extend it to emit a **`NeedCapacity` event** carrying
   the blocked job(s) and the resource shortfall (cores, mem) for each
   cloud-backed queue.
2. **Cloud Elastic Controller (CEC)** — the sole orchestrator of node
   membership. It receives `NeedCapacity` / `NodeIdle` / `NodeFree` / `NodeDown`
   events, computes how many nodes of which pool to add/remove, and talks to the
   appropriate CRP. It does **not** run on a timer; it is a pure event loop.
3. **Cloud Resource Provider (CRP)** — per-provider driver (azure/aws/...) that
   provisions/starts/stops/hibernates/deallocates VMs and reports back.

The CEC keeps the final decision authority over *how many* nodes (it owns the
`cloud_min_nodes` / `cloud_max_nodes` / cooldown / in-flight accounting), but the
*trigger* for adding a node comes from a real scheduler event, not a timer.

---

## 3. Event taxonomy

Define a small event set over a bus (in-process channel in `pbs_sched`, or a
shared socket/bus if CEC is a separate process). Suggested event payloads:

### 3.1 `NeedCapacity`
Emitted by the scheduler when, during a cycle, one or more jobs could not be
placed **and** the queue is cloud-backed.
```json
{
  "event": "capacity",
  "queue": "batch",
  "provider": "azure",
  "sku": "Standard_D8s_v3",
  "jobs": [
    {"id": "120.local", "ncpus": 4, "mem_kb": 8192000, "need": "cores"},
    {"id": "121.local", "ncpus": 1, "mem_kb": 0, "need": "cores"}
  ],
  "shortfall": {
    "cores": 5,          // Σ need - Σ immediately available on existing free nodes of this queue
    "nodes": 1,          // minimum additional nodes to satisfy the backlog of this cycle
    "blocked": 2         // number of queued jobs that could not start this cycle
  }
}
```
Fields: which queue/provider/SKU it applies to, the resource shortfall, and the
count of blocked jobs.

### 3.2 `NodeFree`
Emitted (or observed) when a node has no assigned jobs (`pbsnodes` `state=free`,
`jobs=` empty). Used to start the per-node idle timer for scale-in.

### 3.3 `NodeIdle`
Emitted by the CEC itself when a `NodeFree` has persisted for `cloud_idle_time`
**and** the pool can afford to shrink (pool size - 1 ≥ `cloud_min_nodes`).
Indicates a reclaim candidate.

### 3.4 `NodeDown`
Emitted when a node fails health checks / disappears (spot eviction, crash).
The CEC must replace it toward `cloud_min_nodes` or the demand that existed.

### 3.5 `JobDispatchFailure` (optional refinement)
If we later add real `send_job`/commit failure handling (§ `undoJobDispatch`),
the CEC can also react to a job that *was* assigned but failed to start, rather
than only to jobs left queued.

---

## 4. Where the capacity signal originates (scheduler)

In `internal/sched/scheduler/scheduler.go`, `runCycle`:

1. When `findNodeForJob(sinfo, jinfo)` returns `nil`, the job is **not** placed
   and `jinfo.CanNotRun = true` is set (already the case).
2. We add: for each such job, accumulate the shortfall per **queue**, and after
   the cycle, if any queue has `blocked > 0` and that queue is cloud-backed
   (queue carries `cloud_provider`), emit a **`NeedCapacity`** event for that
   queue.

Accumulation rule (kept conservative):
- Treat each unplaceable job as needing `max(1, jinfo.CPUReq)` cores on a new
  node of the queue's SKU, minus the cores the job could have used on existing
  free nodes that were too small = shortfall.
- Do **not** pre-warm to `cloud_max_nodes` greedily: only signal what this cycle
  could not place (plus a small headroom factor, e.g. ×1 for v1).

The server currently sends only a 1-byte "event happened" marker to the trigger
socket. For capacity, the external `pbs_sched` already *knows* the shortfall
locally, so it can build the JSON `NeedCapacity` event itself and forward it to
the CEC — no server change needed for the signal. (The server's 1-byte trigger
simply wakes the sched cycle earlier; the sched cycle produces the rich event.)

---

## 5. Queue attributes (unchanged from prior design, reused verbatim)

```go
CloudProvider string   // "azure" | "aws" | "" (static pool)
CloudVMSKU    string
CloudMinNodes int
CloudMaxNodes int
CloudIdleTime int
CloudReclaim  string // "deallocate" | "hibernate"
```

`cloud_provider != ""` flags the queue as cloud-backed and selects the CRP via
the provider-name registry.

---

## 6. Cloud Elastic Controller (CEC) — event loop

Architecture: a service with an event inbox; no timer for capacity decisions.

```
loop {
  ev := <-eventInbox        // NeedCapacity | NodeFree | NodeDown | NodeIdle | ticker(heartbeat)
  switch ev := ev.(type) {
  case Capacity:
      pool := pools[ev.queue]
      if cooldownActive(pool) break
      target := desiredSize(pool, ev.shortfall)   // see §7
      if target > pool.running { provision(target - pool.running) }
  case NodeFree:
      pool.byNode[ev.node].idleSince = now
      if idleSince + pool.idleTime <= now && pool.running > pool.min {
          emit NodeIdle → drain → deregister → reclaim(ev.node)
      }
  case NodeDown:
      pool.byNode[ev.node].decommissioned = true
      if pool.running < pool.min { provision(pool.min - pool.running) } // restore floor
      // if a job was on it, that resurfaces as NeedCapacity on the next sched cycle
  }
}
```

The only timers inside the CEC are **cooldown** for anti-flap and the **idle**
countdown for scale-in — not a polling loop for capacity.

### 6.1 In-flight provisioning guard
The CEC tracks `inflight` (VMs being provisioned). `desiredSize` must account
for inflight so a burst of `NeedCapacity` events does not overshoot the cap:
```
effective = running + inflight
if effective >= max → no provision
else provision = min(target - effective, max - effective)
```

### 6.2 Cooldown (anti-flap)
After a scale action on a pool, hold at least `cooldown` (e.g. 3 min) before the
next scale action on that same pool. `NeedCapacity` arriving during cooldown is
queued/merged; once cooldown lapses, coalesced demand is applied once. This
prevents N rapid submits from triggering N provisioning calls.

---

## 7. Desired-size computation (event-driven, not tick)

Given an incoming `NeedCapacity` shortfall, compute **how many additional
nodes**:

```
nodeCores   = cores per SKU node (np from SKU)
coresShort  = Σ over blocked jobs of max(1, job.ncpus)   // conservative
newNodes    = ceil(coresShort / nodeCores)
capped      = min(newNodes, cloud_max_nodes - (running + inflight))
provision   = max(0, capped)
```

Because every *unplaceable* job already failed to find `FreeCPUs >= cpuReq` on
existing nodes, `coresShort` is a lower bound of what must be provisioned; a new
node of `nodeCores` absorbs `nodeCores` cores of backlog.

If a blocked job is bigger than a node (`job.ncpus > nodeCores`), it cannot be
placed on a single node of this SKU at all — flag it (see §11 gap) rather than
provisioning a node that cannot host it.

---

## 8. Waking the scheduler to use the new node

After the CRP reports an added node's MOM is up and `pbsnodes` shows it, the CEC
should trigger a scheduling cycle so the newly queued jobs dispatch promptly:

- Reuse the existing trigger: server `triggerSched()` or directly ping the
  external `pbs_sched` trigger socket (`127.0.0.1:25003`), or have the CEC call a
  small `schedule()` RPC. This closes the loop: capacity event → provision →
  node free → re-schedule → jobs run.

---

## 9. Scale-in lifecycle (event-driven)

Scale-in is also event-driven, keyed on real idle observation, not a timer:

1. `NodeFree` observed for a node (no assigned jobs).
2. **Idle window**: keep observing; if any job is assigned within
   `cloud_idle_time`, reset the timer (cancel scale-in).
3. If idle persists for `cloud_idle_time` **and** `pool.running > cloud_min_nodes`:
   - Mark node `offline` (`pbsnodes -o`) so the scheduler stops selecting it.
   - Drain: wait for running jobs to finish, bounded by a drain timeout. Any new
     assignment to the node during drain cancels scale-in of that node.
   - Deregister node from the server (`qmgr delete node` / RPC) and remove from
     `server_priv/nodes`.
   - Instruct CRP: `reclaim(vm, cloud_reclaim)` → `deallocate` or `hibernate`.
4. Hibernate keeps the VM record so a later scale-out is a fast `resume`
   (start) of an existing VM instead of a fresh provision.

---

## 10. Event transport options

### Option A — in-process (single binary, simple)
CEC and scheduler live in the same process or share a Go channel/bus. Lowest
latency, no serialization. Good for a first milestone; unclear unit boundary.

### Option B — loopback JSON socket (recommended for milestone 1)
`pbs_sched` (or a small agent in it) opens a client connection to the CEC's
event port and writes the JSON events from §3. CEC binds `127.0.0.1:<cec_port>`.
Mirrors the existing `sched_trigger_port` pattern; low dependency.

### Option C — message bus (production, multi-node)
NATS/Redis stream. Necessary if the server and CEC run on separate hosts or
multiple servers (HA). Higher ops cost; defer.

Recommendation: **B for milestone 1**, revisit C when HA/multi-server lands.

---

## 11. Gaps this design exposes (feed into `TODO.md`)

- **Multi-node jobs** (`-l nodes=N:ppn=M`, `select/place`) don't exist (TODO 1.4);
  shortfall math assumes 1 node/job. Until real multi-node placement exists, a
  job larger than the node SKU is unplaceable — detect and surface it.
- **Per-queue/SKU `free cores` snapshot API**: the CEC needs a quick way to know
  each pool's current free cores without parsing `pbsnodes` text. Add a status
  RPC (`qstat -B` style) exposing per-queue cloud attributes + running node
  count + free cores (TODO 2.9 remainder).
- **Admission/preemption**: scale-out fired by `CanNotRun` is conservative; with
  backfill/preemption (TODO 2.2) the signal can be refined.
- **`job.ncpus > nodeCores`**: unplaceable on this SKU; needs either SKU upgrade
  logic or explicit error, not infinite provisioning.

---

## 12. Milestone plan

- **M0 (this doc + queue attrs)**: add the `cloud_*` queue attributes end-to-end
  (model, `applyQueueAttrs`, display, persistence) — enables config, no behavior.
- **M1 (event loop skeleton)**: `pbs_sched` emits `NeedCapacity` on `CanNotRun`;
  CEC event loop + in-flight guard + cooldown; CRP adapter interface stubs
  (`ensure/describe/reclaim/resume/health`); logging only, no real cloud calls.
- **M2 (Azure CRP + bootstrap)**: Azure driver (VMSS or single VMs) + cloud-init
  that installs `pbs_mom` and registers; dynamic node add/remove (§7 of prior doc).
- **M3 (scale-in + hibernate)**: `NodeFree`→idle→drain→reclaim; `hibernate` fast
  resume.
- **M4 (refine)**: cooldown tuning, headroom factor, `NeedCapacity` merging,
  drain timeout policies, HA.
