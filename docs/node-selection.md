# Node Selection & Host Groups

This guide covers how OpenTorque places a job on a compute node: explicit host
pinning, node properties/features, and named host groups (node pools). Both the
**built-in** scheduler (`Server.selectNodeForJob`) and the **external** scheduler
(`pbs_sched`, `Scheduler.findNodeForJob`) enforce the same rules.

---

## 1. Pin a job to a specific node — `-l host=<node>` (TODO 1.1)

Force a job onto one named node:

```bash
echo "sleep 10" | qsub -l host=worker1
qsub -l host=worker1,worker2  # (sequential layout not yet supported; see caveat)
```

- The candidate list is filtered case-insensitively by node name.
- If the target node is down or has no free CPUs the job stays `Q`.

## 2. Node properties / features — `-l feature=a,b` (TODO 1.2)

Tag nodes with arbitrary properties and require them with `-l feature=`:

```bash
# admin: mark a node as a GPU node
qmgr -c "set node gpu-host properties=gpu,fast"

# user: only run on nodes that have both properties
qsub -l feature=gpu,fast script.sh
```

- `-l feature/features/properties` are accepted synonyms; matching is a
  case-insensitive subset check (`nodeHasAllFeatures`).
- Properties are reported by `pbsnodes` and persisted in `server_priv/nodes`.

## 3. Host groups / node pools — `-l host=@<group>` (TODO 1.3)

Group nodes into named pools and schedule within a pool:

```bash
# admin: put nodes into pools
qmgr -c "set node worker1 hostgroups=gpu,fast"
qmgr -c "set node worker2 hostgroups=gpu"
qmgr -c "set node worker3 hostgroups=cpu"

# user: run only inside the gpu pool
qsub -l host=@gpu script.sh
```

- Membership is stored per node (`Node.Groups`), surfaced as the `hostgroups`
  status attribute, and persisted as `groups=` in `server_priv/nodes`.
- A job pinned to `@gpu` will **not** run on `worker3` even if it is otherwise
  free.
- Group pinning composes with feature and resource constraints (see
  [Resource Constraints](resource-constraints.md)).

## 4. Local-first dispatch (static vs. cloud nodes)

When a queue is cloud-backed, the scheduler always prefers **static local
nodes** and only falls back to auto-registered **dynamic** cloud nodes once
local capacity is exhausted. This keeps an idle local node from being
shortchanged by a still-registered rented VM. See
[Cloud Bursting](cloud-bursting.md).

---

## Reference matrix

| Capability | User flag | Admin setup | Enforced by |
|-----------|-----------|-------------|-------------|
| Host pin | `-l host=<node>` | — | both schedulers |
| Feature | `-l feature=a,b` | `qmgr set node <n> properties=a,b` | both |
| Host group | `-l host=@<grp>` | `qmgr set node <n> hostgroups=grp` | both |

## Live verification

See `docs/live-azure-verification-report.md` (sections 1.1–1.3) and the unit
tests `TestFindNodeForJobHostGroup` / node `HasGroup`.