# Container Fixes: Terminal Colors + Serena LSP

**Branch:** `a/container-fixes`
**Status:** Design approved, ready for implementation

## Problem

Two independent issues degrade the developer experience in clawker containers:

1. **Terminal colors limited to 8 colors** — `zsh-in-docker` hardcodes `TERM=xterm` in `.zshrc`. The Dockerfile template sets no `TERM`, `COLORTERM`, or `LANG` defaults. Containers get 8-color output instead of 256/truecolor.

2. **Serena language servers fail** — `.serena/project.yml` configures `bash` and `yaml` language servers which require `npm`. Node.js is not installed in the container. Serena's all-or-nothing LSP init aborts `gopls` too.

## Design

### 1. `internal/term/term.go` (new file)

`Term` struct captures the full terminal state. `FromEnv()` constructor reads from the real system.

```go
type Term struct {
    in           *os.File
    out          *os.File
    errOut       *os.File
    isTTY        bool
    colorEnabled bool
    is256Enabled bool
    hasTrueColor bool
    width        int

    // widthOverride allows callers to override the detected terminal width.
    // Reserved for future use (e.g., responsive table formatting).
    widthOverride int

    // widthPercent expresses a percentage of terminal width for layout calculations.
    // Reserved for future use (e.g., column width allocation in table output).
    widthPercent int
}

func FromEnv() *Term
```

`FromEnv()` detection logic:
- `in`/`out`/`errOut` = `os.Stdin`/`os.Stdout`/`os.Stderr`
- `isTTY` = `term.IsTerminal(out.Fd())`
- `colorEnabled` = TTY and `$TERM` not empty and not `"dumb"`
- `is256Enabled` = `$TERM` contains `"256color"`
- `hasTrueColor` = `$COLORTERM` is `"truecolor"` or `"24bit"`
- Cascade: truecolor implies 256, 256 implies color
- `width` = `term.GetSize(out.Fd())` with fallback to 80
- `widthOverride` = 0 (future use)
- `widthPercent` = 0 (future use)

Exported getters: `IsTTY()`, `IsColorEnabled()`, `Is256ColorSupported()`, `IsTrueColorSupported()`, `Width()`.

### 2. `internal/iostreams/iostreams.go`

Unexported `term` interface and private `term` field:

```go
// term describes terminal capabilities. Unexported — commands access
// terminal info through IOStreams methods, never directly.
type term interface {
    IsTTY() bool
    IsColorEnabled() bool
    Is256ColorSupported() bool
    IsTrueColorSupported() bool
    Width() int
}

type IOStreams struct {
    term term  // set by System(), private
    // ... existing fields unchanged
}
```

Note: existing `golang.org/x/term` import aliased to avoid collision.

New `System()` constructor — captures the host terminal state:

```go
func System() *IOStreams {
    ios := &IOStreams{
        In:     os.Stdin,
        Out:    os.Stdout,
        ErrOut: os.Stderr,
        term:   term.FromEnv(),
        // ... existing init (TTY cache, progress, spinner env)
    }
    return ios
}
```

New delegating methods:

```go
func (s *IOStreams) Is256ColorSupported() bool  { return s.term != nil && s.term.Is256ColorSupported() }
func (s *IOStreams) IsTrueColorSupported() bool { return s.term != nil && s.term.IsTrueColorSupported() }
```

Existing `NewIOStreams()` remains for backward compat. `NewTestIOStreams()` unchanged (`term` nil, new methods return false).

### 3. `internal/cmd/factory/default.go`

`ioStreams()` helper calls `System()`, can layer clawker config overrides:

```go
func ioStreams(f *cmdutil.Factory) *iostreams.IOStreams {
    io := iostreams.System()
    cfg, err := f.Config()
    if err != nil {
        return io
    }
    // Future: clawker config overrides for terminal settings
    // Currently none — System() defaults are sufficient
    return io
}
```

### 4. `internal/docker/env.go`

Replace `RuntimeEnv(cfg *config.Project)` with explicit opts struct:

```go
type RuntimeEnvOpts struct {
    // Editor preferences
    Editor string
    Visual string

    // Firewall
    FirewallEnabled  bool
    FirewallDomains  []string
    FirewallOverride bool

    // Terminal capabilities (from host)
    Is256Color bool
    TrueColor  bool

    // User-defined overrides (arbitrary pass-through)
    AgentEnv       map[string]string
    InstructionEnv map[string]string
}

func RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)
```

RuntimeEnv terminal logic:
- If `Is256Color`: set `TERM=xterm-256color`
- If `TrueColor`: set `COLORTERM=truecolor`
- `AgentEnv` overrides base defaults (including TERM/COLORTERM)
- `InstructionEnv` overrides everything

### 5. Container command call sites

All commands that call `RuntimeEnv` build opts from config + iostreams:

```go
runtimeEnv, err := docker.RuntimeEnv(docker.RuntimeEnvOpts{
    Editor:           cfg.Agent.Editor,
    Visual:           cfg.Agent.Visual,
    FirewallEnabled:  cfg.Security.FirewallEnabled(),
    FirewallDomains:  cfg.Security.Firewall.GetFirewallDomains(config.DefaultFirewallDomains),
    FirewallOverride: cfg.Security.Firewall.IsOverrideMode(),
    Is256Color:       ios.Is256ColorSupported(),
    TrueColor:        ios.IsTrueColorSupported(),
    AgentEnv:         cfg.Agent.Env,
    InstructionEnv:   cfg.Build.Instructions.Env,
})
```

### 6. `internal/bundler/assets/Dockerfile.tmpl`

Hardcoded safe defaults (static, no conditional logic):

```dockerfile
ENV TERM=xterm-256color
ENV COLORTERM=truecolor
ENV LANG=en_US.UTF-8
```

zsh-in-docker `-a` flag (both Alpine and Debian blocks):

```
-a "export TERM=xterm-256color"
```

These are unconditional. The Dockerfile defaults cover the common case. RuntimeEnv overrides at container creation with host-accurate values.

### 7. `internal/bundler/hash_test.go`

Update content hash golden values — Dockerfile template content changes.

### 8. `clawker.yaml`

Add `nodejs` and `npm` to `build.packages`:

```yaml
packages:
  - git
  - curl
  - ripgrep
  - less
  - procps
  - sudo
  - fzf
  - nodejs
  - npm
```

`registry.npmjs.org` is already in `init-firewall.sh` hardcoded defaults — no firewall change needed.

## Files Changed

| File | Change |
|------|--------|
| `internal/term/term.go` | **NEW** — Term struct + FromEnv() |
| `internal/term/term_test.go` | **NEW** — Unit tests |
| `internal/iostreams/iostreams.go` | Unexported term interface, private term field, System() constructor, delegating methods |
| `internal/iostreams/iostreams_test.go` | Tests for System(), Is256ColorSupported(), IsTrueColorSupported() |
| `internal/cmd/factory/default.go` | ioStreams() calls System() |
| `internal/docker/env.go` | RuntimeEnvOpts struct, refactored RuntimeEnv() |
| `internal/docker/env_test.go` | Update tests for new signature + terminal env vars |
| `internal/cmd/container/create/create.go` | Build RuntimeEnvOpts |
| `internal/cmd/container/run/run.go` | Build RuntimeEnvOpts |
| `internal/cmd/container/start/start.go` | Build RuntimeEnvOpts |
| `internal/cmd/container/attach/attach.go` | Build RuntimeEnvOpts |
| `internal/bundler/assets/Dockerfile.tmpl` | ENV defaults + zsh-in-docker -a flag |
| `internal/bundler/hash_test.go` | Update golden hashes |
| `clawker.yaml` | Add nodejs, npm to packages |

## Precedence

Terminal env var precedence (last wins):

1. Dockerfile template ENV defaults (build-time, hardcoded safe defaults)
2. RuntimeEnv from host terminal detection (container creation time)
3. `agent.env` from clawker.yaml (user override)
4. `build.instructions.env` from clawker.yaml (highest priority)
5. zsh-in-docker `-a` flag (interactive shell only, matches Dockerfile default)

## Testing

- Unit tests for `term.FromEnv()` with mocked env vars
- Unit tests for IOStreams `System()` and delegating methods
- Unit tests for `RuntimeEnv()` with terminal capability fields
- Unit tests verifying AgentEnv overrides auto-detected TERM/COLORTERM
- Existing `env_test.go` precedence tests updated for new signature
- Content hash golden value update
