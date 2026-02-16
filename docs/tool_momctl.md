# momctl — PBS MOM Control Tool

## Synopsis

```
momctl [-d level] [-q attr] [-s] [-h host] [-p port]
```

## Description

`momctl` provides diagnostic and control capabilities for the pbs_mom daemon. It connects directly to a MOM daemon to query status or send control commands.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-d` | 0-3 | — | Diagnostic level |
| `-q` | attribute | — | Query a specific attribute |
| `-s` | — | — | Show full status |
| `-h` | host | localhost | MOM hostname |
| `-p` | port | 15002 | MOM port |
| `-S` | — | — | Shut down MOM |

## Diagnostic Levels

| Level | Description |
|-------|-------------|
| 0 | Basic connectivity check |
| 1 | Protocol information |
| 2 | Job information |
| 3 | Full diagnostic dump |

## Examples

```bash
# Check MOM connectivity
momctl -d 0

# Check remote MOM
momctl -d 0 -h compute01

# Query specific attribute
momctl -q loadave -h compute01
```

## Exit Status

- `0` — Success
- `1` — Operation failed
- `2` — Cannot connect to MOM
