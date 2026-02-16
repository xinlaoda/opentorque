# qmsg — Send Message to PBS Job Output

## Synopsis

```
qmsg [-E] [-O] message job_id...
```

## Description

`qmsg` sends a text message that is appended to a running job's output file (stdout or stderr).

## Options

| Flag | Description |
|------|-------------|
| `-E` | Append message to the job's standard error file |
| `-O` | Append message to the job's standard output file |

If neither `-E` nor `-O` is specified, the message is sent to stderr by default.

## Protocol

Sends `BatchReqMessJob` (type 10) to pbs_server with the job ID, file option (1=stderr, 2=stdout), and message string.

## Examples

```bash
# Send message to stderr (default)
qmsg "Checkpoint requested by admin" 123.server

# Send message to stdout
qmsg -O "Data processing phase 2 starting" 123.server

# Send to multiple jobs
qmsg -E "System maintenance in 30 minutes" 123.server 124.server
```

## Exit Status

- `0` — Message sent successfully
- `1` — Message delivery failed
- `2` — Cannot connect to server
