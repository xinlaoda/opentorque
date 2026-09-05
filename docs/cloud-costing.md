# Cloud cost attribution (finops / chargeback)

OpenTorque's cloud-native cost model attributes **real VM spend** to the
accounts/projects that actually used the compute — not a naive
"job walltime x unit price" estimate.

## Why not job-walltime x price?

- A cloud VM is billed for the **whole time it is powered on** (boot + execution
  + idle + drain), which is **not** the sum of the job walltimes that ran on it.
- A single (often shared/pooled) node can run jobs from **many accounts** on one
  billed VM.
- So per-job pricing undercounts actual spend (it ignores idle/boot/drain) and
  cannot split one VM's bill across the accounts that shared it.

## The model: node = cost pool, apportioned by core-seconds

1. Each node/VM has a **real bill** = SKU price/hour x its billed (powered-on) hours.
2. Each job records **account, ncpus, start, end** -> **core-seconds** per node.
3. Each node's bill is split among the accounts that used it **by their share of
   that node's used core-seconds**. Idle/boot/drain time is implicitly charged
   pro-rata to whoever used the node (the whole bill is covered - no undercount).
4. A node that was billed but had **zero usage** (e.g. a wrongly-scaled empty VM)
   goes to an **overhead** bucket, attributable to the pool/queue that brought it up.

This reconciles with the provider bill at the node/VM granularity, and handles
shared/multi-node jobs and idle time automatically.

## Tool: `pbs_cost`

`pbs_cost` reads a day's accounting file, builds per-(account, node) core-seconds,
takes each node's bill, and prints the per-account report.

```
# accounting lives at <home>/server_priv/accounting/YYYYMMDD
pbs_cost -home /var/spool/torque -date 20260904 \
    -uptime-hours 'nodeA=24,nodeB=24' \
    -node-sku   'nodeA=Standard_D2s_v3,nodeB=Standard_D4s_v3' \
    -price      'Standard_D2s_v3=0.096,Standard_D4s_v3=0.192' \
    -default-price 0.1
# or give exact node bills directly (most accurate, matches Azure bill):
pbs_cost -home /var/spool/torque -date 20260904 \
    -bill-usd 'nodeA=2.30,nodeB=4.10'
```

Output example:

```
account                core-seconds    cost(USD)
------------------------------------------------
projA                         21600      10.0000
projB                         28800       8.0000
------------------------------------------------
TOTAL                                    18.0000
```

### Flags

| flag | meaning |
|---|---|
| `-home` | PBS home; accounting read from `<home>/server_priv/accounting` |
| `-accounting` | override the accounting directory |
| `-date` | accounting day `YYYYMMDD` (default today) |
| `-bill-usd` | exact node bills `node=USD,...` (preferred; matches the real Azure bill) |
| `-uptime-hours` | node billed hours `node=H,...` |
| `-node-sku` | node -> SKU `node=sku,...` |
| `-price` | SKU -> USD/hour `sku=price,...` |
| `-default-price` | fallback USD/hour when a node's SKU price is unknown |

## Attribution

Jobs must carry an account for clean per-project reporting: submit with
`qsub -A <account>` (stored on the job as `Account_Name`, written into the
accounting S and E records as `account=<acct>`).

## Reconciliation with Azure

- Tag dynamic nodes / VMSS pools with an Azure cost tag (`project=<pool>`) when
  they are dedicated to a project, so Azure Cost Management groups spend by tag.
- Compare Azure's per-VM bill with `pbs_cost -bill-usd` for those nodes; they
  should match at node granularity. Differences indicate only SKU-price config
  drift, not a flaw in apportionment (the full billed amount is always covered).
- Idle-with-no-jobs overhead is shown separately and should be minimized by the
  elastic controller's scale-in (TODO 4.4c / M3).
