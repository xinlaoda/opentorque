# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).


## [Unreleased]

### Changed
- **HA crash-recovery reconciliation (TODO 5.1 Phase 0).** On server start
  `recoverJobs` keeps Running jobs Running and `restoreRecoveredRunningJobs`
  rebuilds node slot accounting, so a job a MOM is still executing continues
  after a server crash/failover. The MOM now answers a `jobs` status query, and
  the server (`reconcileRunningJobsWithMOMs`) requeues a Running job only when
  its MOM confirms it is no longer running - no double execution, orphans
  requeued. Unit tests in internal/server/reconcile_test.go.

- **PostgreSQL state store (`PostgresStore`, HA).** Added a `PostgresStore`
  backend behind the `Store` interface (selectable via the `PBS_PG_DSN` env
  var), storing serverdb / queues / nodes / jobs+scripts as blobs in
  PostgreSQL. Ships with `pbstool migrate -d <home> -dsn <dsn>` to migrate an
  existing file store into PG, and a gated unit test (`TestPostgresStoreRoundTrip`,
  runs with `PBS_PG_TEST_DSN`). Adds the pure-Go `pgx/v5` dependency. Also fixed
  an underlying gap: job scripts are now restored from the store on recovery,
  so queued/running jobs can be (re)dispatched after a restart (file or PG).
  Live-tested on Azure with a local PostgreSQL: migrate, run to completion, and
  state recovery across restart all work.

- **Persistence abstraction (`ServerStore`, HA groundwork).** Introduced the
  `Store` interface + `FileStore` backend in `internal/server/store.go`; the
  server now persists serverdb / queues / nodes / jobs+scripts through it,
  with `FileStore` reproducing the exact on-disk layout and write-then-rename
  behavior (a behavior-preserving refactor). Paves the way for a PostgreSQL
  backend for multi-master HA (TODO 5.1).

- **Removed the built-in (in-process FIFO) scheduler.** The external `pbs_sched`
  is now the only scheduler and the default; `scheduler_mode: builtin` is ignored
  (server always uses `external`). Deferred (`-a`/Waiting) job promotion is now
  server-side and mode-independent, so it works under the external scheduler too.
- **External scheduler health warning.** When `pbs_sched` is not reachable on the
  trigger port, `pbs_server` logs a periodic `WARNING` so a missing scheduler is
  not mistaken for a quiet cluster. Also, the server's `SchedTriggerPort` now
  defaults to `25003` (matching `pbs_sched`) so event-driven external scheduling
  and the health warning work with no configuration.
  trigger port, `pbs_server` logs a periodic `WARNING` so a missing scheduler is
  not mistaken for a quiet cluster. No scheduler config is required (defaults are
  external + trigger port 25003).

### Added
- **Cloud bursting / elastic cloud pool** (M1–M4)
  - Event-driven Cloud Elastic Controller (CEC) — pure event loop reacting to
    `capacity` / `nodefree` / `nodeidle` / `nodedown`; never polls to scale out.
  - Azure Cloud Resource Provider (CRP) driver with MSI identity token auth
    (`AZURE_CLIENT_ID`), dynamic worker-VM auto-registration, and `PROVISIONING`
    state with VM↔job binding during boot.
  - Per-queue `cloud_*` burst attributes (`cloud_provider`, `cloud_vm_sku`,
    `cloud_min_nodes`, `cloud_max_nodes`, `cloud_idle_time`, `cloud_reclaim`
    (`deallocate`|`hibernate`), `cloud_subnet_id`, `cloud_image_id`,
    `cloud_disk_size`/`type`, `cloud_ssh_key`, `cloud_location`, `cloud_rg_name`).
  - Event-driven scale-in with idle reclaim windows, `deallocate`/`hibernate`
    reclaim, and hibernate fast-resume.
  - Elasticity tuning: per-pool scale-out `cooldown`, `scale_headroom`,
    `provision-timeout`, and `drain-timeout`; node **drain/exclusive** rollout
    primitive (4.1/4.2).
  - **Local-first dispatch** — `findNodeForJob` prefers static local nodes and
    only falls back to auto-registered (dynamic/cloud) nodes once local capacity
    is exhausted; node `is_dynamic` persisted/recovered.
  - `qstat -B`: per-pool free-cores status snapshot (M4 follow-up).
  - Job arrays (`qsub -t` expansion to sub-jobs), `qmove` for held/waiting jobs
    (reject running), queue routing (`queue_type=R`) + admission gate, `momctl`
    direct MOM attribute query (3.4), queue reconcile + `Priority` +
    `disallowed_types` gate (3.5), queue limits/resource-interval persistence
    across restart.
  - First unit tests (queue routing, local-first dispatch, scheduler).
- **Node selection & queue policy (1.3/1.5/1.6)** — host groups / node pools
  (`-l host=@group`, node `hostgroups`), queue `naccesspolicy`
  (`shared`/`exclusive`), queue `hostlist` node-pool binding, and queue-level
  `acl_hosts` submission-host ACL — enforced by both the built-in and external
  schedulers, persisted across restart.
- **Multi-node placement (1.4)** — `-l nodes=N:ppn=M` / `select=` allocate N
  distinct nodes (each >= ppn free), record a `+`-joined `exec_host`, and
  dispatch the job to every node; MOM emits `PBS_NODEFILE`/`PBS_NODELIST`.
- **Completed-job read-back (5.2)** — `.JB` persists full job attributes
  (resource request, timing, exec/exit, multi-node layout) and completed jobs
  are reloaded on restart and stay queryable until the `keep_completed` purge.
- **Backfill (2.2)** — new `backfill` sched-config knob (default on) lets the
  external scheduler run fitting jobs past a blocked head even in strict FIFO;
  the built-in scheduler already backfills.
- **Generic named-resource constraints (2.1)** — nodes carry arbitrary
  `resources_available.<name>` capacity (persisted as `gres.<name>=N`, reloaded on
  restart) and jobs request them with `qsub -l <name>=N`; both schedulers gate
  placement on remaining capacity and surface `gres_used.<name>` accounting, with
  host-group / feature interplay (built-in and external schedulers).

- **Deployment hardening** — sample systemd units for `pbs_server`, `pbs_sched`,
  `pbs_mom` with foreground `-D` mode, ordering/deps, and `AZURE_CLIENT_ID` for
  the cloud scheduler; daemons migrated from ad-hoc spawned processes to
  boot-persistent systemd services.

### Fixed
- Queue-level `acl_hosts` admission now resolves the submit host from `PBS_O_HOST` (falling back to the `Job_Owner` host); the route admission gate previously used `hostOf(Job_Owner)`, which is empty for direct `qsub` and rejected even allow-listed hosts (1.6).
- `getWorkDir` falls back to an existing workdir / `/` instead of failing when
  `PBS_O_WORKDIR`/`HOME` is missing — fixes jobs sticking in `Q` with a stale
  MOM binary (commit reply `15004` / re-dispatch `15014`).
- Scale-in destroy path now deletes each VM's NICs and public IP after the VM
  delete completes — no leaked network resources at scale.
- Orphaned MOM job sessions are reaped on restart/re-dispatch and their `.SID`
  sidecar removed on cleanup.
- Auto-requeue running jobs on node-down and a server RWMutex self-deadlock in
  auto-requeue/`qrerun` (2.10).
- Node CPU capacity accounted per-job `CPUReq` (2.6); hidden stale nodes no longer
  skew dispatch.
- IMDS token `expires_in` string tolerance + required `api-version`; correct
  per-resource-type API version for NIC vs VM calls.

### Changed
- Default scheduler mode set to `external`.
- Queue/node accounting keeps `model`/`configured`/`used` attribute sets
  consistent (3.5); `qmgr` keeps comma-list queue attributes and shows
  `resources_min`.
- New design/governance docs: cloud bursting, event-driven cloud elasticity,
  node scaling design, and updated AGENTS.md conventions.
- New user/admin feature docs: `node-selection.md`, `multi-node-placement.md`,
  `queue-policy.md`, `resource-constraints.md` (GPU/license + backfill), and
  `job-persistence.md`; README updated with a scheduling/resource feature overview,
  quick-start example, and an index of the new guides.
## [0.2.0] - 2026-02-16

### Added
- **Full CLI parameter parity** with C TORQUE across all 30 commands
  - qstat: `-c` (hide completed), `-x` (XML output), `-1`, `-G`, `-M`, `-t`, `-e`
  - qdel: `-p` (purge enforcement), `-m` (delete message), `-t`, `-W`
  - qalter: `-A` (account), `-c` (checkpoint), `-j` (join), `-k` (keep), `-q` (queue), `-W`
  - qselect: `-a` (date), `-A` (account), `-c` (checkpoint), `-p` (priority), `-r` (rerun)
  - pbsnodes: `-x` (XML output), `-A` (append note), `-n` (notes only), `-d` (diagnostic)
  - pbsdsh: `-h` (hostname), `-n` (node number), `-u` (unique), `-e`/`-E` (env)
  - momctl: `-c` (clear job), `-r` (reconfigure), `-C` (cycle)
  - qhold/qrls/qrun/qsig/qrerun: additional compat flags
- **Job dependency scheduling** — `afterok`, `afternotok`, `afterany`, `before*` types
  - Dependency resolution on job completion
  - Checked in both built-in and external scheduler paths
- **File staging** — `stagein`/`stageout` via scp in MOM
- **Job accounting system** — TORQUE-compatible Q/S/E/D/A/R records
  - `server_priv/accounting/YYYYMMDD` dated files
  - tracejob searches accounting records
- **YYYYMMDD dated log files** — daily rotation for server, MOM, scheduler logs
  - Compatible with tracejob date-based search
- **All missing qsub parameters** (15 new flags)
  - `-a` (exec time), `-A` (account), `-c` (checkpoint), `-C` (prefix), `-D` (root dir)
  - `-f` (fault tolerant), `-F` (script args), `-h` (hold), `-k` (keep), `-p` (priority)
  - `-r` (rerunnable), `-S` (shell), `-t` (array), `-u` (user list), `-W` (extended attrs), `-z` (quiet)
- **Hold enforcement** — `qsub -h` sets state H; deferred execution with `-a` sets state W
- **All missing MOM configuration parameters** — 77 directives (96% coverage)
- **CLI documentation** for pbsdsh, momctl (new), and updated all existing CLI docs
- **Analytics** — CLI comparison summary, MOM config comparison
- **Multi-node test report** — 55/56 tests passed on two-VM deployment

### Fixed
- `qrls` now clears `Hold_Types` field to prevent scheduler from re-holding the job
- tracejob short-ID matching improved to reduce false positives

### Changed
- All daemons use shared `pkg/pbslog` package for consistent YYYYMMDD log rotation
- Server `applyJobAttrs` expanded to handle 20+ new attribute types
- `formatJobStatus` reports all new job fields (Priority, Account, Hold, Mail, etc.)

## [0.1.0] - 2026-02-16

### Added
- Initial release of OpenTorque — a complete Go reimplementation of TORQUE/PBS
- **pbs_server**: Central job/queue/node management daemon
  - DIS wire protocol (PBS batch protocol compatible)
  - HMAC-SHA256 token authentication (no trqauthd needed)
  - Built-in FIFO scheduler for high-throughput workloads
  - External scheduler mode for advanced algorithms
  - Job/queue/node state persistence with atomic writes
  - 97 server configuration attributes (94% C TORQUE coverage)
  - XML serverdb format compatible with C TORQUE
  - Enforcement of max_running, max_user_run, max_group_run, max_user_queuable
  - ACL user validation at job submission
- **pbs_mom**: Compute node execution agent
  - Job execution with process tracking
  - Resource monitoring (CPU, memory)
  - Prologue/epilogue script support
  - Cross-platform: Linux (amd64/arm64), macOS, Windows
- **pbs_sched**: External scheduler with advanced algorithms
  - FIFO, shortest/longest job first, priority sorting
  - Fair-share scheduling with exponential usage decay
  - Round-robin and by-queue iteration modes
  - Starvation prevention
  - Load balancing (spread vs. pack)
- **21 CLI tools**: qsub, qstat, qdel, qhold, qrls, qalter, qmove, qorder, qrun,
  qrerun, qsig, qmsg, qchkpt, qstart, qstop, qenable, qdisable, qterm, qselect,
  qmgr, pbsnodes
  - Token-based authentication (no trqauthd dependency)
  - PBS-compatible flags and output formats
  - qmgr supports +=/-= operators for ACL lists
- **5 utility tools**: tracejob, printjob, pbsdsh, momctl, pbs_track
- **2 libraries**: DRMAA shared library, PAM authentication module
- **Packaging**: DEB/RPM/SUSE package build system
  - Server, Compute (MOM), and CLI packages
  - Embedded auth_key generation at build time
  - systemd-ready postinst/prerm scripts
