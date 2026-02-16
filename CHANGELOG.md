# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
