# OpenTorque Package Installation Guide

This guide covers installing OpenTorque from pre-built packages (DEB/RPM).

## Package Overview

OpenTorque is split into three packages:

| Package | Contents | Install On |
|---------|----------|------------|
| **opentorque-server** | `pbs_server`, `pbs_sched`, systemd units, scheduler config | Head node |
| **opentorque-compute** | `pbs_mom`, `momctl`, `pbs_track`, systemd unit | Compute nodes |
| **opentorque-cli** | All CLI tools (`qsub`, `qstat`, `qdel`, etc.) | All nodes / submit hosts |

## Prerequisites

- **OS**: Linux (Ubuntu/Debian, RHEL/CentOS/Rocky, SUSE)
- **Architecture**: amd64 (x86_64) or arm64 (aarch64)
- **Root access**: Required for package installation
- **Network**: All nodes must be able to reach the head node on **port 15001** (server) and compute nodes on **port 15002** (MOM)
- **openssl**: Required at build time (for auth key generation)
- **Hostname resolution**: All nodes must resolve each other's hostnames

## Building Packages

If you need to build packages from source:

```bash
git clone https://github.com/xinlaoda/opentorque.git
cd opentorque

# Build DEB packages (Ubuntu/Debian)
make packages-deb

# Build RPM packages (RHEL/CentOS/Rocky/SUSE)
make packages-rpm

# Build for different architecture
GOARCH=arm64 ./scripts/packaging/build-packages.sh deb 0.1.0
```

Packages are output to the `dist/` directory.

## Installation Steps

### Step 1: Install Server Package (Head Node)

**Ubuntu/Debian:**
```bash
sudo dpkg -i opentorque-server_0.1.0-1_amd64.deb
```

**RHEL/CentOS/Rocky:**
```bash
sudo rpm -ivh opentorque-server-0.1.0-1.x86_64.rpm
```

**SUSE:**
```bash
sudo zypper install opentorque-server-0.1.0-1.x86_64.rpm
```

The server postinstall script automatically:
- Sets `/var/spool/torque/server_name` to the current hostname
- Copies default scheduler config to `/var/spool/torque/sched_priv/sched_config`

> **Note:** The `auth_key` is generated at build time and embedded in all three packages.
> No manual key distribution is needed when all packages are built together.

### Step 2: Initialize the Server (First Time Only)

```bash
sudo pbs_server -t create
```

This creates the server database. Stop it after initialization (`Ctrl+C` or `kill`).

### Step 3: Configure the Server

**Register compute nodes:**
```bash
# Using qmgr (server must be running)
sudo pbs_server &
qmgr -c "create node compute01 np=8"
qmgr -c "create node compute02 np=16"
```

**Create a default queue:**
```bash
qmgr -c "create queue batch"
qmgr -c "set queue batch queue_type = Execution"
qmgr -c "set queue batch started = True"
qmgr -c "set queue batch enabled = True"
qmgr -c "set server default_queue = batch"
```

**Scheduler configuration** (optional):
Edit `/var/spool/torque/sched_priv/sched_config` to customize scheduling behavior.

### Step 4: Auth Key (Already Distributed)

When packages are built together using `build-packages.sh`, all three packages contain the **same auth_key**. No manual copying is needed.

If you rebuild packages separately or need to rotate the key:
```bash
# Generate a new key on the server
openssl rand -hex 32 > /var/spool/torque/auth_key
chmod 644 /var/spool/torque/auth_key

# Copy to compute nodes and CLI hosts
scp /var/spool/torque/auth_key compute01:/var/spool/torque/auth_key
scp /var/spool/torque/auth_key compute02:/var/spool/torque/auth_key
```

### Step 5: Install Compute Package (Compute Nodes)

On each compute node:

```bash
# Ubuntu/Debian
sudo dpkg -i opentorque-compute_0.1.0-1_amd64.deb

# RHEL/CentOS
sudo rpm -ivh opentorque-compute-0.1.0-1.x86_64.rpm
```

**Configure MOM** to point to the server:
```bash
echo '$pbsserver headnode' > /var/spool/torque/mom_priv/config
```
Replace `headnode` with the actual hostname of your PBS server.

### Step 6: Install CLI Package (Submit Hosts)

On any host that needs job submission capability:

```bash
# Ubuntu/Debian
sudo dpkg -i opentorque-cli_0.1.0-1_amd64.deb

# RHEL/CentOS
sudo rpm -ivh opentorque-cli-0.1.0-1.x86_64.rpm
```

**Configure server name:**
```bash
echo 'headnode' > /var/spool/torque/server_name
```

### Step 7: Start Services

**Head node:**
```bash
sudo systemctl enable --now pbs_server
sudo systemctl enable --now pbs_sched
```

**Compute nodes:**
```bash
sudo systemctl enable --now pbs_mom
```

### Step 8: Verify Installation

```bash
# Check node status (wait ~45 seconds for MOM heartbeat)
pbsnodes -a

# Check server status
qmgr -c "list server"

# Submit a test job
echo '#!/bin/bash
echo "Hello from OpenTorque"
hostname' | qsub -N test-job

# Check job status
qstat

# Check output (after completion)
cat ~/test-job.o*
```

## Directory Structure

```
/var/spool/torque/
├── auth_key                    # Shared authentication key (built into all packages)
├── server_name                 # PBS server hostname
├── server_priv/                # Server private data
│   ├── nodes                   # Registered compute nodes
│   ├── jobs/                   # Job state files (.JB, .SC)
│   ├── serverdb                # Server database
│   └── acl_svr/                # Access control lists
├── sched_priv/                 # Scheduler private data
│   └── sched_config            # Scheduler configuration
├── mom_priv/                   # MOM private data
│   ├── config                  # MOM configuration
│   └── jobs/                   # MOM job tracking
├── server_logs/                # Server log directory
├── mom_logs/                   # MOM log directory
├── sched_logs/                 # Scheduler log directory
├── spool/                      # Job output spool
├── aux/                        # Auxiliary files (node files)
└── undelivered/                # Undelivered output files
```

## Ports

| Service | Default Port | Protocol |
|---------|-------------|----------|
| pbs_server | 15001 | TCP (DIS batch protocol) |
| pbs_mom | 15002 | TCP (DIS batch protocol) |

Ensure these ports are open in your firewall:
```bash
# Ubuntu/Debian (ufw)
sudo ufw allow 15001/tcp
sudo ufw allow 15002/tcp

# RHEL/CentOS (firewalld)
sudo firewall-cmd --permanent --add-port=15001/tcp
sudo firewall-cmd --permanent --add-port=15002/tcp
sudo firewall-cmd --reload
```

## Package Removal

```bash
# Ubuntu/Debian
sudo dpkg -r opentorque-server opentorque-compute opentorque-cli

# RHEL/CentOS
sudo rpm -e opentorque-server opentorque-compute opentorque-cli
```

The removal scripts automatically stop services. Configuration files in `/var/spool/torque/` are **preserved** across removal for data safety.

## Troubleshooting

**Authentication errors ("permission denied"):**
- Ensure `/var/spool/torque/auth_key` exists and is readable (`chmod 644`)
- Verify the key file is identical on all nodes (`md5sum /var/spool/torque/auth_key`)

**Node stays "down":**
- Verify MOM is running: `systemctl status pbs_mom`
- Check MOM config: `cat /var/spool/torque/mom_priv/config`
- Verify network connectivity: `telnet headnode 15001`
- Wait up to 45 seconds for the first IS heartbeat

**Jobs stay "Queued":**
- Verify the scheduler is running: `systemctl status pbs_sched`
- Check queue is started and enabled: `qmgr -c "list queue batch"`
- Check for available nodes: `pbsnodes -a`
- Check scheduler logs in `/var/spool/torque/sched_logs/` or `/tmp/*sched*.log`

**Job output not delivered:**
- Check `qstat -f <jobid>` for `Output_Path`
- Verify the MOM can write to the user's home directory
- Check MOM logs for delivery errors
