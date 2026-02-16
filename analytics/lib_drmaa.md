# DRMAA — Distributed Resource Management Application API

## Overview

The `drmaa` package provides a Go implementation of the DRMAA v1 standard
(OGF-GFD.133). DRMAA is a portable API for submitting and controlling jobs
on batch scheduling systems. Applications using this API can work with
any DRMAA-compatible resource manager without modification.

## Package

```go
import "github.com/opentorque/opentorque/pkg/drmaa"
```

## API Reference

### Session Management

| Function | Description |
|----------|-------------|
| `NewSession(contact)` | Create session, connect to server |
| `session.Close()` | Close session, release resources |

### Job Templates

| Method | Description |
|--------|-------------|
| `AllocateJobTemplate()` | Create a new job template |
| `jt.SetRemoteCommand(cmd)` | Set executable path |
| `jt.SetArgs(args)` | Set command arguments |
| `jt.SetJobName(name)` | Set job name |
| `jt.SetQueue(queue)` | Set destination queue |
| `jt.SetWorkingDir(dir)` | Set working directory |
| `jt.SetOutputPath(path)` | Set stdout path |
| `jt.SetErrorPath(path)` | Set stderr path |
| `jt.SetResource(name, val)` | Set resource requirement |
| `jt.SetEnv(key, val)` | Set environment variable |
| `jt.SetNativeSpec(spec)` | Set PBS-specific options |

### Job Control

| Method | Description |
|--------|-------------|
| `RunJob(jt)` | Submit job, return job ID |
| `GetJobStatus(id)` | Query job state |
| `Wait(id, timeout)` | Wait for job completion |
| `Control(id, action)` | Hold, release, terminate, suspend, resume |

### Job States

| Constant | Value | Description |
|----------|-------|-------------|
| `JobStateQueued` | 0x10 | Job is queued |
| `JobStateRunning` | 0x20 | Job is running |
| `JobStateDone` | 0x30 | Job completed |
| `JobStateFailed` | 0x40 | Job failed |
| `JobStateUserOnHold` | 0x12 | User hold |

### Control Actions

| Constant | Description |
|----------|-------------|
| `ActionSuspend` | Send SIGSTOP |
| `ActionResume` | Send SIGCONT |
| `ActionHold` | Place user hold |
| `ActionRelease` | Release user hold |
| `ActionTerminate` | Delete job |

## Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/opentorque/opentorque/pkg/drmaa"
)

func main() {
    session, err := drmaa.NewSession("")
    if err != nil { log.Fatal(err) }
    defer session.Close()

    jt, _ := session.AllocateJobTemplate()
    jt.SetRemoteCommand("/bin/sleep")
    jt.SetArgs([]string{"10"})
    jt.SetJobName("drmaa_test")
    jt.SetResource("walltime", "00:01:00")

    jobID, err := session.RunJob(jt)
    if err != nil { log.Fatal(err) }
    fmt.Println("Submitted:", jobID)

    status, err := session.Wait(jobID, 120)
    if err != nil { log.Fatal(err) }
    fmt.Printf("Job %s completed with exit status %d\n", status.JobID, status.ExitStatus)
}
```

## Compatibility

This implementation follows the DRMAA v1.0 specification (OGF-GFD.133).
It is source-compatible with other DRMAA Go bindings at the API level.
