# OpenTorque

**A modern, cross-platform PBS/TORQUE-compatible resource manager written in Go.**

OpenTorque is a clean-room reimplementation of the [TORQUE Resource Manager](https://github.com/adaptivecomputing/torque) in Go. It provides the same batch job scheduling and resource management capabilities, with a focus on simplicity, portability, and modern engineering practices.

## Why OpenTorque?

| | TORQUE (C/C++) | OpenTorque (Go) |
|---|---|---|
| **Language** | C/C++ (~300K LOC) | Go (clean, minimal) |
| **Platforms** | Linux only | Linux, macOS, Windows, ARM64 |
| **Authentication** | trqauthd daemon (Unix domain sockets) | HMAC-SHA256 token auth (no extra daemon) |
| **Build** | autoconf/automake + 20+ dependencies | `go build` (zero external dependencies) |
| **Scheduler** | External process only | Built-in FIFO + external advanced scheduler |
| **Protocol** | DIS (Data Is Strings) wire protocol | Same DIS protocol (backward compatible) |

## Features

- **Full job lifecycle**: submit → queue → schedule → execute → complete
- **PBS-compatible CLI tools**: `qsub`, `qstat`, `qdel`, `qhold`, `qrls`, `pbsnodes`, `qmgr`
- **Multiple scheduling algorithms**: FIFO, shortest/longest job first, priority-based, fair-share, round-robin, starvation prevention
- **Token-based authentication**: HMAC-SHA256, no separate auth daemon needed
- **Cross-platform**: compiles natively for Linux (amd64/arm64), macOS, and Windows
- **Cloud bursting**: elastic cloud pool — local nodes first, burst to cloud VMs only when local capacity is exhausted, auto scale-in
- **High availability (cloud-native)**: dual active/standby masters sharing a
  managed PostgreSQL, leader-elected via a lease, behind an Azure load balancer
  that follows the active (transparent job/MOM failover with running-job
  continuation). See [docs/opentorque-ha.md](docs/opentorque-ha.md).
- **Node selection**: `-l host=<node>` pinning, `-l feature=a,b` properties, and
  named host groups / node pools (`-l host=@group`)
- **Multi-node placement**: `-l nodes=N:ppn=M` / `-l select=` allocates N distinct
  nodes with `PBS_NODEFILE` / `PBS_NODELIST`
- **Queue policy**: pack-vs-exclusive `naccesspolicy`, queue `hostlist` node-pool
  binding, and `acl_hosts` submission-host ACLs
- **Generic resources**: arbitrary `resources_available.<name>` capacity (GPU /
  license counts) requested with `-l <name>=N`, with `gres_used` accounting
- **Backfill**: fittable jobs run in the gap left by a blocked head-of-line job
  (default on)
- **Job persistence**: full job attributes persisted; completed jobs remain
  queryable across server restart
- **Wire-compatible**: uses the same DIS protocol as TORQUE for interoperability

## Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌───────────────────┐
│  CLI Tools  │────▶│    pbs_server    │────▶│    pbs_mom        │
│ qsub/qstat  │     │  jobs · queues   │     │  (execution)      │
│ qdel/qmgr   │     │  · nodes · FIFO  │     │  on LOCAL node    │
└─────────────┘     └───────┬──────────┘     └───────────────────┘
                   status ▲ │ dynamic node
                          │ ▼ auto-registration
      ┌────────────────────┴──────────────┐
      │          pbs_sched (external)     │──┐
      │  advanced algorithms + CEC        │  │ capacity / idle
      │  (cloud elastic controller)       │◀─┘ events
      └───────────────┬───────────────────┘
                      │ provision / scale / reclaim (CRP)
                      ▼
         ┌────────────────────────────┐
         │   cloud worker VMs (Azure) │────▶ auto-register as dynamic
         │   burst pool (per-queue)   │      nodes; reclaimed when idle
         └────────────────────────────┘
               ▲ cloud-bursting path ▲
```

### Components

| Component | Binary | Description |
|-----------|--------|-------------|
| **Server** | `pbs_server` | Central daemon managing jobs, queues, and nodes |
| **MOM** | `pbs_mom` | Compute node agent that executes jobs and reports resources |
| **Scheduler** | `pbs_sched` | External scheduler with advanced algorithms (optional) |
| **CLI** | `qsub`, `qstat`, etc. | User and admin command-line tools |

## Quick Start

### Prerequisites

- Go 1.21 or later

### Build

```bash
# Build all components
make all

# Or build individually
make server
make mom
make sched
make cli
```

### Install

```bash
sudo make install
```

This installs:
- Daemons to `/usr/local/sbin/` (`pbs_server`, `pbs_mom`, `pbs_sched`)
- CLI tools to `/usr/local/bin/` (`qsub`, `qstat`, `qdel`, `qhold`, `qrls`, `pbsnodes`, `qmgr`)
- Default config to `/var/spool/torque/`

### First-Time Setup

```bash
# Initialize server (creates queues, generates auth key)
sudo pbs_server -t create

# Configure a compute node
echo "\$pbsserver $(hostname)" | sudo tee /var/spool/torque/mom_priv/config
echo "$(hostname) np=$(nproc)" | sudo tee /var/spool/torque/server_priv/nodes

# Start daemons
sudo pbs_server &
sudo pbs_mom &

# Create a default queue
qmgr -c "create queue batch"
qmgr -c "set queue batch queue_type = Execution"
qmgr -c "set queue batch started = True"
qmgr -c "set queue batch enabled = True"
qmgr -c "set server default_queue = batch"
```


### Run as systemd Services

For resilient, boot-persistent deployments the daemons can run under systemd.
Ready-made units are provided in `configs/systemd/` (`pbs_server.service`,
`pbs_sched.service`, `pbs_mom.service`). Install and enable them on the relevant
hosts:

```bash
sudo cp configs/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pbs_server pbs_sched pbs_mom
```

The scheduler unit already injects the Azure managed-identity `AZURE_CLIENT_ID`
needed by the cloud bursting controller.

### Submit a Job

```bash
# Submit a simple job
echo '#!/bin/bash
echo "Hello from OpenTorque"
hostname
sleep 5' | qsub -N my_first_job

# Check status
qstat

# View node status
pbsnodes -a
```

## Scheduling, Placement & Resource Features

Beyond basic FIFO, OpenTorque implements a set of TORQUE-style scheduling and
resource controls, exposed to users via `qsub -l ...` and to admins via `qmgr`.

| Area | User | Admin | Doc |
|------|------|-------|-----|
| Node pinning | `-l host=<node>` | — | `docs/node-selection.md` |
| Node features | `-l feature=a,b` | `qmgr set node <n> properties=a,b` | `docs/node-selection.md` |
| Host groups / pools | `-l host=@<grp>` | `qmgr set node <n> hostgroups=grp` | `docs/node-selection.md` |
| Multi-node | `-l nodes=2:ppn=4` / `-l select=` | — | `docs/multi-node-placement.md` |
| Pack vs. exclusive | — | `qmgr set queue q naccesspolicy=exclusive` | `docs/queue-policy.md` |
| Queue node-pool binding | — | `qmgr set queue q hostlist=a,b` | `docs/queue-policy.md` |
| Submission-host ACL | — | `qmgr set queue q acl_host_enable=True` + `acl_hosts` | `docs/queue-policy.md` |
| Generic resources (GPU/license) | `-l gpu=2` | `qmgr set node <n> resources_available.gpu=2` | `docs/resource-constraints.md` |
| Backfill | — | `sched_config`: `backfill: true` | `docs/resource-constraints.md` |
| Job persistence / read-back | — | server `keep_completed` | `docs/job-persistence.md` |

A short example tying several together:

```bash
# admin: node with 2 GPUs, in the "gpu" pool, serving an exclusive queue
qmgr -c "set node worker1 resources_available.gpu = 2"
qmgr -c "set node worker1 hostgroups = gpu"
qmgr -c "create queue gpuq"
qmgr -c "set queue gpuq queue_type = Execution"
qmgr -c "set queue gpuq started = True"
qmgr -c "set queue gpuq enabled = True"
qmgr -c "set queue gpuq naccesspolicy = exclusive"
qmgr -c "set queue gpuq hostlist = worker1"

# user: 2-node job pinned to the gpu pool, 1 GPU per node
qsub -q gpuq -l nodes=2:ppn=1 -l gpu=1 -l host=@gpu mpi.sh
```

## Scheduler Configuration

OpenTorque uses a **single external scheduler**, `pbs_sched` — the in-process
built-in scheduler was removed. **No configuration is required**: the server
defaults to external mode and, if a `sched_priv/sched_config` file exists, any
stale `scheduler_mode: builtin` value is ignored (a warning is logged).

If `pbs_sched` is **not running**, the server logs a clear periodic warning
(`WARNING: external scheduler (pbs_sched) is NOT running ... jobs will NOT be
scheduled until it is started`) so a missing scheduler is never mistaken for a
quiet cluster.

The scheduler's advanced-algorithm knobs are configured in
`$PBS_HOME/sched_priv/sched_config` (only applies to `pbs_sched`):
```
backfill: true            # run fittable jobs past a blocked head (default on)
scheduler_interval: 10
by_queue: true          ALL
sort_by: shortest_job_first ALL
fair_share: false       ALL
help_starving_jobs: true ALL
max_starve: 24:00:00
load_balancing: false   ALL
```

See [docs/scheduling_algorithms.md](docs/scheduling_algorithms.md) for the full algorithm reference.

## High Availability (cloud-native)

OpenTorque provides **high availability for cloud deployments (Azure-first)**
with transparent failover and zero state loss, because the authority lives in a
**managed database** and an **internal load balancer** is the stable address
clients and compute MOMs use.

Two modes:
- **Dual-master (hot standby)** — two control-plane VMs share a managed
  PostgreSQL; a lease elects one active; an active-only health port tells the
  load balancer where to send `15001`. Measured **~16-20 s** failover. State
  (jobs/queues/nodes) carries over via the shared DB, and running jobs are
  reconciled so they are never re-dispatched.
- **Single-master (auto-replace)** — one control-plane VM; on death a new master
  is provisioned from a custom image / VMSS. Failover (once the new master is
  up) is **~16 s**; the total RTO is dominated by VM provisioning (~2-4 min).

All masters run daemons under **systemd** (so a reboot self-heals the full
control plane) and share a 32-byte `auth_key` and `server_name`.

See [docs/opentorque-ha.md](docs/opentorque-ha.md) for deployment, settings, cost,
LB/PostgreSQL sizing and best practices. Ops scripts under `scripts/`:
`ha-deploy.sh` (provision), `ha-failover-drill.sh` (switchover measurement),
`ha-status.sh` + `ha-ops.sh` (status / ops front-end), and
`ha-single-master-vmss.sh` (single-master auto-replace image/VMSS).


## Cloud Bursting (Elastic Cloud Pool)

Cloud bursting lets a fixed local cluster **overflow onto cloud VMs only when
local capacity is exhausted** and automatically scale them back down when they
sit idle — so you pay for cloud compute on demand instead of over-provisioning
local hardware, and never leave local nodes idle while a rented VM runs.

Cloud bursting is **local-first** by design: the scheduler always places jobs on
static local nodes and only falls back to auto-registered (dynamic) cloud nodes
once local capacity is gone. A still-registered idle cloud VM is never dispatched
ahead of a free local node (see `findNodeForJob` in
`internal/sched/scheduler/scheduler.go`).

It is configured **per queue** with `cloud_*` burst attributes and driven at
runtime by the event-driven **Cloud Elastic Controller (CEC)** plus an Azure
**Cloud Resource Provider (CRP)**.

### Minimal configuration

Make the `batch` queue cloud-backed by pointing it at Azure, a subnet, and the
burst bounds (via `qmgr`):

```text
# basic local queue
qmgr -c "create queue batch"
qmgr -c "set queue batch queue_type = Execution"
qmgr -c "set queue batch enabled = True"
qmgr -c "set queue batch started = True"

# cloud burst for this queue (Azure)
qmgr -c "set queue batch cloud_provider = azure"
qmgr -c "set queue batch cloud_vm_sku = Standard_D8s_v3"
qmgr -c "set queue batch cloud_max_nodes = 8"
qmgr -c "set queue batch cloud_idle_time = 300"
qmgr -c "set queue batch cloud_reclaim = deallocate"
qmgr -c "set queue batch cloud_subnet_id = /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/virtualNetworks/<vnet>/subnets/<sb>"
```

### Submit

Just submit normally — local nodes are used first, and cloud VMs are provisioned
automatically when needed and reclaimed when idle:

```bash
# runs on a local node while one is free
echo "sleep 10" | qsub -l ncpus=2 -N local

# overflows to freshly-provisioned cloud VMs only once local cores are used up
for i in $(seq 1 16); do echo "sleep 60" | qsub -l ncpus=2; done
```

Monitor with `qstat` and `pbsnodes` (dynamic cloud nodes show
`is_dynamic = true`). See [docs/cloud-bursting.md](docs/cloud-bursting.md) for
the full lifecycle, attribute reference, and tuning knobs.


## Project Structure

```
opentorque/
├── cmd/                    # Executable entry points
│   ├── pbs_server/         # Server daemon
│   ├── pbs_mom/            # MOM daemon
│   ├── pbs_sched/          # External scheduler
│   ├── qsub/               # Job submission
│   ├── qstat/              # Job/queue status
│   ├── qdel/               # Job deletion
│   ├── qhold/              # Job hold
│   ├── qrls/               # Job release
│   ├── pbsnodes/           # Node status
│   └── qmgr/               # Queue manager
├── internal/               # Shared internal packages
│   ├── server/             # Server core logic
│   ├── mom/                # MOM core logic
│   ├── sched/              # Scheduler algorithms
│   ├── cli/                # CLI client library
│   ├── dis/                # DIS protocol codec
│   ├── auth/               # Token authentication
│   ├── config/             # Configuration parsing
│   ├── job/                # Job data structures
│   ├── node/               # Node management
│   └── queue/              # Queue management
├── docs/                   # Documentation
├── configs/                # Example configuration files
├── scripts/                # Setup and utility scripts
├── Makefile
├── go.mod
└── README.md
```

## Documentation

- [Installation Guide](docs/INSTALL.md)
- [Scheduling Algorithms](docs/scheduling_algorithms.md)
- [Node Selection & Host Groups](docs/node-selection.md)
- [Multi-Node Placement](docs/multi-node-placement.md)
- [Queue Policy](docs/queue-policy.md)
- [Resource Constraints (GPU/license + backfill)](docs/resource-constraints.md)
- [Job & Data Persistence](docs/job-persistence.md)
- [Cloud Bursting](docs/cloud-bursting.md)
- [High Availability (cloud-native) — reference](docs/opentorque-ha.md)
- [High Availability — User Guide](docs/ha-guide.md)
- [High Availability — Blog](docs/blog-ha.md)
- [Cloud Elastic — Event-Driven Design](docs/cloud-elastic-event-driven-design.md)
- [Cloud Elastic — Node Scaling Design](docs/cloud-elastic-node-scaling-design.md)
- [Run as systemd services](configs/systemd/)
- [Data Persistence](docs/data_persistence_analysis.md)
- [CLI Reference](docs/cli.md)
- [PBS Server Analysis](docs/pbs_server_analysis.md)
- [PBS MOM Analysis](docs/pbs_mom_analysis.md)
- [PBS Scheduler Analysis](docs/pbs_sched_analysis.md)

## Compatibility

OpenTorque implements the PBS DIS wire protocol and is designed to be a drop-in replacement for TORQUE. Key compatibility points:

- **PBS commands**: same CLI names and flags (`qsub`, `qstat`, `qdel`, etc.)
- **PBS_HOME**: uses the same `/var/spool/torque/` directory structure
- **DIS protocol**: wire-compatible with TORQUE servers and MOMs
- **Job scripts**: existing PBS job scripts work without modification
- **Environment variables**: `PBS_JOBID`, `PBS_NODEFILE`, `PBS_O_WORKDIR`, etc.

## Acknowledgments

OpenTorque is inspired by the TORQUE Resource Manager, originally developed by
Adaptive Computing Enterprises, Inc. as a derivative of OpenPBS v2.3. OpenPBS
was created by NASA Ames Research Center, Lawrence Livermore National Laboratory,
and Veridian Information Solutions, Inc.

We gratefully acknowledge the decades of work by the PBS and TORQUE communities
that established the batch scheduling paradigm that OpenTorque builds upon.

## License

OpenTorque is licensed under the [Apache License 2.0](LICENSE).

See [NOTICE](NOTICE) for attribution details.
