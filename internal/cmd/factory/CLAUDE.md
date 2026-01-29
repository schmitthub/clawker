# Factory Package

The factory wiring layer. Constructs a fully-wired `*cmdutil.Factory` with
real dependency implementations. Called exactly once at the CLI entry point.

## Key File

| File | Purpose |
|------|---------|
| `default.go` | `New(version, commit)` — constructs Factory with all lazy closures |

## Usage

```go
// Entry point only (internal/clawker/cmd.go)
f := factory.New(Version, Commit)
rootCmd := root.NewCmdRoot(f)
```

**Tests NEVER import this package.** Tests construct minimal Factory structs:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{IOStreams: tio.IOStreams, Version: "1.0.0"}
```

## Dependency Wiring Order

New() initializes closures in topological order:
1. IOStreams (no deps — created eagerly)
2. RegistryLoader, Registry (filesystem)
3. Resolution (depends on Registry + WorkDir)
4. ConfigLoader, Config (depends on Resolution + WorkDir)
5. SettingsLoader, Settings (depends on Resolution)
6. Client (lazy Docker connection)
7. HostProxy, EnsureHostProxy, StopHostProxy, HostProxyEnvVar
8. Prompter (depends on IOStreams)
9. CloseClient, ResetConfig, InvalidateSettingsCache (cleanup closures)

All closures use `sync.Once` for lazy single-initialization.
Cross-references use `f.Resolution()`, `f.Registry()` etc. via the Factory pointer.
