# qchkpt — Checkpoint a Running PBS Job

## Synopsis

```
qchkpt job_id...
```

## Description

`qchkpt` requests a checkpoint of one or more running jobs. Checkpointing saves the job's state so it can be restarted later from the saved state rather than from the beginning.

## Protocol

Sends `BatchReqCheckpointJob` (type 27) to pbs_server with the job ID.

## Examples

```bash
# Checkpoint a single job
qchkpt 123.server

# Checkpoint multiple jobs
qchkpt 123.server 124.server
```

## Notes

- Job must be in Running (R) state
- Actual checkpoint support depends on the application and OS
- The MOM daemon on the compute node handles the checkpoint operation
- Commonly used before system maintenance to preserve job progress

## Exit Status

- `0` — Checkpoint requested successfully
- `1` — Checkpoint request failed
- `2` — Cannot connect to server
