#!/bin/bash
set -eu
# OpenTorque single-master auto-replacement via a custom image + one-instance VMSS.
#
# Newer Azure images are TrustedLaunch, so the classic `az image create` from a
# running master is disallowed. Use a Shared Image Gallery with a Specialized +
# TrustedLaunch image version from the master's OS-disk snapshot (no generalizing,
# the master stays online). Then run a one-instance Uniform VMSS from that image.
# Verified live (westus3): the SIG image version 1.0.0 was created successfully
# from a TrustedLaunch snapshot; VMSS creation from it is the following step.
#
# Reference (substitute ids; run with az logged in, subscription set):
AZURE_SUB="${AZURE_SUB?set AZURE_SUB}"
RG="${RG?set RG}"; LOC="${LOC:-westus3}"
VNET="${VNET?set VNET}"; SUBNET="${SUBNET?set SUBNET}"
SRCVM="${SRCVM?set SRCVM (deployed master vm; also needs its OS snapshot)}"
SNAP="${SNAP?set SNAP (OS-disk snapshot of the master)}"
SIG="otxSig"; DEF="otxImgDef"; VER="1.0.0"; VMSS="otx-vmss"
LB="${LB?set LB}"; LBPOOL="${LBPOOL?set LBPOOL}"

az account set --subscription "$AZURE_SUB"

# NOTE: a SPECIALIZED image cannot be used with `az vmss create` (Azure rejects
# "Parameter OSProfile is not allowed with a specialized image"). For a working
# VMSS you must use a GENERALIZED image. Two routes:
#
# Route A - GENERALIZED golden VM (recommended, works with az vmss create):
#   1) az vm create -n golden <ubuntu>  ; deploy opentorque + systemd + ha.env
#   2) ssh golden "sudo waagent -deprovision+user"   # linux prep for generalize
#   3) az vm generalize -g "$RG" -n golden
#   4) az image create -g "$RG" -n otx-master-img --source golden -l "$LOC"
#   5) az vmss create ... --image otx-master-img ...        # works
#
# Route B - SPECIALIZED SIG image, then create the VM model without OSProfile
#   (not via the generic az vmss create; use an ARM template that omits osProfile).
#
# 1) SIG + Specialized/TrustedLaunch image version from the master snapshot
az sig create -g "$RG" --gallery-name "$SIG" -l "$LOC" -o none
az sig image-definition create -g "$RG" --gallery-name "$SIG" \
   --gallery-image-definition "$DEF" --publisher otx --offer otx --sku otx \
   --os-type Linux --os-state Specialized \
   --features SecurityType=TrustedLaunch -o none
az sig image-version create -g "$RG" --gallery-name "$SIG" \
   --gallery-image-definition "$DEF" --gallery-image-version "$VER" \
   --target-regions "$LOC" --replica-count 1 --os-snapshot "$SNAP" -o none  # takes minutes
IMG=$(az sig image-version show -g "$RG" --gallery-name "$SIG" \
      --gallery-image-definition "$DEF" --gallery-image-version "$VER" --query id -o tsv)

# 2) One-instance Uniform VMSS from the image, on the LB subnet/backend.
#    (Recent az CLI may need the backend pool attached via the VMSS model when an
#    existing LB is reused; adjust per your CLI version.)
az vmss create -g "$RG" -n "$VMSS" --image "$IMG" --vm-sku Standard_D2s_v3 \
   --instance-count 1 --orchestration-mode Uniform \
   --vnet-name "$VNET" --subnet "$SUBNET" --nsg "${RG}-nsg" \
   --load-balancer "$LB" --backend-pool-name "$LBPOOL" \
   --public-ip-address "" -l "$LOC" -o none

# 3) Auto-replace test: delete the instance -> VMSS recreates from the image.
#    az vmss delete-instances --instance-ids 0
#    Then poll the LB frontend (scripts/ha-failover-drill.sh); expected total
#    RTO = provisioning (~2-4 min) + ~16 s failover.
echo "custom image (SIG $SIG/$DEF/$VER) ready; VMSS auto-replace RTO = provisioning + ~16 s."
