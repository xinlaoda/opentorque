# qmove — Move a PBS Job to Another Queue

## Synopsis

```
qmove destination job_id...
```

## Description

`qmove` moves one or more jobs to a different queue. The destination can be a queue name, optionally with a server specification.

## Arguments

| Argument | Description |
|----------|-------------|
| `destination` | Target queue name, optionally `queue@server` |
| `job_id` | One or more job identifiers to move |

## Protocol

Sends `BatchReqMoveJob` (type 12) to pbs_server with the job ID and destination queue name.

## Examples

```bash
# Move to priority queue
qmove express 123.server

# Move multiple jobs
qmove low_priority 123.server 124.server

# Move to queue on another server
qmove batch@other_server 123.server
```

## Notes

- Job must be in Queued (Q) state
- Running jobs cannot be moved
- Destination queue must exist and accept the job's resource requirements
- Queue counters are updated automatically

## Exit Status

- `0` — All jobs moved successfully
- `1` — One or more jobs could not be moved
- `2` — Cannot connect to server
