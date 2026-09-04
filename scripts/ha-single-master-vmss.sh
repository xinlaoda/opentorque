#!/bin/bash
set -eu
# OpenTorque single-master auto-replacement via VMSS + custom image (Azure).
#
# With only ONE master VM, the fastest self-healing cloud pattern is:
#   - capture a custom VM image of a fully-deployed master
#     (software installed, pbs_server+pbs_sched systemd-enabled, ha.env set)
#   - run that image in a one-instance VMSS inside the LB backend
#   - when the instance dies, VMSS auto-recreates it from the image; the master
#     re-joins: acquires the lease, opens the health port, LB flips -> ~2-4 min RTO
#     (provisioning-dominated), vs ~20s for two-master hot standby.
#
# Reference commands (review/substitute ids; run from the machine with az):
AZURE_SUB="${AZURE_SUB?set AZURE_SUB}"
RG="${RG?set RG}"; LOC="${LOC:-westus3}"
VNET="${VNET?set VNET}"; SUBNET="${SUBNET?set SUBNET}"; SRC="${SRC?set SRC (deployed master vm name)}"
IMAGE="otx-master-img"; VMSS="otx-master-vmss"
LB="${LB?set LB}"; LBPOOL="${LBPOOL?set LBPOOL}"; LBHEALTH="${LBHEALTH:-15150}"

az account set --subscription "$AZURE_SUB"

# 1) Capture a custom image of the deployed master (from its OS disk).
#    az image create copies the disk; it does NOT generalize, so the VM is untouched.
az image create -g "$RG" -n "$IMAGE" -l "$LOC" --source "$SRC" -o none

# 2) Create a one-instance VMSS from the image, on the LB subnet.
#    (systemd units + ha.env are baked in by the build, so boot = ready master.)
az vmss create -g "$RG" -n "$VMSS" -l "$LOC" --image "$IMAGE" \
   --vm-sku Standard_D2s_v3 --instance-count 1 \
   --orchestration-mode Uniform \
   --vnet-name "$VNET" --subnet "$SUBNET" \
   --public-ip-address "" --load-balancer "$LB" --backend-pool-name "$LBPOOL" -o none
# mark the VMSS instances as the LB health-probe target on the SAME health port
az network lb probe update -g "$RG" --lb-name "$LB" -n otxHealth --port "$LBHEALTH" --interval 5 --probe-threshold 1 -o none

# 3) Test auto-replacement: delete the instance -> VMSS recreates from the image;
#    time from delete until the LB frontend serves the (re)started master.
#    az vmss delete-instances --instance-ids 0
#    then poll the LB frontend (scripts/ha-failover-drill.sh) - expect ~2-4 min.
echo "single-master VMSS deployed. Auto-replace RTO ~2-4 min (provisioning-bound)."
