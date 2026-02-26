# E2E Tests

End-to-end tests that exercise the **full Cobra command pipeline** in-process via `h.Run()` → `root.NewCmdRoot(factory)`. Every command flows through the same root command, flag parsing, PreRunE hooks, and execution path as the shipped binary. Tests wire a `cmdutil.Factory` struct literal with **real production dependencies** (Docker client, config, project manager) isolated by `testenv` XDG dirs and `docker.TestLabelConfig` labels.

## Non-Negotiable Rules

1. **Full CLI pipeline** — `h.Run(args...)` calls `root.NewCmdRoot(h.Factory)` → `cmd.SetArgs()` → `cmd.Execute()`. This IS the real CLI.
2. **No Docker SDK for operations** — no `moby/client` imports for container create/start/stop/rm/inspect. Use `h.Run("container", "exec", ...)`, `h.Run("container", "inspect", ...)`, etc.
3. **No TestMain** — every test is fully isolated. No shared state, no global setup/teardown.
4. **Self-cleaning** — each test creates its own project, containers, and volumes, and cleans them up via `h.Run("container", "stop/rm", ...)` in `t.Cleanup`.
5. **Config via production flow** — `h.Run("project", "init", "name", "--yes")` scaffolds config. Modifications via `config.NewConfig()` + `ProjectStore().Set()` + `.Write()` (writes to the same files the CLI reads).

## Test Structure

```
test/e2e/
├── harness/              # In-process CLI harness (Factory + root.NewCmdRoot + isolated dirs)
├── credentials_test.go   # Credential forwarding env vars + SSH key signing
└── workdir_mounts_test.go # Working directory override
```

## Pattern for New Tests

```go
func TestSomething(t *testing.T) {
    // 1. Wire Factory with real deps + test isolation.
    tio, _, out, errOut := iostreams.Test()
    f := &cmdutil.Factory{
        Version:  "test",
        IOStreams: tio,
        Logger:   func() (*logger.Logger, error) { return logger.Nop(), nil },
        TUI:      tui.NewTUI(tio),
        Config:   func() (config.Config, error) { return config.NewConfig() },
        Client: func(ctx context.Context) (*docker.Client, error) {
            cfg, _ := config.NewConfig()
            c, _ := docker.NewClient(ctx, cfg, nil,
                docker.WithLabels(docker.TestLabelConfig(cfg, t.Name())))
            docker.WireBuildKit(c)
            return c, nil
        },
        ProjectManager: func() (project.ProjectManager, error) {
            cfg, _ := config.NewConfig()
            return project.NewProjectManager(cfg, logger.Nop(), nil)
        },
    }
    h := &harness.Harness{T: t, Factory: f}

    // 2. Isolated filesystem (testenv XDG dirs + project subdir + chdir).
    h.NewIsolatedFS(nil)

    // 3. Scaffold project via CLI (creates clawker.yaml + registers in project registry).
    initRes := h.Run("project", "init", "myproject", "--yes")
    require.NoError(t, initRes.Err)
    out.Reset(); errOut.Reset()

    // 4. Config mutation via store object (optional — writes same files CLI reads).
    cfg, _ := config.NewConfig()
    cfg.ProjectStore().Set(func(p *config.Project) { /* modify */ })
    cfg.ProjectStore().Write()

    // 5. Build + run via CLI.
    h.Run("build")
    h.Run("container", "run", "--detach", "--disable-firewall",
        "--agent", "dev", "@", "sleep", "infinity")
    t.Cleanup(func() {
        h.Run("container", "stop", "--agent", "dev")
        h.Run("container", "rm", "--agent", "dev", "--volumes")
    })

    // 6. Assert via CLI output capture.
    h.Run("container", "exec", "--agent", "dev", "--", "env")
    envMap := parseEnvLines(out.String())
    assert.Contains(t, envMap, "SSH_AUTH_SOCK")
}
```

## Hidden Commands (bridge, etc.)

Hidden commands like `bridge serve` are registered on `root.NewCmdRoot` and can be invoked via `h.Run("bridge", "serve", "--container", id)` — same as any visible command. This is how tests exercise out-of-process daemons (like the socket bridge) without needing to build a separate binary. The bridge command is self-contained: it creates its own logger, config, and Docker client internally. It does NOT use Factory IOStreams, so running it in a goroutine on the same harness has no buffer contention.

## Environment Isolation

testenv creates temp dirs under `os.TempDir()` (`/private/var/folders/...` on macOS), which is **outside** `$HOME`. This triggers `IsOutsideHome(".")` in `container run/create`. To avoid the safety prompt blocking tests:
- `t.Setenv("HOME", result.BaseDir)` — makes CWD appear inside "home"
- `os.MkdirAll(filepath.Join(result.BaseDir, ".claude"), 0o755)` — container init expects `~/.claude/` to exist
- `agent.claude_code.use_host_auth: false` via config mutation — no real Claude credentials in test env

## Output Capture

`iostreams.Test()` returns `(*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer)` — the last three are the `*bytes.Buffer` backing in, out, and errOut. All `h.Run()` output flows through these buffers. Reset between commands with `out.Reset(); errOut.Reset()`.

## Helpers

| Helper | File | Purpose |
|--------|------|---------|
| `newTestHarness(t, withSocketBridge)` | `credentials_test.go` | Factory + Harness with optional SocketBridge wiring |
| `setupProject(t, h, out, errOut)` | `credentials_test.go` | `NewIsolatedFS` + `project init --yes` + `config.NewConfig()` |
| `parseEnvLines(output)` | `credentials_test.go` | Parses `env` output into `map[string]string` |
| `boolPtr(b)` | `credentials_test.go` | `*bool` helper for config fields |

## Running

```bash
go test ./test/e2e/... -v -timeout 15m                           # All e2e
go test ./test/e2e/... -v -run TestCredentialEnvInjection -timeout 10m
go test ./test/e2e/... -v -run TestSSHKeySigning -timeout 10m
go test ./test/e2e/... -v -run TestWorkdirOverride -timeout 10m
```

`TestSSHKeySigning` requires a running SSH agent with loaded keys (`SSH_AUTH_SOCK` set, `ssh-add -L` succeeds). Skipped automatically if not available.
