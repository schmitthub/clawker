# Docker Client Import Cleanup - COMPLETED

## Task Overview
Cleaned up Docker client import violations to enforce the architectural layering:
- **`pkg/whail`** - The ONLY package allowed to import `github.com/moby/moby/client`
- **`internal/docker`** - The ONLY package that should import `pkg/whail`
- **All other packages** - Use `internal/docker` exclusively

## Implementation

### Type Aliases Created

**`pkg/whail/types.go`** - Type aliases for Docker SDK types:
- Filters, ContainerAttachOptions, ContainerListOptions, ContainerLogsOptions
- ContainerRemoveOptions, SDKContainerCreateOptions, ContainerInspectResult
- ExecCreateOptions, ExecStartOptions, ExecAttachOptions, ExecResizeOptions
- CopyToContainerOptions, CopyFromContainerOptions
- ImageListOptions, ImageRemoveOptions, ImageBuildOptions, ImagePullOptions
- **ImageListResult, ImageSummary** (added for mock testing - use these in tests, NOT moby types)
- VolumeCreateOptions, NetworkCreateOptions, NetworkInspectOptions
- HijackedResponse

Note: `SDKContainerCreateOptions` is for raw SDK API bypass (e.g., in volume.go for temp containers).
`ContainerCreateOptions` is whail's custom struct with clawker-specific fields.

**`internal/docker/types.go`** - Re-exports from whail plus whail-specific types:
- All whail type aliases
- ContainerStartOptions, EnsureNetworkOptions, Labels (whail-specific)
- DockerError (error type)

### Files Updated

#### internal/docker (use whail types)
- `client.go` - Changed client.* to whail.*
- `labels.go` - Changed client.Filters to whail.Filters  
- `volume.go` - Changed to whail types; uses SDKContainerCreateOptions for raw API calls

#### internal/cmd/* (use internal/docker types)
- container: exec.go, attach.go, logs.go, cp.go, run.go, start.go, inspect.go, create.go, restart.go
- volume: create.go
- network: create.go, inspect.go
- image: list.go, remove.go
- monitor: up.go

#### Utility files
- `internal/cmdutil/resolve.go` - docker.Filters, docker.ImageListOptions
- `internal/cmdutil/output.go` - docker.DockerError
- `internal/term/pty.go` - docker.HijackedResponse

### Test Files - Use whail Types for Mocks

**IMPORTANT:** When using `testutil.NewMockDockerClient()` in unit tests, always use `pkg/whail` type aliases:

```go
// CORRECT - use whail types
import "github.com/schmitthub/clawker/pkg/whail"

m.Mock.EXPECT().
    ImageList(gomock.Any(), gomock.Any()).
    Return(whail.ImageListResult{
        Items: []whail.ImageSummary{{RepoTags: []string{"image:tag"}}},
    }, nil)

// WRONG - do NOT import moby types directly in test files
import "github.com/moby/moby/client"  // NO!
import "github.com/moby/moby/api/types/image"  // NO!
```

**Exception:** Integration tests using `testutil.NewRawDockerClient()` may import moby types
directly when needed for low-level Docker API access.

## Verification Commands
```bash
# Verify moby/client ONLY in pkg/whail and internal/testutil (excluding test files)
grep -r "github.com/moby/moby/client" . --include="*.go" | grep -v "pkg/whail" | grep -v "internal/testutil" | grep -v "_test.go"

# Verify pkg/whail ONLY in internal/docker
grep -r "schmitthub/clawker/pkg/whail" internal/docker --include="*.go"

# Build and test
go build ./...
go test ./...
```

## Status: COMPLETED
All production code now follows the architectural layering. Build and tests pass.
