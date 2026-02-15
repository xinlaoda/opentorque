# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-02-15

### Added
- Initial release of OpenTorque
- **pbs_server**: Central job/queue/node management daemon
  - DIS wire protocol (PBS batch protocol compatible)
  - HMAC-SHA256 token authentication (no trqauthd needed)
  - Built-in FIFO scheduler for high-throughput workloads
  - External scheduler mode for advanced algorithms
  - Job/queue/node state persistence with atomic writes
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
- **CLI tools**: qsub, qstat, qdel, qhold, qrls, pbsnodes, qmgr
  - Token-based authentication (no trqauthd dependency)
  - PBS-compatible flags and output formats
