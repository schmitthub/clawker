# Docker CLI to Go SDK Mapping Reference

> **LLM Reference Document**: Comprehensive mapping of Docker CLI commands to Go SDK methods for implementing clawker's Docker operations.

## Table of Contents

1. [Overview](#overview)
2. [SDK Package Structure](#sdk-package-structure)
3. [Client Initialization](#client-initialization)
4. [Command Hierarchy Diagram](#command-hierarchy-diagram)
5. [Container Commands](#container-commands)
   - [run](#docker-run)
   - [exec](#docker-exec)
   - [ps / list](#docker-ps)
   - [create](#docker-create)
   - [start](#docker-start)
   - [stop](#docker-stop)
   - [kill](#docker-kill)
   - [restart](#docker-restart)
   - [rm](#docker-rm)
   - [logs](#docker-logs)
   - [attach](#docker-attach)
   - [cp](#docker-cp)
   - [stats](#docker-stats)
   - [top](#docker-top)
   - [pause / unpause](#docker-pause-unpause)
   - [rename](#docker-rename)
   - [update](#docker-update)
   - [wait](#docker-wait)
   - [port](#docker-port)
   - [inspect](#docker-container-inspect)
6. [Image Commands](#image-commands)
   - [build](#docker-build)
   - [images / ls](#docker-images)
   - [pull](#docker-pull)
   - [rmi](#docker-rmi)
   - [inspect](#docker-image-inspect)
7. [Network Commands](#network-commands)
   - [create](#docker-network-create)
   - [ls](#docker-network-ls)
   - [rm](#docker-network-rm)
   - [connect](#docker-network-connect)
   - [disconnect](#docker-network-disconnect)
   - [inspect](#docker-network-inspect)
8. [Volume Commands](#volume-commands)
   - [create](#docker-volume-create)
   - [ls](#docker-volume-ls)
   - [rm](#docker-volume-rm)
   - [inspect](#docker-volume-inspect)
9. [Common Patterns](#common-patterns)
10. [Error Handling](#error-handling)

---

## Overview

This document maps Docker CLI commands to their corresponding Go SDK methods from `github.com/docker/docker/client`. The SDK provides programmatic access to all Docker Engine functionality.

**Key Import Paths:**
```go
import (
    "github.com/docker/docker/client"
    "github.com/docker/docker/api/types"
    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/image"
    "github.com/docker/docker/api/types/network"
    "github.com/docker/docker/api/types/volume"
    "github.com/docker/docker/api/types/filters"
    "github.com/docker/go-connections/nat"
)
```

---

## SDK Package Structure

| Package | Purpose |
|---------|---------|
| `client` | API client methods and options |
| `container` | Container config, host config, options structs |
| `image` | Image-related types and options |
| `network` | Network configuration types |
| `volume` | Volume types and options |
| `filters` | Filter argument construction |
| `nat` | Port/NAT types for port binding |
| `mount` | Mount configuration types |

---

## Client Initialization

```go
package main

import (
    "context"
    "github.com/docker/docker/client"
)

func main() {
    ctx := context.Background()

    // Create client from environment (DOCKER_HOST, DOCKER_API_VERSION)
    cli, err := client.NewClientWithOpts(client.FromEnv)
    if err != nil {
        panic(err)
    }
    defer cli.Close()

    // Optional: Enable API version negotiation
    cli.NegotiateAPIVersion(ctx)
}
```

---

## Command Hierarchy Diagram

```
docker
├── Container Commands (top-level shortcuts)
│   ├── run          → ImagePull + ContainerCreate + ContainerStart + ContainerAttach
│   ├── exec         → ContainerExecCreate + ContainerExecAttach + ContainerExecStart
│   ├── ps           → ContainerList
│   ├── build        → ImageBuild
│   └── images       → ImageList
│
├── container
│   ├── attach       → ContainerAttach
│   ├── cp           → CopyToContainer / CopyFromContainer
│   ├── create       → ContainerCreate
│   ├── inspect      → ContainerInspect
│   ├── kill         → ContainerKill
│   ├── logs         → ContainerLogs
│   ├── ls           → ContainerList
│   ├── pause        → ContainerPause
│   ├── port         → ContainerInspect (parse NetworkSettings)
│   ├── rename       → ContainerRename
│   ├── restart      → ContainerRestart
│   ├── rm           → ContainerRemove
│   ├── start        → ContainerStart
│   ├── stats        → ContainerStats / ContainerStatsOneShot
│   ├── stop         → ContainerStop
│   ├── top          → ContainerTop
│   ├── unpause      → ContainerUnpause
│   ├── update       → ContainerUpdate
│   └── wait         → ContainerWait
│
├── image
│   ├── build        → ImageBuild
│   ├── inspect      → ImageInspectWithRaw
│   ├── ls           → ImageList
│   ├── pull         → ImagePull
│   └── rm           → ImageRemove
│
├── network
│   ├── connect      → NetworkConnect
│   ├── create       → NetworkCreate
│   ├── disconnect   → NetworkDisconnect
│   ├── inspect      → NetworkInspect
│   ├── ls           → NetworkList
│   └── rm           → NetworkRemove
│
└── volume
    ├── create       → VolumeCreate
    ├── inspect      → VolumeInspect
    ├── ls           → VolumeList
    └── rm           → VolumeRemove
```

---

## Container Commands

### docker run

**CLI Syntax:**
```
docker run [OPTIONS] IMAGE [COMMAND] [ARG...]
```

**SDK Implementation:** Composite operation using multiple SDK calls.

**SDK Methods:**
1. `ImagePull()` - Pull the image if not present
2. `ContainerCreate()` - Create the container
3. `ContainerStart()` - Start the container
4. `ContainerAttach()` - Attach to container (if not detached)

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `--name` | `ContainerCreate` | `containerName` parameter |
| `-d, --detach` | N/A | Skip ContainerAttach |
| `-i, --interactive` | `container.Config` | `OpenStdin: true, StdinOnce: true` |
| `-t, --tty` | `container.Config` | `Tty: true` |
| `-e, --env` | `container.Config` | `Env: []string{"KEY=value"}` |
| `--env-file` | `container.Config` | `Env` (parse file first) |
| `-v, --volume` | `container.HostConfig` | `Binds: []string{"/host:/container"}` |
| `--mount` | `container.HostConfig` | `Mounts: []mount.Mount{}` |
| `-p, --publish` | `container.HostConfig` | `PortBindings: nat.PortMap{}` |
| `-P, --publish-all` | `container.HostConfig` | `PublishAllPorts: true` |
| `--network` | `container.HostConfig` | `NetworkMode: "network_name"` |
| `-w, --workdir` | `container.Config` | `WorkingDir: "/path"` |
| `-u, --user` | `container.Config` | `User: "uid:gid"` |
| `-h, --hostname` | `container.Config` | `Hostname: "name"` |
| `--entrypoint` | `container.Config` | `Entrypoint: []string{}` |
| `-l, --label` | `container.Config` | `Labels: map[string]string{}` |
| `-m, --memory` | `container.HostConfig` | `Memory: int64` |
| `--cpus` | `container.HostConfig` | `NanoCPUs: int64` |
| `--restart` | `container.HostConfig` | `RestartPolicy: container.RestartPolicy{}` |
| `--rm` | `container.HostConfig` | `AutoRemove: true` |
| `--privileged` | `container.HostConfig` | `Privileged: true` |
| `--read-only` | `container.HostConfig` | `ReadonlyRootfs: true` |
| `--cap-add` | `container.HostConfig` | `CapAdd: []string{}` |
| `--cap-drop` | `container.HostConfig` | `CapDrop: []string{}` |
| `--security-opt` | `container.HostConfig` | `SecurityOpt: []string{}` |
| `--init` | `container.HostConfig` | `Init: &boolTrue` |
| `--dns` | `container.HostConfig` | `DNS: []string{}` |
| `--expose` | `container.Config` | `ExposedPorts: nat.PortSet{}` |
| `--pull` | N/A | Control ImagePull behavior |
| `--platform` | `ImagePull` | `Platform: "linux/amd64"` |

**Example:**
```go
import (
    "context"
    "io"
    "os"

    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/image"
    "github.com/docker/docker/api/types/network"
    "github.com/docker/docker/client"
    "github.com/docker/go-connections/nat"
)

func dockerRun(ctx context.Context, cli *client.Client) error {
    // 1. Pull image
    reader, err := cli.ImagePull(ctx, "alpine:latest", image.PullOptions{})
    if err != nil {
        return err
    }
    io.Copy(io.Discard, reader) // Must drain reader
    reader.Close()

    // 2. Port bindings: -p 8080:80
    exposedPorts := nat.PortSet{
        "80/tcp": struct{}{},
    }
    portBindings := nat.PortMap{
        "80/tcp": []nat.PortBinding{
            {HostIP: "0.0.0.0", HostPort: "8080"},
        },
    }

    // 3. Create container
    resp, err := cli.ContainerCreate(ctx,
        &container.Config{
            Image:        "alpine:latest",
            Cmd:          []string{"echo", "hello"},
            Tty:          false,
            OpenStdin:    true,
            StdinOnce:    true,
            Env:          []string{"MY_VAR=value"},
            WorkingDir:   "/app",
            Labels:       map[string]string{"app": "myapp"},
            ExposedPorts: exposedPorts,
        },
        &container.HostConfig{
            Binds:        []string{"/host/path:/container/path"},
            PortBindings: portBindings,
            RestartPolicy: container.RestartPolicy{
                Name: container.RestartPolicyUnlessStopped,
            },
            AutoRemove: false,
        },
        &network.NetworkingConfig{},
        nil, // platform
        "my-container", // container name
    )
    if err != nil {
        return err
    }

    // 4. Start container
    if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
        return err
    }

    // 5. Attach (if interactive)
    attachResp, err := cli.ContainerAttach(ctx, resp.ID, container.AttachOptions{
        Stream: true,
        Stdin:  true,
        Stdout: true,
        Stderr: true,
    })
    if err != nil {
        return err
    }
    defer attachResp.Close()

    // Handle I/O streams
    go io.Copy(os.Stdout, attachResp.Reader)
    io.Copy(attachResp.Conn, os.Stdin)

    return nil
}
```

---

### docker exec

**CLI Syntax:**
```
docker exec [OPTIONS] CONTAINER COMMAND [ARG...]
```

**SDK Methods:**
1. `ContainerExecCreate()` - Create exec instance
2. `ContainerExecAttach()` - Attach to exec for I/O
3. `ContainerExecStart()` - Start execution (alternative to attach)
4. `ContainerExecInspect()` - Get exit code

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-d, --detach` | `container.ExecOptions` | `Detach: true` |
| `-i, --interactive` | `container.ExecOptions` | `AttachStdin: true` |
| `-t, --tty` | `container.ExecOptions` | `Tty: true` |
| `-e, --env` | `container.ExecOptions` | `Env: []string{}` |
| `-u, --user` | `container.ExecOptions` | `User: "user"` |
| `-w, --workdir` | `container.ExecOptions` | `WorkingDir: "/path"` |
| `--privileged` | `container.ExecOptions` | `Privileged: true` |
| `--detach-keys` | `container.ExecAttachOptions` | `DetachKeys: "ctrl-p,ctrl-q"` |

**Example:**
```go
func dockerExec(ctx context.Context, cli *client.Client, containerID string) error {
    // 1. Create exec instance
    execConfig := container.ExecOptions{
        AttachStdin:  true,
        AttachStdout: true,
        AttachStderr: true,
        Tty:          true,
        Cmd:          []string{"/bin/sh"},
        User:         "root",
        WorkingDir:   "/app",
        Env:          []string{"TERM=xterm"},
    }

    execResp, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
    if err != nil {
        return err
    }

    // 2. Attach to exec
    attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{
        Tty: true,
    })
    if err != nil {
        return err
    }
    defer attachResp.Close()

    // 3. Handle bidirectional I/O
    go func() {
        io.Copy(os.Stdout, attachResp.Reader)
    }()
    io.Copy(attachResp.Conn, os.Stdin)

    // 4. Get exit code
    inspectResp, err := cli.ContainerExecInspect(ctx, execResp.ID)
    if err != nil {
        return err
    }
    fmt.Printf("Exit code: %d\n", inspectResp.ExitCode)

    return nil
}
```

**HijackedResponse Handling:**

The `ContainerExecAttach` returns a `types.HijackedResponse` containing:
- `Conn` - Raw network connection for writing
- `Reader` - Buffered reader for output
- `Close()` - Cleanup method

For TTY mode, output is raw. For non-TTY mode, use `stdcopy.StdCopy()` to demux stdout/stderr.

---

### docker ps

**CLI Syntax:**
```
docker ps [OPTIONS]
```

**SDK Method:** `ContainerList(ctx, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-a, --all` | `container.ListOptions` | `All: true` |
| `-f, --filter` | `container.ListOptions` | `Filters: filters.Args` |
| `-n, --last` | `container.ListOptions` | `Limit: n` |
| `-l, --latest` | `container.ListOptions` | `Limit: 1` |
| `-q, --quiet` | N/A | Return only IDs |
| `-s, --size` | `container.ListOptions` | `Size: true` |

**Filter Examples:**
```go
import "github.com/docker/docker/api/types/filters"

// Filter by label
f := filters.NewArgs()
f.Add("label", "com.clawker.managed=true")
f.Add("label", "com.clawker.project=myproject")

// Filter by status
f.Add("status", "running")

// Filter by name
f.Add("name", "clawker.")

containers, err := cli.ContainerList(ctx, container.ListOptions{
    All:     true,
    Filters: f,
})
```

**Example:**
```go
func dockerPs(ctx context.Context, cli *client.Client, all bool) ([]types.Container, error) {
    return cli.ContainerList(ctx, container.ListOptions{
        All:  all,
        Size: true,
    })
}
```

---

### docker create

**CLI Syntax:**
```
docker create [OPTIONS] IMAGE [COMMAND] [ARG...]
```

**SDK Method:** `ContainerCreate(ctx, config, hostConfig, networkingConfig, platform, containerName)`

Same options as `docker run` without starting. See [docker run](#docker-run) for full flag mapping.

**Example:**
```go
func dockerCreate(ctx context.Context, cli *client.Client) (string, error) {
    resp, err := cli.ContainerCreate(ctx,
        &container.Config{
            Image: "alpine:latest",
            Cmd:   []string{"sleep", "infinity"},
        },
        &container.HostConfig{},
        nil,
        nil,
        "my-container",
    )
    if err != nil {
        return "", err
    }
    return resp.ID, nil
}
```

---

### docker start

**CLI Syntax:**
```
docker start [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerStart(ctx, containerID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-a, --attach` | N/A | Call ContainerAttach after start |
| `-i, --interactive` | N/A | Attach stdin |
| `--detach-keys` | `container.StartOptions` | `DetachKeys: "..."` |

**Example:**
```go
func dockerStart(ctx context.Context, cli *client.Client, containerID string) error {
    return cli.ContainerStart(ctx, containerID, container.StartOptions{})
}
```

---

### docker stop

**CLI Syntax:**
```
docker stop [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerStop(ctx, containerID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-s, --signal` | `container.StopOptions` | `Signal: "SIGTERM"` |
| `-t, --timeout` | `container.StopOptions` | `Timeout: &seconds` |

**Example:**
```go
func dockerStop(ctx context.Context, cli *client.Client, containerID string, timeout int) error {
    timeoutSec := timeout
    return cli.ContainerStop(ctx, containerID, container.StopOptions{
        Timeout: &timeoutSec,
        Signal:  "SIGTERM",
    })
}
```

---

### docker kill

**CLI Syntax:**
```
docker kill [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerKill(ctx, containerID, signal)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Parameter |
|----------|---------------|
| `-s, --signal` | `signal` string (default "KILL") |

**Example:**
```go
func dockerKill(ctx context.Context, cli *client.Client, containerID, signal string) error {
    if signal == "" {
        signal = "KILL"
    }
    return cli.ContainerKill(ctx, containerID, signal)
}
```

---

### docker restart

**CLI Syntax:**
```
docker restart [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerRestart(ctx, containerID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-s, --signal` | `container.StopOptions` | `Signal: "SIGTERM"` |
| `-t, --timeout` | `container.StopOptions` | `Timeout: &seconds` |

**Example:**
```go
func dockerRestart(ctx context.Context, cli *client.Client, containerID string, timeout int) error {
    timeoutSec := timeout
    return cli.ContainerRestart(ctx, containerID, container.StopOptions{
        Timeout: &timeoutSec,
    })
}
```

---

### docker rm

**CLI Syntax:**
```
docker rm [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerRemove(ctx, containerID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-f, --force` | `container.RemoveOptions` | `Force: true` |
| `-l, --link` | `container.RemoveOptions` | `RemoveLinks: true` |
| `-v, --volumes` | `container.RemoveOptions` | `RemoveVolumes: true` |

**Example:**
```go
func dockerRm(ctx context.Context, cli *client.Client, containerID string, force bool) error {
    return cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
        Force:         force,
        RemoveVolumes: true,
    })
}
```

---

### docker logs

**CLI Syntax:**
```
docker logs [OPTIONS] CONTAINER
```

**SDK Method:** `ContainerLogs(ctx, containerID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-f, --follow` | `container.LogsOptions` | `Follow: true` |
| `--since` | `container.LogsOptions` | `Since: "2021-01-01T00:00:00"` |
| `--until` | `container.LogsOptions` | `Until: "..."` |
| `-n, --tail` | `container.LogsOptions` | `Tail: "100"` |
| `-t, --timestamps` | `container.LogsOptions` | `Timestamps: true` |
| `--details` | `container.LogsOptions` | `Details: true` |

**Example:**
```go
import "github.com/docker/docker/pkg/stdcopy"

func dockerLogs(ctx context.Context, cli *client.Client, containerID string, follow bool) error {
    opts := container.LogsOptions{
        ShowStdout: true,
        ShowStderr: true,
        Follow:     follow,
        Tail:       "100",
        Timestamps: true,
    }

    reader, err := cli.ContainerLogs(ctx, containerID, opts)
    if err != nil {
        return err
    }
    defer reader.Close()

    // For non-TTY containers, demux stdout/stderr
    _, err = stdcopy.StdCopy(os.Stdout, os.Stderr, reader)
    return err
}
```

---

### docker attach

**CLI Syntax:**
```
docker attach [OPTIONS] CONTAINER
```

**SDK Method:** `ContainerAttach(ctx, containerID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `--detach-keys` | `container.AttachOptions` | `DetachKeys: "..."` |
| `--no-stdin` | `container.AttachOptions` | `Stdin: false` |
| `--sig-proxy` | N/A | Handle in application |

**Example:**
```go
func dockerAttach(ctx context.Context, cli *client.Client, containerID string) error {
    resp, err := cli.ContainerAttach(ctx, containerID, container.AttachOptions{
        Stream: true,
        Stdin:  true,
        Stdout: true,
        Stderr: true,
    })
    if err != nil {
        return err
    }
    defer resp.Close()

    // Bidirectional I/O
    go io.Copy(os.Stdout, resp.Reader)
    io.Copy(resp.Conn, os.Stdin)

    return nil
}
```

---

### docker cp

**CLI Syntax:**
```
docker cp [OPTIONS] CONTAINER:SRC_PATH DEST_PATH|-
docker cp [OPTIONS] SRC_PATH|- CONTAINER:DEST_PATH
```

**SDK Methods:**
- `CopyFromContainer(ctx, containerID, srcPath)` - Container to host
- `CopyToContainer(ctx, containerID, dstPath, content, options)` - Host to container

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-a, --archive` | `container.CopyToContainerOptions` | `AllowOverwriteDirWithFile: false` |
| `-L, --follow-link` | `container.CopyToContainerOptions` | `CopyUIDGID: true` |

**Example - Copy FROM Container:**
```go
import (
    "archive/tar"
    "io"
)

func copyFromContainer(ctx context.Context, cli *client.Client, containerID, srcPath, dstPath string) error {
    reader, stat, err := cli.CopyFromContainer(ctx, containerID, srcPath)
    if err != nil {
        return err
    }
    defer reader.Close()

    // stat contains file info (name, size, mode, etc.)
    _ = stat

    // reader is a tar archive - extract it
    tr := tar.NewReader(reader)
    for {
        header, err := tr.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }
        // Extract files based on header
        _ = header
    }
    return nil
}
```

**Example - Copy TO Container:**
```go
import (
    "archive/tar"
    "bytes"
)

func copyToContainer(ctx context.Context, cli *client.Client, containerID, srcPath, dstPath string) error {
    // Create tar archive of source
    var buf bytes.Buffer
    tw := tar.NewWriter(&buf)

    // Add file to tar (simplified - real implementation needs file walking)
    content := []byte("file contents")
    header := &tar.Header{
        Name: "filename",
        Mode: 0644,
        Size: int64(len(content)),
    }
    tw.WriteHeader(header)
    tw.Write(content)
    tw.Close()

    // Copy to container
    return cli.CopyToContainer(ctx, containerID, dstPath, &buf, container.CopyToContainerOptions{
        AllowOverwriteDirWithFile: true,
    })
}
```

---

### docker stats

**CLI Syntax:**
```
docker stats [OPTIONS] [CONTAINER...]
```

**SDK Methods:**
- `ContainerStats(ctx, containerID, stream)` - Streaming stats
- `ContainerStatsOneShot(ctx, containerID)` - Single snapshot

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Parameter/Method |
|----------|----------------------|
| `-a, --all` | List all containers first |
| `--no-stream` | Use `ContainerStatsOneShot` instead |
| `--no-trunc` | N/A (formatting in client) |

**Example - Streaming Stats:**
```go
import (
    "encoding/json"
    "github.com/docker/docker/api/types/container"
)

func dockerStats(ctx context.Context, cli *client.Client, containerID string) error {
    // Streaming stats
    stats, err := cli.ContainerStats(ctx, containerID, true) // stream=true
    if err != nil {
        return err
    }
    defer stats.Body.Close()

    decoder := json.NewDecoder(stats.Body)
    for {
        var stat container.StatsResponse
        if err := decoder.Decode(&stat); err != nil {
            if err == io.EOF {
                break
            }
            return err
        }

        // Process stats
        fmt.Printf("CPU: %.2f%%, Memory: %d bytes\n",
            calculateCPUPercent(&stat),
            stat.MemoryStats.Usage,
        )
    }
    return nil
}

func calculateCPUPercent(stat *container.StatsResponse) float64 {
    cpuDelta := float64(stat.CPUStats.CPUUsage.TotalUsage - stat.PreCPUStats.CPUUsage.TotalUsage)
    systemDelta := float64(stat.CPUStats.SystemUsage - stat.PreCPUStats.SystemUsage)
    if systemDelta > 0 && cpuDelta > 0 {
        return (cpuDelta / systemDelta) * float64(len(stat.CPUStats.CPUUsage.PercpuUsage)) * 100
    }
    return 0
}
```

**Example - One-shot Stats:**
```go
func dockerStatsOnce(ctx context.Context, cli *client.Client, containerID string) (*container.StatsResponse, error) {
    stats, err := cli.ContainerStatsOneShot(ctx, containerID)
    if err != nil {
        return nil, err
    }
    defer stats.Body.Close()

    var stat container.StatsResponse
    if err := json.NewDecoder(stats.Body).Decode(&stat); err != nil {
        return nil, err
    }
    return &stat, nil
}
```

---

### docker top

**CLI Syntax:**
```
docker top CONTAINER [ps OPTIONS]
```

**SDK Method:** `ContainerTop(ctx, containerID, arguments)`

**Example:**
```go
func dockerTop(ctx context.Context, cli *client.Client, containerID string) error {
    top, err := cli.ContainerTop(ctx, containerID, []string{"-aux"})
    if err != nil {
        return err
    }

    // Print headers
    fmt.Println(strings.Join(top.Titles, "\t"))

    // Print processes
    for _, proc := range top.Processes {
        fmt.Println(strings.Join(proc, "\t"))
    }
    return nil
}
```

---

### docker pause / unpause

**CLI Syntax:**
```
docker pause CONTAINER [CONTAINER...]
docker unpause CONTAINER [CONTAINER...]
```

**SDK Methods:**
- `ContainerPause(ctx, containerID)`
- `ContainerUnpause(ctx, containerID)`

**Example:**
```go
func dockerPause(ctx context.Context, cli *client.Client, containerID string) error {
    return cli.ContainerPause(ctx, containerID)
}

func dockerUnpause(ctx context.Context, cli *client.Client, containerID string) error {
    return cli.ContainerUnpause(ctx, containerID)
}
```

---

### docker rename

**CLI Syntax:**
```
docker rename CONTAINER NEW_NAME
```

**SDK Method:** `ContainerRename(ctx, containerID, newContainerName)`

**Example:**
```go
func dockerRename(ctx context.Context, cli *client.Client, containerID, newName string) error {
    return cli.ContainerRename(ctx, containerID, newName)
}
```

---

### docker update

**CLI Syntax:**
```
docker update [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerUpdate(ctx, containerID, updateConfig)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-m, --memory` | `container.UpdateConfig` | `Memory: int64` |
| `--memory-reservation` | `container.UpdateConfig` | `MemoryReservation: int64` |
| `--memory-swap` | `container.UpdateConfig` | `MemorySwap: int64` |
| `-c, --cpu-shares` | `container.UpdateConfig` | `CPUShares: int64` |
| `--cpus` | `container.UpdateConfig` | `NanoCPUs: int64` |
| `--cpuset-cpus` | `container.UpdateConfig` | `CpusetCpus: "0-3"` |
| `--cpuset-mems` | `container.UpdateConfig` | `CpusetMems: "0,1"` |
| `--pids-limit` | `container.UpdateConfig` | `PidsLimit: &int64` |
| `--restart` | `container.UpdateConfig` | `RestartPolicy: RestartPolicy{}` |

**Example:**
```go
func dockerUpdate(ctx context.Context, cli *client.Client, containerID string) error {
    memory := int64(512 * 1024 * 1024) // 512MB
    _, err := cli.ContainerUpdate(ctx, containerID, container.UpdateConfig{
        Resources: container.Resources{
            Memory:   memory,
            NanoCPUs: 1000000000, // 1 CPU
        },
        RestartPolicy: container.RestartPolicy{
            Name: container.RestartPolicyUnlessStopped,
        },
    })
    return err
}
```

---

### docker wait

**CLI Syntax:**
```
docker wait CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerWait(ctx, containerID, condition)`

**Wait Conditions:**
- `container.WaitConditionNotRunning` - Wait until not running
- `container.WaitConditionNextExit` - Wait for next exit
- `container.WaitConditionRemoved` - Wait until removed

**Example:**
```go
func dockerWait(ctx context.Context, cli *client.Client, containerID string) (int64, error) {
    resultC, errC := cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

    select {
    case result := <-resultC:
        if result.Error != nil {
            return -1, fmt.Errorf("wait error: %s", result.Error.Message)
        }
        return result.StatusCode, nil
    case err := <-errC:
        return -1, err
    }
}
```

---

### docker port

**CLI Syntax:**
```
docker port CONTAINER [PRIVATE_PORT[/PROTO]]
```

**SDK Method:** `ContainerInspect(ctx, containerID)` then parse `NetworkSettings.Ports`

**Example:**
```go
func dockerPort(ctx context.Context, cli *client.Client, containerID string) (nat.PortMap, error) {
    inspect, err := cli.ContainerInspect(ctx, containerID)
    if err != nil {
        return nil, err
    }
    return inspect.NetworkSettings.Ports, nil
}
```

---

### docker container inspect

**CLI Syntax:**
```
docker container inspect [OPTIONS] CONTAINER [CONTAINER...]
```

**SDK Method:** `ContainerInspect(ctx, containerID)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | Notes |
|----------|-------|
| `-f, --format` | Process in client |
| `-s, --size` | Included in response |

**Example:**
```go
func dockerContainerInspect(ctx context.Context, cli *client.Client, containerID string) (*types.ContainerJSON, error) {
    inspect, err := cli.ContainerInspect(ctx, containerID)
    if err != nil {
        return nil, err
    }
    return &inspect, nil
}
```

---

## Image Commands

### docker build

**CLI Syntax:**
```
docker build [OPTIONS] PATH | URL | -
```

**SDK Method:** `ImageBuild(ctx, buildContext, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-t, --tag` | `types.ImageBuildOptions` | `Tags: []string{}` |
| `-f, --file` | `types.ImageBuildOptions` | `Dockerfile: "Dockerfile"` |
| `--build-arg` | `types.ImageBuildOptions` | `BuildArgs: map[string]*string{}` |
| `--target` | `types.ImageBuildOptions` | `Target: "stage"` |
| `--no-cache` | `types.ImageBuildOptions` | `NoCache: true` |
| `--pull` | `types.ImageBuildOptions` | `PullParent: true` |
| `--rm` | `types.ImageBuildOptions` | `Remove: true` |
| `--force-rm` | `types.ImageBuildOptions` | `ForceRemove: true` |
| `-q, --quiet` | `types.ImageBuildOptions` | `SuppressOutput: true` |
| `--label` | `types.ImageBuildOptions` | `Labels: map[string]string{}` |
| `--platform` | `types.ImageBuildOptions` | `Platform: "linux/amd64"` |
| `--network` | `types.ImageBuildOptions` | `NetworkMode: "host"` |
| `--cache-from` | `types.ImageBuildOptions` | `CacheFrom: []string{}` |
| `--shm-size` | `types.ImageBuildOptions` | `ShmSize: int64` |
| `--ulimit` | `types.ImageBuildOptions` | `Ulimits: []*units.Ulimit{}` |
| `--secret` | `types.ImageBuildOptions` | `BuildContext: map[string]Context{}` |

**Example:**
```go
import (
    "archive/tar"
    "bytes"
    "io"

    "github.com/docker/docker/api/types"
)

func dockerBuild(ctx context.Context, cli *client.Client, contextDir string, tags []string) error {
    // Create build context tar
    buildContext, err := createTarContext(contextDir)
    if err != nil {
        return err
    }

    opts := types.ImageBuildOptions{
        Tags:       tags,
        Dockerfile: "Dockerfile",
        Remove:     true,
        NoCache:    false,
        BuildArgs: map[string]*string{
            "VERSION": strPtr("1.0.0"),
        },
        Labels: map[string]string{
            "com.clawker.managed": "true",
        },
    }

    resp, err := cli.ImageBuild(ctx, buildContext, opts)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    // Read build output
    _, err = io.Copy(os.Stdout, resp.Body)
    return err
}

func strPtr(s string) *string {
    return &s
}

func createTarContext(dir string) (io.Reader, error) {
    var buf bytes.Buffer
    tw := tar.NewWriter(&buf)
    // Walk directory and add files to tar
    // ... (implementation details)
    tw.Close()
    return &buf, nil
}
```

---

### docker images

**CLI Syntax:**
```
docker images [OPTIONS] [REPOSITORY[:TAG]]
```

**SDK Method:** `ImageList(ctx, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-a, --all` | `image.ListOptions` | `All: true` |
| `-f, --filter` | `image.ListOptions` | `Filters: filters.Args` |
| `-q, --quiet` | N/A | Return only IDs |
| `--digests` | N/A | Include in output formatting |

**Example:**
```go
func dockerImages(ctx context.Context, cli *client.Client, all bool) ([]image.Summary, error) {
    return cli.ImageList(ctx, image.ListOptions{
        All: all,
    })
}
```

---

### docker pull

**CLI Syntax:**
```
docker image pull [OPTIONS] NAME[:TAG|@DIGEST]
```

**SDK Method:** `ImagePull(ctx, refStr, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-a, --all-tags` | `image.PullOptions` | `All: true` |
| `--platform` | `image.PullOptions` | `Platform: "linux/amd64"` |
| `-q, --quiet` | N/A | Don't print progress |

**Example:**
```go
func dockerPull(ctx context.Context, cli *client.Client, ref string) error {
    reader, err := cli.ImagePull(ctx, ref, image.PullOptions{})
    if err != nil {
        return err
    }
    defer reader.Close()

    // Must read completely for pull to finish
    _, err = io.Copy(os.Stdout, reader)
    return err
}
```

---

### docker rmi

**CLI Syntax:**
```
docker rmi [OPTIONS] IMAGE [IMAGE...]
```

**SDK Method:** `ImageRemove(ctx, imageID, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-f, --force` | `image.RemoveOptions` | `Force: true` |
| `--no-prune` | `image.RemoveOptions` | `PruneChildren: false` |

**Example:**
```go
func dockerRmi(ctx context.Context, cli *client.Client, imageID string, force bool) error {
    _, err := cli.ImageRemove(ctx, imageID, image.RemoveOptions{
        Force:         force,
        PruneChildren: true,
    })
    return err
}
```

---

### docker image inspect

**CLI Syntax:**
```
docker image inspect [OPTIONS] IMAGE [IMAGE...]
```

**SDK Method:** `ImageInspectWithRaw(ctx, imageID)`

**Example:**
```go
func dockerImageInspect(ctx context.Context, cli *client.Client, imageID string) (*types.ImageInspect, error) {
    inspect, _, err := cli.ImageInspectWithRaw(ctx, imageID)
    if err != nil {
        return nil, err
    }
    return &inspect, nil
}
```

---

## Network Commands

### docker network create

**CLI Syntax:**
```
docker network create [OPTIONS] NETWORK
```

**SDK Method:** `NetworkCreate(ctx, name, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-d, --driver` | `network.CreateOptions` | `Driver: "bridge"` |
| `--subnet` | `network.CreateOptions` | `IPAM.Config[].Subnet` |
| `--gateway` | `network.CreateOptions` | `IPAM.Config[].Gateway` |
| `--ip-range` | `network.CreateOptions` | `IPAM.Config[].IPRange` |
| `--ipam-driver` | `network.CreateOptions` | `IPAM.Driver` |
| `-o, --opt` | `network.CreateOptions` | `Options: map[string]string{}` |
| `--label` | `network.CreateOptions` | `Labels: map[string]string{}` |
| `--internal` | `network.CreateOptions` | `Internal: true` |
| `--attachable` | `network.CreateOptions` | `Attachable: true` |
| `--ipv6` | `network.CreateOptions` | `EnableIPv6: true` |

**Example:**
```go
func dockerNetworkCreate(ctx context.Context, cli *client.Client, name string) (string, error) {
    resp, err := cli.NetworkCreate(ctx, name, network.CreateOptions{
        Driver: "bridge",
        IPAM: &network.IPAM{
            Driver: "default",
            Config: []network.IPAMConfig{
                {
                    Subnet:  "172.28.0.0/16",
                    Gateway: "172.28.0.1",
                },
            },
        },
        Labels: map[string]string{
            "com.clawker.managed": "true",
        },
        Attachable: true,
    })
    if err != nil {
        return "", err
    }
    return resp.ID, nil
}
```

---

### docker network ls

**CLI Syntax:**
```
docker network ls [OPTIONS]
```

**SDK Method:** `NetworkList(ctx, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-f, --filter` | `network.ListOptions` | `Filters: filters.Args` |
| `-q, --quiet` | N/A | Return only IDs |

**Example:**
```go
func dockerNetworkLs(ctx context.Context, cli *client.Client) ([]network.Summary, error) {
    f := filters.NewArgs()
    f.Add("label", "com.clawker.managed=true")

    return cli.NetworkList(ctx, network.ListOptions{
        Filters: f,
    })
}
```

---

### docker network rm

**CLI Syntax:**
```
docker network rm NETWORK [NETWORK...]
```

**SDK Method:** `NetworkRemove(ctx, networkID)`

**Example:**
```go
func dockerNetworkRm(ctx context.Context, cli *client.Client, networkID string) error {
    return cli.NetworkRemove(ctx, networkID)
}
```

---

### docker network connect

**CLI Syntax:**
```
docker network connect [OPTIONS] NETWORK CONTAINER
```

**SDK Method:** `NetworkConnect(ctx, networkID, containerID, config)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `--ip` | `network.EndpointSettings` | `IPAddress: "..."` |
| `--ip6` | `network.EndpointSettings` | `GlobalIPv6Address: "..."` |
| `--alias` | `network.EndpointSettings` | `Aliases: []string{}` |
| `--link` | `network.EndpointSettings` | `Links: []string{}` |

**Example:**
```go
func dockerNetworkConnect(ctx context.Context, cli *client.Client, networkID, containerID string) error {
    return cli.NetworkConnect(ctx, networkID, containerID, &network.EndpointSettings{
        Aliases: []string{"myalias"},
    })
}
```

---

### docker network disconnect

**CLI Syntax:**
```
docker network disconnect [OPTIONS] NETWORK CONTAINER
```

**SDK Method:** `NetworkDisconnect(ctx, networkID, containerID, force)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Parameter |
|----------|---------------|
| `-f, --force` | `force` bool |

**Example:**
```go
func dockerNetworkDisconnect(ctx context.Context, cli *client.Client, networkID, containerID string, force bool) error {
    return cli.NetworkDisconnect(ctx, networkID, containerID, force)
}
```

---

### docker network inspect

**CLI Syntax:**
```
docker network inspect [OPTIONS] NETWORK [NETWORK...]
```

**SDK Method:** `NetworkInspect(ctx, networkID, options)`

**Example:**
```go
func dockerNetworkInspect(ctx context.Context, cli *client.Client, networkID string) (network.Inspect, error) {
    return cli.NetworkInspect(ctx, networkID, network.InspectOptions{
        Verbose: true,
    })
}
```

---

## Volume Commands

### docker volume create

**CLI Syntax:**
```
docker volume create [OPTIONS] [VOLUME]
```

**SDK Method:** `VolumeCreate(ctx, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-d, --driver` | `volume.CreateOptions` | `Driver: "local"` |
| `-o, --opt` | `volume.CreateOptions` | `DriverOpts: map[string]string{}` |
| `--label` | `volume.CreateOptions` | `Labels: map[string]string{}` |
| `[VOLUME]` | `volume.CreateOptions` | `Name: "volume-name"` |

**Example:**
```go
func dockerVolumeCreate(ctx context.Context, cli *client.Client, name string) (volume.Volume, error) {
    return cli.VolumeCreate(ctx, volume.CreateOptions{
        Name:   name,
        Driver: "local",
        Labels: map[string]string{
            "com.clawker.managed": "true",
            "com.clawker.project": "myproject",
        },
    })
}
```

---

### docker volume ls

**CLI Syntax:**
```
docker volume ls [OPTIONS]
```

**SDK Method:** `VolumeList(ctx, options)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Struct | SDK Field |
|----------|------------|-----------|
| `-f, --filter` | `volume.ListOptions` | `Filters: filters.Args` |
| `-q, --quiet` | N/A | Return only names |

**Example:**
```go
func dockerVolumeLs(ctx context.Context, cli *client.Client) ([]*volume.Volume, error) {
    f := filters.NewArgs()
    f.Add("label", "com.clawker.managed=true")

    resp, err := cli.VolumeList(ctx, volume.ListOptions{
        Filters: f,
    })
    if err != nil {
        return nil, err
    }
    return resp.Volumes, nil
}
```

---

### docker volume rm

**CLI Syntax:**
```
docker volume rm [OPTIONS] VOLUME [VOLUME...]
```

**SDK Method:** `VolumeRemove(ctx, volumeID, force)`

**Key CLI Flags to SDK Mapping:**

| CLI Flag | SDK Parameter |
|----------|---------------|
| `-f, --force` | `force` bool |

**Example:**
```go
func dockerVolumeRm(ctx context.Context, cli *client.Client, volumeID string, force bool) error {
    return cli.VolumeRemove(ctx, volumeID, force)
}
```

---

### docker volume inspect

**CLI Syntax:**
```
docker volume inspect [OPTIONS] VOLUME [VOLUME...]
```

**SDK Method:** `VolumeInspect(ctx, volumeID)`

**Example:**
```go
func dockerVolumeInspect(ctx context.Context, cli *client.Client, volumeID string) (volume.Volume, error) {
    return cli.VolumeInspect(ctx, volumeID)
}
```

---

## Common Patterns

### Filter Construction

```go
import "github.com/docker/docker/api/types/filters"

// Multiple filters (AND logic within same key, OR logic across keys)
f := filters.NewArgs()
f.Add("label", "com.clawker.managed=true")
f.Add("label", "com.clawker.project=myproject")
f.Add("status", "running")
f.Add("status", "paused") // OR: running OR paused
```

### Port Binding

```go
import "github.com/docker/go-connections/nat"

// Parse port specification like "8080:80/tcp"
exposedPorts, portBindings, err := nat.ParsePortSpecs([]string{
    "8080:80/tcp",
    "127.0.0.1:3000:3000",
    "5000-5005:5000-5005",
})

// Manual construction
exposedPorts := nat.PortSet{
    "80/tcp":  struct{}{},
    "443/tcp": struct{}{},
}

portBindings := nat.PortMap{
    "80/tcp": []nat.PortBinding{
        {HostIP: "0.0.0.0", HostPort: "8080"},
    },
    "443/tcp": []nat.PortBinding{
        {HostIP: "0.0.0.0", HostPort: "8443"},
    },
}
```

### Context Management

```go
// Per-operation context (correct)
func (e *Engine) ContainerStart(ctx context.Context, id string) error {
    return e.cli.ContainerStart(ctx, id, container.StartOptions{})
}

// Cleanup with background context
defer func() {
    cleanupCtx := context.Background()
    cli.ContainerRemove(cleanupCtx, containerID, container.RemoveOptions{Force: true})
}()
```

### Streaming Output

```go
import "github.com/docker/docker/pkg/stdcopy"

// For non-TTY containers, demux stdout/stderr
func handleOutput(reader io.Reader, isTTY bool) error {
    if isTTY {
        _, err := io.Copy(os.Stdout, reader)
        return err
    }
    _, err := stdcopy.StdCopy(os.Stdout, os.Stderr, reader)
    return err
}
```

### HijackedResponse Handling

```go
// For attach/exec with TTY
resp, err := cli.ContainerAttach(ctx, id, container.AttachOptions{
    Stream: true,
    Stdin:  true,
    Stdout: true,
    Stderr: true,
})
if err != nil {
    return err
}
defer resp.Close()

// resp.Conn - write stdin
// resp.Reader - read stdout/stderr
// resp.CloseWrite() - signal EOF on stdin
```

---

## Error Handling

### Check Error Types

```go
import "github.com/docker/docker/client"

if err != nil {
    if client.IsErrNotFound(err) {
        // Container/image/volume not found
    }
    if client.IsErrConnectionFailed(err) {
        // Docker daemon not available
    }
    // Other errors
}
```

### Common Error Patterns

```go
// Wrap with context
if err != nil {
    return fmt.Errorf("failed to start container %s: %w", containerID, err)
}

// Handle specific conditions
info, err := cli.ContainerInspect(ctx, containerID)
if err != nil {
    if client.IsErrNotFound(err) {
        return nil, nil // Not found is not an error in this context
    }
    return nil, err
}
```

---

## Quick Reference Table

| CLI Command | SDK Method | Options Type |
|-------------|------------|--------------|
| `docker run` | `ContainerCreate` + `ContainerStart` | `container.Config`, `container.HostConfig` |
| `docker exec` | `ContainerExecCreate` + `ContainerExecAttach` | `container.ExecOptions` |
| `docker ps` | `ContainerList` | `container.ListOptions` |
| `docker start` | `ContainerStart` | `container.StartOptions` |
| `docker stop` | `ContainerStop` | `container.StopOptions` |
| `docker kill` | `ContainerKill` | signal string |
| `docker restart` | `ContainerRestart` | `container.StopOptions` |
| `docker rm` | `ContainerRemove` | `container.RemoveOptions` |
| `docker logs` | `ContainerLogs` | `container.LogsOptions` |
| `docker attach` | `ContainerAttach` | `container.AttachOptions` |
| `docker cp` | `CopyToContainer` / `CopyFromContainer` | `container.CopyToContainerOptions` |
| `docker stats` | `ContainerStats` | stream bool |
| `docker top` | `ContainerTop` | arguments []string |
| `docker pause` | `ContainerPause` | - |
| `docker unpause` | `ContainerUnpause` | - |
| `docker rename` | `ContainerRename` | newName string |
| `docker update` | `ContainerUpdate` | `container.UpdateConfig` |
| `docker wait` | `ContainerWait` | condition string |
| `docker inspect` | `ContainerInspect` | - |
| `docker build` | `ImageBuild` | `types.ImageBuildOptions` |
| `docker images` | `ImageList` | `image.ListOptions` |
| `docker pull` | `ImagePull` | `image.PullOptions` |
| `docker rmi` | `ImageRemove` | `image.RemoveOptions` |
| `docker network create` | `NetworkCreate` | `network.CreateOptions` |
| `docker network ls` | `NetworkList` | `network.ListOptions` |
| `docker network rm` | `NetworkRemove` | - |
| `docker network connect` | `NetworkConnect` | `*network.EndpointSettings` |
| `docker network disconnect` | `NetworkDisconnect` | force bool |
| `docker volume create` | `VolumeCreate` | `volume.CreateOptions` |
| `docker volume ls` | `VolumeList` | `volume.ListOptions` |
| `docker volume rm` | `VolumeRemove` | force bool |

---

## Version History

- **v1.0** - Initial comprehensive mapping (2026-01)
