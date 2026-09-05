# Fair-share is a lie in the cloud: what to do instead

*Fair-share scheduling is one of the most beloved features of a batch
scheduler. On a fixed on-premise cluster it is the only way to keep one team
from starving the others. In the cloud, it solves a problem that no longer
exists - and people keep porting it over out of habit. This article is a
change-of-mind: why fair-share fails in the cloud, and the mechanisms that
actually deliver fairness when your capacity is elastic and every VM-hour is
money.*

## What fair-share was really for

Classic fair-share (PBS/TORQUE/Maui, Slurm `fairshare`) answers one question:
**"of this fixed, scarce, shared machine, how much should each team get?"**

It does this by tracking historical usage per account and decaying it over
time, then scheduling the *under-using* accounts ahead so everyone converges
on their share. It exists because the resource - the cluster - is a **fixed
finite hardware capex** that everyone must slice.

Three things fair-share buys you on-prem:
1. **Arbitration** - when 10 teams want the same 500 cores, decide who gets them.
2. **Protection** - an important job isn't starved by a flood of small ones
   (usually via priority **preemption**).
3. **Starvation avoidance** - no single account permanently monopolizes.

Every one of these three raisons d'etre is **weakened or inverted** by the cloud.

## Why the cloud breaks fair-share

### 1. The scarcity that made it necessary is gone

Fair-share arbitrates a **fixed number of cores**. In the cloud, capacity is
`N * (whatever you are willing to pay)`. When a team needs more, you
**scale out** - add a VMSS instance - instead of telling them "sorry, it's not
your turn." Arbitrating scarcity is meaningless when scarcity is a budget
decision, not a hardware ceiling.

The moment you stop thinking "who owns the box" and start thinking "who pays
for the VM-hours," fair-share's central computation (decayed share of a fixed
machine) has no object to operate on.

### 2. The real scarce resource is money, not cores

On-prem, marginal compute is ~free (the capex is sunk). In the cloud,
**every core-hour is metered spend**. The thing you actually need to divide
fairly is the **budget**, not the machine - and a "share of the box" algorithm
tells you nothing about cost. A team can run exactly its fair *share* of a
node and still blow the monthly cloud bill, or (conversely) demand "more
cores" when the real ask is "more money."

### 3. Preemptive QoS is the wrong tool now

On-prem, protecting a priority job means **preempting** (suspend/requeue) a
lower-priority running job - because you only have one pool of finite cores.

In the cloud this is actively harmful:
- A suspended VM/spot instance you're billed for anyway, or a job you kill and
  re-run, **costs you and wastes the partially-computed work**.
- There is no reason to fight over one pool: put the priority job in an
  **on-demand / reserved-instance pool** and the tolerant work in the
  **spot / cheap pool**. That is QoS by *capacity tier*, not by killing work.

### 4. Hard limits are already enforced by the platform

The "quota" job of multi-tenancy (`max cores per user`, `max jobs per group`)
is, in the cloud, already done for you and better: **subscription vCPU quota
per series, VMSS capacity, cost budgets, Management Groups, VNet/private
-endpoint isolation.** Reimplementing a quota engine on top of an elastic
platform that enforces its own cap is duplicating the thing that already
guards the boundary - and doing it *worse* (you don't see the money; it does).

### 5. Elastic isolation beats fair-share arbitration

The deepest reason fair-share exists is so one team can't crowd another. In
the cloud you don't arbitrate the shared pool - you **give each project its own
pool**. A dedicated queue + node group / VMSS per tenant means a noisy
neighbor literally cannot consume your capacity, because it isn't in your
pool. "Fairness" becomes an **isolation property**, not a scheduling score.

## What actually delivers "fair-share's intent" in the cloud

Keep the goal ("no one starves, important work gets through, spend is fair")
but change the mechanism:

| On-prem fair-share intent | Cloud-native replacement |
|---|---|
| Arbitrate a fixed machine | **Elastic scale-out** - budget is decided by spend, not share |
| Historical fair-share score | **Chargeback / cost attribution** - tag jobs by account, measure real spend (`pbs_cost`) |
| Share-of-box fairness over time | **Per-project budget/concurrent cap** - cap a project's running cores and spend |
| QoS via preemption | **Capacity tiers** - on-demand vs **spot** vs **reserved** pools as the QoS level |
| One shared pool, policing | **Per-tenant isolation** - dedicated queue + node group / VMSS so one project cannot crowd another |
| Soft quota engine | **Platform quota + budgets** (subscription vCPU quota, VMSS capacity, Azure budgets) as the hard ceiling |

The shift in mindset is the message:

> **Stop arbitrating scarcity; start governing spend and isolation.** Fairness
> in the cloud is not "everyone gets their share of the box," it is "every
> project's bill is attributed and capped, important work uses a higher
> capacity tier, and no project can consume another's pool."

## Concretely, in OpenTorque

OpenTorque's cloud-native posture (documented in `docs/cloud-costing.md` and
`TODO.md` 2.2 / 2.7 / 2.8) is exactly the "change of mind" above:

- **Do not implement** classic hierarchical fair-share math, preemptive QoS,
  or advance reservation - capacity is elastic, so these add complexity without
  the fairness they bought on a fixed box (`TODO 2.2`, `2.7`).
- **Implemented (part 1):** `-A account` attribution into accounting and
  `pbs_cost` - apportions each node's *real* VM bill across accounts by share
  of used core-seconds (handles shared nodes and idle/boot time; reconciles
  with the provider bill). This is cloud fairness: **divide the money, not the
  machine.** (`TODO 2.7`, `docs/cloud-costing.md`)
- **Roadmap (part 2):** per-project concurrent-running caps, routing projects
  to on-demand/spot/reserved pools as the QoS tier, and dedicated queue +
  node group pools for tenant isolation - the *isolation and budget* model
  instead of a fair-share score.

## The bottom line

Fair-share assumes a scarcer resource than you actually have in the cloud. Its
real intentions - "no one starves, important work goes through, spend is fair"
- are better served by **cost attribution, budget caps, capacity tiers, and
  per-tenant isolation**, all of which lean on mechanisms the platform already
  gives you. If you find yourself implementing a decayed-usage share-of-the-box
  fair-share engine for an elastic fleet, pause: the thing you're trying to be
  fair *about* is no longer the box - it is the bill.

See also: `docs/cloud-costing.md` (the cost-attribution model), `docs/opentorque-ha.md`
(cloud-native HA), `README.md` (project posture), `TODO.md` items 2.2 / 2.7 / 2.8.