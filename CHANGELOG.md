# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
