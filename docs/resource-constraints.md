# Resource Constraints — generic named resources & backfill (TODO 2.1, 2.2)

OpenTorque understands `ncpus`, `mem`, `walltime`, and `nodes`. On top of those,
it now supports **arbitrary named resources** (GPU / accelerator counts, license
counts, or any admin-defined integer resource) plus **backfill** so an
unsatisfiable head job does not needlessly block the queue.

---

## 1. Generic named-resource constraints (TODO 2.1)

### 1.1 Declare capacity on a node (admin)

Any integer resource can be assigned to a node as `resources_available.<name>`:

```bash
# worker1 has 4 GPU slots; srv has 3 floating license seats
qmgr -c "set node worker1 resources_available.gpu = 4"
qmgr -c "set node srv     resources_available.license = 3"
```

- Capacity is surfaced by `pbsnodes` as `resources_available.gpu = 4` and
  persisted as `gres.gpu=4` in `server_priv/nodes` (reloaded on restart).
- Any non-built-in `Resource_List` name is treated as a generic resource.

### 1.2 Request a resource (user)

```bash
# run only on a node with at least 2 free GPU slots
qsub -l gpu=2 training.sh

# run only on a node with at least 1 license seat
qsub -l license=1 sim.sh
```

### 1.3 Enforcement & accounting

- A node is schedulable for a job only if
  `resources_available.<name> − gres_used.<name> ≥ request`.
- `gres_used.<name>` is the sum of generic-resource requests across the node's
  running (assigned) jobs and is reported by `pbsnodes`; it returns to `0` when
  the jobs complete.
- Both the **built-in** (`Server.nodeHasGRes`) and **external**
  (`Scheduler.nodeHasGRes`) schedulers gate placement on this check.
- Generic-resource constraints compose with host groups (`-l host=@group`),
  node features (`-l feature=`), and CPU availability — a node must satisfy
  **all** of them.

### 1.4 Example

```bash
# admin
qmgr -c "set node worker1 resources_available.gpu = 2"
qmgr -c "set node worker1 hostgroups = gpu"

# user
qsub -l gpu=1,host=@gpu train.sh   # runs on worker1 (gpu + group both met)
qsub -l gpu=100,host=@gpu train2.sh # stays Q (group met, gpu over capacity)
```

> Note: an unsatisfiable gres request keeps the job in `Q` — it does **not**
> provision a cloud node, and (like any permanently-blocked head job) it relies
> on backfill below for later fittable jobs to proceed.

## 2. Backfill / non-strict FIFO (TODO 2.2)

By default the schedulers **do not** hold the whole queue behind one blocked
head job — later jobs that fit the current free capacity run in the gap.

- `sched_config` knob `backfill` (default **on**): with backfill on, even
  `strict_fifo` continues to later fittable jobs instead of stopping at the
  first unrunnable job (`Scheduler.strictStop`); with backfill off, strict FIFO
  halts as before.
- The built-in scheduler already iterates every queued job and dispatches
  anything that fits, so it backfills naturally.

```text
# $PBS_HOME/sched_priv/sched_config
scheduler_mode: external
backfill: true      # (default)
strict_fifo: true   # only meaningful when backfill is off
```

Example — head job `gpu=3` cannot run (capacity is 2); a later `gpu=1` job is
still dispatched to the free node (and the head job stays `Q`):

```bash
qsub -l gpu=3 blocked.sh   # stays Q
qsub -l gpu=1 fits.sh      # runs anyway (backfill)
```

> Open items: advance job **reservations** (future start time) and
> **preemption** are not yet implemented — see TODO 2.2.

## Live verification

Generic resources + host-group interplay + accounting + backfill were verified
on Azure westus3 (external scheduler). See section 2.1 of
`docs/live-azure-verification-report.md`.