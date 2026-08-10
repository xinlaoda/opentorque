# OpenTorque Installation Guide

## Prerequisites

- **Go 1.21 or later** (https://go.dev/dl/)
- **Linux, macOS, or Windows** (amd64 or arm64)
- Root/administrator access for daemon installation

## Building from Source

```bash
git clone https://github.com/yourusername/opentorque.git
cd opentorque
make all
```

Individual components:
```bash
make server    # Build pbs_server
make mom       # Build pbs_mom
make sched     # Build pbs_sched
make cli       # Build all CLI tools
```

### Cross-Compilation

```bash
# Linux ARM64
GOOS=linux GOARCH=arm64 make all

# macOS
GOOS=darwin GOARCH=amd64 make all

# Windows
GOOS=windows GOARCH=amd64 make all
```

## Installation

### Standard Install

```bash
sudo make install
```

This installs to:
- `/usr/local/sbin/`: `pbs_server`, `pbs_mom`, `pbs_sched`
- `/usr/local/bin/`: `qsub`, `qstat`, `qdel`, `qhold`, `qrls`, `pbsnodes`, `qmgr`

### Custom Prefix

```bash
sudo make install PREFIX=/opt/opentorque
```

## Initial Configuration

### 1. Create PBS Home Directory

```bash
sudo mkdir -p /var/spool/torque/{server_priv,mom_priv,sched_priv,server_logs,mom_logs,sched_logs}
```

### 2. Initialize Server

```bash
# First-time server initialization (creates database)
sudo pbs_server -t create
```

This generates:
- `/var/spool/torque/auth_key` — HMAC authentication key
- `/var/spool/torque/server_priv/` — server state files

### 3. Configure Compute Node

On each compute node, create the MOM configuration:

```bash
echo "\$pbsserver <server_hostname>" | sudo tee /var/spool/torque/mom_priv/config
```

On the server, register the node:

```bash
echo "<node_hostname> np=<num_cpus>" | sudo tee -a /var/spool/torque/server_priv/nodes
```

### 4. Distribute Authentication Key

Copy `/var/spool/torque/auth_key` from the server to all compute nodes
and CLI-only machines. This key enables token-based authentication.

```bash
scp /var/spool/torque/auth_key <node>:/var/spool/torque/auth_key
```

### 5. Start Daemons

```bash
# On the server
sudo pbs_server &

# On each compute node
sudo pbs_mom &

# Optional: external scheduler (if not using built-in FIFO)
sudo pbs_sched &
```

### 6. Create Default Queue

```bash
qmgr -c "create queue batch"
qmgr -c "set queue batch queue_type = Execution"
qmgr -c "set queue batch started = True"
qmgr -c "set queue batch enabled = True"
qmgr -c "set server default_queue = batch"
```

### 7. Verify

```bash
pbsnodes -a          # Should show your node(s)
echo "sleep 1" | qsub   # Submit a test job
qstat                # Should show the job
```

## Scheduler Configuration

The scheduler reads `$PBS_HOME/sched_priv/sched_config`. The default is
**external** mode; set `scheduler_mode` explicitly to choose:

```text
# external advanced scheduler (default)
scheduler_mode: external
backfill: true            # run fittable jobs past a blocked head (default on)

# or the built-in in-process FIFO scheduler
# scheduler_mode: builtin
```

If you run `pbs_sched` (external mode) make sure `sched_priv/` exists and is
writable. See [Scheduling Algorithms](scheduling_algorithms.md) for the full
algorithm reference.

## Optional Features

OpenTorque ships several TORQUE-style scheduling/resource features on top of
basic FIFO. A few one-line setups:

```bash
# host group / node pool
qmgr -c "set node worker1 hostgroups = gpu"

# generic named resource (e.g. GPU / license capacity)
qmgr -c "set node worker1 resources_available.gpu = 4"
qmgr -c "set node worker1 resources_available.license = 2"

# exclusive (one-job-per-node) queue bound to a pool, with a submit-host ACL
qmgr -c "set queue gpuq naccesspolicy = exclusive"
qmgr -c "set queue gpuq hostlist = worker1"
qmgr -c "set queue gpuq acl_host_enable = True"
qmgr -c "set queue gpuq acl_hosts = 10.0.0.5,10.0.0.6"

# cloud burst on the queue (see Cloud Bursting below)
qmgr -c "set queue batch cloud_provider = azure"
qmgr -c "set queue batch cloud_vm_sku = Standard_D8s_v3"
qmgr -c "set queue batch cloud_max_nodes = 8"
qmgr -c "set queue batch cloud_idle_time = 300"
qmgr -c "set queue batch cloud_reclaim = deallocate"
```

Detailed guides:
- [Node Selection & Host Groups](node-selection.md)
- [Multi-Node Placement](multi-node-placement.md)
- [Queue Policy](queue-policy.md)
- [Resource Constraints (GPU/license + backfill)](resource-constraints.md)
- [Job & Data Persistence](job-persistence.md)
- [Cloud Bursting](cloud-bursting.md)

## Upgrading from TORQUE

OpenTorque uses the same `$PBS_HOME` directory structure as TORQUE.

1. Stop TORQUE daemons
2. Install OpenTorque binaries
3. Generate auth_key: `pbs_server -t create` (or start server once)
4. Copy `auth_key` to all nodes
5. Start OpenTorque daemons

Existing job scripts, queue configurations, and node definitions are compatible.

## Systemd Service Files

Example unit files are provided in `configs/systemd/`:

```bash
sudo cp configs/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pbs_server pbs_sched pbs_mom
```
The scheduler unit injects `AZURE_CLIENT_ID` (Azure managed identity) used by
the cloud-bursting controller. For external mode, keep
`/var/spool/torque/sched_priv/sched_config` present so `pbs_sched` starts clean.

## Troubleshooting

### Server won't start
- Check `/var/spool/torque/` exists and is writable by root
- Check port 15001 is not in use: `ss -tlnp | grep 15001`

### MOM can't connect to server
- Verify `$pbsserver` in `mom_priv/config` points to the correct hostname
- Verify `auth_key` is identical on server and node
- Check firewall allows port 15001 (server) and 15002 (MOM)

### CLI commands fail with "authentication error"
- Verify `/var/spool/torque/auth_key` exists and is readable
- Ensure the key matches the server's key
