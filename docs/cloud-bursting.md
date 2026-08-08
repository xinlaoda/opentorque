# Cloud Bursting (Elastic Cloud Pool)

Cloud bursting lets a **fixed local cluster** (statically-configured, always-on
nodes) automatically overflow onto **dynamically-provisioned cloud VMs** when
local capacity is exhausted — and just as automatically scale those cloud VMs
back down when they sit idle. The defining guarantee of OpenTorque's cloud
bursting is:

> **Local (fixed/on-prem) nodes are always preferred. Cloud VMs are created and
> dispatched only when local capacity cannot satisfy demand.**

This is the behavior the scheduler is designed and tested around: the cloud is a
*burst buffer*, never a co-primary pool that would let an idle, already-paid
local node sit unused while a rented cloud VM keeps a lease alive.

## Why local-first matters

If a cloud-backed queue still has a registered (but idle) cloud VM, and a new job
arrives, a naive scheduler could place it on the cloud node even while a local
node sits idle past its reclaim window. That would:

- keep the cloud VM alive (and incurring cost / holding a lease) unnecessarily, and
- leave the local machine stranded idle — exactly the opposite of what bursting
  is for.

To prevent this, every node carries a `Dynamic` flag (`is_dynamic=true`). Nodes
auto-registered by the Cloud Resource Provider (CRP) are marked dynamic; the
statically-configured local nodes are not. `findNodeForJob` does a *stable*
sort that puts **static (local) nodes ahead of dynamic (cloud) nodes**, only
falling back to cloud nodes when no local node can take the job. Verified in
`internal/sched/scheduler/test` (`TestFindNodeForJobLocalFirst`,
`TestFindNodeForJobCloudFallback`) and live on Azure: with a local `srv` node
and a free cloud VM both idle, a new job is dispatched to `srv`, not the cloud VM.

The same preference exists in both scheduling paths:

- **Built-in** — `internal/server/server.go` (`FindNodeForJob`)
- **External** — `internal/sched/scheduler/scheduler.go` (`findNodeForJob`)

See `AGENTS.md` (“The two scheduling paths”) for the rule that both must stay in sync.

## Architecture overview

```
                 pbs_server (job/queue/node mgmt)
                   ▲    │  dynamic node auto-registration
        DIS status │    ▼
                   │  pbs_sched ──► CEC (Cloud Elastic Controller)
                   │                    │  capacity / nodefree / nodeidle / nodedown events
                   │                    ▼
                   │                 CRP (Cloud Resource Provider) — Azure driver
                   │                    │
                   ▼                    ▼
             local nodes          cloud worker VMs (auto-registered, dynamic)
```

- **Queue `cloud_*` burst attributes** define the cloud pool per queue
  (provider, VM SKU, min/max nodes, idle time, reclaim policy, subnet, image,
  disk, ssh key, location, resource group).
- **CEC (`internal/cec`)** is a pure **event loop** — the sole orchestrator of
  cloud node membership. It reacts to `capacity`, `nodefree`, `nodeidle`,
  `nodedown` events and only *advances* existing idle windows on a reclaim
  timer; it never polls to *make* a scale-out decision.
- **CRP (`internal/crp`)** implements the `crp.Provider` interface. The Azure
  driver (`azure.go`) authenticates via the instance metadata service (MSI)
  using `AZURE_CLIENT_ID`, and creates/describes/reclaims/resumes worker VMs.

## Scale-out (burst) lifecycle

1. The scheduler detects a cloud-backed queue with demand that local nodes
   cannot satisfy and emits a `capacity` event for that pool.
2. The CEC computes the shortfall and ensures up to
   `cloud_min_nodes`..`cloud_max_nodes` VMs, provisioning
   `shortfall + headroom` (a burst cushion) — subject to a per-pool
   `cooldown` (global default **30s**) between scale-outs.
3. Each provisioned VM has a `PROVISIONING` state; the CEC binds
   `vmID → jobID` during boot so a not-yet-up VM is not mistaken for an idle
   node. A still-booting VM that cannot come up within
   `provision-timeout` (default **10m**) is reclaimed rather than leaked.
4. The VM boots with cloud-init, its `pbs_mom` auto-registers with the server,
   `is_dynamic=true`, and it becomes available as a dynamic node.

## Scale-in (reclaim) lifecycle

1. When a cloud node finishes its jobs it fires `nodefree`; the CEC records an
   `IdleSince` timestamp (idle window does **not** advance while the pool is at
   its minimum).
2. The idle sweep (every **3s**, default) reclaims any owned node idle for
   `>= cloud_idle_time`, as long as `Running > MinNodes`.
3. Reclaim follows the pool's `cloud_reclaim` policy:
   - `deallocate` (default) — **deallocate** the VM: cost-free, slow to restart.
   - `hibernate` — hibernate/resume: slightly higher cost, near-instant resume,
     tracked in the CEC `Hibernated` set so scale-out can `Resume` instead of
     re-provisioning + re-running cloud-init.
4. Before destroying a VM, the CEC **drains** it (marks it offline so the
   scheduler stops dispatching) and deregisters it. A `drain-timeout` rate-limits
   reclaim of the same node.
5. On the final destroy path, the registry **deletes the VM's network interface
   and public IP** along with the VM — so scale-in does not leak NICs/public IPs
   in the resource group (they are cleaned up after the VM delete completes).

## Configuration

A queue becomes cloud-backed when `cloud_provider` is non-empty. Example
(`qmgr`):

```text
set queue batch resources_default.nodes = 1
set queue batch cloud_provider = azure
set queue batch cloud_vm_sku = Standard_D8s_v3
set queue batch cloud_min_nodes = 0
set queue batch cloud_max_nodes = 8
set queue batch cloud_idle_time = 300
set queue batch cloud_reclaim = deallocate
set queue batch cloud_subnet_id = /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/virtualNetworks/<vnet>/subnets/<sb>
set queue batch cloud_image_id = <image-resource-id>
set queue batch cloud_disk_size = 100
set queue batch cloud_disk_type = Premium_LRS
set queue batch cloud_ssh_key = /path/to/ssh/key
set queue batch cloud_location = westus
set queue batch cloud_rg_name = <resource-group>
set queue batch enabled = True
set queue batch started = True
```

### Queue `cloud_*` attributes

| Attribute          | Meaning                                                        |
|--------------------|----------------------------------------------------------------|
| `cloud_provider`   | Cloud name: `azure` / `aws` / `""` (static pool)               |
| `cloud_vm_sku`     | VM size / family, e.g. `Standard_D8s_v3`                       |
| `cloud_min_nodes`  | Minimum worker VMs to keep                                    |
| `cloud_max_nodes`  | Maximum worker VMs to burst to                                |
| `cloud_idle_time`  | Seconds idle before a node becomes a scale-in candidate       |
| `cloud_reclaim`    | `deallocate` (default) or `hibernate`                         |
| `cloud_subnet_id`  | Azure subnet resource ID for worker VMs                       |
| `cloud_image_id`   | VM image resource ID                                          |
| `cloud_disk_size`  | OS disk size (GB); `0` = provider default                     |
| `cloud_disk_type`  | OS disk type (e.g. `Premium_LRS`)                             |
| `cloud_ssh_key`    | SSH key for the worker VMs                                    |
| `cloud_location`   | Azure region                                                  |
| `cloud_rg_name`    | Azure resource group                                          |

### Scheduler / CEC tuning

| Parameter            | Scope     | Default | Meaning                                        |
|----------------------|-----------|---------|------------------------------------------------|
| `cooldown`           | global    | `30s`   | Min delay between scale-outs                   |
| `reclaim_interval`   | internal  | `3s`    | Idle-reclaim sweep cadence                     |
| `provision-timeout`  | per pool  | `10m`   | Max wait for a booting VM before reclaim       |
| `scale_headroom`     | per pool  | `0`     | Extra VMs beyond exact shortfall (burst cushion) |
| `drain-timeout`      | per pool  | `0`     | Min interval / give-up window between reclaims  |

## Deployment note

The cloud scheduler (`pbs_sched`) needs the Azure user-assigned managed-identity
client id injected so the CRP can request a token. Under systemd this is wired via
the unit's `Environment=AZURE_CLIENT_ID=...` (see `configs/systemd/pbs_sched.service`).
Under plain run-command shells it was inherited implicitly — one reason the
daemons are deployed as proper `systemd` services.

## Related documents

- `docs/cloud-elastic-event-driven-design.md` — event model and CEC design
- `docs/cloud-elastic-node-scaling-design.md` — scale-out/scale-in design
- `AGENTS.md` — cloud elasticity invariants and the two scheduling paths
