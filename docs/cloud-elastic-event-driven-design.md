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



## 12. Design revision: stuck-job lookahead, provisioning state, VM-ID binding, dynamic registration

This section refines the model to address four realities of cloud elasticity
that a static-cluster assumption hides.

### 12.1 Lookahead sizing in strict FIFO (don't stop at the head)

Current scheduler behavior (verified in `internal/sched/scheduler/scheduler.go`
`runCycle`): when `findNodeForJob` returns nil, `jinfo.CanNotRun=true`; with
`strict_fifo` the cycle **stops** and later jobs are not even considered. That is
right for a static cluster (later jobs can't run behind a blocked head anyway)
but wrong for elasticity: we can size **one or more new VMs** to satisfy the
head **and** the jobs behind it that would then fit.

**Change** — in `runCycle`, when `strict_fifo` AND a cloud-backed queue:

1. Do not `break` on the first blocked job. Instead fix the head, then keep
   walking the iterator to **accumulate demand** (cores/mem) for all jobs that
   are themselves placeable-on-a-this-SKU-node, until we either exhaust the
   iterator or hit a second non-placeable job that is *also* bigger than the
   remaining capacity (see below).
2. The shortfall for the queue becomes the **cumulative** demand, so the CEC
   provisions enough nodes to clear the whole visible backlog, not just the
   head job.
3. Non-`strict_fifo` queues already try every job; this gives them the same
   cumulative shortfall for free.

Look-ahead must not over-provision: cap accumulated shortfall at
`cloud_max_nodes - (running + inflight)`, and stop accumulating past a job
whose `ncpus > nodeCores` (unplaceable on this SKU — surface separately, §11).

### 12.2 The boot-time gap: provisioning state and job<->VM binding

**Problem.** Today `RunJob` moves a job `Q -> R` because the node already
exists and can accept it immediately. In the cloud, a VM takes minutes to boot;
if we dispatch to a not-yet-ready node and mark the job `R` today, the job is
"running" on nothing. If we keep the job `Q` and leave the booting VM
unbound, the **next scheduler cycle may dispatch that same VM to a different
job**, and the total demand estimate drifts.

**Solution: an intermediate `PROVISIONING` (a.k.a. `DISPATCHING`) state plus an
explicit, recorded job<->VM binding.**

- New job state `PROVISIONING` sits between `Q` and `R`. A job in
  `PROVISIONING` holds the reservation on a specific VM (by VM ID), occupies its
  cores logically, and is **not** offered again by the iterator.
- The scheduler records on the job: the target **VM ID** (`vm_id`), the
  provisional node name, the cores reserved, and `state=PROVISIONING`. This
  binding is the authoritative link so no other job can claim that VM while it
  boots.
- When the MOM on the new VM registers and the node is `free`, the server
  transitions the bound job `PROVISIONING -> R` and dispatches it to that node.

Two binding representations (pick one, or both; §12.3 argues for the job-side
attribute as the durable record):

- **Job attribute (recommended, durable + queryable):** new job field
  `ProvisionVM` / `ProvisionNode`, persisted in the `.JB` file (extend the
  `saveJob` writer) and visible via `qstat -f`. Survives server restart.
- **In-memory provisioning table:** `vmID -> jobID` map in server/CEC. Fast but
  volatile; keep as a secondary index, not the source of truth.

Migration/cleanup concerns:
- **Provisioning timeout:** if a `PROVISIONING` job's VM never comes up within a
  bound (e.g. 2× expected provision time), fail the reservation, return the job
  to `Q`, and release the VM binding (CRP `reclaim` the failed VM).
- **Job delete during provisioning:** `qdel` on a `PROVISIONING` job must cancel
  the binding, tell the CRP to reclaim the still-unused VM, and free the cores.

### 12.3 VM ID as the stable handle

Cloud VMs get **dynamic IPs and random hostnames**, but the **VM ID is assigned
at creation time, before boot**, while IP/hostname are known only after boot.

- Use the **VM ID (Azure resource id / AWS instance id)** as the cluster's
  stable node identity for the life of the VM.
- The CEC asks the CRP for the VM (returns `{vmID, sku, state}`) **immediately
  on `Ensure`**, before waiting for boot, so each `PROVISIONING` job can be
  bound to a concrete VM ID right away.
- The **job-side binding** (§12.2) stores `vm_id`, the durable handle that
  outlives the transient hostname.
- **Node name policy:** register the node under the VM ID (which the MOM can
  report via `$hostname` override or a `mom_node_name` in config), so
  `pbsnodes` shows a stable, unique name that maps 1:1 to a VM. This avoids
  collisions when cloud recycles hostnames and makes the job<->VM<->node mapping
  unambiguous. Map VM id -> node name in a small CEC table (`cloud_priv/nodes`).

### 12.4 Dynamic node registration with IP-range ACL (LSF-style)

A MOM on a freshly booted VM must join the cluster automatically, but auto-
registration must be safe. Mirror LSF: the server has a toggle for accepting
dynamic nodes, plus an allowed source-IP range.

- **Server config gating** (new attributes, next to `allow_node_submit` and
  `auto_node_np` which already exist):
  - `redirect_server` / `allow_dynamic_nodes` (bool, default **false**) — master
    switch: accept and auto-register MOMs that are not pre-seeded.
  - `node_allowed_ip_ranges` (list of CIDRs, e.g. `10.0.0.0/8`,
    `20.150.0.0/16`) — only MOMs whose **source IP** falls in an allowed range
    may self-register.
- **Behavior** in `handleISMessage` (`internal/server/server.go`): today an
  unknown MOM is ignored ("IS message from unknown node ... ignoring"). With
  `allow_dynamic_nodes=true` AND `remote-IP ∈ allowed ranges`, auto-register the
  node (`nodeMgr.AddNode(hostname, np=...)`), persist via `saveNodes()`, and then
  process its status. Otherwise keep ignoring.
- **np from SKU:** reuse `auto_node_np` semantics or a per-queue/mapping default
  so the node has the right core count before it fully reports.
- **First registration drives the `PROVISIONING -> R` transition:** on first
  IS contact from the bound VM, the server finds any `PROVISIONING` job bound to
  that VM ID, dispatches it (`Q/PROVISIONING -> R`), and normal scheduling
  resumes. This closes the loop in §8.
- **Safety:** only IP-range-matched MOMs auto-register; anything else is still
  ignored. Combined with the existing shared `auth_key`/IS auth, this keeps
  rogue nodes out even in a cloud with transient addresses.

### 12.5 Revised event flow (with the new state)

```
job(s) unmatchable  ──> scheduler: accumulate backlog (13.1)
       └─> NeedCapacity(queue, sku, shortfall=Σjobs)  ──> CEC
            └─> cooldown? no ──> CRP Ensure(n, sku)
                 └─> returns vmIDs immediately
                 └─> for each vmID: bind job jk → vmID (job.ProvisionVM, state=PROVISIONING)
            └─> VM boots; MOM first IS contact (source IP in allowed range) ──> server auto-registers node
                 └─> server: find PROVISIONING job bound to this VM → PROVISIONING -> R, dispatch
CEC observe NodeFree (no new job on it) for cloud_idle_time ──> NodeIdle ──> drain ──> deregister ──> CRP reclaim(vmID, policy)
```

### 12.6 New config / fields summary

- Queue: `cloud_provider`, `cloud_vm_sku`, `cloud_min_nodes`,
  `cloud_max_nodes`, `cloud_idle_time`, `cloud_reclaim` (unchanged).
- Server: `allow_dynamic_nodes` (bool), `node_allowed_ip_ranges` (CIDR list).
- Job: `ProvisionVM` (vm id string), `ProvisionNode` (name), plus the
  `PROVISIONING` state; all persisted in `.JB` and shown in `qstat -f`.
- CEC state: `running`, `inflight`, `provisioning` (vmID -> jobID), `byNode`
  (idle timers), cooldown — persisted in `cloud_priv/cec_state` for restart.


## 13. Milestone plan

- **M0 (this doc + queue attrs)**: add the `cloud_*` queue attributes end-to-end
  (model, `applyQueueAttrs`, display, persistence) — enables config, no behavior.
- **M1 (event loop skeleton + state machine)**: `pbs_sched` emits
  `NeedCapacity` using the **lookahead accumulation** of §12.1 (incl. strict FIFO);
  add the `PROVISIONING` job state and job<->VM (`vm_id`) binding (§12.2/12.3);
  CEC event loop + in-flight guard + cooldown; CRP adapter interface stubs
  (`ensure/describe/reclaim/resume/health`) that return `vmID` before boot;
  logging only, no real cloud calls.
- **M2 (Azure CRP + bootstrap)**: Azure driver (VMSS or single VMs) + cloud-init
  that installs `pbs_mom` and registers; dynamic node add/remove (§7 of prior doc).
- **M3 (scale-in + hibernate)**: `NodeFree`→idle→drain→reclaim; `hibernate` fast
  resume. **[DONE -- implemented & integration-tested, 2026-08]**
  `deallocate` live-verified; `hibernate` fast-resume + provisioning-timeout
  remain stubs (see TODO.md 4.4c).
