# Base Image Build Feature

## STATUS: IMPLEMENTATION COMPLETE - READY FOR TESTING

## Bug Fix Applied
Fixed `GenerateDockerfiles` in `pkg/build/dockerfile.go` - was missing 6 of 8 required scripts.
Now writes all scripts: entrypoint.sh, init-firewall.sh, statusline.sh, claude-settings.json,
host-open.sh, callback-forwarder.sh, git-credential-clawker.sh, ssh-agent-proxy.go

## All Tests Pass
All code compiles and tests pass. Binary rebuilt at `bin/clawker`.

## Ready to Test
Run `clawker init` interactively to test the base image build feature.

---

# Base Image Build Feature

## Branch: `a/user-vs-project-init`

## Summary

The `clawker init` command now offers to build an initial base image during setup.

## User Flow

```
$ clawker init

Setting up clawker user settings...
(Press Enter to accept defaults)

Build an initial base image?
  > 1. Yes - Build a clawker-optimized base image (Recommended)
    2. No - Skip - specify images per-project later
Enter selection [1]: 1

Select Linux flavor
  > 1. bookworm - Debian stable (Recommended)
    2. trixie - Debian testing
    3. alpine3.22 - Alpine Linux 3.22
    4. alpine3.23 - Alpine Linux 3.23
Enter selection [1]: 1

Starting base image build in background...

Created: ~/.local/clawker/settings.yaml

Building clawker-default:latest... (this may take a few minutes)

Build complete! Image: clawker-default:latest

Next Steps:
  1. Navigate to a project directory
  2. Run 'clawker project init' to set up the project
```

## Implementation Details

### Files Modified

1. **`pkg/cmd/init/init.go`**
   - Added `DefaultImageTag = "clawker-default:latest"` constant
   - Added `flavorOption` struct and `flavorOptions` slice
   - Modified `runInit()` to prompt for build preference and flavor
   - Added `buildDefaultImage()` function that:
     - Resolves latest Claude Code version from npm
     - Generates Dockerfile using `build.DockerfileManager`
     - Builds image using `docker.Client.BuildImage()`
   - Build runs in goroutine while settings are saved
   - On success, settings are updated with the built image tag

2. **`pkg/cmd/project/init/init.go`**
   - Removed hardcoded `"node:20-slim"` fallback
   - When `--yes` is used without default image configured, returns error with helpful message
   - In interactive mode, prompts for image with no default if none configured

3. **`.claude/docs/CLI-VERBS.md`**
   - Updated `clawker init` documentation with new prompts and flavor table
   - Updated `clawker project init` to note `--yes` requirement

4. **`CLAUDE.md`**
   - Updated init command description

### Key APIs Used

```go
// Resolve latest version
mgr := build.NewVersionsManager()
versions, err := mgr.ResolveVersions(ctx, []string{"latest"}, build.ResolveOptions{})

// Generate dockerfiles
dfMgr := build.NewDockerfileManager(buildDir, nil)
dfMgr.GenerateDockerfiles(versions)

// Build image
client, err := docker.NewClient(ctx)
buildContext, err := build.CreateBuildContextFromDir(dockerfilesDir, dockerfilePath)
err = client.BuildImage(ctx, buildContext, docker.BuildImageOpts{
    Tags:       []string{DefaultImageTag},
    Dockerfile: dockerfileName,
    Labels:     map[string]string{...},
})
```

### Non-Interactive Behavior

When `--yes` is passed:
- `clawker init --yes`: Skips base image build, saves empty default_image
- `clawker project init --yes`: Requires default_image to be configured, fails with error if not

This ensures CI/scripting can work predictably while interactive users get the recommended setup.
