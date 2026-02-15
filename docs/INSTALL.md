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
sudo systemctl enable --now pbs_server pbs_mom
```

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
