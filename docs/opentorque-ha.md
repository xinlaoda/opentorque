# OpenTorque High Availability (cloud-native)

OpenTorque HA is designed for **cloud deployments** (Azure-first) and leans on
managed platform services wherever possible. Local/on-prem HPC clusters already
have mature schedulers; this project focuses on the cloud, where a resource
manager is expected to burst, scale, and survive node loss with managed
infrastructure.

## Architecture (Azure)

```
                        clients / compute MOMs
                                 │  (connect to 10.0.0.10:15001)
                                 ▼
               ┌──────────────────────────────────┐
               │  Azure Internal Load Balancer    │  = the VIP / stable address
               │  frontend 10.0.0.10 · rule 15001 │
               │  probe Tcp :15150 (active-only)  │
               └───────┬───────────────┬──────────┘
                       ▼               ▼
                ┌─────────────┐  ┌─────────────┐
                │  master A   │  │  master B   │   two pbs_server (+pbs_sched)
                │ (VM1,10.0.0.4)│ │ (VM2,10.0.0.5)│
                └──────┬──────┘  └──────┬──────┘
                       │  PBS_PG_DSN     │   (leader lease + all state)
                       └───────┬─────────┘
                               ▼
                ┌──────────────────────────────┐
                │ Azure Database for PostgreSQL │  managed, shared store
                └──────────────────────────────┘
```

Two masters share one **managed PostgreSQL** as the authoritative store. A
**leader lease** (an `ot_lease` row, 10s TTL, 3s renewal) elects exactly one
active master. Only the active binds a **health port** (default 15150) that the
**load balancer** probes, so the LB forwards `15001` only to the active. On
failover the standby acquires the lease, opens the health port, and the LB
flips to it — clients and MOMs at the LB frontend follow transparently.

## Key behaviors

- **Single active**: only the lease holder notifies its scheduler and accepts
  `RunJob`; standbys stay idle (no split brain, no double dispatch).
- **State continuity**: jobs/queues/nodes live in PostgreSQL, so the standby
  recovers the same state on takeover.
- **Running jobs continue**: on takeover the new active runs the startup MOM
  reconciliation — a running job is never re-dispatched if a MOM confirms it is
  still executing (see TODO 5.1), and orphans are re-queued.
- **Address failover** = the load balancer (managed VIP) + the active-only
  health port. (A floating secondary IP is not used: Azure does not allow the LB
  frontend IP to also be a NIC ip-config.)

## Configuration (per master, environment)

| Var | Meaning | Default |
|---|---|---|
| `PBS_PG_DSN` | libpq DSN to the shared cluster DB (`…?sslmode=require`) | "" → file store |
| `PBS_HA` | non-empty ⇒ participate in leader election | "" |
| `PBS_HA_HEALTH_PORT` | port the ACTIVE opens for the LB probe | 0 (off) |
| `PBS_HA_VIP` | optional floating CIDR to bind while active | "" |
| `PBS_HA_VIP_DEV` | interface for the VIP | eth0 |

Both masters must share the same `server_name` **and** the same
`$PBS_HOME/auth_key` (a 32-byte hex key), so job IDs are stable and any master
can authenticate clients/MOMs.

## Deploy (systemd, Azure)

`configs/systemd/pbs_server.service` runs `pbs_server -d /var/spool/torque -t
warm -p 15001`. Inject the env above (e.g. `Environment=PBS_HA=1`,
`Environment=PBS_PG_DSN=…`, `Environment=PBS_HA_HEALTH_PORT=15150`). Run
`pbs_sched` and the compute `pbs_mom` on their own hosts.

Networking:
- Add each master NIC to the LB backend pool; probe = `Tcp:15150`.
- NSG on the master NICs: allow `15001` (server) from `VirtualNetwork`,
  `15150` from `AzureLoadBalancer`, and the MOM service port from the compute
  subnet — with **higher precedence than any deny-Internet rule**.

## Switchover time (measured)

With the topology above (two masters behind the LB, shared managed PG), stopping
the active `pbs_server` gave an end-to-end, client-visible outage through the
LB frontend of **≈ 39 s** (probed continuously from a third compute host):

| phase | time |
|---|---|
| LB drops the stopped active (probe fails) | ~0.1 s |
| lease expiry (10 s TTL, standby stops renewing) | ~10 s |
| standby lease-renewal loop acquires + opens health port | ~ +3 s |
| Azure LB health probe re-marks the new active healthy | ~ +26 s |
| **total client-visible switchover** | **≈ 39 s** |

The LB probe bracket is the dominant, tunable part: lower the probe `intervalInSeconds` (default 5) and `numberOfProbes` (default 2) to shrink it to a few seconds. The lease-expiry floor is bounded by `PBS_HA` lease TTL (10 s). After switchover, jobs submitted through the frontend run normally on
dedicated compute MOMs, and queued/running state carries over from the shared
PostgreSQL.

## Failover drill

1. Client/MOM connects to `frontend:15001`.
2. Stop the active `pbs_server` (or the VM): `sudo systemctl stop pbs_server`.
3. Lease expires (~10s) → standby acquires → opens `15150` → LB flips.
4. Verify: `ot_lease.holder` is the standby; standby log shows `Acquired HA
   leader lease; taking over as active`.
5. Submit a job via the frontend → new active schedules it on a reachable MOM.

## Cloud-native guidance (hairpin caveat)

Azure LB does **not** service a client that is colocated on the same host as the
active master (self/"hairpin" connections). In a cloud deployment this is a
non-issue, because:

- **Clients** run on your/admin hosts, not on the master.
- **Compute MOMs** are separate nodes (or auto-scaled/elastic) that join via the
  LB/private IP — they are never co-resident with a master (the master is not a
  compute node).

Keep masters as pure control-plane VMs; run MOMs on dedicated compute nodes, so
every client and every MOM reaches the LB cross-host and follows the active
automatically.
