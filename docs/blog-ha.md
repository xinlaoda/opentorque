# High Availability for a cloud HPC scheduler: the practical guide

*Batch schedulers in the cloud need to survive a VM dying — without losing jobs or
running them twice. Here's how we made OpenTorque highly available by leaning on
managed services, plus how to configure both modes and test failover in a minute.*

## The problem

A batch cluster's **control plane** (the master that accepts jobs, queues them,
and dispatches to compute nodes) is a single point of failure. If that VM dies
you lose submissions, and if you aren't careful you can re-run jobs that were
still executing. On-prem clusters solve this with shared filesystems and
pacemaker; in the cloud we have better tools: a managed database and a load
balancer.

## The core idea

Keep the state somewhere that isn't bound to one machine, and give clients one
stable address that always follows the live master:

- **State** lives in a managed PostgreSQL (jobs, queues, nodes).
- **A lease** elects one active master; standbys idle.
- **The active** opens a health port the load balancer probes, so the LB only
  sends `15001` to it.
- When the active dies, a standby takes the lease, opens the health port, and
  the LB flips over. Clients and MOMs at the LB frontend never change address.

Measured on Azure: **~16–20 s** client-visible failover with two masters —
no job loss, running jobs are reconciled so they aren't re-dispatched.

## Mode 1: dual-master (hot standby)

Two control-plane VMs behind an internal LB, sharing one managed PostgreSQL.

```
clients ──> internal LB ──> master A (active)   master B (standby)
                                 └──────┬────────┘
                                        v
                            managed PostgreSQL
```

Configure each master:

```bash
# /etc/opentorque/ha.env
PBS_HA=1
PBS_PG_DSN=postgres://pbs:...@pg.postgres.database.azure.com:5432/pbs?sslmode=require
PBS_HA_HEALTH_PORT=15150
```

```bash
systemctl enable --now pbs_server pbs_sched
```

Both masters share one `auth_key` and one `server_name`. Point the compute MOMs'
`$pbsserver` at the **LB frontend**, and they'll follow whichever master is
active.

**Test:** from any cross-host client:

```bash
scripts/ha-failover-drill.sh 10.0.0.10:15001 azureuser@<active-master> -i ~/.ssh/id_rsa
```

It probes the LB frontend, stops the active master, and reports how long the
frontend was down — typically ~16-20 s. Submit a job right after and it runs on
a compute MOM.

## Mode 2: single-master (auto-replace)

Cheaper: one master VM, and when it dies a **new master is provisioned** from a
custom image (e.g. a single-instance VMSS). State is still in PostgreSQL and
the LB still follows the health port, so the new master picks up where the old
one left off.

- **End-to-end RTO ~45 s** (measured) with a pre-baked generalized image - a private VMSS instance needs a **NAT gateway** (outbound) to reach the managed PostgreSQL at boot, otherwise the replaced master falls back to the file store and stays out of HA.

The trade-off is cost vs. downtime. See
`scripts/ha-single-master-vmss.sh` for the image/VMSS setup.

## Testing checklist (either mode)

1. `scripts/ha-ops.sh status` — lease holder + LB health + nodes.
2. `scripts/ha-failover-drill.sh ...` — stop the active, watch the switchover.
3. Submit a job → it runs to completion on a compute MOM.
4. Reboot a master → both `pbs_server` and `pbs_sched` come back (systemd) and
   jobs still schedule.

## Key lessons

- **Put the scheduler under systemd too.** If only `pbs_server` auto-starts on
  reboot, jobs sit queued until `pbs_sched` is up.
- **Separate compute MOMs from masters.** This is both the cloud model and the
  fix for the LB hairpin limitation.
- **Make the DB itself HA** if it must not be a single point of failure.
- **Tune the LB probe to 5 s** (Azure minimum) for the fastest flip.

---

*OpenTorque HA is cloud-native by design: managed PostgreSQL + internal LB as
the VIP + lease-based leadership, with a single-master economy option. The
source is in `docs/opentorque-ha.md` and the scripts in `scripts/`.*




