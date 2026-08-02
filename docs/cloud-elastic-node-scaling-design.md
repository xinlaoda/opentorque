# OpenTorque Cloud Elastic Node Scaling — Design

Status: Draft proposal
Scope: External cloud resource provider + queue-driven elastic node up/down
Owner: xin
Date: 2026-08-01

> **2026-08-02 revision note:** §8 of this document described a fixed-tick
> (30–60 s) polling policy for computing the desired pool size. That polling
> approach is **superseded** by the event-driven model in
> `docs/cloud-elastic-event-driven-design.md`: scale-out/reclaim are now driven
> by real scheduler/cluster events (`NeedCapacity` from `CanNotRun`, `NodeFree`/
> `NodeIdle`/`NodeDown`) instead of a periodic timer. The queue-attribute model,
> CRP architecture, dynamic node add/remove, and reclaim semantics in this
> document remain valid and are reused; only the *when to scale* logic changes
> to event-driven.

---

## 1. Overview

OpenTorque currently schedules jobs against a **static** set of `pbsnodes`
managed by `pbs_server` (nodes are seeded from `server_priv/nodes`, created via
`qmgr create node`, and kept alive by MOMs that report status over the IS
protocol). There is no cloud elasticity: node VMs must be provisioned,
started, and destroyed manually.

This document proposes a **queue-driven, external cloud resource provider**
architecture that lets a queue declare a cloud backend and dynamically scale the
number of compute-node VMs between a minimum and maximum, based on the demand of
jobs sitting in that queue. Idle nodes are reclaimed after a configurable idle
time, either by **deallocating** (recreate fresh next time) or **hibernating**
(fast resume for immediate dispatch).

The design keeps OpenTorque's scheduler and server unchanged in spirit; the new
capability is delivered by an **external orchestrator process** (the Cloud
Resource Provider) that reads queue configuration and job state, talks to a
cloud API (Azure/AWS/...), and asks `pbs_server` to add/remove nodes.

---

## 2. Goals and Non-Goals

### Goals
- Define queue-level cloud attributes (provider, VM SKU, min/max, idle time,
  reclaim policy).
- Add an external Cloud Resource Provider (CRP) process that is selected by
  cloud-provider name and drives node lifecycle.
- Support dynamic node add/remove in the cluster (register + deregister
  compute nodes while server is running).
- Support both reclaim policies: `deallocate` and `hibernate`.
- Work with both scheduler modes (`builtin` and `external`).

### Non-Goals
- Reimplement a cloud SDK in OpenTorque core; the CRP is a separate process.
- In-place autoscaling of an already-running VM's size/family (VM SKU is fixed
  per queue; horizontal scaling only).
- Multi-queue shared-node elasticity policies (each queue manages its own pool)
  — can be layered later.
- Detailed satellite/drain semantics beyond what is described here (future).

---

## 3. Terminology
| Term | Meaning |
|------|---------|
| CRP | Cloud Resource Provider — external process that provisions/tears down node VMs |
| Pool | The set of compute-node VMs managed for a single queue + provider + SKU |
| SKU | Cloud VM size/family (e.g. `Standard_D8s_v3`, `p4d.24xlarge`) |
| min | Minimum nodes to keep in the pool (never scale below) |
| max | Maximum nodes the pool may reach (never scale above) |
| idle time | Seconds a node stays idle (no assigned job) before it may be reclaimed |
| reclaim | Action taken on an idle node: `deallocate` or `hibernate` |
| MOM | The `pbs_mom` agent running on each compute node |

---

## 4. Queue-Level Cloud Attributes

New queue attributes (consistent with the existing attribute style, applied via
`applyQueueAttrs` and exposed by `formatQueueStatus` / `qmgr p q`):

| Attribute | Type | Default | Meaning |
|-----------|------|---------|---------|
| `cloud_provider` | string | (empty) | Cloud platform name, e.g. `azure`, `aws`. Empty = static queue (no elasticity). The CRP registry maps this name to a running provider process. |
| `cloud_vm_sku` | string | (empty) | VM size/family, e.g. `Standard_D8s_v3`. Node base `np`/resources can be derived from the SKU's published spec. |
| `cloud_min_nodes` | int | 0 | Minimum nodes to keep **healthily available** (even with no jobs). Union with currently-running nodes; never scale below this. |
| `cloud_max_nodes` | int | 0 | Maximum nodes allowed in the pool (0 = unbounded up to account limits). Hard cap on concurrent VMs. |
| `cloud_idle_time` | int | 300 | Seconds a node may be idle before the CRP considers it reclaimable. |
| `cloud_reclaim` | enum | `deallocate` | `deallocate`: release the VM and register a **fresh** node next time; `hibernate`: stop/deallocate-and-deallocate-as-hibernation so the VM resumes quickly (e.g. Azure `PowerState/deallocated` vs. AKS-style hibernate, or AWS `stop`). |

Rationale for naming: attributes are namespaced with a `cloud_` prefix to avoid
colliding with future/other queue attributes and to make `qselect`/
administration obvious.

When `cloud_provider` is empty, the queue behaves exactly as today (static,
manual node management).

---

## 5. High-Level Architecture

```
                                  +----------------------+
  qsub --q <queue>                |   pbs_server         |
   job with -l resources  --->    |  +-----------------+ |
                                  |  | builtin sched   | |   (mode=builtin)
                                  |  +-----------------+ |
                                  |  +-----------------+ |
                                  |  | node manager    | |--- pbsnodes
                                  |  +-----------------+ |
                                  +-----------+----------+
                                     Register/unregister nodes (qmgr/pbsnodes RPC)
                                              |
  +-------------------------------------------+---------------------------+
  |            Cloud Elastic Controller (external)                        |
  |  +----------------------+      +----------------------+               |
  |  | Demand/Autoscale     |      | Cloud Resource       |               |
  |  | Logic (reads qstat)  |----->| Provider (CRP)       |               |
  |  +----------------------+      |  azure / aws / ...   |               |
  |        idle tracking           +-----------+----------+               |
  +-------------------------------------------+---------------------------+
                                              |
                                    +---------v--------+
                                    |  Cloud API       |
                                    |  (provision/     |
                                    |   start/stop/    |
                                    |   deallocate VM) |
                                    +------------------+
                                              |
                                  +-----------v-----------+
                                  |   Compute-node VM(s)  |
                                  |  runs pbs_mom, joins  |
                                  |  pool, reports IS     |
                                  +-----------------------+
```

Key idea: **the server stays the source of truth for what jobs exist and what
nodes are known.** The external controller is the *intelligence*: it reads queue
attributes + job demand, computes the desired pool size, drives cloud VMs via
the CRP, and registers/deregisters nodes with the server.

---

## 6. Components

### 6.1 Cloud Elastic Controller (CEC) — orchestrator/loop
A long-running process (service) that, per scheduling tick (e.g. every 30–60s):

1. Enumerate queues whose `cloud_provider` is non-empty.
2. For each such queue, read its `cloud_*` attributes and current node/job state.
3. Compute the **desired pool size** (see §8).
4. Diff `desired` vs. actual running pool → expand / shrink.
5. For expansion: instruct the CRP to provision `n` VMs of the SKU, wait for
   each MOM to register, then confirm the node is known + free.
6. For shrink: mark candidates idle, wait `cloud_idle_time`, drain (release
   jobs), deregister the node from the server, then instruct the CRP to
   `deallocate` / `hibernate` the VM.
7. Persist last-action timestamps / cooldown state (to avoid flapping).

The CEC is the only component that changes node membership and calls cloud
APIs. It may be a single binary with subcommands or a plugin per cloud.

### 6.2 Cloud Resource Provider (CRP) — per-cloud driver
Resolved by `cloud_provider` name via a registry:

| Provider name | Driver | Capabilities |
|---------------|--------|--------------|
| `azure` | Azure driver (az CLI / REST / SDK) | provision VMSS or single VMs, start, stop (hibernate), deallocate, custom-data/cloud-init to install+start `pbs_mom` |
| `aws` | AWS driver (EC2) | run instances, start/stop, terminate, user-data to install+start `pbs_mom` |
| `generic` | DRMAA/script hook | for non-cloud or test harness |

Driver responsibilities:
- `Ensure(n, sku, readyCheck)` — provision `n` VMs of the SKU, each with a
  user-data/cloud-init script that installs OpenTorque MOM and points it at the
  cluster server (`$pbsserver` + shared `auth_key`).
- `Describe(sku)` — list current VMs and their power state.
- `Reclaim(vm, policy)` — `deallocate` (delete/release) or `hibernate`
  (stop-and-keep-disks so a later `start` is fast).
- `Resume(vm)` — start a hibernated VM back up.

A process *name registry* maps `cloud_provider` → a running external service
(e.g. `localhost:port` of a CRP instance). This literally satisfies
"communicate with the external provider process corresponding to the provider
name".

### 6.3 Compute-node bootstrap (cloud-init)
Each spawned VM must, at first boot:
1. Install OpenTorque `pbs_mom` (copy package / image).
2. Write mom config: `$pbsserver <server-ip>`, `$clienthost`, shared
   `auth_key`.
3. Configure node resources (`np` derived from SKU), optional `properties`.
4. Start `pbs_mom`; it connects to the server, reports IS status → node becomes
   known (`pbsnodes`).

The node may be pre-registered with the server (see §7) before the MOM connects,
or discovered on first report (server-side option).

---

## 7. Dynamic Node Add / Remove in the Cluster

OpenTorque already supports adding nodes at runtime via `qmgr create node`
(after recovering `server_priv/nodes`), and MOMs report state via IS messages.
For elasticity we add a clean, explicit control surface (either reuse `qmgr` or
a small server RPC):

### 7.1 Add (scale up)
- Optional: pre-register the node so it exists before the MOM connects:
  `server_priv/nodes` entry or `qmgr create node <name> np=<n>` (idempotent).
- MOM boots, connects, and the node transitions to `free` (`state=free`) once it
  reports. Scheduler (`builtin`/`external`) can then dispatch to it like any
  other node.

### 7.2 Remove (scale down)
Order matters to avoid dispatching to a node we are about to tear down:
1. Mark node `offline` (`pbsnodes -o <node>` / node manager) so the scheduler
   stops selecting it for new jobs.
2. Wait for all running jobs to finish (drain); enforce with a drain timeout.
   (`qrerun`/migrate is future work.)
3. Deregister: remove the node from the server (`qmgr delete node` / RPC), and
   remove its `server_priv/nodes` line so it does not reappear after a server
   restart.
4. Hand the VM to the CRP for `deallocate` / `hibernate`.

If a VM disappears abruptly (crash, spot eviction), the server's node health
check already marks it `down`; on next reconcile the CRP converges to the
desired count.

---

## 8. Scaling Policy (demand + autoscale)

The **desired pool size** for a queue is computed per tick from:

```
runningJobs   = jobs in the queue currently R (count toward need)
queuedJobs    = jobs in the queue currently Q (waiting)
nodeCores     = cores per node (from SKU np we expose)
coresNeeded   = Σ over queued/running jobs of max(1, job.ncpus/nodes)
baseNodes     = cloud_min_nodes
demandNodes   = ceil(coresNeeded / nodeCores)          // minimum to satisfy demand
pendingExtra  = ceil(queuedJobs / queueMaxRunningPerNode)  // keep room for concurrency
desired       = clamp(max(baseNodes, demandNodes), cloud_min_nodes, cloud_max_nodes)
```

- `cloud_min_nodes` guarantees a ready floor (e.g. always keep 1 node hot).
- `cloud_max_nodes` caps cost and concurrency.
- Cooldown: only act in one direction per tick and require a stable desired for
  N ticks before a scale-out / scale-in decision (prevents flapping).
- Optionally weight small jobs differently (packing on existing free cores is
  preferred before adding a node).

### 8.1 Scale-out triggers
- A queued job cannot be scheduled because no node has enough free cores (or
  cores are full), and `desired < cloud_max_nodes` → provision more.

### 8.2 Scale-in / reclaim triggers
- A node has zero assigned jobs for `cloud_idle_time` seconds **and** the
  remaining pool ≥ `cloud_min_nodes` → candidate for reclaim.
- Reclaim picks the least-loaded / least-recently-used candidates first and
  drains them.

---

## 9. Idle Detection & Reclaim Lifecycle

The CEC tracks per-node idle time = now − (time since last job left that node).
Source of truth: `pbsnodes` `jobs=`/`state=free` + accounting (`E` records) +
server state. Practical rule: a node is "idle" when `pbsnodes` shows
`state = free` with `jobs = ` for a continuous `cloud_idle_time` window.

Lifecycle for a reclaimable node:
```
RUNNING (free, no job)
   → idle timer starts (first free moment)
   → idle_time elapsed AND pool > min
   → Drain: mark offline, await job-completion/drain timeout
   → Deregister from server
   → CRP Reclaim:
        policy=deallocate  → release VM; node gone; future scale-up creates fresh VM
        policy=hibernate   → VM stopped (disks kept); node record retained as
                             'hibernated'; future scale-up = fast Resume(start)
```
Hibernate keeps the VM record so a scale-up can `start` it in seconds rather
than provisioning (which may take minutes for large/GPU SKUs).

---

## 10. Cloud Provider Registry & Communication

- A registry maps `cloud_provider` name → endpoint of a CRP process.
  e.g. config file or env:
  ```
  cloud_provider: azure → azure-crp:9100
  cloud_provider: aws   → aws-crp :9101
  ```
- CEC ↔ CRP communication: a small JSON-REST or gRPC interface (scheme in §13
  Appendix). Minimal calls:
  - `Ensure(n, sku, state)` 
  - `Describe(sku)`
  - `Reclaim(vm, policy)`
  - `Resume(vm)`
  - `Health()`
- The provider name on the **queue** (`cloud_provider`) selects which CRP is
  used — matching the user's requirement #1.

---

## 11. Configuration & Data Model Changes

### 11.1 Queue model (`internal/queue/queue.go`)
Add fields:
```go
CloudProvider string   // "azure" | "aws" | "" (static)
CloudVMSKU    string
CloudMinNodes int
CloudMaxNodes int
CloudIdleTime int
CloudReclaim  string // "deallocate" | "hibernate"
```

### 11.2 Attribute handling (`internal/server/server.go` `applyQueueAttrs`)
Add cases:
```go
case "cloud_provider": q.CloudProvider = a.Value
case "cloud_vm_sku":   q.CloudVMSKU = a.Value
case "cloud_min_nodes": fmt.Sscanf(a.Value, "%d", &q.CloudMinNodes)
case "cloud_max_nodes": fmt.Sscanf(a.Value, "%d", &q.CloudMaxNodes)
case "cloud_idle_time": fmt.Sscanf(a.Value, "%d", &q.CloudIdleTime)
case "cloud_reclaim":   q.CloudReclaim = strings.ToLower(a.Value)
```
Also expose them in `formatQueueStatus` and persist via the queue persistence
writer (so they survive restart).

### 11.3 CEC configuration (its own file, e.g. `cloud_priv/cec_config`)
- Provider registry (name → CRP endpoint)
- Global cooldown / tick interval
- Draining timeout, max inflight provisions
- Cloud credentials location (or use VM-managed identity / instance role)

---

## 12. Scheduler Integration

The existing schedulers need **no change** for the core loop, because they only
see nodes that exist. To make elasticity effective:

- **external `pbs_sched`**: unchanged; it dispatches to whatever nodes the
  server knows. The CEC ensures nodes exist/are removed as demand changes.
- **builtin `scheduleJob`**: unchanged; uses `nodeMgr.FindNodeForJob`.
- Optional enhancement: expose a `NodeNotFound`/`insufficient capacity` signal
  so the CEC can proactively scale out — but polling `qstat -Q`/`pbsnodes` is
  sufficient for v1.

CEC reads job/queue state via the same client (`qstat`, `qselect`,
`pbsnodes`) or a server status RPC to avoid shelling out.

---

## 13. Appendix: Suggested CRP Interface (JSON-REST)

```
POST /ensure        {sku, count, state:"hibernate"|"running"} -> {vms:[...]}
GET  /describe?sku=  -> {vms:[{id,host,powerState,momUp}]}
POST /reclaim       {vm:"id", policy:"deallocate"|"hibernate"}
POST /resume        {vm:"id"}          // start a hibernated VM
GET  /health        -> {"ok":true}
```

Hib-like state model for a VM: `running`, `hibernated`, `released`.

---

## 14. Open Questions / Future Work
- Multi-queue node sharing (one cloud pool serving several queues).
- Queue-Assignment of a node `properties` for placement (e.g. GPU pools) —
  ties into the open `hostlist`/`features_required` gaps in TODO.md.
- Preemption / backfill combined with elasticity (scale-down under contention).
- Spot/preemptible capacity for scale-down-able workers.
- Metric-driven autoscale (utilization) in addition to queue-depth-driven.
- Failure semantics when the CRP itself is down (degrade to static, alarm).

---

## 15. Summary
Deliver an **external Cloud Elastic Controller + pluggable Cloud Resource
Provider**. Queue attributes `cloud_provider`, `cloud_vm_sku`,
`cloud_min_nodes`, `cloud_max_nodes`, `cloud_idle_time`, `cloud_reclaim`
describe each elastic queue. The CEC computes desired pool size from job demand,
calls the provider to provision/start/stop/deallocate VMs, and dynamically
registers/deregisters nodes with the server. This matches the requested
behavior: provider-name-based communication, SKU per queue, min/max bounds,
idle-time reclaim, and deallocate-vs-hibernate reclaim policies — with
*dynamic node add/remove* handled by reusing the existing MOM registration and
node-manager interfaces.
