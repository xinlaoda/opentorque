#!/bin/bash
# HA cluster status: lease holder, per-master health, node list, job count.
# Usage: ha-status.sh [<lb-frontend-gateway>]  (default LB_EP or prompt)
set -eu
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
LB_EP="${1:-${LB_EP:-10.0.0.10}}"

echo "== lease holder =="
PGPASSWORD="${PBS_PG_PASS?:set PBS_PG_PASS}" psql \
  "host=${PBS_PG_HOST?:set PBS_PG_HOST} user=pbs dbname=pbs sslmode=require" \
  -tc "SELECT holder||'  expires_fresh='|| (expires > extract(epoch from now()))::text FROM ot_lease;" 2>&1 || echo "(cannot query lease)"

echo "== LB frontend =="
timeout 3 bash -c "cat </dev/null >/dev/tcp/$LB_EP/15001" 2>/dev/null && echo "UP" || echo "DOWN"

echo "== nodes =="
export PBS_DEFAULT="$LB_EP"
/usr/local/bin/pbsnodes -a 2>&1 | grep -E '^[a-z]|state = |np = ' || echo "(no nodes)"

echo "== jobs =="
/usr/local/bin/qstat 2>&1 | tail -n +1
