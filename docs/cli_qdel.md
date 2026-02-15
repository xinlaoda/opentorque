# qdel - Delete PBS Batch Jobs

## Synopsis

```
qdel [options] job_id [job_id...]
```

## Description

`qdel` deletes (cancels) one or more PBS batch jobs. If a job is running, it is terminated. If a job is queued or held, it is removed from the queue.

The command accepts one or more job identifiers as arguments. Each job is deleted independently; if one deletion fails, the others are still attempted.

The Go implementation uses HMAC-SHA256 token authentication.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-p` | | Purge the job. Forces deletion even if the MOM is unreachable. |
| `-m` | message | Attach a message to the deletion event (recorded in server logs). |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Exit Status

- `0` — All jobs deleted successfully
- `1` — One or more deletions failed

## Error Codes

| Code | Description |
|------|-------------|
| 15001 | Unknown job ID — the specified job does not exist |
| 15007 | Permission denied — user is not authorized to delete the job |

## Examples

```bash
# Delete a single job
qdel 42.DevBox

# Delete multiple jobs
qdel 42.DevBox 43.DevBox 44.DevBox

# Force-delete a job when MOM is unreachable
qdel -p 42.DevBox

# Delete with a message
qdel -m "No longer needed" 42.DevBox
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qsub(1), qstat(1), qhold(1), qrls(1)
