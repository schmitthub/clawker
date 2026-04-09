# Container Start Command

Starts one or more stopped clawker containers. Supports attach (`-a`) and interactive (`-i`) modes.

## Attach-Then-Start Pattern

Interactive sessions (`-ai`) follow the canonical pattern from `run.go`:

```
Attach → Wait channel → I/O goroutines → Start → Socket bridge → Resize → Wait
```

**Key separation**: I/O streaming (`pty.Stream`) starts pre-start; resize (`signals.NewResizeHandler`) starts post-start. This matches Docker CLI's split between `attachContainer()` and `MonitorTtySize()`.

### Ordering rationale

1. **Attach before start** — prevents race with short-lived containers that exit before attach
2. **Wait channel before start** — uses `WaitConditionNextExit` because a "created" container is already not-running
3. **I/O goroutines before start** — ensures kernel pipe buffers are being drained when container output begins
4. **Resize after start** — Docker API rejects resize on non-running containers; +1/-1 trick forces SIGWINCH
5. **2s detach timeout** — distinguishes Ctrl+P Ctrl+Q detach (no exit status) from normal exit

### `waitForContainerExit` helper

Local helper wrapping `ContainerWait` dual channels into a single `<-chan int`. Simplified vs `run.go` version — always uses `WaitConditionNextExit` (start command never has `--rm`/autoRemove).

## Phase Structure

```
Phase A: Config + Docker connect + agent name resolution
Phase B: Container start via shared.ContainerStart()
         ├── BootstrapServicesPreStart (firewall daemon, host proxy)
         ├── client.ContainerStart (Docker engine start)
         └── BootstrapServicesPostStart (eBPF program attachment, socket bridge)
```

Both attach and non-attach paths delegate to `shared.ContainerStart()`, passing a `shared.CommandOpts` for DI. The `CommandOpts` wires: Config, Client, ProjectManager, HostProxy, SocketBridge, Logger. Firewall eBPF programs are attached from outside the container by the eBPF manager, not by a container entrypoint.

See `shared/CLAUDE.md` for `ContainerStart`, `BootstrapServicesPreStart`, and `BootstrapServicesPostStart` docs.

## Non-Attach Path

`startContainersWithoutAttach` — iterates containers, calls `shared.ContainerStart()` per container, prints names to stdout on success, errors to stderr.

## Testing

- **Tier 1** (flag parsing): `start_test.go` — `TestNewCmdStart`, `TestCmdStart_Properties`
- **Tier 2** (Cobra+Factory): `start_test.go` — `TestStartRun_Success`, `TestStartRun_MultipleContainers`, `TestStartRun_PartialFailure`, `TestStartRun_DockerConnectionError`, `TestStartRun_NilHostProxy`
- **Integration**: `test/commands/container_start_test.go` — exercises non-attach path with real Docker
- **Visual UAT**: attach path tested manually (`clawker container start -ai <name>`)
