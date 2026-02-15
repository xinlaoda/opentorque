# qhold - Hold PBS Batch Jobs

## Synopsis

```
qhold [-h hold_type] job_id [job_id...]
```

## Description

`qhold` places a hold on one or more PBS batch jobs, preventing them from being scheduled for execution. A held job remains in the queue with state `H` until the hold is released with `qrls`.

Multiple hold types can be applied. A job must have all holds released before it becomes eligible for scheduling.

The Go implementation uses HMAC-SHA256 token authentication.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-h` | hold_type | Specifies the type of hold to place. Default is `u` (user hold). |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Hold Types

| Type | Name | Description |
|------|------|-------------|
| `u` | User | A hold placed by the job owner. Can be released by the job owner or an administrator. |
| `o` | Other/Operator | A hold placed by an operator. Can only be released by an operator or administrator. |
| `s` | System | A hold placed by the system or an administrator. Can only be released by an administrator. |

Multiple hold types can be combined: `qhold -h uo` places both user and operator holds.

## Exit Status

- `0` — All holds placed successfully
- `1` — One or more hold operations failed

## Error Codes

| Code | Description |
|------|-------------|
| 15001 | Unknown job ID — the specified job does not exist |
| 15007 | Permission denied — user is not authorized to hold the job |

## Examples

```bash
# Place a user hold on a job
qhold 42.DevBox

# Place a system hold
qhold -h s 42.DevBox

# Hold multiple jobs
qhold 42.DevBox 43.DevBox 44.DevBox

# Place combined user and operator hold
qhold -h uo 42.DevBox
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qrls(1), qsub(1), qstat(1), qdel(1)
