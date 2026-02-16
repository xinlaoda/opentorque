# qdisable — Disable PBS Queue Submissions

## Synopsis

```
qdisable queue[@server]...
```

## Description

`qdisable` disables one or more queues from accepting new job submissions. Existing jobs in the queue are not affected and can still be scheduled if the queue is started.

## Protocol

Sends `BatchReqManager` (type 9) with `MGR_CMD_SET` on `MGR_OBJ_QUEUE`, setting the `enabled` attribute to `False`.

## Examples

```bash
# Disable submissions to batch queue
qdisable batch

# Disable during maintenance
qdisable batch express low_priority
```

## Notes

- Running and queued jobs are not affected
- Scheduling continues if queue is `started`
- Use `qenable` to re-enable submissions

## Exit Status

- `0` — All queues disabled
- `1` — One or more queues could not be disabled
- `2` — Cannot connect to server
