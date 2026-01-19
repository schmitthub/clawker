---
paths:
  - "**/*.go"
---

# CLI Testing Guide

> **LLM Memory Document**: Reference this document when writing CLI command tests. Contains both automated integration tests and manual test patterns.

## CRITICAL: All Tests Must Pass

**No code change is complete until ALL tests pass.** This is non-negotiable.

## 11. Testing Strategy

### 11.1 Integration Regression Tests

Tests run against real Docker—no mocking:

- Ensures actual Docker behavior
- Catches API compatibility issues
- Validates label filtering works correctly

**Before completing any code change:**

1. Run `go test ./...` - all unit tests must pass
2. Run `go test ./pkg/cmd/...` - all integration tests must pass

### 11.2 Table-Driven Tests

Single test functions with case tables:

**engine test**

```go
func TestContainerOperations(t *testing.T) {
    test := []struct {
        name    string
        setup   func()
        action  func() error
        verify  func() error
    }{
        {"create and start", ...},
        {"stop running", ...},
        {"remove stopped", ...},
    }
    for _, tt := range test {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

**cli command test**

```go
func TestNewCmdToken(t *testing.T) {
 tests := []struct {
  name       string
  input      string
  output     TokenOptions
  wantErr    bool
  wantErrMsg string
 }{
  {
   name:   "no flags",
   input:  "",
   output: TokenOptions{},
  },
  {
   name:   "with hostname",
   input:  "--hostname github.mycompany.com",
   output: TokenOptions{Hostname: "github.mycompany.com"},
  },
  {
   name:   "with user",
   input:  "--user test-user",
   output: TokenOptions{Username: "test-user"},
  },
  {
   name:   "with shorthand user",
   input:  "-u test-user",
   output: TokenOptions{Username: "test-user"},
  },
  {
   name:   "with shorthand hostname",
   input:  "-h github.mycompany.com",
   output: TokenOptions{Hostname: "github.mycompany.com"},
  },
  {
   name:   "with secure-storage",
   input:  "--secure-storage",
   output: TokenOptions{SecureStorage: true},
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   ios, _, _, _ := iostreams.Test()
   f := &cmdutil.Factory{
    IOStreams: ios,
    Config: func() (gh.Config, error) {
     cfg := config.NewBlankConfig()
     return cfg, nil
    },
   }
   argv, err := shlex.Split(tt.input)
   require.NoError(t, err)

   var cmdOpts *TokenOptions
   cmd := NewCmdToken(f, func(opts *TokenOptions) error {
    cmdOpts = opts
    return nil
   })
   // TODO cobra hack-around
   cmd.Flags().BoolP("help", "x", false, "")

   cmd.SetArgs(argv)
   cmd.SetIn(&bytes.Buffer{})
   cmd.SetOut(&bytes.Buffer{})
   cmd.SetErr(&bytes.Buffer{})

   _, err = cmd.ExecuteC()
   if tt.wantErr {
    require.Error(t, err)
    require.EqualError(t, err, tt.wantErrMsg)
    return
   }

   require.NoError(t, err)
   require.Equal(t, tt.output.Hostname, cmdOpts.Hostname)
   require.Equal(t, tt.output.SecureStorage, cmdOpts.SecureStorage)
  })
 }
}
```

### 11.3 Manual Testing

**test requiring builds**
- Builds (ie: `go build ...`) always go in the project root's binary directory `./bin/`, never in the project root, to avoid polluting the working directory.
