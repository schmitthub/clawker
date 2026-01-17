# Moby/Moby Client v0.2.1 Upgrade

## Target Versions
- `github.com/moby/moby/client v0.2.1` - Go client (proper Go module)
- `github.com/moby/moby/api v1.52.0` - API types (separate module)

## Key API Changes from docker/docker

### Import Path Changes
- `github.com/docker/docker/client` → `github.com/moby/moby/client`
- `github.com/docker/docker/api/types/*` → `github.com/moby/moby/api/types/*`
- `github.com/docker/docker/pkg/stdcopy` → `github.com/moby/moby/api/pkg/stdcopy`
- `filters.Args` is replaced by `client.Filters` (in client package, not separate)

### Error Checking
- NEW: Check what's available - may need different approach

### Method Signature Changes - ALL methods now return (ResultType, error)

#### Container Methods
```go
// ContainerCreate now takes single options struct
ContainerCreate(ctx, ContainerCreateOptions) (ContainerCreateResult, error)
// ContainerCreateOptions includes: Name, Config, HostConfig, NetworkingConfig, Platform

// Start/Stop/Remove all return Result types now
ContainerStart(ctx, id, ContainerStartOptions) (ContainerStartResult, error)
ContainerStop(ctx, id, ContainerStopOptions) (ContainerStopResult, error)
ContainerRemove(ctx, id, ContainerRemoveOptions) (ContainerRemoveResult, error)

// Inspect returns result type
ContainerInspect(ctx, id, ContainerInspectOptions) (ContainerInspectResult, error)

// Attach returns HijackedResponse (now in client package)
ContainerAttach(ctx, id, ContainerAttachOptions) (HijackedResponse, error)

// Logs options moved to client package
ContainerLogs(ctx, id, ContainerLogsOptions) (io.ReadCloser, error)

// Exec methods renamed and changed
ExecCreate(ctx, containerID, ExecCreateOptions) (ExecCreateResult, error)
ExecAttach(ctx, execID, ExecAttachOptions) (HijackedResponse, error)
ExecInspect(ctx, execID, ExecInspectOptions) (ExecInspectResult, error)

// Ping needs options
Ping(ctx, PingOptions) (PingResult, error)
```

#### Volume Methods
```go
VolumeCreate(ctx, VolumeCreateOptions) (VolumeCreateResult, error)
VolumeRemove(ctx, name, VolumeRemoveOptions) (VolumeRemoveResult, error)
VolumeInspect(ctx, name, VolumeInspectOptions) (VolumeInspectResult, error)
VolumeList(ctx, VolumeListOptions) (VolumeListResult, error)
```

#### Network Methods
```go
NetworkCreate(ctx, name, NetworkCreateOptions) (NetworkCreateResult, error)
NetworkRemove(ctx, name, NetworkRemoveOptions) (NetworkRemoveResult, error)
NetworkInspect(ctx, name, NetworkInspectOptions) (NetworkInspectResult, error)
NetworkList(ctx, NetworkListOptions) (NetworkListResult, error)
```

#### Image Methods
```go
ImageList(ctx, ImageListOptions) (ImageListResult, error)
ImageRemove(ctx, imageID, ImageRemoveOptions) (ImageRemoveResult, error)
ImageBuild(ctx, buildContext, ImageBuildOptions) (ImageBuildResult, error)
ImagesPrune(ctx, ImagePruneOptions) (ImagePruneResult, error)
```

#### Copy Methods
```go
CopyToContainer(ctx, containerID, CopyToContainerOptions) (CopyToContainerResult, error)
CopyFromContainer(ctx, containerID, CopyFromContainerOptions) (CopyFromContainerResult, error)
ContainerStatPath(ctx, containerID, ContainerStatPathOptions) (ContainerStatPathResult, error)
```

### Result Type Structures
```go
type ContainerListResult struct { Items []types.Container }
type ContainerCreateResult struct { ID string; Warnings []string }
type VolumeListResult struct { Volumes []*types.Volume; Warnings []string }
type NetworkListResult struct { Networks []types.NetworkResource }
type VolumeCreateResult struct { Volume *types.Volume }
type NetworkCreateResult struct { ID string; Warning string }
type HijackedResponse struct { Conn net.Conn; Reader *bufio.Reader } // now in client package
```

### Options Type Structures (all in client package now)
```go
type ContainerListOptions struct { All bool; Limit int; SizeBytes bool; Filters Filters }
type ContainerStartOptions struct {}
type ContainerStopOptions struct { Timeout *int; Signal string }
type ContainerRemoveOptions struct { RemoveVolumes bool; Force bool }
type ContainerAttachOptions struct { Stream bool; Stdin bool; Stdout bool; Stderr bool }
type ContainerLogsOptions struct { ShowStdout bool; ShowStderr bool; Since string; Timestamps bool; Follow bool; Tail string }
type ExecCreateOptions struct { User string; Privileged bool; TTY bool; AttachStdin bool; AttachStdout bool; AttachStderr bool; Env []string; WorkingDir string; Cmd []string }
type ExecAttachOptions struct { TTY bool }
type VolumeListOptions struct { Filters Filters }
type VolumeCreateOptions struct { Name string; Driver string; DriverOpts map[string]string; Labels map[string]string }
type VolumeRemoveOptions struct { Force bool }
type NetworkCreateOptions struct { Driver string; IPAM *network.IPAM; Internal bool; Attachable bool; Labels map[string]string }
type NetworkRemoveOptions struct {}
```
