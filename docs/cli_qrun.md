# qrun — Force Run a PBS Job

## Synopsis

```
qrun [-H host] job_id...
```

## Description

`qrun` forces one or more queued jobs to start execution immediately, bypassing the normal scheduler. The administrator can optionally specify which compute node should run the job.

## Options

| Flag | Argument | Description |
|------|----------|-------------|
| `-H` | host | Destination host/node to run the job on |

## Protocol

Sends `BatchReqRunJob` (type 15) to pbs_server with the job ID and optional destination node.

## Examples

```bash
# Force run on any available node
qrun 123.server

# Force run on a specific node
qrun -H compute01 123.server

# Force run multiple jobs
qrun 123.server 124.server
```

## Notes

- Requires administrator privileges
- Job must be in Queued (Q) state
- If `-H` is not specified, the server selects a node
- Overrides scheduler decisions

## Exit Status

- `0` — All jobs started successfully
- `1` — One or more jobs could not be started
- `2` — Cannot connect to server
