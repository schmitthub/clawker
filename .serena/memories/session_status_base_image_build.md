# Session Status: Base Image Build Feature

## Branch: `a/user-vs-project-init`

## STATUS: IMPLEMENTATION COMPLETE ✅

All code changes are done. Tests pass. Binary rebuilt.

## Files Modified

1. **`pkg/cmd/init/init.go`** - Added base image build prompts and `buildDefaultImage()` function
2. **`pkg/cmd/project/init/init.go`** - Updated to require default image for `--yes` flag
3. **`pkg/cmd/init/init_test.go`** - Added tests for new constants and options
4. **`pkg/build/dockerfile.go`** - Fixed `GenerateDockerfiles` to write all 8 required scripts
5. **`.claude/docs/CLI-VERBS.md`** - Updated documentation
6. **`CLAUDE.md`** - Updated init command description

## What the Feature Does

`clawker init` now offers to build a base image:
1. Prompts "Build an initial base image?" (Yes recommended)
2. If Yes: prompts for Linux flavor (bookworm, trixie, alpine3.22, alpine3.23)
3. Resolves latest Claude Code from npm
4. Generates Dockerfile and all scripts
5. Builds `clawker-default:latest`
6. Updates settings with the built image

## Bug Fixed This Session

`GenerateDockerfiles` was only writing 2 of 8 scripts. Fixed to write all:
- entrypoint.sh, init-firewall.sh, statusline.sh, claude-settings.json
- host-open.sh, callback-forwarder.sh, git-credential-clawker.sh, ssh-agent-proxy.go

## To Continue

1. Run `clawker init` to test the feature interactively
2. If all works, commit the changes
3. Create PR when ready

## Commands to Test

```bash
# Rebuild if needed
go build -o bin/clawker ./cmd/clawker

# Test interactively
./bin/clawker init

# Run tests
go test ./...
```
