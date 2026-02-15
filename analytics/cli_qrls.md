# qrls - Release Holds on PBS Batch Jobs

## Synopsis

```
qrls [-h hold_type] job_id [job_id...]
```

## Description

`qrls` releases holds that were previously placed on PBS batch jobs with `qhold`. Once all holds are released, the job transitions from held (`H`) state back to queued (`Q`) state and becomes eligible for scheduling.

The Go implementation uses HMAC-SHA256 token authentication.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-h` | hold_type | Specifies the type of hold to release. Default is `u` (user hold). |
| `-s` | server | Connect to the specified PBS server instead of the default. |

## Hold Types

| Type | Name | Who Can Release |
|------|------|-----------------|
| `u` | User | Job owner or administrator |
| `o` | Other/Operator | Operator or administrator |
| `s` | System | Administrator only |

## Exit Status

- `0` — All holds released successfully
- `1` — One or more release operations failed

## Error Codes

| Code | Description |
|------|-------------|
| 15001 | Unknown job ID — the specified job does not exist |
| 15007 | Permission denied — user is not authorized to release the hold |

## Examples

```bash
# Release a user hold
qrls 42.DevBox

# Release a system hold (requires admin privileges)
qrls -h s 42.DevBox

# Release multiple jobs
qrls 42.DevBox 43.DevBox 44.DevBox

# Release user and operator holds
qrls -h uo 42.DevBox
```

## Environment Variables

- `PBS_HOME` — TORQUE home directory (default: `/var/spool/torque`)
- `PBS_DEFAULT` — Default PBS server name

## Authentication

Uses HMAC-SHA256 shared key authentication. Requires the `auth_key` file in `$PBS_HOME/`.

## See Also

qhold(1), qsub(1), qstat(1), qdel(1)
