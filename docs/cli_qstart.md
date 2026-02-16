# qstart — Start PBS Queue Scheduling

## Synopsis

```
qstart queue[@server]...
```

## Description

`qstart` starts scheduling for one or more queues. When a queue is started, the scheduler can dispatch queued jobs from that queue to compute nodes. This is the opposite of `qstop`.

## Protocol

Sends `BatchReqManager` (type 9) with `MGR_CMD_SET` on `MGR_OBJ_QUEUE`, setting the `started` attribute to `True`.

## Examples

```bash
# Start a single queue
qstart batch

# Start multiple queues
qstart batch express low_priority
```

## Notes

- A queue must be both `started` (for scheduling) and `enabled` (for submission)
- `qstart` affects scheduling only; use `qenable` for submission control
- Requires administrator privileges

## Exit Status

- `0` — All queues started
- `1` — One or more queues could not be started
- `2` — Cannot connect to server
