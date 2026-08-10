# Queue Policy — placement, binding & admission (TODO 1.5, 1.6)

This guide covers the per-queue controls that shape **where** a queue's jobs can
run and **who** can submit to it.

---

## 1. `naccesspolicy` — pack or exclusive nodes (TODO 1.5)

Controls how many jobs may run on a node simultaneously.

| Value | Meaning |
|-------|---------|
| `shared` (default) | Pack multiple jobs onto a node as long as free CPUs allow |
| `exclusive` | Run **one job per node** (`singleuser` is treated the same) |

```bash
qmgr -c "set queue batch naccesspolicy = exclusive"
```

When `exclusive`, once a node is running one job it is taken out of the
schedulable pool for the rest of that queue's cycle until the job completes.

## 2. Queue `hostlist` — bind a queue to a node pool (TODO 1.6)

Restrict which nodes a queue may schedule onto (node-pool binding):

```bash
# only worker1 and worker2 may serve this queue
qmgr -c "set queue gpuq hostlist = worker1,worker2"
```

- `queueNodeOK` returns false for any node not in `hostlist`, so the queue will
  never place a job outside the pool — **even if the pool has no free capacity**
  (it will wait rather than overflow elsewhere).

## 3. Submission-host ACL — `acl_host_enable` + `acl_hosts` (TODO 1.6)

Limit which client hosts may submit to a queue:

```bash
qmgr -c "set queue batch acl_host_enable = True"
qmgr -c "set queue batch acl_hosts = 10.0.0.5,10.0.0.6"
```

- When enabled and `acl_hosts` is non-empty, the submitting client host must be
  listed or the job is rejected at admission (`acl_host_enable = False` or an
  empty list allows anyone).
- The submit host is resolved from the **`PBS_O_HOST`** job environment, with a
  fallback to the job owner's host. Using `PBS_O_HOST` is important: direct
  `qsub` does not populate `Job_Owner`'s host, so a naive check would reject even
  allow-listed local hosts (fixed in `e4c8899`).

## 4. Reference

| Control | Applies to | Effect |
|---------|-----------|--------|
| `naccesspolicy` | node sharing | one job per node vs. packing |
| `hostlist` | node placement | restrict schedulable nodes |
| `acl_host_enable` / `acl_hosts` | submission | allow/deny by client host |

All three are enforced by both the built-in and external schedulers (or at
admission for the ACL) and persist across restart. See section 1.5/1.6 of
`docs/live-azure-verification-report.md` for live results.