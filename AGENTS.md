# AGENTS.md

Guidance for AI coding agents working in this repository. These instructions
are in addition to (not instead of) `CONTRIBUTING.md` and the repo docs.

## What this project is

OpenTorque is a clean-room, PBS/TORQUE-compatible batch resource manager and
scheduler written in Go with **zero external dependencies** (standard library
only). It speaks the TORQUE DIS wire protocol, so its CLI tools (`qsub`,
`qstat`, `qdel`, `pbsnodes`, `qmgr`, ...) and daemons interoperate with the
real TORQUE ecosystem.

Interoperability and behavioral parity with TORQUE/PBS semantics are first-class
requirements. A feature is only "done" if it behaves the way TORQUE would.

## Repository layout

- `cmd/<tool>/` — one executable per directory: daemons (`pbs_server`,
  `pbs_mom`, `pbs_sched`), CLI tools (`qsub` ...), utility tools (`tracejob`,
  `pbs_track`, `momctl`, `pbsdsh`, ...).
- `internal/` — shared packages. The important ones:
  - `server/` — `pbs_server` core: job lifecycle, queue/node tracking, request
    handling, node status, and the server-side scheduler watch (Waiting-job
    promotion + external-scheduler health warning) (`server.go`).
  - `sched/scheduler/` — **external** `pbs_sched` advanced scheduler.
  - `cec/` — Cloud Elastic Controller. **Pure event loop**; the sole
    orchestrator of cloud node membership for cloud-backed queues. Never polls
    on a fixed timer for capacity decisions.
  - `crp/` — Cloud Resource Provider interface + Azure driver.
  - `job/`, `node/`, `queue/` — domain managers.
  - `dis/` — the wire protocol (codec/message/protocol), `auth/` — HMAC token
    auth, `acct/` — accounting, `mom/` — `pbs_mom` internals.
- `docs/` — per-tool and design docs. `TODO.md` — the authoritative backlog
  (see below). `configs/` — example sched config + systemd units.

## The single scheduling path (CRITICAL)

OpenTorque has **one scheduler**: the external `pbs_sched` daemon, implemented
in `internal/sched/scheduler/scheduler.go`. The old in-process built-in FIFO
scheduler in `internal/server/server.go` was **removed**.

- `pbs_server` defaults to external mode (no `scheduler_mode` config needed) and
  **delegates all job placement to `pbs_sched`**: on job/node events it pings the
  scheduler on the local trigger port (`SchedTriggerPort`, default 25003), and it
  logs a clear `WARNING` whenever `pbs_sched` is not reachable.
- The server keeps a small, mode-independent watchdog (`schedulerWatchLoop` in
  `internal/server/server.go`) that promotes deferred (Waiting/`-a`) jobs to
  Queued once their execution time passes — the sibling of `pbs_sched`'s job
  placement — plus the health warning. Do not reintroduce in-process placement.

When you change job/node capacity accounting, node selection, or dispatch
logic, apply it in `internal/sched/scheduler/scheduler.go` (placement) and, if
it touches server-side state, in `internal/server/server.go`, and say so
explicitly in the commit and in `TODO.md`.

## Workflow & hygiene

- **Build**: `make all` (targets: `server`, `mom`, `sched`, `cli`, `tools`).
  `go test ./...` for tests. There are currently **no unit tests** (TODO 5.3),
  so verification happens by building + live deployment.
- **Go tooling may not be on `$PATH`** in every shell (esp. git-bash on
  Windows). Build/compile for deployment happens on the target Linux Azure
  VMs, not necessarily in the local shell.
- **Add tests when a logical place exists** and there is an established test
  pattern. Don't scaffold a whole new test harness just for one change.
- **Formatting**: standard `gofmt` + `go vet`. Comments and all repo-facing
  docs in English.
- **Surgical changes**: touch only what the task requires. Don't refactor
  unrelated code. Match existing style even if you'd do it differently.
- **Commits**: Conventional Commits with a scoped type and the TODO item when
  applicable, e.g. `fix(sched,server): account node CPU capacity per-job CPUReq (TODO 2.6)`,
  `feat(sched,cec): ...`, `docs(todo,...): ...`.

## TODO.md — the working backlog

`TODO.md` catalogs missing TORQUE/PBS features with a legend:
`[BUG]` (wrong behavior), `[GAP]` (missing entirely), `[STUB]` (data model
exists, never wired), and `[DONE — ...]` when resolved. When you implement an
item:

- Mark it `[DONE — implemented & tested]` (or similar) and **summarize what
  changed + how it was verified** directly in the item body.
- Keep related or newly-discovered gaps visible; cross-reference item numbers.
- Follow the established style doc design docs + per-item "code change + live
  test" format used in existing items.

Work top-down on the next high-value open item (currently e.g. 2.10, then 1.4)
unless the user directs otherwise.

## Cloud elasticity (CEC/CRP) — know the invariants

- `internal/cec` is an **event loop**, not a poller. Events are `capacity`,
  `nodefree`, `nodeidle`, `nodedown`. Preserve this model.
- The CRP (`internal/crp`) is behind the `crp.Provider` interface; the Azure
  driver is `azure.go`. Keep cloud-provider specifics out of the CEC core.
- Node state in OpenTorque includes `PROVISIONING`; cloud nodes are
  auto-registered. Don't break dynamic registration or the event-driven
  scale-in/out invariants when touching scheduling.

## Gotchas

- `-l` resource requests (`ncpus`, `nodes`, `mem`, `walltime`, `host`,
  `feature`, ...) are parsed loosely; verify both scheduling paths honor the
  same resource semantics (see prior CPUReq work).
- Queue config is persisted; keep the `model`/`configured`/`used` attribute
  sets consistent (TODO 3.5) when touching queue fields.
- Prefer the DIS/`internal/dis` helpers for any new wire fields so
  interop is preserved.
