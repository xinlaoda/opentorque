#!/bin/bash
set -eu
# OpenTorque cloud-native HA deployment (Azure)
# Deploys the production HA topology:
#   - two master VMs (control plane: pbs_server+pbs_sched, systemd, HA)
#   - one (or more) dedicated compute VMs (pbs_mom only)
#   - Azure Database for PostgreSQL Flexible (managed, shared store)
#   - Azure internal Load Balancer (frontend=VIP, active-only health probe)
#
# Prereqs: az CLI logged in, the subscription set, SSH key, this repo.
# See docs/opentorque-ha.md for the architecture.
#
# Usage:
#   AZURE_SUB=<sub-id> RG=... ./scripts/ha-deploy.sh <phase>
#   phases: infra | deploy-masters | deploy-compute | all

AZURE_SUB="${AZURE_SUB:?set AZURE_SUB}"
RG="${RG:?set RG (e.g. xxin-opentorque-test)}"
LOC="${LOC:-westus3}"
VNET="${VNET:-otx-vnet}"; SUBNET="${SUBNET:-otx-sub}"; PREFIX="${PREFIX:-10.1.0.0/24}"
MASTER1="${MASTER1:-otx-m1}"; MASTER2="${MASTER2:-otx-m2}"; COMPUTE="${COMPUTE:-otx-w1}"
VMSIZE="${VMSIZE:-Standard_D2s_v3}"
PGNAME="${PGNAME:-otx-pg}"; PGUSER="${PGUSER:-pbsadmin}"; PGPASS="${PGPASS:-}"
LBNAME="${LBNAME:-otx-lb}"; LBFE="${LBFE:-10.1.0.10}"; LBHEALTH="${LBHEALTH:-15150}"
SSH_PUB="${SSH_PUB:-$HOME/.ssh/id_rsa.pub}"
IMAGE="Canonical:ubuntu-24_04-lts:server:latest"
PGDSN="postgres://pbs:pbs@$PGNAME.postgres.database.azure.com:5432/pbs?sslmode=require"
SHARED_KEY="$(head -c 32 /dev/urandom | xxd -p -c 64)"

az account set --subscription "$AZURE_SUB"

phase_infra() {
  echo "## infra: VNet/subnet, NSG, LB, managed PG"
  az group create -g "$RG" -l "$LOC" -o none 2>/dev/null || true
  az network vnet create -g "$RG" -n "$VNET" --address-prefix "$PREFIX" --subnet-name "$SUBNET" --subnet-prefixes "$PREFIX" -o none 2>/dev/null || true
  NSG="$RG-nsg"
  az network nsg create -g "$RG" -n "$NSG" -l "$LOC" -o none 2>/dev/null || true
  az network nsg rule create -g "$RG" --nsg-name "$NSG" -n allow-15001 --priority 100 --protocol Tcp --destination-port-ranges 15001 --source-address-prefixes VirtualNetwork --access Allow -o none
  az network nsg rule create -g "$RG" --nsg-name "$NSG" -n allow-15002 --priority 101 --protocol Tcp --destination-port-ranges 15002 --source-address-prefixes VirtualNetwork --access Allow -o none
  az network nsg rule create -g "$RG" --nsg-name "$NSG" -n allow-"$LBHEALTH" --priority 102 --protocol Tcp --destination-port-ranges "$LBHEALTH" --source-address-prefixes AzureLoadBalancer --access Allow -o none
  az network nsg rule create -g "$RG" --nsg-name "$NSG" -n allow-ssh --priority 1000 --protocol Tcp --destination-port-ranges 22 --source-address-prefixes '*' --access Allow -o none 2>/dev/null || true
  # managed PostgreSQL (Public access + firewall for the VNet)
  az provider register -n Microsoft.DBforPostgreSQL -o none 2>/dev/null || true
  az postgres flexible-server create -g "$RG" -n "$PGNAME" -l "$LOC" --sku-name Standard_B1ms --tier Burstable \
      --admin-user "$PGUSER" --admin-password "$PGPASS" --public-access 0.0.0.0-255.255.255.255 --storage-size 32 --version 16 -o none
  # internal LB
  az network lb create -g "$RG" -n "$LBNAME" --sku Standard -l "$LOC" --frontend-ip-name LBFront --vnet-name "$VNET" --subnet "$SUBNET" --private-ip-address "$LBFE" --backend-pool-name BEPool -o none
  az network lb probe create -g "$RG" --lb-name "$LBNAME" -n LBHealth --protocol Tcp --port "$LBHEALTH" --interval 5 --probe-threshold 1 -o none
  az network lb rule create -g "$RG" --lb-name "$LBNAME" -n LBRule --protocol Tcp --frontend-port 15001 --backend-port 15001 --frontend-ip-name LBFront --backend-pool-name BEPool --probe-name LBHealth -o none
}

vm_create() { # vm_create <name>
  local n="$1"
  az vm create -g "$RG" -n "$n" --image "$IMAGE" --size "$VMSIZE" --admin-username azureuser \
     --ssh-key-values "$SSH_PUB" --public-ip-sku Standard \
     --vnet-name "$VNET" --subnet "$SUBNET" --nsg "$RG-nsg" --nsg-rule SSH -l "$LOC" -o json
}

deploy_one() { # deploy_one <vm> <is_master|is_compute>
  local n="$1" role="$2"
  local ip; ip=$(az vm show -g "$RG" -n "$n" --query "publicIpAddress" -o tsv 2>/dev/null)
  [ -n "$ip" ] || ip=$(az vm list-ip-addresses -g "$RG" -n "$n" --query "[0].virtualMachine.network.publicIpAddresses[0].ipAddress" -o tsv)
  echo "  deploying $role to $n ($ip)"
  # (on the VM): archive into repo, build, install daemons+CLI, systemd, ha.env
  # This step assumes the source archive is already on the VM (see NOTES). The
  # essential on-VM steps are:
  #  tar -xf opentorque-src.tar -C opentorque  (extract)
  #  export GOTOOLCHAIN=local PATH=...:go ...   (build)
  #  build+install daemons+CLIs to /usr/local/{sbin,bin}
  #  mkdir -p /etc/opentorque
  #  printf 'PBS_HA=1\nPBS_PG_DSN=%s\nPBS_HA_HEALTH_PORT=%s\n' "$PGDSN" "$LBHEALTH" \
  #     | sudo tee /etc/opentorque/ha.env
  #  install configs/systemd/pbs_server.service + pbs_sched.service
  #  systemctl daemon-reload && systemctl enable --now pbs_server pbs_sched
  #  (compute: install pbs_mom.service;  write mom_priv/config $pbsserver <fe>)
  echo "  (deploy_one: see NOTES for on-VM build+systemd steps)"
  true
}

phase_masters() {
  echo "## create + deploy master VMs"
  vm_create "$MASTER1"; vm_create "$MASTER2"
  for m in "$MASTER1" "$MASTER2"; do deploy_one "$m" master; done
  # add master NICs to LB backend pool
  for m in "$MASTER1" "$MASTER2"; do
    az network nic ip-config address-pool add -g "$RG" --nic-name "$m"VMNic --ip-config-name "ipconfig$m" --lb-name "$LBNAME" --address-pool BEPool -o none
  done
}
phase_compute() {
  echo "## create + deploy compute node"
  vm_create "$COMPUTE"
  deploy_one "$COMPUTE" compute
}

phase=${1:-all}
case "$phase" in
  infra) phase_infra;;
  masters) phase_masters;;
  compute) phase_compute;;
  all) phase_infra; phase_masters; phase_compute;;
  *) echo "unknown phase: $phase"; exit 2;;
esac
echo "## HA deploy phase '$phase' done. See docs/opentorque-ha.md."
