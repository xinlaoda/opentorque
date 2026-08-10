# Job & Data Persistence (TODO 5.2)

OpenTorque uses a **file-based persistence model** with no external database.
All server state lives under `$PBS_HOME/server_priv/`. This page focuses on job
persistence and **completed-job read-back across restart**; for the full
file-layout reference see [Data Persistence](data_persistence_analysis.md).

---

## 1. Where jobs are stored

```
/var/spool/torque/server_priv/
├── nodes                 # node inventory + per-node gres/groups
├── serverdb              # next job id, scheduling config
├── jobs/
│   ├── 0/ ... 9/         # hash buckets by job number
│   └── <jobid>.JB .SC    # job attributes + script
├── queues/               # one file per queue
└── arrays/               # job-array templates
```

## 2. Full attribute persistence (TODO 5.2)

Each job's `.JB` file now persists **all** attributes needed to reconstruct a
job exactly, including:

- resource request (`Resource_List.*`, e.g. `ncpus`, `mem`, `walltime`, `nodes`,
  and generic `gpu`/`license` — see [Resource Constraints](resource-constraints.md))
- timing (`ctime`/`qtime`/`start_time`/`comp_time`)
- placement and sizing (`exec_host`, node count, `ppn`)
- outcome (`exit_status`, multi-node layout)

Completed jobs are **reloaded on restart** and remain queryable with `qstat`
until purged by the `keep_completed` window.

## 3. Node and gres persistence

Node capacity and group membership persist across restart:

- `gres.<name>=N` — generic named-resource capacity (2.1)
- `groups=` — host-group membership (1.3)

## 4. Example

```bash
# submit and let it finish
echo "sleep 3" | qsub -l gpu=1 -l nodes=1:ppn=1

# restart the server; the completed job is still queryable
sudo systemctl restart pbs_server
qstat -f <jobid>   # still shows exec_host / start_time / exit_status
```

> Minor known limitation: a completed job's `resources_used.*` is visible
> before restart but not re-displayed after a restart (core read-back of
> `comp_time` / `exit_status` / `start_time` / `exec_host` is retained). See the
> note in `docs/live-azure-verification-report.md`.

## Live verification

See section 5.2 of `docs/live-azure-verification-report.md`.