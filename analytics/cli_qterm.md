# qterm — Terminate PBS Server

## Synopsis

```
qterm [-t type] [server]
```

## Description

`qterm` initiates a shutdown of the PBS server. The shutdown can be immediate or wait for running jobs to complete.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-t` | type | `quick` | Shutdown type: `quick` or `immediate` |

## Shutdown Types

| Type | Description |
|------|-------------|
| `quick` | Wait for currently running jobs to complete, then shut down |
| `immediate` | Shut down immediately; running jobs are killed |

## Protocol

Sends `BatchReqShutdown` (type 17) to pbs_server with the shutdown manner code.

## Examples

```bash
# Graceful shutdown (wait for running jobs)
qterm

# Immediate shutdown
qterm -t immediate
```

## Notes

- Requires administrator privileges
- Server saves all state to disk before exiting
- After shutdown, restart with `pbs_server`

## Exit Status

- `0` — Shutdown initiated
- `1` — Shutdown failed
- `2` — Cannot connect to server
