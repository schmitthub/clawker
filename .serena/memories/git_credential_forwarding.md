# Git Credential Forwarding Feature

## Status: Implementation Complete

## Overview
Container git credential forwarding following VSCode DevContainers pattern. Extends existing host proxy architecture.

## Implementation Phases

### Phase 1: Configuration Schema
- **Status**: Complete
- **Files**: `internal/config/schema.go`
- Add `GitCredentialsConfig` struct to `SecurityConfig`
- Fields: `ForwardHTTPS`, `ForwardSSH`, `CopyGitConfig` (all *bool with defaults)

### Phase 2: Host Proxy Git Credential Handler
- **Status**: Complete
- **Files**: `internal/hostproxy/git_credential.go` (new), `internal/hostproxy/server.go`
- Add `POST /git/credential` endpoint
- Handler executes `git credential fill/approve/reject` on host

### Phase 3: Container Credential Helper Script
- **Status**: Complete
- **Files**: `pkg/build/templates/git-credential-clawker.sh` (new), `pkg/build/dockerfile.go`
- Shell script reading stdin, POSTing to proxy, outputting to stdout
- Embed in dockerfile.go, add COPY to Dockerfile.tmpl

### Phase 4: SSH Agent Forwarding
- **Status**: Complete
- **Files**: `internal/workspace/ssh.go` (new), `internal/workspace/setup.go`
- Linux: bind mount SSH_AUTH_SOCK
- macOS: use `/run/host-services/ssh-auth.sock` (Docker Desktop magic)
- Windows: not supported initially

### Phase 5: Git Config Handling
- **Status**: Complete
- **Files**: `internal/workspace/gitconfig.go` (new)
- Mount host ~/.gitconfig read-only, filter credential.helper lines

### Phase 6: Entrypoint Integration
- **Status**: Complete
- **Files**: `pkg/build/templates/entrypoint.sh`
- Configure `git config --global credential.helper clawker` on startup

### Phase 7: Run/Create Command Integration
- **Status**: Complete
- **Files**: `pkg/cmd/container/run/run.go`, `pkg/cmd/container/create/create.go`
- Add CLAWKER_GIT_HTTPS and SSH_AUTH_SOCK env vars

## Key Design Decisions

1. **Extend hostproxy pattern** - Reuse existing HTTP proxy infrastructure
2. **Shell script credential helper** - Simple, no Go binary needed in container
3. **Zero-config defaults** - Works out of box if host has git/SSH configured
4. **macOS SSH magic path** - Docker Desktop provides `/run/host-services/ssh-auth.sock`
5. **Never log credentials** - Security critical

## Research Sources

- VSCode DevContainers: credential helper shim + RPC pipe
- DevPod: gRPC tunnel to host
- Docker Desktop: SSH agent magic socket forwarding
- git-credential-forwarder: TCP/socket pattern

## Lessons Learned

1. **Use jq for JSON in shell scripts** - sed-based JSON parsing fails on escaped quotes and special chars
2. **Check curl exit codes AND HTTP status** - silent failures cause hard-to-debug issues
3. **Exit non-zero on credential lookup failure** - git expects exit 1 when creds not found
4. **Add scanner.Err() after loops** - bufio.Scanner can have scanning errors
5. **Extract duplicated setup code** - git credential setup was copied between run.go and create.go, now in workspace/git.go

## Testing Checklist

- [ ] HTTPS clone private repo
- [ ] SSH clone private repo
- [ ] Graceful fallback when SSH agent not running
- [x] Unit tests for credential parsing (parseGitCredentialOutput has scanner.Err() check)
- [x] Integration tests for run command (all tests pass)

## PR #50 Fixes Applied

- **git-credential-clawker.sh**: Rewritten with jq for JSON, proper curl error handling, exit 1 on failure
- **internal/workspace/git.go**: New shared helper to eliminate code duplication in run/create commands
- **entrypoint.sh**: Added warnings for silent failures (firewall, gitconfig, credential helper)
- **git_credential.go**: Added scanner.Err() check, improved action mapping comment
- **ssh.go**: Improved macOS SSH check comment
