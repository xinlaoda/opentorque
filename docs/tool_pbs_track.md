# pbs_track — Track External Process Resources

## Synopsis

```
pbs_track -j job_id [-p pid]
```

## Description

`pbs_track` registers an external process with PBS so its resource usage (CPU, memory) is accounted to the specified job. This is useful for tracking helper processes or pre/post-processing tasks that run outside the normal job script.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-j` | job_id | (required) | Job ID to associate the process with |
| `-p` | pid | current PID | PID of the process to track |

## Examples

```bash
# Track current process
pbs_track -j 123.server

# Track a specific PID
pbs_track -j 123.server -p 12345
```

## Notes

- Creates a tracking file in `$PBS_HOME/mom_priv/tracking/`
- The MOM daemon reads tracking files to include external processes in job resource accounting
- Requires write access to the tracking directory

## Exit Status

- `0` — Process registered successfully
- `1` — Invalid arguments
- `2` — Cannot write tracking file
