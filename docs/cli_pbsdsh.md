# pbsdsh — Distribute Commands to PBS Job Nodes

## Synopsis

```
pbsdsh [-c copies] [-o] [-s] [-v] [-h hostname] [-n nodenum]
       [-u] [-e envlist] [-E] [command [args...]]
```

## Description

`pbsdsh` executes a command on all nodes allocated to a PBS job. It reads the `PBS_NODEFILE` environment variable to determine which nodes to target and runs the command on each node via SSH in parallel.

This command is intended to be run from within a PBS job script.

## Options

| Option | Argument | Description |
|--------|----------|-------------|
| `-c` | copies | Number of copies to run. Default is one per unique node (0). |
| `-o` | | Run on the first node only. |
| `-s` | | Read the command from stdin instead of the argument list. |
| `-v` | | Verbose output. Prints the node list and per-node execution messages to stderr. |
| `-h` | hostname | Run the command on a specific host instead of using `PBS_NODEFILE`. |
| `-n` | nodenum | Run on a specific node number (index) from `PBS_NODEFILE`. |
| `-u` | | Run once per unique hostname (deduplicates repeated entries in `PBS_NODEFILE`). |
| `-e` | envlist | Comma-separated list of environment variables to pass to remote nodes. Compatibility flag. |
| `-E` | | Pass all environment variables to remote nodes. Compatibility flag. |

## Environment Variables

- `PBS_NODEFILE` — File containing allocated node names (one per line). Set automatically by PBS when a job starts.
- `PBS_JOBID` — Current job ID.

## Exit Status

- `0` — Command executed successfully on all nodes
- `1` — One or more remote executions failed

## Examples

```bash
# Run hostname on all allocated nodes
pbsdsh hostname

# Run a script on all unique nodes
pbsdsh -u /home/user/my_setup.sh

# Run on first node only
pbsdsh -o /usr/local/bin/init_master

# Run on a specific node by index
pbsdsh -n 2 /usr/local/bin/worker_setup

# Run on a specific host
pbsdsh -h compute05 uptime

# Verbose mode: see which nodes are targeted
pbsdsh -v /home/user/my_task.sh

# Read command from stdin
echo "uname -a" | pbsdsh -s

# Limit to 4 copies
pbsdsh -c 4 /home/user/benchmark.sh
```

## Notes

- Nodes are contacted via SSH with `StrictHostKeyChecking=no` and `BatchMode=yes`.
- Each node execution runs in parallel; output may be interleaved.
- When `-u` is used (or no `-h` is specified), duplicate hostnames in `PBS_NODEFILE` are deduplicated.

## See Also

qsub(1), pbsnodes(1), qstat(1)
