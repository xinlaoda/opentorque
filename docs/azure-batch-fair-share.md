# Fair-share on Azure Batch: a cloud-native redesign

*Fair-share scheduling was invented for a fixed, scarce, shared on-premise
cluster: "of these 500 cores, who gets their share, and nobody starves." Cloud
Batch breaks every assumption behind that — capacity is elastic, every VM-hour
is metered spend, and the platform already enforces hard quotas. This article
redesigns "fair-share" as a governance layer on top of Azure Batch: not a
share-of-the-box algorithm, but cost attribution, budget caps, capacity tiers,
and per-tenant isolation.*

---

## 1. Why classic fair-share stops making sense

Classic fair-share computes a decayed "share of a fixed machine" per account
and schedules under-users ahead. Its three goals on-prem were:

1. **Arbitration** — 10 teams fighting over the same finite cores.
2. **Protection** — priority work isn't starved (usually via preemption).
3. **Starvation avoidance** — no single account permanently monopolizes.

In the cloud each one loses its anchor:

- **The scarce thing is gone.** Capacity = "whatever you're willing to pay."
  When a team needs more cores you **scale out**, you don't say "not your turn."
- **The real scarce resource is money, not cores.** On-prem marginal compute is
  ~free (sunk capex); in the cloud every core-hour is billed. The thing to
  divide fairly is the **budget**, which a "share of the box" score can't see.
- **Preemptive QoS is now harmful.** Killing/suspending billed work throws away
  partial results and the VM still bills. You protect priority work with a
  **capacity tier** (dedicated/on-demand vs spot), not by evicting others.
- **Hard limits are the platform's job.** Subscription vCPU quotas, pool size,
  VNet isolation and budgets already guard the boundary — better than a
  hand-rolled quota engine that can't even see the money.

> Shift: **stop arbitrating scarcity; start governing spend and isolation.**
> "Fairness" = every project's bill is attributed and capped, important work uses
> a higher capacity tier, and no project consumes another's pool.

---

## 2. The cloud-native fairness model, mapped to Azure Batch

Azure Batch already gives the primitives. Fairness is **configuration +
governance on top**, not a new scheduler algorithm:

| Cloud-native "fairness" | Azure Batch mechanism |
|---|---|
| Per-tenant **isolation** | one **pool** per project (or one **Batch account** per project for max isolation) |
| **Capacity tiers** (QoS, no preemption) | per project: a **dedicated pool** + a **low-priority (spot) pool**; route tasks by priority/tier |
| **Cost attribution / chargeback** | **user-subscription allocation mode**; tag pool VMs with `project`; Azure **Cost Management** groups spend by tag |
| **Budget / quota caps** | pool **autoscale formula + max-node** (derived from budget) + per-project resource group + subscription vCPU quota + **Azure budgets** |
| Fairness within a **shared** pool | task-level **vCPU-hour × unit price** apportionment engine (when projects share a stack for economy) |

---

## 3. Reference architecture

```
   submitters / projects (account, priority=tier)
                |
                v
  +------------------------------------------------------+
  |  Governance layer  (your code, or a future Batch feature) |
  |   - admission: does this project have budget/quota left?  |
  |   - routing: task -> dedicated pool or spot pool          |
  |   - budget alerts: approaching cap => stop / downgrade    |
  +--------------------------+------------------------------+
                             |
                             v
                 Azure Batch (per-project pool pair)
  +------------------------------------------------------+
  |  projA: poolA-dedicated   poolA-spot    (autoscale)  |
  |  projB: poolB-dedicated   poolB-spot    (autoscale)  |
  |  ... each pool's max-node = budget-derived            |
  +--------------------------+------------------------------+
                             |
                             v
        Cost attribution (two layers)
         1. VMs tagged project= -> Cost Management per-tag spend
         2. task-level vCPU-hour apportionment (shared-stack case)
                            -> per-project report / reconcile
```

Design rules:

1. **Isolation before arbitration.** Prefer "one project, one pool" so a noisy
   project physically cannot crowd another — this *is* cloud fairness, and it
   removes most arbitration entirely.
2. **Max isolation option: one Batch account per project.** Each account gets
   its own resource group, quota, and budget; cost attribution is trivially
   per-account, and a misbehaving project can't consume another's quota or get
   shut down together. Trade-off: more accounts to operate.
3. **Shared-stack option (economy).** When many small projects share one pool
   to save cost, allocate the pool's real bill to projects by their share of
   used vCPU-seconds — the same "node = cost pool, apportion by usage" model as
   finops chargeback.

---

## 4. What Azure Batch itself still needs (product gaps)

To make this native and out-of-the-box, Azure Batch lacks several things:

1. **Native per-pool / per-project cost attribution.**
   In default **Batch Service** allocation mode, pool VMs are invisible to your
   subscription and costs roll up to the whole Batch account — no per-pool or
   per-project breakdown. Today you must use **user-subscription** mode and
   manually tag VMs. **Gap:** Batch should expose per-pool / per-project cost
   breakdown into Cost Management even in Batch Service mode, without manual
   tagging.

2. **Per-project budget/quota admission control.**
   Today you can cap a pool's max nodes, but there is no scheduler-level guard
   such as "this project has no budget left, reject/downgrade new submissions."
   **Gap:** a per-project spend budget/quota enforced at job/task submission
   (reject or drop to the spot tier on overage).

3. **Task-level cost telemetry & apportionment primitive.**
   Batch tracks task lifecycle but not "vCPU-seconds consumed per task/project"
   nor node up/down (billed) times, so idle/overhead can't be attributed.
   Users must assemble events themselves. **Gap:** billed telemetry — per-task
   vCPU-seconds and per-node start/stop times — plus an official apportionment
   API for shared pools.

4. **Per-project tiered capacity quotas (dedicated vs spot).**
   You can size each pool, but there's no first-class "project A may have at
   most X dedicated cores and Y low-priority cores." **Gap:** a tiered capacity
   quota per project — the concrete embodiment of "fairness."

5. **Eviction-aware effective cost.**
   Spot is cheap but gets preempted/requeued. If fairness is judged by the raw
   bill, a project living on spot looks falsely cheap and is over-allocated.
   **Gap:** per-project eviction count + requeue cost, so "effective cost"
   (not the raw bill) is what fairness is measured against.

6. **Budget-aware autoscale.**
   Autoscale today sizes by capacity metrics (pending tasks, CPU). **Gap:** a
   "budgeted autoscale" that scales out only while the projected cost stays
   within the remaining budget — the scaling upper bound comes from money, not
   task backlog.

7. **(Enabling) Per-project priority semantics in a shared stack, and closer
   binding of retry/eviction to the QoS tier.**

---

## 5. Building it today: an external fair-share orchestrator on Azure Batch

The previous sections describe the *shape* of cloud fairness. This section is
the practical "can I build it with the APIs and events Azure Batch exposes
today" answer. **Yes — and the recommended architecture is to run your
fair-share logic in an external control plane and treat Batch as a
scalable executor.** Batch already supplies every primitive you need: you use
its APIs to place/scale/schedule, and its events as the accounting feed. What
you must build yourself is the fairness business logic (admission, budget,
apportionment, effective cost), which is exactly the part that should be yours.

### 5.1 Component and data flow

```
   submitters
      |
      v
 +------------------------------------------------------------+
 |  Your external fair-share orchestrator (control plane)       |
 |   - admission: account/quota/budget left?                    |
 |   - routing:   pick per-project pool + tier (dedicated/spot) |
 |   - scale:     pool resize (you are the autoscaler)          |
 |   - bookkeeper: task usage, evictions, idle, effective cost  |
 +----------+---------------------------+----------------------+
      | submit                | listen                  | cost
      v                       v                          v
  Batch task/job          Event Grid /               Cost Management
  + pool resize           Log Analytics              (PoolName tag / RG)
                          (Batch events)
```

### 5.2 API / event surface you can drive today

**Control (call)** — `az batch` / Batch SDK / Batch REST:

| capability | API |
|---|---|
| submit work to a project's pool | `POST jobs/{job}/tasks` (also `jobs` create/update, `task priority`) |
| scale a pool | `pool resize` (and `EvaluateAutoScale` to dry-run a formula without applying it) |
| orchestrate lifecycle | `job terminate/disable`, `task terminate`, `node reboot/reimage`, `pool patch` (metadata/tags) |
| read authoritative state | `pool get`, `job list/get`, `task list/get`, `node list/get` |

**Events (subscribe via diagnostic settings → Event Grid / Log Analytics)**
primary batch events used as the accounting feed:

| event | what it gives your bookkeeper |
|---|---|
| `TaskStartEvent` / `TaskCompleteEvent` / `TaskFailEvent` / `TaskRetryEvent` | task begin/end timestamps → **vCPU-seconds** per task/project (× known VM size) |
| `JobScheduledEvent` | a job became schedulable (queue depth trends) |
| `PoolResizeCompleteEvent` / `PoolStartEvent` / `PoolDeleteEvent` | node up/down windows → **idle/boot billing** per pool |
| low-priority node preemption event (`NodePreempted`-class) | **spot evictions** → effective-cost accounting |
| node state events | reboot/reimage/maintenance windows |

**Metrics** — Azure Monitor: per-pool/per-node CPU, `vcpu`, node count for
live scaling signals. **Cost** — Cost Management queries grouped by the
per-pool resource group or the auto `PoolName` tag (user-subscription mode,
verified: Batch auto-tags each pool RG with `PoolName` + `BatchAccountName`).

### 5.3 Pool resize state machine (you are the autoscaler)

Driving `resize` from your orchestrator instead of Batch's hosted autoscale
formula is the cleanest path (formulas are a small fixed function set; your
policy is not). State machine to handle the async nature:

```
IDLE/steady ──resize(target)──► RESUMING (async) ──PoolResizeCompleteEvent┐
    ▲                                     │  or poll allocationState        │
    └────────── steady ◄──────────────────┘  until Steady                    │
                         (provision/scale-in continues in the background)  ─┘
on FAILURE (AllocationFailed / quota):  → backoff, alert, downgrade tier
on eviction spike:                     → resize spot pool, retry tolerant tasks
```

Rules to encode:
- One in-flight resize per pool; coalesce rapid calls (or use `autoscale` on
  the pool with the effective formula if you prefer debounce built-in).
- Scale-in only after a **grace / drain** period so in-flight tasks finish and
  spot evictions don't lose re-runnable work.
- Track your resize target vs `currentDedicatedNodes`/`currentLowPriorityNodes`
  as the source of truth for idle-cost attribution.

### 5.4 Accounting mapping (event -> per-project usage)

```
core_seconds(project, node) += (task_complete_ts - task_start_ts) * vcpu_used
node_billed_hours: from PoolStart/PoolResizeComplete/Delete events (your resize history)
idle_hours(node)  = billed_hours - busy(node-derived core-seconds/free capacity)
eviction_cost     = per preempted spot task: retried work + wasted partial run
apportion: node_bill split to projects by share of used core-seconds
           (idle/boot -> pro-rata; billed-with-no-usage -> overhead bucket)
```

This mirrors the finops "node = cost pool, apportion by usage" model from
`docs/cloud-costing.md`-style chargeback.

### 5.5 Reconciliation — don't trust the event stream alone

Batch events are **at-least-once, unordered, and can be lost**. Treat them as a
fast-path accelerator, not as the source of truth:

- **Authoritative state** = `task list` / `node list` / `pool get` polls, on
  a safety-net interval.
- Reconcile counters (running/allocation/idle) from polls and diff against
  event-derived values; correct drift on `TaskComplete`/`PoolResizeComplete`.
- Deduplicate events by idempotent keys (task id + recorded event timestamps)
  so an idle/duplicated event can't double-count vCPU-seconds.
- On orchestrator restart, rebuild state from Batch polls + your persisted
  ledger, then resume event consumption.

### 5.6 What you must build (Batch gives the primitives, not the fairness)

1. Submission gate: account/quota/**budget** admission (reject or downgrade to
   the spot tier when a project is over).
2. Router: project -> per-project pool + tier, with per-project
   dedicated/spot capacity caps.
3. Bookkeeper: task vCPU-second computation, idle attribution, eviction
   **effective cost**, and the apportionment split.
4. The fairness policy itself (your score / caps / priority rules).

### 5.7 Net assessment

- **Fully implementable today** with Batch's control APIs + event feed +
  Cost Management, especially in **user-subscription** mode where Batch
  auto-tags each pool's resource group with `PoolName` for per-pool cost.
- The honest gaps are the four things in 5.6 (budget admission, task-level
  apportionment telemetry, eviction effective cost, and the policy) — and
  these are rightly *your* control plane, not Batch's job.

---

## 6. Bottom line

"Fair-share on Azure Batch" is **not a Batch scheduling algorithm** — Batch's
pools, autoscale, spot pools, priority and tagging already supply the building
blocks. The real work is a governance layer that maps the new fairness to them:

- **isolate** projects into their own pools (or accounts),
- **tier** capacity (dedicated vs spot) instead of preempting,
- **cap** each project by budget / quota (pool max-node + platform quota +
  Azure budgets),
- **attribute** and apportion the bill (tags → Cost Management; task-level
  vCPU-hour apportionment for shared stacks),
- **measure fairness on effective cost**, including spot evictions.

The three product gaps that would make this "native" are #1 (per-project/pool
cost attribution without manual tagging), #2 (per-project budget admission),
and #3 (task-level cost telemetry + apportionment API). Close those and
fair-share in the cloud becomes an out-of-the-box finops governance model —
not a share-of-the-box score.