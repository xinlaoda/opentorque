# qrerun — Requeue a Running PBS Job

## Synopsis

```
qrerun [-f] job_id...
```

## Description

`qrerun` requeues one or more running jobs. The job is killed on the compute node and returned to the Queued (Q) state. The job's resources on the execution node are released.

## Options

| Flag | Description |
|------|-------------|
| `-f` | Force rerun even if the job's `Rerunable` attribute is set to `n` |

## Protocol

Sends `BatchReqRerun` (type 14) to pbs_server. The force flag is transmitted as the extension string "RERUNFORCE".

## Examples

```bash
# Rerun a running job
qrerun 123.server

# Force rerun (ignore rerunable=n)
qrerun -f 123.server
```

## Notes

- Job must be in Running (R) or Queued (Q) state
- The job process on the compute node is terminated
- Node resources (CPU slots) are released
- The job returns to Queued state and will be rescheduled

## Exit Status

- `0` — All jobs requeued successfully
- `1` — One or more jobs could not be requeued
- `2` — Cannot connect to server
