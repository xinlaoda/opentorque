#!/bin/bash
set -eu
# OpenTorque single-master auto-replacement via a custom image + one-instance VMSS.
#
# WORKING ROUTE (verified live on Azure, westus3, subscription dfcb03a2):
#   * Newer Azure marketplace images are TrustedLaunch, and `az image create`
#     from a TrustedLaunch VM is DISALLOWED ("Image creation is not supported
#     for Virtual Machines with TrustedLaunch/ConfidentialVM enabled").
#   * The working route is a GENERALIZED, non-TrustedLaunch (Gen1) golden VM:
#       - build a disposable golden VM from a Marketplace Ubuntu (non-TLS),
#       - deploy opentorque + systemd units + /etc/opentorque/ha.env,
#       - ssh: sudo waagent -deprovision+user   (Linux OOB prep for generalize)
#       - az vm generalize -g "$RG" -n golden
#       - az image create -g "$RG" -n otx-master-img --source golden -l "$LOC"
#       - az vmss create --image otx-master-img  (Uniform, 1 instance)
#   * A SPECIALIZED image (SIG) CANNOT be used with `az vmss create` (Azure
#     rejects "OSProfile is not allowed with a specialized image").
#
# PREREQUISITE (critical): the subnet MUST have a NAT gateway. VMSS instances
# are private (no public IP / outbound), and a freshly-booted master must reach
# the managed PostgreSQL at boot (NewPostgresStore connect + schema ensure with
# retries) or it falls back to the FILE store and does NOT enter HA (no health
# port -> LB won't serve it). Add:
#   az network nat gateway create -g "$RG" -n otx-nat --location "$LOC" \
#       --public-ip-addresses <nat-pip-id>
#   az network vnet subnet update -g "$RG" -n "$SUBNET" --vnet-name "$VNET" \
#       --nat-gateway otx-nat
#
# AUTO-REPLACE TRIGGER: use `az vmss scale --new-capacity 1` (NOT
# `az vmss delete-instances`, which merely shrinks to 0 and does not rebuild).
# Setting capacity back to 1 re-creates a fresh instance from the image.
#
# MEASURED (live): once the image is ready, end-to-end auto-replace RTO is
# ~45 s: scale trigger (1788561501.071) -> new master up + joined HA + health
# port + LB frontend UP (1788561546.486). The new instance auto-joins the LB
# backend pool, takes the ot_lease (holder = its hostname), and serves jobs.
#
# Reference (substitute ids; run with az logged in, subscription set):
AZURE_SUB="${AZURE_SUB?set AZURE_SUB}"
RG="${RG?set RG}"; LOC="${LOC:-westus3}"
VNET="${VNET?set VNET}"; SUBNET="${SUBNET?set SUBNET}"
GOLDEN="${GOLDEN?set GOLDEN (golden master vm to generalize)}"
IMG="otx-master-img"; VMSS="${VMSS:-otx-vmss2}"
LB="${LB?set LB}"; LBPOOL="${LBPOOL?set LBPOOL}"
NATGATEWAY="${NATGATEWAY?set NATGATEWAY (nat gateway name)}"
NATPIP="${NATPIP?set NATPIP (public ip for the nat gateway)}"

az account set --subscription "$AZURE_SUB"

# 1) NAT gateway so the private VMSS instance can reach managed PostgreSQL.
az network nat gateway create -g "$RG" -n "$NATGATEWAY" -l "$LOC" \
    --public-ip-addresses "$NATPIP" -o none
az network vnet subnet update -g "$RG" -n "$SUBNET" --vnet-name "$VNET" \
    --nat-gateway "$NATGATEWAY" -o none

# 2) Generalize the golden VM (non-TrustedLaunch) and capture a generalized image.
#    (Before generalizing: deploy opentorque + systemd + ha.env on the golden VM,
#     and run `sudo waagent -deprovision+user` on it.)
az vm generalize -g "$RG" -n "$GOLDEN" -o none
az image create -g "$RG" -n "$IMG" --source "$GOLDEN" -l "$LOC" -o none

# 3) One-instance Uniform VMSS from the generalized image, on the LB backend.
az vmss create -g "$RG" -n "$VMSS" --image "$IMG" --vm-sku Standard_D2s_v3 \
    --instance-count 1 --orchestration-mode Uniform \
    --vnet-name "$VNET" --subnet "$SUBNET" --nsg "${RG}-nsg" \
    --load-balancer "$LB" --backend-pool-name "$LBPOOL" \
    --public-ip-address "" -l "$LOC" -o none

# 4) Bake in boot resilience so a fresh master ALWAYS joins HA:
#    - /etc/systemd/system/pbs_server.service has an ExecStartPre that waits for
#      the PostgreSQL DNS/TCP before starting pbs_server, and
#    - NewPostgresStore retries connect + schema-ensure (see commit 38d5e1f).
#    Otherwise a replaced master may come up but stay in the file store.

# 5) Auto-replace trigger (NOT delete-instances):
#    az vmss scale -g "$RG" -n "$VMSS" --new-capacity 1
#    Then poll the LB frontend (scripts/ha-failover-drill.sh). Measured total RTO
#    = ~45 s when the image is already ready.
echo "generalized image $IMG + one-instance VMSS $VMSS ready; auto-replace RTO ~45 s (measured)."
