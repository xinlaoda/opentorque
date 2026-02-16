# momctl — Control and Diagnose pbs_mom

## Synopsis

```
momctl -d level [-h host] [-p port]
momctl -q attr [-h host] [-p port]
momctl -s [-h host] [-p port]
momctl -S [-h host] [-p port]
momctl -c jobid [-h host] [-p port]
momctl -r config [-h host] [-p port]
```

## Description

`momctl` provides diagnostic and control capabilities for the `pbs_mom` daemon. It connects directly to the MOM on the specified host and port to query status or send control commands.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-d` | level | Diagnostic level (0–3). Higher levels provide more verbose output. |
| `-q` | attr | Query a specific MOM attribute by name. |
| `-s` | | Show full MOM status (host, port, connection state). |
| `-S` | | Shut down the MOM daemon. |
| `-c` | jobid | Clear (remove) a stale job from MOM. |
| `-r` | config | Reconfigure MOM with the specified configuration. |
| `-h` | host | MOM hostname to connect to (default: `localhost`). |
| `-p` | port | MOM port number (default: `15002`). |

## Exit Status

- `0` — Command completed successfully
- `1` — Operation failed or not implemented
- `2` — Cannot connect to MOM

## Examples

```bash
# Show MOM status on localhost
momctl -s

# Run diagnostics at level 2
momctl -d 2

# Run diagnostics on a remote node
momctl -d 1 -h compute05

# Query a specific attribute
momctl -q arch -h compute05

# Clear a stale job from MOM
momctl -c 42.server -h compute05

# Shut down MOM on a remote node
momctl -S -h compute05

# Connect to MOM on a non-default port
momctl -s -h compute05 -p 15003

# Full diagnostics on a remote host
momctl -d 3 -h compute05 -p 15002
```

## Notes

- Connects directly to the MOM daemon via TCP (not through `pbs_server`).
- Some operations (`-S`, `-c`, `-r`) are not yet fully implemented in this version.
- For node status via the server, use `pbsnodes -a` instead.

## See Also

pbsnodes(1), qstat(1), qmgr(1)
