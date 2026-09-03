#!/bin/bash
set -u
# OpenTorque HA failover drill.
#
# Measures the client-visible switchover window of the dual-master + Azure LB +
# managed-PostgreSQL topology by continuously probing the LB frontend, stopping
# the active master's pbs_server, and reporting when the frontend recovers on
# the standby.
#
# Run from any host that can reach the LB frontend cross-host (i.e. NOT the
# active master; a compute MOM or an admin box is ideal).
#
# Usage:
#   scripts/ha-failover-drill.sh <LB_FRONTEND:PORT> <active-ssh-args...>
# example:
#   scripts/ha-failover-drill.sh 10.0.0.10:15001 azureuser@20.38.11.96 -i ~/.ssh/id_rsa
#
# After the drill, restart the stopped master:  systemctl start pbs_server

LB_EP="${1:?usage: ha-failover-drill.sh <lb:port> <active-ssh-args...>}"
shift
LOG="$(mktemp)"
PROBE_PID=0
cleanup() { [ "$PROBE_PID" -ne 0 ] && kill "$PROBE_PID" 2>/dev/null; rm -f "$LOG"; }
trap cleanup EXIT

probe() { # continuously timestamped-probe the LB frontend
  local ep="$1" t ok
  while :; do
    t="$(date +%s.%3N)"
    if timeout 1 bash -c "cat </dev/null >/dev/tcp/$ep" 2>/dev/null; then ok=UP; else ok=DOWN; fi
    echo "$ok $t" >>"$LOG"
    sleep 0.25
  done
}

probe "$LB_EP" & PROBE_PID=$!
sleep 4
echo "> baseline ($LB_EP):"; grep UP "$LOG" | tail -2

echo "> stopping active pbs_server on: $*"
T0="$(date +%s.%3N)"
if ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$@" "sudo systemctl stop pbs_server"; then
  echo "  stopped at $T0"
else
  echo "  WARN: ssh/stop failed (still measuring the window)"
fi

# wait until we have seen a DOWN and the probe has returned to UP
for _ in $(seq 1 240); do
  if [ "$(grep -c DOWN "$LOG")" -gt 0 ] && [ "$(tail -1 "$LOG" | cut -d' ' -f1)" = UP ]; then break; fi
  sleep 1
done
sleep 2

DOWN_T="$(grep -m1 DOWN "$LOG" | cut -d' ' -f2)"
UP_T="$(awk '/^DOWN/{s=1} /^UP/&&s{print $2; exit}' "$LOG")"
echo "> first DOWN (client-visible): $DOWN_T"
echo "> first UP   (recovered)     : $UP_T"
if [ -n "$DOWN_T" ] && [ -n "$UP_T" ]; then
  python3 - "$DOWN_T" "$UP_T" <<'PY'
import sys
d,u=map(float,sys.argv[1:3])
print(f"> client-visible switchover: {u-d:.1f} s")
PY
else
  echo "> no DOWN observed - the LB stayed healthy; is the target really the active?"
fi
echo "> done. Restart the stopped master with:  systemctl start pbs_server"
