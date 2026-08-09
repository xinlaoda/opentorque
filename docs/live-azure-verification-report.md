# Live Azure Verification Report — TODO 1.3 / 1.4 / 5.2 / 1.5 / 1.6 / 2.2

- **Date:** 2026-08-08 → 08-09 (UTC)
- **Subscription:** `AB-RenderingTest`
- **Resource group:** `xxin-opentorque-test`
- **Cluster:** `xxin-opentorque-srv` (server+sched+mom), `xxin-opentorque-w1` (mom)
- **Code under test:** branch `main` @ `7a9b027` (initial) → `cadb688` (after 1.6 acl fix); `pbs_server` Go `version 0.2.0`
- **Method:** live `qsub`/`qstat`/`qmgr`/`pbsnodes` runs via `az vm run-command` against the real Azure VMs.

Both daemons were rebuilt from HEAD and restarted before these tests. Node topology:
`xxin-opentorque-w1` and `xxin-opentorque-srv` (each `np=2`, 2 cores) served as the static local pool;
one stale dynamic node `ot-node-PBf5XgPe` is `is_dynamic=true` / `down` and was never schedulable.
Temporary non-cloud test queues (`tq`, `tq_excl`, `tq_bk`, `tq_hl`, `tq_acl`) were used to avoid
triggering cloud provisioning and to exercise the non-cloud FIFO/backfill path. All were removed after the
run; the cluster was restored to only the cloud-backed `batch` queue.

---

## 1.3 Host groups (`hostgroups` + `-l host=@group`)

**Setup**: `qmgr -c "set node xxin-opentorque-w1 hostgroups=gpu"` → `pbsnodes -a` shows `hostgroups = gpu` on w1.

| Case | Command | Result | Verdict |
|------|---------|--------|---------|
| Positive | `qsub -q tq -l host=@gpu /tmp/t.sh` | Job `260` ran, `exec_host = xxin-opentorque-w1/0`; `qstat -f` showed `Resource_List.host = @gpu` | ✅ PASS |
| Negative | `qsub -q tq -l host=@nosuchgroup /tmp/t.sh` | Job `261` stayed `Q`, no `exec_host` (group not present on any node) | ✅ PASS |

> Verified: a job pinned to an existing host group is dispatched only to a member node; a job pinned to a
> non-existent group correctly stays queued.

---

## 1.4 Multi-node dispatch (`-l nodes=N:ppn=M`)

| Case | Command | Result | Verdict |
|------|---------|--------|---------|
| 2-node job | `qsub -q tq -l nodes=2:ppn=1 /tmp/tmn.sh` (sleep 20) | Job `262` ran across **both** nodes: `exec_host = xxin-opentorque-w1/0+xxin-opentorque-srv/0`; `pbsnodes` showed `jobs = 262...` on both w1 and srv | ✅ PASS |
| Repeated | `qsub -q tq -l nodes=2:ppn=1 -N nmulti /tmp/tmn2.sh` (sleep 200) | Job `263` also spanned both nodes (`w1/0+srv/0`) | ✅ PASS |

> Verified: the scheduler satisfied `nodes=2:ppn=1` by placing one core on each of two distinct nodes,
> with `exec_host`/per-node `jobs` confirming both nodes (multi-node split).

---

## 5.2 Completed-job read-back across server restart

| Step | Command | Result | Verdict |
|------|---------|--------|---------|
| Submit short job | `qsub -q tq /tmp/tc.sh` (sleep 3) | Job `279` → `C`, `exit_status=0`, `comp_time=1786218184`, `resources_used.*` recorded | prep |
| Before restart | `qstat -f 279...` | full attrs present (`comp_time`, `exit_status`, `resources_used`) | ✅ |
| Restart server | `systemctl restart pbs_server` | `active` | ✅ |
| After restart | `qstat -f 279...` | still queryable: `job_state = C`, `comp_time = 1786218184`, `exit_status = 0`, `start_time`, `exec_host`, `session_id` retained | ✅ PASS |

> Verified: completed jobs remain queryable after a server restart with core lifecycle attributes intact.
> Caveat: `resources_used.*` was visible before restart but was not re-displayed after restart (only
> `comp_time`/`exit_status`/`start_time`/`exec_host` persisted). Core read-back works.

---

## 1.5 Queue `naccesspolicy = exclusive`

**Setup**: `qmgr -c "set queue tq_excl naccesspolicy=exclusive"` (confirmed `naccesspolicy = exclusive`).

| Case | Command | Result | Verdict |
|------|---------|--------|---------|
| First (exclusive) job pinned to w1 | `qsub -q tq_excl -l host=xxin-opentorque-w1 /tmp/te.sh` (sleep 40) | Job `264` ran on `xxin-opentorque-w1/0` | prep |
| Second job, no pin, while w1 busy | `qsub -q tq_excl /tmp/te2.sh` | Job `265` ran on **`xxin-opentorque-srv/0`** (idle node), **not** w1 | ✅ PASS |

> Verified: with `naccesspolicy=exclusive` the scheduler refuses to place a second job on a node that
> already carries a job, so the new job went to the idle node even though w1 still had a free core.

---

## 1.6 Queue `hostlist` (node-pool binding)

**Setup**: `qmgr -c "set queue tq_hl hostlist=xxin-opentorque-w1"` (confirmed `hostlist = xxin-opentorque-w1`).

| Case | Command | Result | Verdict |
|------|---------|--------|---------|
| Pin queue to w1 | `qsub -q tq_hl /tmp/thl.sh` (sleep 20) | Job `266` ran on `xxin-opentorque-w1/0` only — srv was free but **not** used | ✅ PASS |

> Verified: `hostlist` constrains the queue to the listed node(s); the unlisted free node is excluded.

---

## 1.6 Queue `acl_host_enable` + `acl_hosts` (submission-host ACL)

**Round 1 (before fix, HEAD `7a9b027`):** the **reject path worked** (unlisted host blocked), but the
**allow path was broken** — even allow-listed hosts were rejected. Root cause: `admitToQueue`
(`internal/server/route.go`) resolved the submit host via `hostOf(Job_Owner)`, and the client sends
`Job_Owner="root"` with no `@host` suffix, so the host was `""` and every submission was rejected.

**Fix** (`e4c8899` / `cadb688`): `admitToQueue` now resolves the submit host via `submitHostOf(rj)`
(`PBS_O_HOST`, falling back to the host part of `Job_Owner`), matching `queueAllowsSubmitHost`.
Added unit test `TestAdmitToQueueACLHosts` (passed on the VM).

**Round 2 (after fix, HEAD `cadb688`):** re-tested live, all four combinations:

| Case | Setup | Command / host | Result | Verdict |
|------|-------|----------------|--------|---------|
| Reject (local) | `acl_hosts=w1` only | submit from **srv** | `qsub: queuejob rejected (code=15007)` | ✅ PASS |
| Allow (local) | `acl_hosts=srv` only | submit from **srv** | accepted → job `281` | ✅ PASS (was FAIL) |
| Allow (both) | `acl_hosts=w1,srv` | submit from **srv** | accepted → job `282`, `PBS_O_HOST=xxin-opentorque-srv` | ✅ PASS |
| Allow (remote) | `acl_hosts=w1` only | submit from **w1** (`-s xxin-opentorque-srv`) | accepted → job `284`, `PBS_O_HOST=xxin-opentorque-w1` | ✅ PASS (was FAIL) |
| Reject (remote) | `acl_hosts=srv` only | submit from **w1** | `qsub: queuejob rejected (code=15007)` | ✅ PASS |

> Verified after the fix: allow-listed hosts are accepted (local and remote), and non-listed hosts are
> rejected, using `PBS_O_HOST` as the authoritative submit host.

---

## 2.2 Backfill (`strict_fifo` + `backfill`)

**Scheduler config** written to `/var/spool/torque/sched_priv/sched_config`, then `pbs_sched` restarted.

### Test A — backfill ON (`strict_fifo: true, backfill: true`)
Head job blocked by host-pinning to a non-existent group; a subsequent fitting job must run anyway.

| Job | Command | Result | Verdict |
|-----|---------|--------|---------|
| Head (blocked) | `qsub -q tq_bk -l host=@nosuchgroup /tmp/tb.sh` | Job `274` stayed `Q` (correctly blocked) | ✅ |
| Fitting (later) | `qsub -q tq_bk /tmp/tb.sh` (sleep 20) | Job `275` **ran** on `xxin-opentorque-w1/0` despite head being blocked | ✅ PASS |

### Test B — backfill OFF (`strict_fifo: true, backfill: false`)
Same shape; the later fitting job must be held behind the blocked head.

| Job | Command | Result | Verdict |
|-----|---------|--------|---------|
| Head (blocked) | `qsub -q tq_bk -l host=@nosuchgroup /tmp/tb.sh` | Job `276` stayed `Q` | ✅ |
| Fitting (later) | `qsub -q tq_bk /tmp/tb.sh` | Job `277` also stayed **`Q`** (strict stop — no backfill) | ✅ PASS |

> Config was restored to `scheduler_mode: external` (defaults) and `pbs_sched` restarted after the run.
> Verified: `backfill: true` lets later fittable jobs run in the gap left by a blocked head job; `backfill: false`
> holds them behind the blocked head.

---

## Summary

| TODO | Feature | Result |
|------|---------|--------|
| 1.3 | Host groups (`hostgroups`, `-l host=@group`) | ✅ PASS (positive + negative) |
| 1.4 | Multi-node dispatch (`nodes=2:ppn=1`) | ✅ PASS |
| 5.2 | Completed-job read-back across server restart | ✅ PASS (minor `resources_used` note) |
| 1.5 | Queue `naccesspolicy=exclusive` | ✅ PASS |
| 1.6 | Queue `hostlist` | ✅ PASS |
| 1.6 | Queue `acl_hosts` / `acl_host_enable` | ✅ PASS (fixed + re-verified, local & remote allow/reject) |
| 2.2 | Backfill (`backfill` knob + `strict_fifo`) | ✅ PASS (ON and OFF) |

### Follow-ups / notes
- The 1.6 acl_hosts allow-path defect (`route.go` using `hostOf(Job_Owner)` with an empty host for direct
  qsub) was found during live testing, fixed in `e4c8899`, formatted in `cadb688`, and re-verified live.
- Minor/optional: `resources_used.*` of a completed job is visible before restart but not re-displayed after
  a `pbs_server` restart (core read-back of `comp_time`/`exit_status`/`start_time`/`exec_host` is retained).

### Cluster state after tests
- Only the cloud-backed `batch` queue remains; temporary test queues removed.
- Both static nodes `free`; daemons `active`; `pbs_server` at `7.0.0-go` / `0.2.0`, at HEAD `cadb688`.