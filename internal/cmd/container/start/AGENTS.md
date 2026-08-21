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

Local helper wrapping `ContainerWait` dual channels into a single `<-chan waitResult` (`exitCode` plus a non-nil `err` when the wait itself failed, distinct from a non-zero exit code). Simplified vs `run.go` version — always uses `WaitConditionNextExit` (start command never has `--rm`/autoRemove).

## Phase Structure

```
Phase A: Config + Docker connect + agent name resolution
Phase B: Container start via shared.ContainerStart()
         ├── BootstrapServicesPreStart (host proxy + CP ensure, firewall rules sync, pre_run delivery)
         ├── client.ContainerStart (Docker engine start)
         └── BootstrapServicesPostStart (eBPF program attachment, socket bridge)
```

Both attach and non-attach paths use `shared.CommandOpts` for DI. The `CommandOpts` wires: IOStreams, Config, Client, HostProxy, ControlPlane, AdminClient, SocketBridge, Logger. `AgentName`/`Project` are left empty on both paths — an existing container's registry row already exists. Firewall eBPF programs are attached from outside the container by the eBPF manager, not by a container entrypoint.

A pre-start or Docker-start failure is routed through `shared.ReapFailedStart`, which removes a never-started auto-remove container so its name is freed and appends `shared.ReapedNotice` to the returned error.

**Attach path detail**: `startRun` calls `shared.BootstrapServicesPreStart` directly (under a spinner, in cooked mode), then calls `attachAndStart` which calls `client.ContainerStart` + `shared.BootstrapServicesPostStart` directly — not via `shared.ContainerStart`. The non-attach path uses `shared.ContainerStart` (all three phases).

See `shared/CLAUDE.md` for `ContainerStart`, `BootstrapServicesPreStart`, and `BootstrapServicesPostStart` docs.

## Non-Attach Path

`startContainersWithoutAttach` — iterates containers, calls `shared.ContainerStart()` per container, prints names to stdout on success, errors to stderr.

## Testing

- **Tier 1** (flag parsing): `start_test.go` — `TestNewCmdStart`, `TestCmdStart_Properties`
- **Tier 2** (Cobra+Factory): `start_test.go` — `TestStartRun_Success`, `TestStartRun_MultipleContainers`, `TestStartRun_PartialFailure`, `TestStartRun_DockerConnectionError`, `TestStartRun_NilHostProxy`, `TestStartRun_PreStartFailureReapsAutoRemove`, `TestStartRun_AttachPreStartFailureReapsAutoRemove`
- **Integration**: no dedicated file — non-attach path covered via e2e suite (`test/e2e/`)
- **Visual UAT**: attach path tested manually (`clawker container start -ai <name>`)
