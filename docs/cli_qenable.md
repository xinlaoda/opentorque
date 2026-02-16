# qenable — Enable PBS Queue Submissions

## Synopsis

```
qenable queue[@server]...
```

## Description

`qenable` enables one or more queues to accept new job submissions. This is the opposite of `qdisable`.

## Protocol

Sends `BatchReqManager` (type 9) with `MGR_CMD_SET` on `MGR_OBJ_QUEUE`, setting the `enabled` attribute to `True`.

## Examples

```bash
qenable batch
qenable batch express
```

## Notes

- A queue must be both `enabled` (for submission) and `started` (for scheduling)
- `qenable` affects submission only; use `qstart` for scheduling control

## Exit Status

- `0` — All queues enabled
- `1` — One or more queues could not be enabled
- `2` — Cannot connect to server
