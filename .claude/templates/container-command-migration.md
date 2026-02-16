# Container Command Migration Initiative

**Branch:** `a/pres-run-create-start` (continue from current)
**Parent memory:** `presentation-integration`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: dockertest Setup helpers for missing operations | `complete` | — |
| Task 2: stop + kill + remove (HandleError + review Tier 2) | `complete` | — |
| Task 3: pause + unpause + rename + restart (HandleError + Tier 2) | `complete` | — |
| Task 4: update + wait (HandleError + Tier 2) | `complete` | — |
| Task 5: cp (HandleError + Tier 2) | `complete` | — |
| Task 6: container list (TablePrinter + format/filter) | `complete` | — |
| Task 7: inspect + logs (HandleError + output + Tier 2) | `complete` | — |
| Task 8: top (tabwriter → TablePrinter + Tier 2) | `complete` | — |
| Task 9: stats (tabwriter + HandleError + Tier 2) | `complete` | — |
| Task 10: attach (StreamWithResize → canonical + Tier 2) | `complete` | — |
| Task 11: exec (StreamWithResize → canonical + Tier 2) | `complete` | — |
| Task 12: Cleanup — deprecated helpers + documentation | `complete` | — |

## Key Learnings

(Agents append here as they complete tasks)

- **Task 2**: HandleError → fmt.Errorf migration for stop/kill/remove straightforward. All three had identical pattern: single HandleError for Docker connection + `"Error: %v"` in loop. `remove.go` already had `cs` variable (for SuccessIcon), stop and kill needed it added. Kill had no Tier 2 tests — added `testKillFactory` + 3 tests (success, Docker error, container not found). Stop/remove already had testFactory from SocketBridge tests — added Docker connection error test to each. `SetupFindContainer` takes value `container.Summary`, not pointer — for "not found" tests use `SetupContainerList()` with empty args instead. 3548 tests pass (5 new).
- **Task 3**: HandleError → fmt.Errorf migration for pause/unpause/rename/restart straightforward. Pause, unpause, restart all have identical multi-container loop pattern. Rename has 2 HandleError calls (Docker connection + ContainerRename API) with no loop. `SetupFindContainer` overrides `ContainerListFn` — do NOT call `SetupContainerList()` afterward or it overwrites the find setup. For partial failure tests, rely on `SetupFindContainer` returning a list with only the known container; the missing container naturally won't be found. Rename's `ContainerRenameFn` signature uses `client.ContainerRenameOptions` and `client.ContainerRenameResult` from moby/moby/client; imported as `mobyclient` in test. Error messages from whail Engine wrap API errors — assertions should match outer fmt.Errorf context, not inner API error text. 16 new tests (4 per package), 3564 total pass.
- **Task 4**: HandleError → fmt.Errorf migration for update/wait straightforward. Both have identical pattern to pause/unpause/restart: single HandleError for Docker connection + `"Error: %v"` in multi-container loop. Update command needs `--memory` flag in test args (since `nFlag` check may fail without any resource flags). Wait's `ContainerWait` API returns channels (`Result`/`Error`) — `SetupContainerWait(exitCode)` from Task 1 handles this via `whailtest.FakeContainerWaitExit`. Added `TestWaitRun_NonZeroExitCode` to verify exit code propagation (42 → stdout). 9 new tests (4 update + 5 wait), 3573 total pass.
- **Task 5**: HandleError → fmt.Errorf migration for cp straightforward. 3 HandleError calls: one for Docker connection in `cpRun`, one for CopyFromContainer API in `copyFromContainer`, one for CopyToContainer API in `copyToContainer`. Code review identified inconsistent error wrapping in stdin/stdout paths (bare errors vs wrapped) and asymmetric IOStreams access (parameter vs opts field). Fixed both: removed redundant `ios` parameter from `copyFromContainer`/`copyToContainer` (both now access through `opts.IOStreams`), wrapped all error paths consistently with `fmt.Errorf("copying to/from container %q: %w", ...)`. `SetupCopyFromContainer()` returns empty tar stream — sufficient for stdout test since io.Copy of empty reader succeeds. `SetupCopyToContainer()` accepts any content — sufficient for stdin test. 7 new Tier 2 tests (copy-from stdout, copy-to stdin, Docker connection error, container not found for both directions, both-paths-container, both-paths-host), 3580 total pass.
- **Task 6**: Full rewrite of container list following canonical image list pattern. Replaced `tabwriter.NewWriter` with `opts.TUI.NewTable(headers...)`, replaced `cmdutil.HandleError` with `return fmt.Errorf("connecting to Docker: %w", err)`, removed old `containerForFormat`/`outputFormatted` and added `containerRow` struct + canonical format dispatch switch (quiet→JSON→template→table). Added `FormatFlags` (`--format`, `--json`, `-q`/`--quiet`) + `FilterFlags` (`--filter key=value`) with valid keys `[name, status, agent]`. Kept `--project/-p` as a server-side Docker API filter (not local filter) for ergonomics. Added `matchGlob` for trailing-wildcard filter matching, `strings.EqualFold` for status matching (case-insensitive). Test rewrote: replaced hand-written `splitArgs` with `google/shlex`, added `testFactory` with TUI, 20 tests total: 3 Tier 1 (flag parsing, format flags, properties) + 12 Tier 2 (default table, JSON, quiet, template, filter-by-status, filter-by-agent, invalid filter key, empty results ×2, Docker error, project filter) + 5 unit (formatCreatedTime, truncateImage, matchGlob). Code reviewer flagged `opts.Format.Format` → `opts.Format.Template()` stutter fix; applied. 3602 tests pass.
- **Task 7**: inspect has 1 HandleError (Docker connection) + error loop with bare `"Error: %v"` → migrated to `fmt.Errorf("connecting to Docker: %w", err)` + `cs.FailureIcon()` pattern. Also modernized `interface{}` → `any` in `outputFormatted` template func (linter suggestion). Logs has 1 HandleError (Docker connection) → straightforward `fmt.Errorf`. Neither command needs TUI (no tables), so `testFactory` omits TUI field. For inspect tests, `SetupContainerList(c)` + `SetupContainerInspect(name, c)` provides both the list for `FindContainerByName` and the richer inspect result with State data (needed for `{{.State.Status}}` template test). For logs, `SetupFindContainer` + `SetupContainerLogs` is sufficient since logs just streams output. Multi-container partial failure test works because `FindContainerByName` returns `ErrContainerNotFound` when name doesn't match any item in the (always-returns-all) fake list. 9 new tests, 3611 total pass.
- **Task 8**: Straightforward migration. 2 HandleError calls → `fmt.Errorf`, tabwriter → `opts.TUI.NewTable(top.Titles...)` with dynamic headers from Docker API. `AddRow(proc...)` works cleanly since `top.Processes` is `[][]string`. Added `TUI *tui.TUI` to `TopOptions`, wired `f.TUI`. Removed unused `strings` and `text/tabwriter` imports. 3 new Tier 2 tests (happy path with process table rendering, Docker connection error, container not found). `IOStreams` field retained on struct for consistency even though `topRun` no longer uses it directly. 3614 total tests pass.
- **Task 9**: 1 HandleError + 2 tabwriter usages migrated in stats. `showStatsOnce` replaced tabwriter with `opts.TUI.NewTable(headers...)`. `streamStats` creates a fresh `opts.TUI.NewTable` per refresh cycle (ANSI clear-screen approach kept — BubbleTea migration out of scope). Renamed `printStats(w *tabwriter.Writer, ...)` to `addStatsRow(tp *tui.TablePrinter, ...)` to reflect the new interface. Error output in `showStatsOnce` migrated from `"Error: ..."` to `cs.FailureIcon()` pattern; streaming warnings in `streamStats` migrated from `"Warning: ..."` and `"Error: ..."` to `cs.WarningIcon()`/`cs.FailureIcon()`. Code reviewer flagged dead `IOStreams` field on `TopOptions` (from Task 8) — cleaned up by removing field, assignment, and unused `iostreams` import. `SetupContainerStats(json)` works for `ContainerStatsOneShot` because both route through `FakeAPI.ContainerStatsFn`. 4 new Tier 2 tests (no-stream happy path, Docker connection error, container not found, no running containers). 3618 total tests pass.
- **Task 10**: Straightforward migration — 2 HandleError calls + 1 StreamWithResize. Replaced `StreamWithResize` with `pty.Stream()` in goroutine + immediate resize (+1/-1 trick) + `signals.NewResizeHandler`. Key difference from `start.go`: no `waitForContainerExit` needed since container is already running — simplified to `return <-streamDone`. Non-TTY path (stdcopy demux) was already correct — left unchanged. `SetupContainerList(fixture) + SetupContainerInspect(name, fixture)` provides both the list for `FindContainerByName` and the richer inspect result with State. Config.Tty defaults to false (non-TTY path tested). 4 new Tier 2 tests (Docker connection error, container not found, container not running, non-TTY happy path), 3622 total pass.
- **Task 11**: Exec has 6 HandleError calls (most of any command) — all replaced with contextual `fmt.Errorf`. The TTY path used `StreamWithResize` with an `ExecResize`-based resize function — migrated to `pty.Stream` goroutine + `signals.NewResizeHandler` (same canonical pattern as attach). Credential injection (host proxy + git credentials + socket bridge) left unchanged — it's correct and well-structured. For Tier 2 tests, `testConfig()` must disable host proxy (`EnableHostProxy = &false`) and null git credentials to avoid nil panics — `SecurityConfig.HostProxyEnabled()` defaults to `true` when nil. FakeAPIClient was missing `ExecStartFn`, `ExecAttachFn`, `ExecInspectFn` — added to `whailtest/fake_client.go` along with 3 new `SetupExec*` helpers in `dockertest/helpers.go`. `SetupExecAttach` uses `net.Pipe()` + `bufio.NewReader(strings.NewReader(""))` for the hijacked response. 6 new Tier 2 tests (Docker connection error, container not found, container not running, detach mode, non-TTY happy path, non-zero exit code), 3628 total pass.
- **Task 12**: Final cleanup verified zero deprecated patterns across all container commands: `cmdutil.HandleError` (0), `tabwriter.NewWriter` (0), `StreamWithResize` (0), `"Error: %v"` (0), `"Warning: %v"` (0). Full unit test suite passes (3628 tests, 5 expected skips). Updated `internal/cmd/container/CLAUDE.md` with Migration Status section documenting canonical patterns (error handling, table rendering, Stream+resize, format/filter flags) and cross-references to `attach/`, `exec/`, `start/`, `shared/` CLAUDE.md files. Updated `presentation-integration` memory with initiative summary.
- **Task 1**: All simple action helpers (Stop, Kill, Pause, Unpause, Rename, Restart, Update) return empty result structs — no recordCall needed since FakeAPIClient methods handle recording internally. `SetupContainerInspect` takes both containerID and Summary to populate State field (needed by remove's stop-before-remove flow). `SetupContainerStats` takes a JSON string param for flexibility; empty string gives minimal default. `SetupContainerLogs` returns plain ReadCloser (non-multiplexed, suitable for TTY logs). 14 new helpers added, all compile clean, 3543 tests pass.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run a single `code-reviewer` subagent to review this task's changes, then fix any findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Container commands in `internal/cmd/container/*/` are being migrated to canonical patterns established in `run.go`, `create.go`, and `start.go`. Three commands are already migrated. 17 remain. The migration standardizes error handling, output styling, table rendering, PTY streaming, and test coverage.

**What "migration" means for each command:**
1. Replace `cmdutil.HandleError(ios, err); return err` → `return fmt.Errorf("context: %w", err)`
2. Replace raw `fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)` → use `cs.FailureIcon()` for per-item errors
3. Replace raw `tabwriter.NewWriter` → `opts.TUI.NewTable(headers...)`
4. Replace `StreamWithResize` → `Stream` + separate `signals.NewResizeHandler` (attach/exec only)
5. Add format/filter flags where appropriate (list command)
6. Add Tier 2 (Cobra+Factory) tests where missing
7. Update `cmdutil` import to remove `HandleError` dependency where it becomes unused

**Already migrated (3):** `run`, `create`, `start`

**Current State Audit:**

| Command | HandleError | tabwriter | StreamWithResize | Tier 2 Tests | Output Scenario | SocketBridge | HostProxy |
|---------|:-----------:|:---------:|:----------------:|:------------:|-----------------|:------------:|:---------:|
| stop | 1 | — | — | Yes | Static | Yes | — |
| kill | 1 | — | — | Yes | Static | — | — |
| pause | 1 | — | — | No | Static | — | — |
| unpause | 1 | — | — | No | Static | — | — |
| remove | 1 | — | — | Yes | Static | Yes | — |
| rename | 2 | — | — | No | Static | — | — |
| restart | 1 | — | — | No | Static | — | — |
| update | 1 | — | — | No | Static | — | — |
| wait | 1 | — | — | No | Static | — | — |
| cp | 3 | — | — | No | Static | — | — |
| inspect | 1 | — | — | No | Static | — | — |
| logs | 1 | — | — | No | Static | — | — |
| list | 1 | 1x | — | No | Static | — | — |
| top | 2 | 1x | — | No | Static | — | — |
| stats | 1 | 2x | — | No | Live-display | — | — |
| attach | 2 | — | Yes | No | Live-interactive | — | Yes |
| exec | 6 | — | Yes | No | Live-interactive | Yes | Yes |

**Totals:** 27 HandleError calls, 4 tabwriter usages, 2 StreamWithResize usages, 14 commands missing Tier 2 tests.

### Key Files

| File | Role |
|------|------|
| `internal/cmd/container/run/run.go` | Canonical pattern — error handling, attach-then-start, Stream+resize |
| `internal/cmd/container/start/start.go` | Canonical attach-then-start for already-created containers |
| `internal/cmd/container/create/create.go` | Canonical CreateContainer usage, output styling |
| `internal/cmd/container/run/run_test.go` | Canonical Tier 2 test — testFactory, testConfig |
| `internal/cmd/container/stop/stop_test.go` | Existing Tier 2 test for simple action commands (testFactory with SocketBridge) |
| `internal/cmd/container/start/CLAUDE.md` | Attach-then-start pattern documentation |
| `internal/cmd/image/list/list.go` | Canonical list command — format/filter/TablePrinter reference |
| `internal/cmd/image/list/list_test.go` | Canonical list Tier 2 tests — format modes, filters |
| `internal/docker/dockertest/helpers.go` | FakeClient Setup helpers (some missing, Task 1 adds them) |
| `internal/docker/dockertest/fixtures.go` | Container/image test fixtures |
| `pkg/whail/whailtest/fake_client.go` | FakeAPIClient with all `*Fn` function fields |
| `.serena/memories/cli-output-style-guide` | Authoritative output patterns, error handling recipes, deprecated method migration guide |
| `internal/cmd/container/CLAUDE.md` | Container package documentation |

### Design Patterns

**Cobra+Factory Test Pattern (Tier 2):**
```go
func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
    t.Helper()
    tio := iostreamstest.New()
    return &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        TUI:      tui.NewTUI(tio.IOStreams),
        Client: func(ctx context.Context) (*docker.Client, error) {
            return fake.Client, nil
        },
        Config: func() *config.Config {
            return config.NewConfigForTest(nil, nil)
        },
    }, tio
}
```

**HandleError Replacement:**
```go
// BEFORE                                    // AFTER
client, err := opts.Client(ctx)              client, err := opts.Client(ctx)
if err != nil {                              if err != nil {
    cmdutil.HandleError(ios, err)                return fmt.Errorf("connecting to Docker: %w", err)
    return err                               }
}
```

**Multi-Container Error Loop:**
```go
// BEFORE                                    // AFTER
fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)  cs := ios.ColorScheme()
                                             fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
```

**tabwriter → TablePrinter:**
```go
// BEFORE                                    // AFTER
w := tabwriter.NewWriter(ios.Out, ...)       tp := opts.TUI.NewTable("NAME", "STATUS")
fmt.Fprintln(w, "NAME\tSTATUS")             for _, c := range items {
for _, c := range items {                        tp.AddRow(c.Name, c.Status)
    fmt.Fprintf(w, "%s\t%s\n", ...)         }
}                                            return tp.Render()
w.Flush()
```

**StreamWithResize → Stream + Resize (attach/exec only):**
```go
// BEFORE
return pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc)

// AFTER — I/O before action, resize after
streamDone := make(chan error, 1)
go func() { streamDone <- pty.Stream(ctx, hijacked.HijackedResponse) }()
// ... container is already running for attach/exec, so start resize immediately ...
if pty.IsTerminal() {
    w, h, _ := pty.GetSize()
    resizeFunc(uint(h+1), uint(w+1))  // +1/-1 trick
    resizeFunc(uint(h), uint(w))
    rh := signals.NewResizeHandler(resizeFunc, pty.GetSize)
    rh.Start()
    defer rh.Stop()
}
// Wait for stream
err := <-streamDone
```

**NOTE for attach/exec:** Unlike `start.go` where I/O must start BEFORE `ContainerStart`, in `attach` and `exec` the container is already running. The key improvement is separating `Stream` (I/O only) from resize handling, but the ordering concern about "I/O before start" does not apply — the container is already started.

### Rules

- Read `CLAUDE.md`, `.claude/rules/code-style.md`, `.claude/rules/testing.md`, and `internal/cmd/container/CLAUDE.md` before starting
- Read `.serena/memories/cli-output-style-guide` for output patterns and deprecated method migration guide
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile (`go build ./...`) and tests must pass (`make test`)
- Follow existing test patterns in the package — copy `stop_test.go` structure for simple commands
- When adding dockertest Setup helpers, follow the pattern of existing helpers in `internal/docker/dockertest/helpers.go`
- Do NOT remove `cmdutil.HandleError` itself — other non-container commands still use it; only remove from container commands
- Do NOT add phase comments (Phase A/B/C) to simple action commands — those are for commands with distinct lifecycle phases (run, create, start)
- TUI import: Only add `TUI *tui.TUI` to Options struct if the command needs `NewTable()` — simple action commands don't need it

---

## Task 1: dockertest Setup Helpers for Missing Operations

**Creates/modifies:** `internal/docker/dockertest/helpers.go`, `internal/docker/dockertest/fixtures.go`
**Depends on:** Nothing

### Implementation Phase

The FakeAPIClient (`pkg/whail/whailtest/fake_client.go`) already has `*Fn` function fields for every Docker operation. But the convenience `Setup*` helpers on `FakeClient` (`internal/docker/dockertest/helpers.go`) are missing helpers for: Stop, Kill, Pause, Unpause, Rename, Restart, Update, Inspect, Logs, Top, Stats, CopyFromContainer, ExecCreate.

1. Read `internal/docker/dockertest/helpers.go` to understand the existing Setup helper pattern
2. Read `internal/docker/dockertest/fixtures.go` to understand existing fixtures
3. Add the following Setup helpers to `helpers.go`, following the existing pattern:

```go
// SetupContainerStop configures the fake to accept ContainerStop calls.
func (f *FakeClient) SetupContainerStop() { ... }

// SetupContainerKill configures the fake to accept ContainerKill calls.
func (f *FakeClient) SetupContainerKill() { ... }

// SetupContainerPause configures the fake to accept ContainerPause calls.
func (f *FakeClient) SetupContainerPause() { ... }

// SetupContainerUnpause configures the fake to accept ContainerUnpause calls.
func (f *FakeClient) SetupContainerUnpause() { ... }

// SetupContainerRename configures the fake to accept ContainerRename calls.
func (f *FakeClient) SetupContainerRename() { ... }

// SetupContainerRestart configures the fake to accept ContainerRestart calls.
func (f *FakeClient) SetupContainerRestart() { ... }

// SetupContainerUpdate configures the fake to accept ContainerUpdate calls.
func (f *FakeClient) SetupContainerUpdate() { ... }

// SetupContainerInspect sets up a ContainerInspect response for the given container ID.
func (f *FakeClient) SetupContainerInspect(containerID string) { ... }

// SetupContainerLogs configures the fake to return logs for the given container.
func (f *FakeClient) SetupContainerLogs(logs string) { ... }

// SetupContainerTop configures the fake to return top output.
func (f *FakeClient) SetupContainerTop(titles []string, processes [][]string) { ... }

// SetupContainerStats configures the fake for stats output.
func (f *FakeClient) SetupContainerStats() { ... }

// SetupCopyFromContainer configures the fake to accept CopyFromContainer calls.
func (f *FakeClient) SetupCopyFromContainer() { ... }

// SetupExecCreate configures the fake to accept ExecCreate calls, returning a given exec ID.
func (f *FakeClient) SetupExecCreate(execID string) { ... }
```

4. Each Setup helper should:
   - Set the corresponding `FakeAPI.*Fn` function field
   - Record the call via `f.recordCall("MethodName")` (follow existing pattern)
   - Return a reasonable default success response
   - For methods that need parameters (Inspect, Logs, Top), accept configuration arguments

5. Add any needed fixtures to `fixtures.go` if helpful (e.g., `ContainerInspectFixture`)

### Acceptance Criteria

```bash
go build ./internal/docker/dockertest/...
go test ./internal/docker/dockertest/... -v
# All new Setup helpers compile and don't break existing tests
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Task 1 is complete. Begin Task 2: stop + kill + remove HandleError migration."

---

## Task 2: stop + kill + remove (HandleError + review Tier 2)

**Creates/modifies:** `stop/stop.go`, `kill/kill.go`, `remove/remove.go` + their `*_test.go`
**Depends on:** Task 1 (Setup helpers)

### Implementation Phase

These 3 commands already have Tier 2 tests. Migration is HandleError replacement + output styling.

**For each command (`stop`, `kill`, `remove`):**

1. Read the current `*Run` function body using `find_symbol ... include_body=true`
2. Read the existing `*_test.go` to understand current coverage
3. Replace every `cmdutil.HandleError(ios, err); return err` with `return fmt.Errorf("descriptive context: %w", err)`
4. Replace `fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)` in multi-container loops with:
   ```go
   cs := ios.ColorScheme()
   fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
   ```
5. Remove `cmdutil` import if no longer needed (only if `HandleError` was the only usage)
6. Review existing Tier 2 tests — add a test for the Docker connection error path if missing:
   ```go
   func TestStopRun_DockerConnectionError(t *testing.T) {
       tio := iostreamstest.New()
       f := &cmdutil.Factory{
           IOStreams: tio.IOStreams,
           Client: func(_ context.Context) (*docker.Client, error) {
               return nil, fmt.Errorf("cannot connect to Docker daemon")
           },
           Config: func() *config.Config { return config.NewConfigForTest(nil, nil) },
       }
       cmd := NewCmdStop(f, nil)
       cmd.SetArgs([]string{"mycontainer"})
       cmd.SetIn(&bytes.Buffer{})
       cmd.SetOut(tio.OutBuf)
       cmd.SetErr(tio.ErrBuf)
       err := cmd.Execute()
       require.Error(t, err)
       assert.Contains(t, err.Error(), "connecting to Docker")
   }
   ```

**Special notes:**
- `stop` and `remove` have SocketBridge cleanup — leave that logic unchanged, just migrate error handling around it
- `remove` uses `stopContainer` before removing — trace the error flow carefully
- `kill` has a simpler structure — single container at a time

### Acceptance Criteria

```bash
go build ./internal/cmd/container/stop/... ./internal/cmd/container/kill/... ./internal/cmd/container/remove/...
go test ./internal/cmd/container/stop/... -v
go test ./internal/cmd/container/kill/... -v
go test ./internal/cmd/container/remove/... -v
make test  # Full unit test suite
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-2 are complete. Begin Task 3: pause + unpause + rename + restart HandleError migration + new Tier 2 tests."

---

## Task 3: pause + unpause + rename + restart (HandleError + Tier 2)

**Creates/modifies:** 4 command `.go` files + 4 `*_test.go` files
**Depends on:** Task 1 (Setup helpers)

### Implementation Phase

These 4 commands have Tier 1 tests (flag parsing) but NO Tier 2 tests. Each needs HandleError migration + new Tier 2 tests.

**For each command:**

1. Read the `*Run` function body
2. Replace `cmdutil.HandleError(ios, err); return err` → `return fmt.Errorf("context: %w", err)`
3. Replace `fmt.Fprintf(ios.ErrOut, "Error: %v\n", err)` → `cs.FailureIcon()` pattern
4. Create a per-package `testFactory` helper (copy from `stop_test.go`, remove SocketBridge since these don't need it):
   ```go
   func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
       t.Helper()
       tio := iostreamstest.New()
       return &cmdutil.Factory{
           IOStreams: tio.IOStreams,
           Client: func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
           Config: func() *config.Config { return config.NewConfigForTest(nil, nil) },
       }, tio
   }
   ```
5. Add Tier 2 tests using `NewCmd*(f, nil)` — at minimum:
   - Happy path (operation succeeds, name printed to stdout)
   - Docker connection error (returns error, not HandleError)
   - Container not found error
   - Multi-container partial failure (where applicable)

**Command-specific notes:**
- `pause/unpause`: Simplest commands. Single operation per container.
- `rename`: Takes exactly 2 args (old name, new name). Has `opts.Agent` resolution for both args. 2 HandleError calls.
- `restart`: Multi-container loop, similar to stop.

### Acceptance Criteria

```bash
go test ./internal/cmd/container/pause/... -v
go test ./internal/cmd/container/unpause/... -v
go test ./internal/cmd/container/rename/... -v
go test ./internal/cmd/container/restart/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-3 are complete. Begin Task 4: update + wait HandleError migration + Tier 2 tests."

---

## Task 4: update + wait (HandleError + Tier 2)

**Creates/modifies:** `update/update.go`, `wait/wait.go` + their `*_test.go`
**Depends on:** Task 1 (Setup helpers)

### Implementation Phase

1. Read each command's `*Run` function
2. Apply HandleError → fmt.Errorf migration
3. Apply error output styling (cs.FailureIcon)
4. Add per-package `testFactory` + Tier 2 tests

**Command-specific notes:**
- `update`: Updates container resources (memory, CPU). Single HandleError.
- `wait`: Blocks until container stops, prints exit code. Uses `ContainerWait` API — needs `SetupContainerWait` from Task 1.

### Acceptance Criteria

```bash
go test ./internal/cmd/container/update/... -v
go test ./internal/cmd/container/wait/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-4 are complete. Begin Task 5: cp HandleError migration + Tier 2 tests."

---

## Task 5: cp (HandleError + Tier 2)

**Creates/modifies:** `cp/cp.go`, `cp/cp_test.go`
**Depends on:** Task 1 (Setup helpers — SetupCopyFromContainer, SetupCopyToContainer)

### Implementation Phase

`cp` is more complex than simple action commands — it has bidirectional tar streaming (copyFromContainer, copyToContainer), path parsing, and 3 HandleError calls.

1. Read `cpRun`, `copyFromContainer`, `copyToContainer` using `find_symbol ... include_body=true`
2. Replace all 3 `cmdutil.HandleError(ios, err); return err` → `return fmt.Errorf("context: %w", err)`
3. Add `testFactory` + Tier 2 tests:
   - Copy to container (host → container)
   - Copy from container (container → host)
   - Docker connection error
   - Container not found
   - Invalid path format (both paths are host, or both are container)

**Testing complexity:** cp tests need fake CopyToContainer/CopyFromContainer that accept/return tar streams. The Setup helpers from Task 1 should handle the basic case. For more detailed tests, the agent may need to wire custom `Fn` implementations.

### Acceptance Criteria

```bash
go test ./internal/cmd/container/cp/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-5 are complete. Begin Task 6: container list full rewrite with TablePrinter + format/filter flags."

---

## Task 6: container list (TablePrinter + format/filter)

**Creates/modifies:** `list/list.go`, `list/list_test.go`
**Depends on:** Task 1 (Setup helpers — SetupContainerList already exists)

### Implementation Phase

This is the largest single-command migration. `container list` needs the full format/filter/TablePrinter treatment following the canonical pattern from `internal/cmd/image/list/`.

1. Read `internal/cmd/image/list/list.go` thoroughly — it's the proof-of-concept for this pattern
2. Read `internal/cmd/image/list/list_test.go` for test patterns
3. Read `.serena/memories/cli-output-style-guide` Section 7 (List Commands recipe)
4. Read current `list/list.go` to understand existing structure

**Modifications to `list.go`:**
- Add `TUI *tui.TUI` and `Format *cmdutil.FormatFlags` and `Filter *cmdutil.FilterFlags` to `ListOptions`
- In `NewCmdList`: wire `f.TUI`, call `cmdutil.AddFormatFlags(cmd)` and `cmdutil.AddFilterFlags(cmd)`
- Create a `containerRow` struct for template/JSON output:
  ```go
  type containerRow struct {
      Name    string `json:"name"`
      Status  string `json:"status"`
      Project string `json:"project"`
      Agent   string `json:"agent"`
      Image   string `json:"image"`
      Created string `json:"created"`
  }
  ```
- Rewrite `listRun` with canonical format dispatch switch:
  1. Parse/validate filters
  2. Fetch containers
  3. Apply local filters
  4. Handle empty results → stderr
  5. Build display rows
  6. Switch: quiet → JSON → template → default table
- Replace `tabwriter` with `opts.TUI.NewTable("NAME", "STATUS", "PROJECT", "AGENT", "IMAGE", "CREATED")`
- Remove old `outputFormatted` function and `containerForFormat` struct
- Define valid filter keys: `["name", "status", "project", "agent"]`

**Modifications to `list_test.go`:**
- Add `testFactory` with TUI
- Add Tier 2 tests for: default table, JSON output, quiet mode, template output, filter by project, filter by status, empty results, format mutual exclusivity errors

### Acceptance Criteria

```bash
go test ./internal/cmd/container/list/... -v
make test
# Manual verification:
# clawker container ls                    → styled table
# clawker container ls --json             → JSON array
# clawker container ls -q                 → names only
# clawker container ls --format '{{.Name}}\t{{.Status}}'  → template
# clawker container ls --filter status=running            → filtered
```

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-6 are complete. Begin Task 7: inspect + logs HandleError migration + Tier 2 tests."

---

## Task 7: inspect + logs (HandleError + output + Tier 2)

**Creates/modifies:** `inspect/inspect.go`, `logs/logs.go` + their `*_test.go`
**Depends on:** Task 1 (Setup helpers — SetupContainerInspect, SetupContainerLogs)

### Implementation Phase

**inspect:**
1. Read `inspectRun` — already has custom template support via `--format` and `outputFormatted`
2. Replace the single `cmdutil.HandleError` → `return fmt.Errorf("connecting to Docker: %w", err)`
3. Replace `fmt.Fprintf(ios.ErrOut, "Error: %v\n", e)` in the error loop → `cs.FailureIcon()` pattern
4. The existing `--format` flag and template support can stay as-is (it predates FormatFlags but works correctly for inspect's single-object output)
5. Add `testFactory` + Tier 2 tests: happy path (JSON output), format template, container not found, multi-container partial failure

**logs:**
1. Read `logsRun` function body
2. Replace `cmdutil.HandleError` → `return fmt.Errorf("connecting to Docker: %w", err)`
3. Logs streams output via `io.Copy` — leave streaming logic unchanged
4. Add `testFactory` + Tier 2 tests: happy path (logs printed), follow mode flag, tail count, timestamps flag, Docker connection error

### Acceptance Criteria

```bash
go test ./internal/cmd/container/inspect/... -v
go test ./internal/cmd/container/logs/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 7 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-7 are complete. Begin Task 8: top — tabwriter → TablePrinter + Tier 2 tests."

---

## Task 8: top (tabwriter → TablePrinter + Tier 2)

**Creates/modifies:** `top/top.go`, `top/top_test.go`
**Depends on:** Task 1 (Setup helpers — SetupContainerTop)

### Implementation Phase

1. Read `top.go` — uses `tabwriter` to display process table. Has 2 HandleError calls.
2. Replace both `cmdutil.HandleError` → `return fmt.Errorf()`
3. Replace `tabwriter` with `opts.TUI.NewTable(titles...)`
   - The `ContainerTop` API returns dynamic column titles — pass them to `NewTable`
   - Add `TUI *tui.TUI` to `TopOptions`, wire `f.TUI` in `NewCmdTop`
4. Add `testFactory` + Tier 2 tests: happy path with process table, Docker connection error, container not found

**Note:** `top` returns dynamic headers from Docker API (PID, USER, TIME, COMMAND, etc.). The `NewTable` call uses these dynamic headers: `tp := opts.TUI.NewTable(topResult.Titles...)`.

### Acceptance Criteria

```bash
go test ./internal/cmd/container/top/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 8 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-8 are complete. Begin Task 9: stats — tabwriter + HandleError + Tier 2 tests."

---

## Task 9: stats (tabwriter + HandleError + Tier 2)

**Creates/modifies:** `stats/stats.go`, `stats/stats_test.go`
**Depends on:** Task 1 (Setup helpers — SetupContainerStats)

### Implementation Phase

`stats` has two modes: `--no-stream` (one-shot table) and streaming (live refresh with ANSI clear).

1. Read `statsRun`, `showStatsOnce`, `streamStats`, `printStats` function bodies
2. Replace `cmdutil.HandleError` → `return fmt.Errorf()`
3. For `showStatsOnce` (non-streaming mode): Replace tabwriter with `opts.TUI.NewTable(headers...)`
4. For `streamStats` (streaming mode): Keep the ANSI clear-screen approach for now. Replace the tabwriter inside the loop with `opts.TUI.NewTable` — create a new table per refresh cycle. The ANSI clear + table approach works and a full BubbleTea migration is out of scope for this initiative.
5. Add `TUI *tui.TUI` to `StatsOptions`, wire `f.TUI`
6. Add `testFactory` + Tier 2 tests: `--no-stream` happy path, Docker connection error, no running containers message

**Decision:** Keep ANSI clear-screen for streaming mode. A future initiative can migrate to BubbleTea if needed.

### Acceptance Criteria

```bash
go test ./internal/cmd/container/stats/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 9 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-9 are complete. Begin Task 10: attach — StreamWithResize → canonical pattern + Tier 2 tests."

---

## Task 10: attach (StreamWithResize → canonical pattern + Tier 2)

**Creates/modifies:** `attach/attach.go`, `attach/attach_test.go`, `attach/CLAUDE.md` (new)
**Depends on:** Task 1 (Setup helpers — SetupContainerAttach, SetupContainerInspect already exist)

### Implementation Phase

`attach` connects to an already-running container. Unlike `start.go`, there's no "start after attach" — the container is already running. But it still uses `StreamWithResize` which combines I/O and resize into one call.

1. Read `attachRun` body
2. Read `start/start.go` `attachAndStart` for the canonical Stream+resize pattern
3. Read `internal/docker/pty.go` — understand `Stream` vs `StreamWithResize` signatures

**Replace `StreamWithResize` TTY path:**
```go
// BEFORE (attach.go ~line 170)
return pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc)

// AFTER — separate Stream + resize
streamDone := make(chan error, 1)
go func() {
    streamDone <- pty.Stream(ctx, hijacked.HijackedResponse)
}()

// Resize immediately (container is already running)
if pty.IsTerminal() {
    w, h, err := pty.GetSize()
    if err != nil {
        logger.Debug().Err(err).Msg("failed to get initial terminal size")
    } else {
        resizeFunc(uint(h+1), uint(w+1))  // +1/-1 trick
        resizeFunc(uint(h), uint(w))
    }
    rh := signals.NewResizeHandler(resizeFunc, pty.GetSize)
    rh.Start()
    defer rh.Stop()
}

// Wait for stream (attach has no "wait for exit" — just stream completion)
return <-streamDone
```

4. Replace both `cmdutil.HandleError` calls → `return fmt.Errorf()`
5. Add `signals` import
6. The non-TTY path (stdcopy demux) is already correct — leave unchanged
7. Consider adding exit code propagation via `ContainerWait` (Docker CLI does this for `docker attach`). This would mirror `start.go`'s detach timeout pattern. **Optional enhancement** — only do if straightforward.
8. Add `testFactory` + Tier 2 tests: Docker connection error, container not found, container not running error
9. Create `attach/CLAUDE.md` documenting the attach pattern

### Acceptance Criteria

```bash
go build ./internal/cmd/container/attach/...
go test ./internal/cmd/container/attach/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 10 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-10 are complete. Begin Task 11: exec — StreamWithResize → canonical pattern + Tier 2 tests."

---

## Task 11: exec (StreamWithResize → canonical + Tier 2)

**Creates/modifies:** `exec/exec.go`, `exec/exec_test.go`, `exec/CLAUDE.md` (new)
**Depends on:** Task 1 (Setup helpers — SetupExecCreate)

### Implementation Phase

`exec` is the most complex remaining command — 6 HandleError calls, credential injection, detach mode, TTY with StreamWithResize, non-TTY with stdcopy. The non-TTY path is already correct. Only the TTY path needs Stream+resize migration.

1. Read `execRun` body
2. Read `checkExecExitCode` body

**Replace all 6 HandleError calls:**
- Docker connection → `return fmt.Errorf("connecting to Docker: %w", err)`
- FindContainerByName → `return fmt.Errorf("failed to find container %q: %w", containerName, err)`
- ExecCreate → `return fmt.Errorf("creating exec instance: %w", err)`
- Empty exec ID → `return fmt.Errorf("exec instance returned empty ID")`
- ExecStart (detach) → `return fmt.Errorf("starting detached exec: %w", err)`
- ExecAttach → `return fmt.Errorf("attaching to exec: %w", err)`

**Replace TTY StreamWithResize path:**
```go
// BEFORE
if err := pty.StreamWithResize(ctx, hijacked.HijackedResponse, resizeFunc); err != nil {
    return err
}
return checkExecExitCode(ctx, client, execID)

// AFTER
streamDone := make(chan error, 1)
go func() {
    streamDone <- pty.Stream(ctx, hijacked.HijackedResponse)
}()

// Resize immediately (exec is on a running container)
if pty.IsTerminal() {
    w, h, err := pty.GetSize()
    if err != nil {
        logger.Debug().Err(err).Msg("failed to get initial terminal size")
    } else {
        if err := resizeFunc(uint(h+1), uint(w+1)); err != nil {
            logger.Debug().Err(err).Msg("failed to set artificial exec TTY size")
        }
        if err := resizeFunc(uint(h), uint(w)); err != nil {
            logger.Debug().Err(err).Msg("failed to set actual exec TTY size")
        }
    }
    rh := signals.NewResizeHandler(resizeFunc, pty.GetSize)
    rh.Start()
    defer rh.Stop()
}

if err := <-streamDone; err != nil {
    return err
}
return checkExecExitCode(ctx, client, execID)
```

3. Add `signals` import, add `logger` import if not already present
4. Leave credential injection, host proxy, socket bridge logic unchanged — it's correct
5. Add `testFactory` + Tier 2 tests:
   - Docker connection error
   - Container not found
   - Container not running
   - Detach mode (prints exec ID to stdout)
   - Exit code propagation (non-zero exit)
6. Create `exec/CLAUDE.md` documenting exec patterns (credential injection, TTY path, detach mode)

### Acceptance Criteria

```bash
go build ./internal/cmd/container/exec/...
go test ./internal/cmd/container/exec/... -v
make test
# Integration test still passes:
go test ./test/commands/... -v -timeout 10m -run TestExec
```

### Wrap Up

1. Update Progress Tracker: Task 11 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff prompt:

> **Next agent prompt:** "Continue the container-command-migration initiative. Read the Serena memory `container-command-migration` — Tasks 1-11 are complete. Begin Task 12: Cleanup — verify no deprecated patterns remain, update documentation."

---

## Task 12: Cleanup — deprecated helpers + documentation

**Creates/modifies:** `internal/cmd/container/CLAUDE.md`, `.serena/memories/presentation-integration.md`, `.serena/memories/container-command-migration.md`
**Depends on:** All previous tasks

### Implementation Phase

1. **Verify zero HandleError in container commands:**
   ```
   search_for_pattern "cmdutil.HandleError" relative_path="internal/cmd/container"
   ```
   Should return zero matches.

2. **Verify zero raw tabwriter in container commands:**
   ```
   search_for_pattern "tabwriter.NewWriter" relative_path="internal/cmd/container"
   ```
   Should return zero matches.

3. **Verify zero StreamWithResize in container commands:**
   ```
   search_for_pattern "StreamWithResize" relative_path="internal/cmd/container"
   ```
   Should return zero matches.

4. **Run full test suite:**
   ```bash
   make test
   go test ./test/commands/... -v -timeout 10m  # Integration tests
   ```

5. **Update `internal/cmd/container/CLAUDE.md`:**
   - Add "Migration Status: All 20 container commands use canonical patterns" section
   - Update error handling section to reflect HandleError removal
   - Add note about format/filter flags on `list` command
   - Add attach/exec CLAUDE.md cross-references

6. **Update `.serena/memories/presentation-integration.md`:**
   - Add "Container Command Migration (Complete)" section summarizing all 12 tasks
   - List commands migrated, patterns applied, test counts

7. **Update this memory (`container-command-migration`):**
   - Mark all tasks complete
   - Add final key learnings

### Acceptance Criteria

```bash
make test          # All unit tests pass
make test-all      # All test suites pass (Docker required)
go build ./...     # Everything compiles
# Zero deprecated patterns in container commands
```

### Wrap Up

1. Update Progress Tracker: Task 12 -> `complete`
2. Append final key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Inform the user the initiative is complete:

> **Initiative complete.** All 17 container commands have been migrated to canonical patterns. 27 HandleError calls removed, 4 tabwriter usages replaced, 2 StreamWithResize usages canonicalized, 14 new Tier 2 test suites added, 3 CLAUDE.md files created/updated. The `container list` command now has full format/filter/TablePrinter support.