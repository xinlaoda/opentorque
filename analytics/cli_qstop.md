# qstop — Stop PBS Queue Scheduling

## Synopsis

```
qstop queue[@server]...
```

## Description

`qstop` stops scheduling for one or more queues. Jobs in the queue remain but will not be dispatched to compute nodes until the queue is started again with `qstart`.

## Protocol

Sends `BatchReqManager` (type 9) with `MGR_CMD_SET` on `MGR_OBJ_QUEUE`, setting the `started` attribute to `False`.

## Examples

```bash
# Stop scheduling for batch queue
qstop batch

# Stop multiple queues for maintenance
qstop batch express
```

## Notes

- Running jobs are not affected; only new dispatching is prevented
- Jobs can still be submitted if the queue is `enabled`
- Use `qstart` to resume scheduling

## Exit Status

- `0` — All queues stopped
- `1` — One or more queues could not be stopped
- `2` — Cannot connect to server
