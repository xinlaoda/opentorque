# qsig — Signal a Running PBS Job

## Synopsis

```
qsig [-s signal] job_id...
```

## Description

`qsig` sends a signal to one or more running jobs. The signal is forwarded through the server to the compute node's MOM daemon, which delivers it to the job's process group.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-s` | signal | `SIGTERM` | Signal name or number (e.g., SIGKILL, SIGUSR1, 9) |

## Protocol

Sends `BatchReqSignalJob` (type 18) to pbs_server with the job ID and signal name string.

## Common Signals

| Signal | Number | Description |
|--------|--------|-------------|
| `SIGTERM` | 15 | Graceful termination (default) |
| `SIGKILL` | 9 | Immediate termination |
| `SIGSTOP` | 19 | Pause the job |
| `SIGCONT` | 18 | Resume a paused job |
| `SIGUSR1` | 10 | User-defined signal 1 |
| `SIGUSR2` | 12 | User-defined signal 2 |

## Examples

```bash
# Send SIGTERM (default)
qsig 123.server

# Send SIGKILL
qsig -s SIGKILL 123.server

# Send SIGUSR1 to multiple jobs
qsig -s SIGUSR1 123.server 124.server
```

## Exit Status

- `0` — Signal sent successfully
- `1` — Signal delivery failed
- `2` — Cannot connect to server
