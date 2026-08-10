# Multi-Node Placement — `nodes=N:ppn=M` / `select=` (TODO 1.4)

OpenTorque allocates **multiple distinct nodes** to a single job instead of
collapsing the request into one node. This matches MPI-style batch jobs.

---

## 1. Requesting multiple nodes

```bash
# 2 nodes, 1 processor each
qsub -l nodes=2:ppn=1 mpi.sh

# 2 nodes, 4 processors each (total 8 cores)
qsub -l nodes=2:ppn=4 mpi.sh

# TORQUE select/place syntax is also accepted
qsub -l select=2:ncpus=4
```

- `nodes=N` → allocate N distinct schedulable nodes.
- `ppn=M` → M processors per node (default 1).
- `select=N:ppn=M` and `select=N:ncpus=M` are parsed to the same node-count ×
  per-node-cores layout. Heterogeneous `+` layouts use the first chunk (see
  caveat below).

## 2. How it works

Both dispatchers must find enough **distinct** schedulable nodes, each with at
least `ppn` free processors:

- **Built-in scheduler** (`Server.selectNodesForJob` / `runJobMulti`): walks all
  nodes honoring the job's host / group / feature / generic-resource gates, picks
  `N` distinct qualifying nodes, and records a `+`-joined `exec_host`.
- **External scheduler** (`Scheduler.countSchedulableForJob` → single-node
  dispatch then `runJobMulti`): gates multi-node eligibility on the count of
  qualifying nodes before dispatching.

The job is then **dispatched to every node's MOM**, and the MOM exports:

- `PBS_NODEFILE` / `PBS_NODELIST` — one node name per line / newline-joined list
- `PBS_NODENUM` — this host's index in the allocation

## 3. Caveats

- `+`-separated heterogeneous layouts (`nodes=2:ppn=4+1:ppn=2`) are currently
  reduced to the first chunk.
- If fewer than `N` distinct nodes qualify, the job stays `Q` (it is not
  partially dispatched); this is governed by the same FIFO / backfill policy as
  any blocked head job (see [Resource Constraints](resource-constraints.md)).

## Live verification

See `docs/multi-node-test-report.md` and section 1.4 of
`docs/live-azure-verification-report.md`.