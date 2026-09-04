# OpenTorque High Availability — User Guide

Learn how OpenTorque gives you **high availability in the cloud**, how to
configure it, and how to test failover — written for users/administrators, not
schedulers.

## What you get with HA

OpenTorque's HA keeps your batch cluster **control plane** available even when
a machine fails, with **no job loss**:

- Jobs/queues/nodes live in a **shared database** (managed PostgreSQL), so any
  healthy master carries the same state.
- An **internal load balancer** is the single address clients and compute MOMs
  use; it always directs traffic to the *active* master.
- A **lease** elects exactly one active master; the others stand by.
- Running jobs are reconciled on takeover — a job still executing is never
  re-dispatched (no double-run), and finished/queued jobs are preserved.

## How it works (principles)

```
 clients / compute MOMs  ->  LB frontend :15001  ->  active master
                                                       |
                                     (lease election)  v
                                    managed PostgreSQL (shared state)
```

1. **Leader lease.** With `PBS_HA=1` each `pbs_server` holds/renews a lease row
   in the shared DB (10 s TTL, 3 s renewal). Exactly one is *active*.
2. **Health port.** The active master opens a port (`PBS_HA_HEALTH_PORT`, e.g.
   15150) that the LB probes. Standbys do **not** open it, so the LB sends
   `15001` only to the active.
3. **Failover.** When the active stops renewing (crash / stop), the lease
   expires, a standby acquires it, opens the health port, and the LB flips to
   it — clients/MOMs at the frontend follow automatically.
4. **State continuity.** Everything is in the shared DB; the new active also
   reconciles running jobs against the MOMs so nothing runs twice.

## Two HA modes

| | Dual-master (hot standby) | Single-master (auto-replace) |
|---|---|---|
| Masters | 2 control-plane VMs | 1 control-plane VM |
| Failover | **~16-20 s** | ~16 s (once a new master is up) + provisioning (~2-4 min) |
| Cost | +1 master VM | saves that VM |
| Best for | minimizing downtime | minimizing cost |
| Replacement | standby takes over instantly | VMSS / custom image re-provisions |

Both use the same managed PostgreSQL + internal LB + active-only health port, so
the state model and client behavior are identical.

## Configuration (per master)

Environment variables (put them in `/etc/opentorque/ha.env`, referenced by the
systemd units):

| Var | Meaning |
|---|---|
| `PBS_HA=1` | participate in leader election |
| `PBS_PG_DSN` | libpq DSN to the shared cluster PostgreSQL (add `?sslmode=require`) |
| `PBS_HA_HEALTH_PORT=15150` | port the ACTIVE opens for the LB probe |
| `PBS_HA_VIP` / `PBS_HA_VIP_DEV` | optional floating address to bind while active (not needed with an LB) |

All masters must share the **same 32-byte hex `auth_key`** and the **same
`server_name`** (stable job IDs).

## Deploy & test (quick recipe)

### Dual-master
1. `./scripts/ha-deploy.sh infra` then `masters` — provisions PG + LB + two
   masters (or follow the manual steps in `docs/opentorque-ha.md`).
2. On each master: install systemd units (`configs/systemd/*`), write
   `/etc/opentorque/ha.env`, `systemctl enable --now pbs_server pbs_sched`.
3. Compute node(s): run `pbs_mom` with `$pbsserver <lb-frontend>` so they follow
   the active.
4. Verify: `scripts/ha-ops.sh status` shows the lease holder and health.

### Test failover (simple)
```bash
# from any cross-host client:
scripts/ha-failover-drill.sh 10.0.0.10:15001 azureuser@<active> -i ~/.ssh/id_rsa
```
The script probes the LB frontend, stops the active master, and prints the
client-visible switchover window. Then submit a job — it runs on a compute MOM
via the new active.

### Single-master auto-replace
See `scripts/ha-single-master-vmss.sh` (custom image + VMSS) so a new master VM
is provisioned automatically when the old one dies.

## Cost / sizing (approx, Azure westus3)

- Azure **internal LB** (Standard): ~$18-25/mo (Basic is free for dev).
- **Managed PostgreSQL** (Flexible, B1ms + 32 GiB): ~$17-22/mo; enable its
  zone-redundant HA (+~$14-16/mo) if the DB itself must not be a single point.
- VM compute: each D2s_v3 ~$50-60/mo.
- **Dual-master** ≈ $180-210/mo; **single-master** ≈ $130-155/mo (excluding the
  PG-HA option).

## Best practices

- Keep masters as **pure control plane**; put compute MOMs on separate nodes
  (this is also the fix for the Azure LB hairpin limitation).
- Run **all daemons under systemd** (`pbs_server` + `pbs_sched` on masters,
  `pbs_mom` on compute) so a reboot self-heals the whole pipeline.
- Share one `auth_key` + `server_name`; keep `ha.env` identical.
- Tune the **LB probe to 5 s** (Azure minimum) for the fastest switchover.
- If the DB must be HA itself, enable the managed PG's HA — otherwise it is the
  single point of failure.

See `docs/opentorque-ha.md` for the full architecture, deployment, spec
requirements and measured numbers.
