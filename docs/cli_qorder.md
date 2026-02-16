# qorder — Reorder PBS Jobs in Queue

## Synopsis

```
qorder job_id1 job_id2
```

## Description

`qorder` swaps the scheduling order of two jobs. After the swap, `job_id2` will be scheduled before `job_id1` (their creation timestamps are exchanged).

## Arguments

| Argument | Description |
|----------|-------------|
| `job_id1` | First job identifier |
| `job_id2` | Second job identifier |

## Protocol

Sends `BatchReqOrderJob` (type 50) to pbs_server with both job IDs. The server swaps their creation timestamps.

## Examples

```bash
# Make job 124 run before job 123
qorder 123.server 124.server
```

## Notes

- Both jobs must exist on the server
- Works by swapping creation timestamps, which affects FIFO ordering
- Effect depends on the active scheduling algorithm

## Exit Status

- `0` — Jobs reordered successfully
- `1` — Reorder failed
- `2` — Cannot connect to server
