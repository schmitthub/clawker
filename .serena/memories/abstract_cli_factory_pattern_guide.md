# Factory Pattern for CLI Applications — Architecture Guide

## Core Problem

A CLI app has dozens of commands. Each command needs access to shared infrastructure (HTTP clients, config, I/O, interactive prompts). You need a way to:

1. Wire real implementations in production
2. Substitute fakes in tests
3. Avoid circular imports
4. Keep commands focused on domain logic, not plumbing
5. Handle dependencies that can't be constructed until a command actually runs

## Architectural Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│  main.go                                                                │
│  func main() {                                                          │
│      f := factory.New(version)          ← one call, wires everything    │
│      root := root.NewCmdRoot(f)         ← passes factory to root cmd   │
│      root.Execute()                                                     │
│  }                                                                      │
└────────────────┬────────────────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  LAYER 1: factory/default.go              "The Assembler"               │
│                                                                         │
│  func New(version string) *cmdutil.Factory {                            │
│      f := &cmdutil.Factory{Version: version}                            │
│      f.Config      = configFunc()         // no deps                    │
│      f.IOStreams   = ioStreams(f)          // depends on Config          │
│      f.HttpClient  = httpClientFunc(f)    // depends on Config, IO      │
│      f.Prompter    = newPrompter(f)       // depends on Config, IO      │
│      f.Browser     = newBrowser(f)        // depends on Config, IO      │
│      return f                                                           │
│  }                                                                      │
│                                                                         │
│  IMPORTS: all infrastructure packages                                   │
│  IMPORTED BY: main only                                                 │
└────────────────┬────────────────────────────────────────────────────────┘
                 │ produces
                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  LAYER 2: cmdutil/                        "The Shared Toolkit"          │
│                                                                         │
│  ┌─ factory.go ────────────────────────────────────────────────────┐    │
│  │  type Factory struct {                                          │    │
│  │      Version    string                                          │    │
│  │      IOStreams  *iostreams.IOStreams   // concrete values        │    │
│  │      Browser    browser.Browser       //                        │    │
│  │      Prompter   prompter.Prompter     //                        │    │
│  │      Config     func() (Config, error)  // lazy closures        │    │
│  │      HttpClient func() (*http.Client, error)                    │    │
│  │  }                                                              │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│  ┌─ errors.go ─── Structured error types                           ┐    │
│  ├─ args.go ───── Argument validation helpers                      │    │
│  ├─ flags.go ──── Custom flag types                                │    │
│  ├─ json.go ───── Shared output formatting (JSON/template)         │    │
│  └─ auth.go ───── Auth gating for commands                         ┘    │
│                                                                         │
│  IMPORTS: command framework, interfaces only — NOTHING heavy            │
│  IMPORTED BY: every command, factory/default.go                         │
└────────────────┬────────────────────────────────────────────────────────┘
                 │ imported by
                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  LAYER 3: cmd/<group>/<verb>/             "The Commands"                │
│                                                                         │
│  ┌─ Options struct ────────────────────────────────────────────────┐    │
│  │  type VerbOptions struct {                                      │    │
│  │      IO         *iostreams.IOStreams    // from Factory         │    │
│  │      HttpClient func() (*http.Client, error)  // from Factory  │    │
│  │      Resolver   SomeService            // NOT from Factory      │    │
│  │      Limit      int                    // from flags            │    │
│  │  }                                                              │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│  ┌─ NewCmd constructor ───────────────────────────────────────────┐     │
│  │  func NewCmdVerb(f *cmdutil.Factory,                           │     │
│  │      runF func(*VerbOptions) error) *cobra.Command {           │     │
│  │                                                                │     │
│  │      opts := &VerbOptions{                                     │     │
│  │          IO:         f.IOStreams,                               │     │
│  │          HttpClient: f.HttpClient,                             │     │
│  │      }                                                         │     │
│  │      cmd := &cobra.Command{                                    │     │
│  │          RunE: func(cmd *cobra.Command, args []string) error { │     │
│  │              if runF != nil {                                   │     │
│  │                  return runF(opts)   // test trapdoor           │     │
│  │              }                                                 │     │
│  │              return verbRun(opts)    // real execution          │     │
│  │          },                                                    │     │
│  │      }                                                         │     │
│  │      // register flags onto cmd                                │     │
│  │      return cmd                                                │     │
│  │  }                                                             │     │
│  └────────────────────────────────────────────────────────────────┘     │
│  ┌─ Run function ─────────────────────────────────────────────────┐     │
│  │  func verbRun(opts *VerbOptions) error {                       │     │
│  │      if opts.Resolver == nil {                                 │     │
│  │          opts.Resolver = realimpl.New(...)  // nil-guard        │     │
│  │      }                                                         │     │
│  │      // business logic using only opts                         │     │
│  │  }                                                             │     │
│  └────────────────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────────┘
```

## Dependency Flow Constraint

```
factory/default.go ──imports──▶ cmdutil ◀──imports── cmd/*
       │                           ▲
       │ imports all infrastructure│ imports only lightweight
       │ packages                  │ types, interfaces, helpers
       ▼                           │
  Returns *cmdutil.Factory    Commands consume it
```

**The one rule**: `cmdutil` must never import infrastructure packages. If it did, every command would transitively pull in the entire dependency graph, and you'd risk circular imports.

## Component Specifications

### 1. Factory Struct (`cmdutil/factory.go`)

A plain struct with no constructor, no methods, no logic.

**Design rules**:
- All fields are public
- No field is required — zero value / nil is valid
- Infrastructure deps that are expensive or need config use **closure fields** (`func() (T, error)`) for lazy evaluation
- Cheap, stateless deps use **concrete fields**
- Never add a `NewFactory()` constructor — the struct literal *is* the API

**A field earns its place if it satisfies ALL of these**:

| Criterion | Rationale |
|---|---|
| Can be constructed at startup, before any command runs | Factory is built once in `main()` |
| Tests need to substitute it | Fakes, mocks, stubs |
| Multiple commands share it | Not command-specific |
| It has side effects or requires configuration | Varies between environments |

**Does NOT belong**: Dependencies that need runtime context (CLI args, resolved targets, user selections). Dependencies that never vary between prod and test. Dependencies used by only 1-2 commands.

### 2. Toolkit Helpers (`cmdutil/*.go`)

Stateless utility functions used by command constructors.

| File | Purpose |
|---|---|
| `errors.go` | Structured error types for the error-handling chain |
| `args.go` | Argument validation funcs |
| `flags.go` | Custom flag types beyond what the framework provides |
| `json.go` | Shared output formatting |
| `auth.go` | Auth gating via command annotations |

**Design rule**: These must only depend on the command framework, standard library, and lightweight interfaces.

### 3. Factory Assembler (`factory/default.go`)

A single `New()` function that constructs a fully-wired `*cmdutil.Factory`.

**Design rules**:
- Only called once, from `main()`
- Only place that imports all infrastructure packages
- Wires closures with captured references to `f` itself for inter-dependency
- Each helper function is private to this package

### 4. Command Package (`cmd/<group>/<verb>/`)

Each package contains exactly three things:

**The Options Struct** — cherry-picks dependencies from Factory, declares runtime-context dependencies, and holds parsed flags:

```go
type VerbOptions struct {
// From Factory (set in constructor)
IO         *iostreams.IOStreams
HttpClient func() (*http.Client, error)

// Needs runtime context (set in run function via nil-guard)
Resolver   SomeService

// From flags (set by command framework)
Limit      int
Query      string
}
```

**The Constructor** — extracts from Factory, defines flags, routes execution:

```go
func NewCmdVerb(f *cmdutil.Factory, runF func(*VerbOptions) error) *cobra.Command
```

**The Run Function** — unexported, receives only Options, never Factory:

```go
func verbRun(opts *VerbOptions) error
```

### 5. Root Command (`cmd/root/`)

Receives the Factory and distributes it to all sub-commands.

## Two Patterns for Dependency Injection

Not every shared dependency belongs in Factory. The deciding factor is **when it can be constructed**.

### Pattern A: Factory Field — "Known at Startup"

```
  main()
    │
    ▼
  factory.New()
    │  constructs deps that only need config or other Factory fields
    │
    ▼
  *cmdutil.Factory ───▶ passed to every command
```

Use when the dependency:
- Can be constructed before any command runs
- Only needs config or other Factory fields to build
- Is shared by many commands

```go
// cmdutil/factory.go — declare
type Factory struct {
    HttpClient func() (*http.Client, error)
}

// factory/default.go — wire once
f.HttpClient = httpClientFunc(f)

// cmd/<verb>/<verb>.go — extract into Options
opts := &VerbOptions{HttpClient: f.HttpClient}
```

### Pattern B: Options Field with Nil-Guard — "Needs Runtime Context"

```
  main()
    │
    ▼
  factory.New()                  ← can't build this dep yet,
    │                               don't know target/args/selections
    ▼
  command.RunE()
    │  parses flags, resolves target
    │
    ▼
  verbRun(opts)
    │  if opts.Resolver == nil {
    │      opts.Resolver = realimpl.New(client, target)
    │  }
    ▼
  uses opts.Resolver
```

Use when the dependency:
- Needs CLI args, resolved targets, or user selections to construct
- Depends on values that aren't known until the command runs

**Breadth of use does not determine the pattern.** A dependency used by 40 commands still uses Pattern B if it needs runtime context.

The nil-guard is the test injection point:

```go
// Production: Resolver is nil, constructed in run function with real context
// Test: set Resolver directly on Options, nil-guard is skipped
opts := &VerbOptions{
    Resolver: &mockResolver{},
}
```

### Decision Flowchart

```
  "Where does my dependency go?"
                │
                ▼
  Can it be constructed at startup,
  before any command runs?
  (Only needs config or other
  Factory fields?)
                │
         ┌──────┴──────┐
         │YES           │NO
         │              │ (needs CLI args, resolved target,
         │              │  user selection, runtime state)
         ▼              │
  Is it shared by       ▼
  3+ commands?        PATTERN B
         │            Options field + nil-guard
    ┌────┴────┐       in the run function
    │YES      │NO
    ▼         ▼
  PATTERN   Put on Options directly.
     A      Command imports the package itself.
  Factory   No Factory involvement.
   field
```

### Side-by-Side Comparison

```
                    PATTERN A                      PATTERN B
                    Factory Field                  Options Nil-Guard
                    ─────────────                  ─────────────────
  Declared in       cmdutil/factory.go             cmd/<verb>/<verb>.go
  Constructed in    factory/default.go             run function
  Constructed       once, at startup               per command execution
  Depends on        config, other Factory fields   CLI args, resolved targets
  Test injection    stub closure on Factory        set field on Options directly

  Production flow   factory.New() → closure        if opts.X == nil {
                    → stored on Factory                opts.X = real.New(...)
                    → extracted to Options          }

  Test flow         f := &cmdutil.Factory{          opts := &VerbOptions{
                        SomeDep: mockFn,                SomeDep: &mock{},
                    }                               }
```

## Testing Architecture

### Overview: Three Test Tiers

Command testing breaks into three tiers, each with a distinct purpose and setup cost:

```
┌────────────────────────────────────────────────────────────────────────────────────┐
│                            THREE TEST TIERS                                        │
├──────────────────┬────────────────────────────┬────────────────────────────────────┤
│  TIER 1           │  TIER 2                    │  TIER 3                            │
│  Flag Parsing     │  Integration               │  Internal Function                 │
│                   │  (Full Pipeline)            │  (Direct Unit Tests)               │
├──────────────────┼────────────────────────────┼────────────────────────────────────┤
│  runF trapdoor    │  Shared test helper         │  Call domain function directly     │
│  Intercepts opts  │  Builds Factory + executes  │  No Factory, no command framework  │
│  No run function  │  through cobra              │  Just inputs → outputs             │
│  No HTTP mocks    │  Full HTTP mock registry    │  Table-driven with mock registry   │
│                   │                             │                                    │
│  Tests that       │  Tests that                 │  Tests that                        │
│  flags → Options  │  flags + API → output       │  inputs → API calls → results      │
│  mapping works    │  works end-to-end           │  work correctly                    │
└──────────────────┴────────────────────────────┴────────────────────────────────────┘
```

---

### Tier 1: Flag Parsing Tests

Uses the `runF` trapdoor to intercept the Options struct *before* the run function executes. Verifies that CLI flags are correctly parsed and mapped onto Options fields.

```go
f := &cmdutil.Factory{
    IOStreams: ios,
}

var got *VerbOptions
cmd := NewCmdVerb(f, func(o *VerbOptions) error {
    got = o         // capture — never executes run function
    return nil
})
cmd.SetArgs([]string{"--limit", "5", "--state", "closed"})
cmd.Execute()

assert.Equal(t, 5, got.Limit)
assert.Equal(t, "closed", got.State)
```

**What this tests**: flag registration, default values, enum validation, mutual exclusion, required args.
**What this does NOT test**: API calls, output formatting, error handling in the run function.
**Factory needs**: minimal — often just IOStreams.

---

### Tier 2: Integration Tests (Full Pipeline)

Exercises the full command pipeline: flag parsing → run function → API calls → output.

#### The Shared Test Helper Pattern

Each command test file typically defines a private `runCommand` helper that encapsulates Factory construction and command execution:

```go
func runCommand(transport http.RoundTripper, isTTY bool, cli string) (*CmdOut, error) {
    ios, _, stdout, stderr := iostreams.Test()
    ios.SetStdoutTTY(isTTY)
    ios.SetStdinTTY(isTTY)
    ios.SetStderrTTY(isTTY)

    stubBrowser := &browser.Stub{}
    factory := &cmdutil.Factory{
        IOStreams: ios,
        Browser:  stubBrowser,
        HttpClient: func() (*http.Client, error) {
            return &http.Client{Transport: transport}, nil
        },
        BaseRepo: func() (Repo, error) {
            return NewRepo("OWNER", "REPO"), nil
        },
    }

    cmd := NewCmdVerb(factory, nil)    // nil runF → full execution

    argv, _ := shlex.Split(cli)
    cmd.SetArgs(argv)
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(io.Discard)
    cmd.SetErr(io.Discard)

    _, err := cmd.ExecuteC()
    return &CmdOut{
        OutBuf:     stdout,
        ErrBuf:     stderr,
        BrowsedURL: stubBrowser.BrowsedURL(),
    }, err
}
```

**Key design choices**:
- `nil` for `runF` → real run function executes
- I/O capture via `iostreams.Test()` — returns four values: streams, stdin buffer, stdout buffer, stderr buffer
- TTY toggling — tests both interactive and non-interactive paths
- Browser stub — captures URLs opened via `opts.Browser.Browse()`
- Cobra output discarded (`io.Discard`) — real output goes through `iostreams`
- Base repo hardcoded — tests control the repo context

Tests call this helper concisely:

```go
func TestVerb_filtering(t *testing.T) {
    reg := &httpmock.Registry{}
    defer reg.Verify(t)

    reg.Register(
        httpmock.GraphQL(`query ItemList\b`),
        httpmock.GraphQLQuery(`{...}`, func(_ string, params map[string]interface{}) {
            assert.Equal(t, []interface{}{"OPEN", "CLOSED"}, params["state"])
        }),
    )

    output, err := runCommand(reg, true, `-s all`)
    assert.NoError(t, err)
    assert.Contains(t, output.String(), "expected text")
}
```

#### The runF Hybrid Pattern

Sometimes integration tests use `runF` NOT to intercept, but to inject dependencies while still calling the real run function:

```go
cmd := NewCmdVerb(factory, func(opts *VerbOptions) error {
    opts.Now      = fakeNow        // inject controlled clock
    opts.Detector = mockDetector   // inject Pattern B dep
    return verbRun(opts)           // ← still calls the real function
})
```

This is used when the run function has Pattern B deps (nil-guard) that you want to control without going through the nil-guard's real construction path. The `runF` becomes a "dependency override hook" rather than a test interceptor.

---

### Tier 3: Internal Function Unit Tests

Tests domain logic functions directly — no Factory, no command framework, no flag parsing. Ideal for functions that do the heavy lifting (API queries, data transformation).

Uses **table-driven tests** with an HTTP stubs callback:

```go
func Test_verbLogic(t *testing.T) {
    type args struct {
        detector  Detector
        target    Repo
        filters   FilterOptions
        limit     int
    }
    tests := []struct {
        name      string
        args      args
        httpStubs func(*httpmock.Registry)
        wantErr   bool
    }{
        {
            name: "default",
            args: args{
                limit:   30,
                target:  NewRepo("OWNER", "REPO"),
                filters: FilterOptions{State: "open"},
            },
            httpStubs: func(reg *httpmock.Registry) {
                reg.Register(
                    httpmock.GraphQL(`query ItemList\b`),
                    httpmock.GraphQLQuery(`{"data": ...}`,
                        func(_ string, params map[string]interface{}) {
                            assert.Equal(t, float64(30), params["limit"])
                            assert.Equal(t, []interface{}{"OPEN"}, params["states"])
                        },
                    ),
                )
            },
        },
        // ... more cases
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            reg := &httpmock.Registry{}
            defer reg.Verify(t)
            if tt.httpStubs != nil {
                tt.httpStubs(reg)
            }
            client := &http.Client{Transport: reg}
            _, err := verbLogic(client, tt.args.detector, tt.args.target, tt.args.filters, tt.args.limit)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

**Key design choices**:
- `httpStubs` is a **callback field** on the test case — each case registers only the stubs it needs
- `defer reg.Verify(t)` — ensures every registered stub was consumed (catches over-stubbing)
- Tests call the internal function directly, not through cobra
- Pattern B deps (like `Detector`) are passed as test case args

---

### HTTP Mock Registry

The mock registry is the backbone of API testing. It provides a `http.RoundTripper` that matches registered request patterns and returns canned responses.

```
┌─────────────────────────────────────────────────────────────────────┐
│  Mock Registry Lifecycle                                            │
│                                                                     │
│  1. Create:    reg := &httpmock.Registry{}                          │
│  2. Register:  reg.Register(matcher, responder)                     │
│  3. Inject:    &http.Client{Transport: reg}                         │
│  4. Execute:   (command or function makes HTTP calls)               │
│  5. Verify:    defer reg.Verify(t)  ← MUST be called               │
│                                                                     │
│  Verify checks:                                                     │
│  - All registered stubs were consumed (no dead stubs)               │
│  - Fails test if any stub was never matched                         │
│                                                                     │
│  Matchers:                                                          │
│  - httpmock.GraphQL(`query Name\b`)      GraphQL operation name     │
│  - httpmock.REST("GET", "repos/...")     REST method + path         │
│                                                                     │
│  Responders:                                                        │
│  - httpmock.StringResponse(`{...}`)      Static JSON                │
│  - httpmock.GraphQLQuery(`{...}`, fn)    JSON + param assertion fn  │
│  - httpmock.StatusStringResponse(404,    Status code + body         │
│      `{"message":"not found"}`)                                     │
└─────────────────────────────────────────────────────────────────────┘
```

The `GraphQLQuery` responder is particularly powerful — it returns a canned response AND lets you assert on the query parameters in the same registration:

```go
reg.Register(
    httpmock.GraphQL(`query ItemList\b`),
    httpmock.GraphQLQuery(`{"data": {...}}`, func(_ string, params map[string]interface{}) {
        assert.Equal(t, "OWNER", params["owner"])
        assert.Equal(t, float64(30), params["limit"])  // JSON numbers are float64
    }),
)
```

---

### I/O Capture Harness

The I/O test harness replaces real terminal streams with captured buffers:

```go
ios, stdin, stdout, stderr := iostreams.Test()

// Control terminal behavior
ios.SetStdoutTTY(true)   // simulate interactive terminal
ios.SetStdinTTY(true)
ios.SetStderrTTY(true)

// After execution, read captured output
assert.Contains(t, stdout.String(), "expected output")
assert.Equal(t, "", stderr.String())
```

Always test both TTY and non-TTY paths — commands often produce different output (e.g., color, tables vs plain text, progress indicators).

---

### Browser Stub

For commands that open URLs in a browser:

```go
stubBrowser := &browser.Stub{}
factory := &cmdutil.Factory{
    Browser: stubBrowser,
}

// ... execute command with --web flag ...

assert.Equal(t, "https://example.com/expected", stubBrowser.BrowsedURL())
```

---

### Testing Pattern B Dependencies

Pattern B dependencies get focused tests — no Factory, no command framework, just the Options struct:

```go
func TestVerbRun_WithService(t *testing.T) {
    ios, _, stdout, _ := iostreams.Test()

    opts := &VerbOptions{
        IO:       ios,
        Resolver: &mockResolver{result: "expected"},
    }

    err := verbRun(opts)
    assert.NoError(t, err)
    assert.Contains(t, stdout.String(), "expected")
}
```

The nil-guard in the run function is never reached — the test pre-populates the field, so the real implementation is never constructed.

---

### Bespoke Factory Construction

Tests construct Factory struct literals with only the fields they need:

```go
// Tier 1 flag test: needs nothing (or just IO)
f := &cmdutil.Factory{}

// Tier 2 output test: needs IO only
f := &cmdutil.Factory{IOStreams: ios}

// Tier 2 API test: needs IO + HTTP
f := &cmdutil.Factory{
    IOStreams:   ios,
    HttpClient: func() (*http.Client, error) {
        return &http.Client{Transport: reg}, nil
    },
}

// Tier 2 repo-aware test: IO + HTTP + BaseRepo
f := &cmdutil.Factory{
    IOStreams:   ios,
    HttpClient: func() (*http.Client, error) {
        return &http.Client{Transport: reg}, nil
    },
    BaseRepo: func() (Repo, error) {
        return NewRepo("OWNER", "REPO"), nil
    },
}
```

No constructor to satisfy. No unused fakes to create. Tests pay for exactly what they use.

---

### Which Tier to Use When

```
┌───────────────────────────────────┬─────────┬─────────┬─────────┐
│  What you're testing              │ Tier 1  │ Tier 2  │ Tier 3  │
├───────────────────────────────────┼─────────┼─────────┼─────────┤
│  Flag default values              │   ✓     │         │         │
│  Flag enum validation             │   ✓     │         │         │
│  Mutual flag exclusion            │   ✓     │         │         │
│  Required arguments               │   ✓     │         │         │
│  TTY vs non-TTY output            │         │   ✓     │         │
│  API request parameters           │         │   ✓     │   ✓     │
│  Output formatting                │         │   ✓     │         │
│  Error messages to user           │         │   ✓     │         │
│  --web flag opens browser         │         │   ✓     │         │
│  Query construction logic         │         │         │   ✓     │
│  Data transformation              │         │         │   ✓     │
│  Feature detection branching      │         │   ✓     │   ✓     │
│  Edge cases in domain logic       │         │         │   ✓     │
└───────────────────────────────────┴─────────┴─────────┴─────────┘
```

### Test File Organization

Each command verb has a single co-located test file:

```
cmd/<group>/<verb>/
├── <verb>.go           # Options + NewCmd + run function
└── <verb>_test.go      # All three tiers in one file
```

Within the test file:
- **Top**: `runCommand` helper (if Tier 2 tests exist)
- **Middle**: Tier 1 + Tier 2 test functions (named `TestVerb_*`)
- **Bottom**: Tier 3 table-driven tests (named `Test_verbLogic`)

## Where Heavy Dependencies Live

Regardless of Pattern A or B, the **implementation** always lives in `internal/`:

```
internal/
├── <service>/                     # Heavy domain service
│   ├── <service>.go               # Interface + real implementation
│   └── <service>_mock.go          # Test mock
├── cmdutil/
│   └── factory.go                 # References service ONLY if Pattern A
└── cmd/
    └── factory/
        └── default.go             # Wires service ONLY if Pattern A
```

The package location is the same either way. The only difference is **who constructs it**:

```
Pattern A: factory/default.go calls service.New() → stores on Factory
Pattern B: cmd/<verb>/<verb>.go calls service.New() → stores on Options
```

## Domain Package Hierarchy

Domain packages in `internal/` form a **directed acyclic graph (DAG)** with three tiers. Cross-importing between domain packages is normal and expected — what matters is the direction of imports.

### The Three Tiers

```
┌─────────────────────────────────────────────────────────────────────────┐
│  LEAF PACKAGES              "Pure Utilities"                            │
│                                                                         │
│  Import: standard library only, no internal siblings                    │
│  Imported by: anyone                                                    │
│                                                                         │
│  Examples:                                                              │
│  - text/           String formatting, truncation, pluralization         │
│  - safepaths/      Path validation and safety                           │
│  - hostnames/      Host URL utilities and normalization                 │
│  - keyring/        Credential storage abstraction                       │
│  - interfaces/     Shared interface definitions (Config, etc.)          │
│  - browser/        Browser launcher interface                           │
└──────────────────────────────────┬──────────────────────────────────────┘
                                   │ imported by
                                   ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  MIDDLE PACKAGES            "Core Domain Services"                      │
│                                                                         │
│  Import: leaves only                                                    │
│  Imported by: commands, composites, the entry point                     │
│                                                                         │
│  Examples:                                                              │
│  - config/         → interfaces, keyring                                │
│  - repocontext/    → hostnames                                          │
│  - prompter/       → hostnames                                          │
│  - authflow/       → browser, hostnames                                 │
│  - featuredetect/  → interfaces                                         │
│  - tableprinter/   → text                                               │
│  - docs/           → text                                               │
│  - archive/        → safepaths                                          │
└──────────────────────────────────┬──────────────────────────────────────┘
                                   │ imported by
                                   ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  COMPOSITE PACKAGES         "Subsystems"                                │
│                                                                         │
│  Import: leaves + middles + own sub-packages                            │
│  Imported by: commands only                                             │
│                                                                         │
│  Examples:                                                              │
│  - remotedev/           → remotedev/api, remotedev/connection,          │
│                           remotedev/portfwd, remotedev/rpc, text        │
│  - remotedev/rpc/       → remotedev/portfwd, remotedev/rpc/ssh,        │
│                           remotedev/rpc/jupyter                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Import Direction Rules

```
  composite ──▶ middle ──▶ leaf
       │            │
       ╰───────────▶ leaf
```

Imports always flow **downward**. A package at a given tier may import packages at the same or lower tier, but **never** at a higher tier.

### What Is / Is Not an Anti-Pattern

```
  ✓  middle → leaf                Config imports Keyring
  ✓  composite → middle           Subsystem imports Config
  ✓  composite → leaf             Subsystem imports Text
  ✓  composite → own children     Subsystem imports Subsystem/API

  ✗  leaf → middle                Text must never import Config
  ✗  leaf → leaf                  Leaves should have zero internal imports
  ✗  middle ↔ middle (lateral)    Config must never import AuthFlow
                                  (unrelated middle packages)
  ✗  Any cycle                    A → B → A is always wrong
```

**Lateral imports between unrelated middle packages** are the most common violation. If two middle packages need to share behavior, extract the shared part into a leaf package.

### Where `cmdutil` Fits

`cmdutil` is a **middle package** that commands and the entry point import. Its helpers (error types, flag builders, output formatters) touch the command framework, so it sits above pure leaves.

However, if a utility in `cmdutil` is also needed by domain/data packages (outside of commands), that's a sign it should be **extracted into a leaf package**:

```
BEFORE (leaky):
  datamodel/result.go ──imports──▶ cmdutil  (for StructExportData)

AFTER (clean):
  datamodel/result.go ──imports──▶ export/  (standalone leaf utility)
  cmdutil/json.go     ──imports──▶ export/  (cmdutil uses it too)
```

**Rule**: If a helper touches the command framework (cobra, Factory), it stays in `cmdutil`. If it's a pure data utility, extract it into its own leaf package.

## Constraints Summary

| Rule | Rationale |
|---|---|
| `cmdutil` imports nothing heavy | Prevents circular deps, keeps compile fast |
| `factory/` is the only heavy importer for Pattern A deps | Single place to understand full wiring |
| Factory has no constructor | Tests construct with struct literals, only set what they need |
| Closure fields for expensive startup deps | Lazy init — a `version` command never creates an HTTP client |
| Options struct per command | Interface segregation — run function only sees what it needs |
| Nil-guard for runtime-context deps | Allows construction after flag parsing; allows test injection |
| `runF` parameter on every constructor | Separates flag-parsing tests from behavior tests |
| Run function is unexported | Only callable from its own package, enforces Options boundary |
| One file per command verb | Self-contained, co-located with tests |
| Breadth of use doesn't determine the pattern | Construction timing does |
| Domain packages form a DAG: leaf → middle → composite | Prevents cycles, keeps compile fast |
| No lateral imports between unrelated middle packages | Extract shared logic into a leaf instead |
| Pure data utilities don't belong in `cmdutil` | Avoids forcing domain packages to import command-layer code |

## File Tree Template

```
mycli/
├── main.go                           # factory.New() → root.NewCmdRoot(f) → Execute()
├── internal/
│   ├── cmdutil/
│   │   ├── factory.go                # Factory struct (plain, no logic)
│   │   ├── errors.go                 # Structured error types
│   │   ├── args.go                   # Arg validators
│   │   ├── flags.go                  # Custom flag types
│   │   └── json.go                   # Output formatting helpers
│   ├── cmd/
│   │   ├── factory/
│   │   │   └── default.go            # New() — wires real Factory
│   │   ├── root/
│   │   │   └── root.go               # NewCmdRoot(f) — registers all commands
│   │   └── <group>/
│   │       └── <verb>/
│   │           ├── <verb>.go          # Options + NewCmd + run func
│   │           └── <verb>_test.go     # Flag tests + behavior tests
│   ├── # ── Leaf packages (no internal imports) ──
│   ├── text/                          # String formatting utilities
│   ├── safepaths/                     # Path safety utilities
│   ├── hostnames/                     # Host URL normalization
│   ├── interfaces/                    # Shared interface definitions
│   ├── export/                        # Data export/serialization helpers
│   │
│   ├── # ── Middle packages (import leaves only) ──
│   ├── config/                        # Config system → interfaces, keyring
│   ├── iostreams/                     # Terminal I/O abstraction
│   ├── httpclient/                    # HTTP client with auth, headers
│   ├── tableprinter/                  # Table output → text
│   ├── <service>/                     # Domain service (Pattern A or B)
│   │   ├── <service>.go              # Interface + implementation
│   │   └── <service>_mock.go         # Test mock
│   │
│   ├── # ── Composite packages (import leaves + middles + own children) ──
│   └── <subsystem>/                   # Self-contained subsystem
│       ├── <subsystem>.go             # Orchestrator
│       ├── api/                       # Subsystem API client
│       ├── connection/                # Subsystem connection handling
│       └── rpc/                       # Subsystem RPC layer
└── api/                               # API client layer
```
