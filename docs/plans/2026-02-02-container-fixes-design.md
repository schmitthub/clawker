# Container Fixes: Terminal Colors + Serena LSP — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix container terminal color support (8→256/truecolor) and Serena LSP failures (missing Node.js).

**Architecture:** New `Term` struct in `internal/term/` detects host terminal capabilities. IOStreams gets `System()` constructor wiring `term.FromEnv()`. `RuntimeEnv` refactored to `RuntimeEnvOpts` struct, receives terminal capabilities from IOStreams. Dockerfile template gets hardcoded safe defaults.

**Tech Stack:** Go, `golang.org/x/term`, `os.Getenv`, Docker template rendering.

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

---

## Implementation Tasks

### Task 1: Term struct + FromEnv() with tests

**Files:**
- Create: `internal/term/capabilities.go`
- Create: `internal/term/capabilities_test.go`

Note: `internal/term/` already exists with `pty.go`, `raw.go`, `signal.go`. The new file sits alongside them.

**Step 1: Write the failing test**

Create `internal/term/capabilities_test.go`:

```go
package term

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromEnv_256Color(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled())
	assert.True(t, tm.Is256ColorSupported())
	assert.False(t, tm.IsTrueColorSupported())
}

func TestFromEnv_TrueColor(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled())
	assert.True(t, tm.Is256ColorSupported())
	assert.True(t, tm.IsTrueColorSupported())
}

func TestFromEnv_TrueColor24bit(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("COLORTERM", "24bit")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled())
	assert.True(t, tm.Is256ColorSupported(), "truecolor implies 256")
	assert.True(t, tm.IsTrueColorSupported())
}

func TestFromEnv_DumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.False(t, tm.IsColorEnabled())
	assert.False(t, tm.Is256ColorSupported())
	assert.False(t, tm.IsTrueColorSupported())
}

func TestFromEnv_EmptyTerm(t *testing.T) {
	t.Setenv("TERM", "")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.False(t, tm.IsColorEnabled())
	assert.False(t, tm.Is256ColorSupported())
}

func TestFromEnv_BasicXterm(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("COLORTERM", "")

	tm := FromEnv()

	assert.True(t, tm.IsColorEnabled())
	assert.False(t, tm.Is256ColorSupported())
	assert.False(t, tm.IsTrueColorSupported())
}

func TestFromEnv_Width(t *testing.T) {
	tm := FromEnv()

	// In test environment (not a TTY), width falls back to 80
	assert.Greater(t, tm.Width(), 0)
}

func TestFromEnv_FileDescriptors(t *testing.T) {
	tm := FromEnv()

	// FromEnv always wires to real stdio
	assert.Equal(t, os.Stdin, tm.in)
	assert.Equal(t, os.Stdout, tm.out)
	assert.Equal(t, os.Stderr, tm.errOut)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/term/ -run TestFromEnv -v`
Expected: FAIL — `FromEnv` not defined

**Step 3: Write the implementation**

Create `internal/term/capabilities.go`:

```go
package term

import (
	"os"
	"strings"

	goterm "golang.org/x/term"
)

// Term represents information about the terminal that a process is connected to.
type Term struct {
	in     *os.File
	out    *os.File
	errOut *os.File

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

// FromEnv creates a Term by reading the current system environment.
// Detects TTY state, color capabilities from $TERM and $COLORTERM,
// and terminal width from the stdout file descriptor.
func FromEnv() *Term {
	t := &Term{
		in:     os.Stdin,
		out:    os.Stdout,
		errOut: os.Stderr,
	}

	// TTY detection from stdout
	t.isTTY = goterm.IsTerminal(int(t.out.Fd()))

	// Color detection from environment
	termEnv := os.Getenv("TERM")
	colorTerm := os.Getenv("COLORTERM")

	// Basic color: any non-empty, non-dumb TERM
	t.colorEnabled = termEnv != "" && termEnv != "dumb"

	// 256 color: TERM contains "256color"
	t.is256Enabled = strings.Contains(termEnv, "256color")

	// Truecolor: COLORTERM is "truecolor" or "24bit"
	t.hasTrueColor = colorTerm == "truecolor" || colorTerm == "24bit"

	// Cascade: truecolor implies 256, 256 implies color
	if t.hasTrueColor {
		t.is256Enabled = true
	}
	if t.is256Enabled {
		t.colorEnabled = true
	}

	// Terminal width from stdout fd, fallback to 80
	t.width = 80
	if t.isTTY {
		if w, _, err := goterm.GetSize(int(t.out.Fd())); err == nil && w > 0 {
			t.width = w
		}
	}

	return t
}

// IsTTY returns whether stdout is connected to a terminal.
func (t *Term) IsTTY() bool { return t.isTTY }

// IsColorEnabled returns whether the terminal supports basic color output.
func (t *Term) IsColorEnabled() bool { return t.colorEnabled }

// Is256ColorSupported returns whether the terminal supports 256 colors.
func (t *Term) Is256ColorSupported() bool { return t.is256Enabled }

// IsTrueColorSupported returns whether the terminal supports 24-bit truecolor.
func (t *Term) IsTrueColorSupported() bool { return t.hasTrueColor }

// Width returns the terminal width in columns.
func (t *Term) Width() int { return t.width }
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/term/ -run TestFromEnv -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/term/capabilities.go internal/term/capabilities_test.go
git commit -m "feat: add Term struct with FromEnv() for terminal capability detection"
```

---

### Task 2: IOStreams term interface + System() constructor

**Files:**
- Modify: `internal/iostreams/iostreams.go` — add term interface, term field, System(), delegating methods
- Modify: `internal/iostreams/iostreams_test.go` — add tests (create if doesn't exist)

**Step 1: Write the failing test**

Add to `internal/iostreams/iostreams_test.go`:

```go
func TestSystem_ReturnsIOStreams(t *testing.T) {
	ios := System()
	assert.NotNil(t, ios)
	assert.NotNil(t, ios.In)
	assert.NotNil(t, ios.Out)
	assert.NotNil(t, ios.ErrOut)
}

func TestSystem_TermCapabilities(t *testing.T) {
	// System() should wire term.FromEnv() internally
	ios := System()
	// These should not panic even if term is wired
	_ = ios.Is256ColorSupported()
	_ = ios.IsTrueColorSupported()
}

func TestNewTestIOStreams_TermNil(t *testing.T) {
	tio := NewTestIOStreams()
	// Test IOStreams has nil term — delegating methods return false
	assert.False(t, tio.Is256ColorSupported())
	assert.False(t, tio.IsTrueColorSupported())
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/iostreams/ -run "TestSystem|TestNewTestIOStreams_TermNil" -v`
Expected: FAIL — `System`, `Is256ColorSupported`, `IsTrueColorSupported` not defined

**Step 3: Write the implementation**

Modify `internal/iostreams/iostreams.go`:

1. Alias the `golang.org/x/term` import to `goterm`:
   ```go
   goterm "golang.org/x/term"
   ```
   Update all existing references from `term.IsTerminal` → `goterm.IsTerminal`, `term.GetSize` → `goterm.GetSize`.

2. Add the unexported interface and field:
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
   ```

3. Add `term term` field to `IOStreams` struct (after `ErrOut`).

4. Add `System()` constructor:
   ```go
   // System creates an IOStreams wired to the real system terminal.
   // Reads terminal capabilities from the host environment via term.FromEnv().
   // The factory calls this, then may layer clawker config overrides.
   func System() *IOStreams {
       ios := &IOStreams{
           In:           os.Stdin,
           Out:          os.Stdout,
           ErrOut:       os.Stderr,
           term:         termcap.FromEnv(),
           isInputTTY:   -1,
           isOutputTTY:  -1,
           isStderrTTY:  -1,
           colorEnabled: -1,
       }

       if ios.IsOutputTTY() && ios.IsStderrTTY() {
           ios.progressIndicatorEnabled = true
       }

       if os.Getenv("CLAWKER_SPINNER_DISABLED") != "" {
           ios.spinnerDisabled = true
       }

       return ios
   }
   ```

   Note: import `internal/term` aliased as `termcap` to avoid collision with the interface name:
   ```go
   termcap "github.com/schmitthub/clawker/internal/term"
   ```

5. Add delegating methods:
   ```go
   // Is256ColorSupported returns whether the host terminal supports 256 colors.
   func (s *IOStreams) Is256ColorSupported() bool {
       return s.term != nil && s.term.Is256ColorSupported()
   }

   // IsTrueColorSupported returns whether the host terminal supports 24-bit truecolor.
   func (s *IOStreams) IsTrueColorSupported() bool {
       return s.term != nil && s.term.IsTrueColorSupported()
   }
   ```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/iostreams/ -v`
Expected: PASS (all existing + new tests)

**Step 5: Commit**

```bash
git add internal/iostreams/iostreams.go internal/iostreams/iostreams_test.go
git commit -m "feat: add System() constructor and term capability delegation to IOStreams"
```

---

### Task 3: Factory wiring — ioStreams() calls System()

**Files:**
- Modify: `internal/cmd/factory/default.go`

**Step 1: Update ioStreams() to call System()**

Change the `ioStreams()` function. Note: the current function takes no args. The design calls for accepting `*cmdutil.Factory` for future config overrides, but `f.Config()` returns `*config.Config` (not an error), so the signature is slightly different from the design pseudocode. For now, since there are no config overrides, keep it simple:

```go
// ioStreams creates an IOStreams with TTY/color/CI detection.
func ioStreams() *iostreams.IOStreams {
	ios := iostreams.System()

	// Auto-detect color support
	if ios.IsOutputTTY() {
		ios.DetectTerminalTheme()
		// Respect NO_COLOR environment variable
		if os.Getenv("NO_COLOR") != "" {
			ios.SetColorEnabled(false)
		}
	} else {
		ios.SetColorEnabled(false)
	}

	// Respect CI environment (disable prompts)
	if os.Getenv("CI") != "" {
		ios.SetNeverPrompt(true)
	}

	return ios
}
```

The only change is `iostreams.NewIOStreams()` → `iostreams.System()`. The rest stays the same. The `NO_COLOR` and `CI` handling stays here in the factory because these are clawker-level policy decisions, not raw terminal detection.

**Step 2: Run all tests**

Run: `go test ./internal/cmd/factory/ -v && go test ./internal/iostreams/ -v && go test ./internal/term/ -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/cmd/factory/default.go
git commit -m "refactor: factory ioStreams() uses iostreams.System()"
```

---

### Task 4: RuntimeEnvOpts + refactored RuntimeEnv

**Files:**
- Modify: `internal/docker/env.go`
- Modify: `internal/docker/env_test.go`

**Step 1: Update the tests first**

Rewrite `internal/docker/env_test.go` to use `RuntimeEnvOpts`:

```go
package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeEnv_Defaults(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=nano")
	assert.Contains(t, env, "VISUAL=nano")
}

func TestRuntimeEnv_EditorOverride(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Editor: "vim",
		Visual: "code",
	})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=vim")
	assert.Contains(t, env, "VISUAL=code")
}

func TestRuntimeEnv_FirewallDomains(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallDomains: []string{"custom.com", "registry.npmjs.org"},
	})
	require.NoError(t, err)

	var found bool
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "CLAWKER_FIREWALL_DOMAINS="); ok {
			found = true
			assert.Contains(t, val, "custom.com")
			assert.Contains(t, val, "registry.npmjs.org")
		}
	}
	require.True(t, found, "expected CLAWKER_FIREWALL_DOMAINS env var")

	for _, e := range env {
		assert.NotEqual(t, "CLAWKER_FIREWALL_OVERRIDE=true", e)
	}
}

func TestRuntimeEnv_FirewallOverride(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled:  true,
		FirewallDomains:  []string{"only-this.com"},
		FirewallOverride: true,
	})
	require.NoError(t, err)

	assert.Contains(t, env, "CLAWKER_FIREWALL_OVERRIDE=true")
}

func TestRuntimeEnv_FirewallDisabled(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		FirewallEnabled: false,
	})
	require.NoError(t, err)

	for _, e := range env {
		assert.NotContains(t, e, "CLAWKER_FIREWALL_DOMAINS=")
	}
}

func TestRuntimeEnv_AgentEnv(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		AgentEnv: map[string]string{"FOO": "bar", "BAZ": "qux"},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "FOO=bar")
	assert.Contains(t, env, "BAZ=qux")
}

func TestRuntimeEnv_InstructionEnv(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		InstructionEnv: map[string]string{"NODE_ENV": "production"},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "NODE_ENV=production")
}

func TestRuntimeEnv_NilMaps(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=nano")
}

func TestRuntimeEnv_Deterministic(t *testing.T) {
	opts := RuntimeEnvOpts{
		Editor:          "vim",
		FirewallEnabled: true,
		FirewallDomains: []string{"example.com"},
		AgentEnv:        map[string]string{"A": "1", "B": "2"},
	}

	env1, err := RuntimeEnv(opts)
	require.NoError(t, err)
	env2, err := RuntimeEnv(opts)
	require.NoError(t, err)

	assert.Equal(t, env1, env2, "RuntimeEnv should produce consistent output")
}

func TestRuntimeEnv_Precedence(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		AgentEnv:       map[string]string{"EDITOR": "vim", "SHARED": "from-agent"},
		InstructionEnv: map[string]string{"SHARED": "from-instructions"},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "EDITOR=vim", "agent env should override base default")
	assert.Contains(t, env, "SHARED=from-instructions", "instruction env should override agent env")
}

func TestRuntimeEnv_256Color(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: true,
	})
	require.NoError(t, err)

	assert.Contains(t, env, "TERM=xterm-256color")
}

func TestRuntimeEnv_TrueColor(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: true,
		TrueColor:  true,
	})
	require.NoError(t, err)

	assert.Contains(t, env, "TERM=xterm-256color")
	assert.Contains(t, env, "COLORTERM=truecolor")
}

func TestRuntimeEnv_NoColorCapabilities(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: false,
		TrueColor:  false,
	})
	require.NoError(t, err)

	// Should not set TERM or COLORTERM when no capabilities detected
	for _, e := range env {
		assert.NotContains(t, e, "TERM=")
		assert.NotContains(t, e, "COLORTERM=")
	}
}

func TestRuntimeEnv_AgentEnvOverridesTerm(t *testing.T) {
	env, err := RuntimeEnv(RuntimeEnvOpts{
		Is256Color: true,
		TrueColor:  true,
		AgentEnv:   map[string]string{"TERM": "xterm", "COLORTERM": ""},
	})
	require.NoError(t, err)

	assert.Contains(t, env, "TERM=xterm", "agent env should override auto-detected TERM")
	assert.Contains(t, env, "COLORTERM=", "agent env should override auto-detected COLORTERM")
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/docker/ -run TestRuntimeEnv -v`
Expected: FAIL — `RuntimeEnvOpts` not defined, signature mismatch

**Step 3: Write the implementation**

Replace `RuntimeEnv` in `internal/docker/env.go`:

```go
// RuntimeEnvOpts defines the explicit values that RuntimeEnv can produce
// as container environment variables. Each field maps to a specific env var
// or group of env vars.
type RuntimeEnvOpts struct {
	// Editor preferences (defaults: nano)
	Editor string
	Visual string

	// Firewall configuration
	FirewallEnabled  bool
	FirewallDomains  []string
	FirewallOverride bool

	// Terminal capabilities (from host terminal detection)
	Is256Color bool
	TrueColor  bool

	// User-defined overrides (arbitrary pass-through)
	AgentEnv       map[string]string
	InstructionEnv map[string]string
}

// RuntimeEnv produces container environment variables from explicit options.
// Precedence: base defaults → terminal capabilities → agent env → instruction env.
func RuntimeEnv(opts RuntimeEnvOpts) ([]string, error) {
	m := make(map[string]string)

	// Base defaults: editor/visual
	editor := opts.Editor
	if editor == "" {
		editor = "nano"
	}
	visual := opts.Visual
	if visual == "" {
		visual = "nano"
	}
	m["EDITOR"] = editor
	m["VISUAL"] = visual

	// Firewall domains
	if opts.FirewallEnabled {
		jsonBytes, err := json.Marshal(opts.FirewallDomains)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal firewall domains: %w", err)
		}
		m["CLAWKER_FIREWALL_DOMAINS"] = string(jsonBytes)

		if opts.FirewallOverride {
			m["CLAWKER_FIREWALL_OVERRIDE"] = "true"
		}
	}

	// Terminal capabilities
	if opts.Is256Color {
		m["TERM"] = "xterm-256color"
	}
	if opts.TrueColor {
		m["COLORTERM"] = "truecolor"
	}

	// Agent env (overrides base defaults + terminal)
	for k, v := range opts.AgentEnv {
		m[k] = v
	}

	// Instruction env (highest precedence)
	for k, v := range opts.InstructionEnv {
		m[k] = v
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+m[k])
	}
	return env, nil
}
```

Remove the `"github.com/schmitthub/clawker/internal/config"` import from `env.go` if it's no longer needed (check if other functions in the file use it).

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/docker/ -run TestRuntimeEnv -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/docker/env.go internal/docker/env_test.go
git commit -m "refactor: RuntimeEnv takes RuntimeEnvOpts instead of *config.Project"
```

---

### Task 5: Update call sites (create.go + run.go)

**Files:**
- Modify: `internal/cmd/container/create/create.go:219`
- Modify: `internal/cmd/container/run/run.go:245`

**Step 1: Update create.go**

Replace at line 219:
```go
runtimeEnv, err := docker.RuntimeEnv(cfg)
```

With:
```go
runtimeEnv, err := docker.RuntimeEnv(docker.RuntimeEnvOpts{
    Editor:           cfg.Agent.Editor,
    Visual:           cfg.Agent.Visual,
    FirewallEnabled:  cfg.Security.FirewallEnabled(),
    FirewallDomains:  cfg.Security.Firewall.GetFirewallDomains(config.DefaultFirewallDomains),
    FirewallOverride: cfg.Security.Firewall.IsOverrideMode(),
    Is256Color:       opts.IOStreams.Is256ColorSupported(),
    TrueColor:        opts.IOStreams.IsTrueColorSupported(),
    AgentEnv:         cfg.Agent.Env,
    InstructionEnv:   instructionEnv(cfg),
})
```

Add a helper at the bottom of the file (or inline):
```go
func instructionEnv(cfg *config.Project) map[string]string {
    if cfg.Build.Instructions != nil {
        return cfg.Build.Instructions.Env
    }
    return nil
}
```

Or inline the nil check at the call site.

**Step 2: Update run.go**

Same change at line 245. The `opts.IOStreams` is available on `RunOptions`.

**Step 3: Build to verify compilation**

Run: `go build ./cmd/clawker`
Expected: SUCCESS

**Step 4: Run existing command tests**

Run: `go test ./internal/cmd/container/create/ -v && go test ./internal/cmd/container/run/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/cmd/container/create/create.go internal/cmd/container/run/run.go
git commit -m "refactor: create/run commands build RuntimeEnvOpts with terminal capabilities"
```

---

### Task 6: Dockerfile template — ENV defaults + zsh-in-docker -a flag

**Files:**
- Modify: `internal/bundler/assets/Dockerfile.tmpl`

**Step 1: Add ENV defaults**

After the existing ENV block (around line 257, after `ENV BROWSER=...`), add:

```dockerfile
ENV TERM=xterm-256color
ENV COLORTERM=truecolor
ENV LANG=en_US.UTF-8
```

**Step 2: Add -a flag to zsh-in-docker (Alpine block, ~line 276)**

Add before the `-x` line:
```
    -a "export TERM=xterm-256color" \
```

**Step 3: Add -a flag to zsh-in-docker (Debian block, ~line 285)**

Same addition before the `-x` line:
```
    -a "export TERM=xterm-256color" \
```

**Step 4: Verify template renders**

Run: `go test ./internal/bundler/ -v`
Expected: PASS (hash tests verify relative properties, not absolute values — they should still pass)

**Step 5: Commit**

```bash
git add internal/bundler/assets/Dockerfile.tmpl
git commit -m "feat: add TERM/COLORTERM/LANG defaults to Dockerfile template"
```

---

### Task 7: clawker.yaml — add nodejs + npm

**Files:**
- Modify: `clawker.yaml`

**Step 1: Add packages**

Add `nodejs` and `npm` to the `build.packages` list in `clawker.yaml`:

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

**Step 2: Commit**

```bash
git add clawker.yaml
git commit -m "fix: add nodejs/npm to container packages for Serena LSP servers"
```

---

### Task 8: Run full test suite + verify

**Step 1: Run all unit tests**

Run: `make test`
Expected: PASS

**Step 2: Build binary**

Run: `go build -o bin/clawker ./cmd/clawker`
Expected: SUCCESS

**Step 3: Verify design doc is accurate**

Update the design doc's "Files Changed" table if any files differ from the plan. Remove `start.go` and `attach.go` from the table (they don't call RuntimeEnv). Remove `hash_test.go` (no golden values to update).

**Step 4: Commit any fixups**

```bash
git add -A
git commit -m "chore: final cleanup and design doc updates"
```
