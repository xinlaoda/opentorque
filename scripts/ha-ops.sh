#!/bin/bash
# OpenTorque HA operations front-end.
#   ha-ops.sh status                    - lease + health + nodes (ha-status.sh)
#   ha-ops.sh drill <lb:port> <active-ssh...>  - failover drill (ha-failover-drill.sh)
#   ha-ops.sh deploy <phase>            - provision topology (ha-deploy.sh)
#   ha-ops.sh vmss-setup                - single-master custom-image/VMSS setup
#   ha-ops.sh stop-active <ssh...>      - stop the given active master (test)
#   ha-ops.sh start-master <ssh...>     - (re)start a master (systemd)
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
case "${1:-}" in
  status) shift; "$DIR/ha-status.sh" "$@";;
  drill)  shift; "$DIR/ha-failover-drill.sh" "$@";;
  deploy) shift; "$DIR/ha-deploy.sh" "$@";;
  vmss-setup) "$DIR/ha-single-master-vmss.sh";;
  stop-active) shift; ssh -o BatchMode=yes "$@" "sudo systemctl stop pbs_server";;
  start-master) shift; ssh -o BatchMode=yes "$@" "sudo systemctl start pbs_server pbs_sched";;
  *) echo "usage: $0 status|drill|deploy|vmss-setup|stop-active|start-master"; exit 2;;
esac
