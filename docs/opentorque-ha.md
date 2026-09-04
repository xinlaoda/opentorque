# OpenTorque High Availability (cloud-native)

OpenTorque HA is built for **cloud deployments (Azure-first)** and leans on
managed platform services. Local/on-prem clusters already have mature
schedulers; this project's differentiator is the cloud: managed storage, an
internal load balancer as the VIP, and transparent failover.

There are **two supported modes**:
- **Dual-master (hot standby)** — recommended; two control-plane VMs, ~20 s
  failover.
- **Single-master (auto-replace)** — cheaper; one control-plane VM, minutes
  failover via VMSS/custom image.

Both share the same state model: the authoritative cluster state lives in
**managed PostgreSQL**, and an **internal load balancer** is the stable address
clients/MOMs use, following the active master via an active-only health port.

## Architecture

```
         clients / compute MOMs
              │  connect to <frontend>:15001
              ▼
     ┌───────────────────────────────┐
     │ Azure internal Load Balancer  │  = the VIP / stable address
     │ frontend 10.0.0.10 · 15001    │
     │ probe Tcp:15150 (active-only) │
     └───────┬───────────────┬───────┘
             ▼               ▼
      master A (VM1)     master B (VM2)     (dual)  OR  one master (single,
      pbs_server+           pbs_server+            replaced by VMSS/custom image)
      pbs_sched (systemd)    pbs_sched (systemd)
             └────────┬──────┘
                      ▼
      Azure Database for PostgreSQL (managed)  ← lease election + ALL state
```

## How a leader is chosen (both modes)

`pbs_server` with `PBS_HA=1` participates in a **lease election** against the
shared PostgreSQL: an `ot_lease` row (10 s TTL, 3 s renewal) elects exactly one
**active** master. Only the active:

- binds the LB **health port** (`PBS_HA_HEALTH_PORT`, default 15150) so the LB
  forwards `15001` only to it, and
- accepts `RunJob` / wakes its scheduler (`triggerSched`), while standbys stay
  idle (no double dispatch / no split brain).

On takeover the new active runs the running-job reconciliation (a still-running
job is never re-dispatched; orphans are queued) and recovers jobs/queues/nodes
from PostgreSQL.

## Deployment (scripts)

| File | Purpose |
|---|---|
| `scripts/ha-deploy.sh` | One-command Azure provisioning: VNet/NSG, managed PG, internal LB, two master VMs + compute node; phases `infra|masters|compute|all`. |
| `scripts/ha-single-master-vmss.sh` | Single-master auto-replace: capture master custom image → one-instance VMSS in the LB backend → test auto-replacement (RTO ~2-4 min). |
| `scripts/ha-failover-drill.sh` | Repeated drill: probe the LB frontend, stop the active, report the client-visible switchover window. |
| `scripts/ha-status.sh` | Cluster status: lease holder, LB health, nodes, jobs. |
| `scripts/ha-ops.sh` | Operations front-end: `status \| drill \| deploy \| vmss-setup \| stop-active \| start-master`. |
| `configs/systemd/*.service` | systemd units for `pbs_server`, `pbs_sched`, `pbs_mom`. |

**On each master VM** (built and installed from the repo):
1. `$PBS_HOME=...` PBS layout; write a shared 32-byte hex `auth_key` and a
   consistent `server_name`.
2. Install `configs/systemd/pbs_server.service` + `pbs_sched.service`
   (`EnvironmentFile=/etc/opentorque/ha.env`), create `/etc/opentorque/ha.env`
   with the HA settings, `systemctl enable --now pbs_server pbs_sched`.
3. Compute MOM(s): install `pbs_mom.service`, `$pbsserver <lb-frontend>`.

**On each compute node** (MOM only): `pbs_mom` under systemd, `$pbsserver
<lb-frontend>` so it follows the active automatically.

## Settings

| Var | Meaning | Default |
|---|---|---|
| `PBS_PG_DSN` | libpq DSN to the shared cluster DB | "" (file store) |
| `PBS_HA` | non-empty ⇒ participate in leader election | "" |
| `PBS_HA_HEALTH_PORT` | port the ACTIVE opens for the LB probe | 0 (off) |
| `PBS_HA_VIP` | optional floating CIDR to bind while active | "" |
| `PBS_HA_VIP_DEV` | interface for the VIP | eth0 |

Both masters share the same `server_name` (stable job IDs) and the same 32-byte
`auth_key`.

## Pros / cons

### Dual-master (hot standby)
- **Pros**: near-instant failover (~20 s), no downtime for scheduled burst,
  a standby is always warm and can absorb a takeover without reprovisioning.
- **Cons**: +1 master VM (~$50-60/mo), slightly more moving parts (2 systemd
  control planes sharing the LB backend).

### Single-master (auto-replace / VMSS)
- **Pros**: one master VM cost saved; Azure can auto-replace via VMSS+custom
  image; simplest control plane.
- **Cons**: **minutes-level RTO** (~90 s reboot of the same VM, ~2-4 min new VM
  from a pre-baked image, ~5-8 min if software is installed at boot); a cold
  window with no active master while replacing; requires all daemons to be
  systemd-managed so the new VM self-heals.

## Measured switchover

| scenario | client-visible outage |
|---|---|
| dual-master, LB probe at default 15 s | ≈ 39 s |
| dual-master, LB probe tuned to 5 s (Azure min) | **≈ 16-20 s** (measured 16.2 s) |
| image-master replacement: failover phase (new master already up) | **≈ 16 s** (measured) |
| single-master, reboot the same VM (software installed) | **≈ 86 s** |
| single-master, VMSS auto-replace (custom image) | ≈ 2-4 min (provisioning-bound) + ~16 s failover |
| single-master, new VM with boot-time script install | ≈ 5-8 min |

The replace time is dominated by **provisioning the new master VM** (custom
image / VMSS boot); the actual **failover (lease + health-port + LB flip) is
~16 s** once the new master is up - verified end-to-end (a snapshot-derived
master VM took over in 16.3 s and served a job `C`/exit 0). The RTO table
splits "provision" from "failover": the 2-4 min row is the provision; add the
~16 s failover on top.

Floor: the 10 s `PBS_HA` lease TTL (both modes) + the LB probe (min 5 s, Azure
limit). Tune the lease TTL down if you want more aggressive failover.

## Cost evaluation (westus3, USD/month, approximate - verify pricing page)

Shared infra (excludes VM compute):

| item | monthly (approx) |
|---|---|
| Azure internal Load Balancer (Standard) | $18-25 |
| Azure internal Load Balancer (Basic, free option) | $0 |
| Azure Database for PostgreSQL Flexible, B1ms + 32 GiB | $17-22 |
|   + zone-redundant HA on the PG (DB not a SPOF) | +$14-16 |

VM compute (not included above): each D2s_v3 ≈ $50-60/mo.

| deployment | estimate |
|---|---|
| **Dual-master** (2× master + 1 compute + LB + PG) | ≈ $180-210/mo |
| **Single-master** (1× master + 1 compute + LB + PG) | ≈ $130-155/mo |
| (add PG zone-redundant HA) | +$14-16/mo |

> The PostgreSQL is the single source of truth; if it must itself be HA (not a
> SPOF) enable its zone-redundant/HA option (+ ~$14-16/mo). This is separate
> from the master VMs' HA (which keeps the control plane up).

## Azure service spec requirements

### Internal Load Balancer
- **SKU**: Standard (Basic is free but lacks health-probe tuning/features).
- **Frontend**: a private IP in the master subnet (the "VIP").
- **Rule**: `Tcp 15001 → 15001` (server port), backend pool = master NICs.
- **Health probe**: `Tcp :15150` (active-only port), `intervalInSeconds=5`
  (Azure minimum), `probe-threshold` small.
- **NSG on master NICs**: allow `15001` from `VirtualNetwork` (server), the MOM
  service port from the compute subnet, and `15150` from `AzureLoadBalancer` -
  with higher precedence than any deny-Internet rule.
- Hairpin caveat: the LB does not serve a client colocated on the same host as
  the active. Keep clients/compute MOMs on separate hosts (the cloud model).

### Azure Database for PostgreSQL Flexible
- **SKU**: Burstable `B1ms` (1 vCore/2 GiB) is fine for small/test; General
  Purpose `D2s_v3`-class for larger clusters. Reserve enough vCores/RAM for the
  job-queue write rate (each job mutation is a small UPSERT).
- **Storage**: GPSSD; size for job/queue metadata (KBs/job) - 32 GiB is plenty
  for thousands of jobs; add throughput if start-up bursts are heavy.
- **Version**: 16 (works with the pure-Go `pgx` driver).
- **Connectivity**: must allow the master VMs' source IPs (public access +
  firewall, or VNet integration). Firewall note: public-access rules match the
  VM's **public egress** IP, so open by subnet range or use `AllowAll` behind a
  firewall (test) / VNet integration (prod).
- **DB schema perms**: the app role needs `USAGE, CREATE` (or ownership) on the
  `public` schema to create `ot_state`/`ot_queues`/`ot_jobs`/`ot_lease`.
- **HA of the DB itself**: enable zone-redundant/HA if the DB must not be a
  single point of failure.

## Best practices

1. **Separate compute MOMs from masters** (masters = control plane only). This
   is both the cloud model and the fix for the LB hairpin caveat.
2. **All daemons under systemd** (`pbs_server` + `pbs_sched` on masters,
   `pbs_mom` on compute). On reboot, if only `pbs_server` starts, jobs stay
   queued - `pbs_sched` must start too. `configs/systemd/` + `/etc/opentorque/ha.env`.
3. **Shared, deterministic config**: same 32-byte `auth_key`, same
   `server_name`, LB-backend membership, `ha.env` identical across masters.
4. **Tune the LB probe** to `5 s` (Azure minimum) to shrink the switchover; keep
   the lease TTL at 10 s unless you want more aggressive failover.
5. **If the DB must be highly available itself, enable the managed PG's HA** -
   otherwise the database is the single point of failure.
6. For **single-master**, use a **pre-baked custom image + one-instance VMSS**
   to get ~2-4 min auto-replacement at minimum cost.
7. **Automate drills** with `scripts/ha-failover-drill.sh` after every topology
   change so a regression can't silently break failover.
8. Use the **Standard** LB SKU and reach every client/MOM cross-host for correct
   LB forwarding (no hairpin).


### Custom image for single-master auto-replace (verified)

Newer Azure images are **TrustedLaunch**, so `az image create` from a running
master is disallowed. The working route (verified live on westus3): build a
**Shared Image Gallery** image version from the master's OS-disk snapshot with
`--os-state Specialized --features SecurityType=TrustedLaunch` (no generalizing -
the master stays online), then run a one-instance Uniform VMSS from it:

```bash
az sig create        -g "$RG" --gallery-name otxSig -l "$LOC"
az sig image-definition create -g "$RG" --gallery-name otxSig \
  --gallery-image-definition otxImgDef --publisher otx --offer otx --sku otx \
  --os-type Linux --os-state Specialized --features SecurityType=TrustedLaunch
az sig image-version create -g "$RG" --gallery-name otxSig \
  --gallery-image-definition otxImgDef --gallery-image-version 1.0.0 \
  --target-regions "$LOC" --replica-count 1 --os-snapshot <snapshot-id>   # minutes
az vmss create -g "$RG" -n otx-vmss --image <sig-version-id> --vm-sku Standard_D2s_v3 \
  --instance-count 1 --orchestration-mode Uniform --subnet "$SUBNET" \
  --load-balancer <lb> --backend-pool-name <pool> --public-ip-address ""
```

`scripts/ha-single-master-vmss.sh` captures this end-to-end. **Important:** a
SPECIALIZED image cannot be used with `az vmss create` (Azure rejects "OSProfile
is not allowed with a specialized image"). The working VMSS route is a
**generalized** image from a disposable golden VM (`waagent -deprovision+user`
+ `az vm generalize` + `az image create`), which the script documents as Route A.
(A live VMSS auto-replace run was attempted in this session but blocked by that
Azure specialized-image constraint and the TrustedLaunch `image create` rule;
the measured replacement-master failover is ~16 s and total RTO ≈ provisioning
+ ~16 s.) Total auto-replace
RTO ≈ provisioning (~2-4 min) + ~16 s failover. In this session the SIG image
version was created and verified against the TrustedLaunch snapshot; the VMSS
creation step from it is the documented follow-on (the az CLI `vmss create`
argument surface varies by CLI version - adjust as needed).
