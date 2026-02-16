# pbsdsh — PBS Distributed Shell

## Synopsis

```
pbsdsh [-c copies] [-o] [-s] [-v] [command [args...]]
```

## Description

`pbsdsh` executes a command on all compute nodes allocated to the current PBS job. It reads the node list from `PBS_NODEFILE` and uses SSH to run the command on each node in parallel.

## Options

| Flag | Argument | Default | Description |
|------|----------|---------|-------------|
| `-c` | copies | 0 | Number of nodes to use (0 = all allocated nodes) |
| `-o` | — | — | Run on first node only |
| `-s` | — | — | Read command from stdin |
| `-v` | — | — | Verbose output |

## Environment

| Variable | Description |
|----------|-------------|
| `PBS_NODEFILE` | File containing allocated node names (one per line) |
| `PBS_JOBID` | Current job ID |

## Examples

```bash
# Run hostname on all nodes
pbsdsh hostname

# Run on first 4 nodes only
pbsdsh -c 4 hostname

# Run a script on all nodes
pbsdsh /shared/scripts/setup.sh

# Pipeline from stdin
echo "uptime" | pbsdsh -s
```

## Notes

- Must be run inside a PBS job (requires PBS_NODEFILE)
- Uses SSH for remote execution (nodes must allow passwordless SSH)
- Commands run in parallel across all nodes
- Duplicate node entries are deduplicated (one execution per unique node)

## Exit Status

- `0` — Command succeeded on all nodes
- `1` — Command failed on one or more nodes
