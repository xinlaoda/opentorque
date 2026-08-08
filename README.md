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

## Scheduler Configuration

OpenTorque supports two scheduling modes configured in `$PBS_HOME/sched_priv/sched_config`:

### Built-in FIFO (default)
```
scheduler_mode: builtin
```
Zero-overhead, in-process FIFO scheduling. Best for simple clusters and high-throughput workloads.

### External Advanced Scheduler
```
scheduler_mode: external
scheduler_interval: 10
by_queue: true          ALL
sort_by: shortest_job_first ALL
fair_share: false       ALL
help_starving_jobs: true ALL
max_starve: 24:00:00
load_balancing: false   ALL
```

See [docs/scheduling_algorithms.md](docs/scheduling_algorithms.md) for the full algorithm reference.

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
- [Cloud Bursting](docs/cloud-bursting.md)
- [Cloud Elastic — Event-Driven Design](docs/cloud-elastic-event-driven-design.md)
- [Cloud Elastic — Node Scaling Design](docs/cloud-elastic-node-scaling-design.md)
- [Run as systemd services](configs/systemd/)
- [Data Persistence](docs/data_persistence_analysis.md)
- [CLI Reference](docs/cli/)
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
