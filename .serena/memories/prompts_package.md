# Prompts Package (`internal/prompts/`)

## Status: COMPLETE

## Overview

The Prompts package provides interactive prompting functionality for CLI commands. It uses IOStreams for testable I/O and respects TTY detection and CI environments.

This package was extracted from `internal/cmdutil/` to separate user interaction concerns from CLI utilities.

## When to Use This Package

- **Interactive prompts** - String input, confirmations, selections
- **Non-interactive fallbacks** - Graceful default values when not TTY
- **CI-aware behavior** - Automatic default selection in CI environments

## Package Files

| File | Purpose |
|------|---------|
| `prompts.go` | Prompter struct with String, Confirm, Select methods |

## Core Types

### Prompter

```go
type Prompter struct {
    ios *iostreams.IOStreams
}

// Create via Factory
prompter := f.Prompter()

// Or directly
prompter := prompts.NewPrompter(ios)
```

### PromptConfig

Configuration for string prompts:

```go
type PromptConfig struct {
    Message   string            // Prompt message
    Default   string            // Default value
    Required  bool              // Fail if empty in non-interactive mode
    Validator func(string) error // Optional validation
}
```

### SelectOption

Options for selection prompts:

```go
type SelectOption struct {
    Label       string // Display text
    Description string // Optional description
}
```

## Core Methods

### String Prompt

Prompt for text input with optional default and validation:

```go
prompter := f.Prompter()

// Simple prompt
name, err := prompter.String(prompts.PromptConfig{
    Message: "Project name",
    Default: "my-project",
})

// Required with validation
name, err := prompter.String(prompts.PromptConfig{
    Message:  "Project name",
    Required: true,
    Validator: func(s string) error {
        if !isValidProjectName(s) {
            return fmt.Errorf("invalid project name")
        }
        return nil
    },
})
```

**Behavior:**
- Interactive: Shows prompt, returns user input or default if empty
- Non-interactive: Returns default (or error if Required and no default)

### Confirm Prompt

Prompt for yes/no confirmation:

```go
prompter := f.Prompter()

// Default to "no"
proceed, err := prompter.Confirm("Continue?", false)  // Shows [y/N]

// Default to "yes"
proceed, err := prompter.Confirm("Continue?", true)   // Shows [Y/n]
```

**Behavior:**
- Interactive: Shows prompt, returns user's choice
- Non-interactive: Returns the default value

### Select Prompt

Prompt user to select from options:

```go
prompter := f.Prompter()

options := []prompts.SelectOption{
    {Label: "Debian Bookworm", Description: "Recommended"},
    {Label: "Alpine 3.22", Description: "Smaller image"},
    {Label: "Custom", Description: "Provide Dockerfile"},
}

idx, err := prompter.Select("Choose base image", options, 0)  // 0 = default selection
if err != nil {
    return err
}
selectedOption := options[idx]
```

**Behavior:**
- Interactive: Shows numbered list with `>` marking default, returns selected index
- Non-interactive: Returns defaultIdx

## Factory Integration

Access Prompter through the Factory:

```go
func runMyCommand(f *cmdutil.Factory, opts *Options) error {
    prompter := f.Prompter()
    
    // Interactive prompting
    if proceed, _ := prompter.Confirm("Continue?", false); !proceed {
        return nil
    }
    
    name, err := prompter.String(prompts.PromptConfig{
        Message: "Project name",
        Default: "my-project",
    })
    if err != nil {
        return err
    }
    
    // ... use name
}
```

## Testing

Use test IOStreams for unit testing prompts:

```go
import (
    "github.com/schmitthub/clawker/internal/iostreams"
    "github.com/schmitthub/clawker/internal/prompts"
)

func TestPromptConfirm(t *testing.T) {
    ios := iostreams.NewTestIOStreams()
    ios.SetInteractive(true)  // Simulate TTY
    ios.InBuf.SetInput("y\n") // Simulate user typing "y"
    
    prompter := prompts.NewPrompter(ios.IOStreams)
    result, err := prompter.Confirm("Continue?", false)
    
    require.NoError(t, err)
    require.True(t, result)
    require.Contains(t, ios.ErrBuf.String(), "Continue?")
}

func TestPromptString_NonInteractive(t *testing.T) {
    ios := iostreams.NewTestIOStreams()
    // Default is non-interactive
    
    prompter := prompts.NewPrompter(ios.IOStreams)
    result, err := prompter.String(prompts.PromptConfig{
        Message: "Name",
        Default: "default-value",
    })
    
    require.NoError(t, err)
    require.Equal(t, "default-value", result)
    // No prompt shown to stderr in non-interactive mode
    require.Empty(t, ios.ErrBuf.String())
}
```

## Legacy Function

A deprecated helper exists for backwards compatibility:

```go
// Deprecated: Use Prompter.Confirm() instead
func PromptForConfirmation(in io.Reader, message string) bool
```

Prefer `Prompter.Confirm()` for new code.

## Environment Variables

| Variable | Effect |
|----------|--------|
| `CI` | When set, IOStreams returns false for `IsInteractive()`, causing prompts to use defaults |

## Common Gotchas

1. **Always use Factory.Prompter()** - Ensures IOStreams is properly configured
2. **Check TTY before prompting** - Use `ios.IsInteractive()` or `ios.CanPrompt()` if you need manual checks
3. **Provide sensible defaults** - Non-interactive mode uses defaults silently
4. **Required prompts fail in CI** - If `Required: true` with no default, non-interactive mode returns error
5. **Prompts write to stderr** - Keeps stdout clean for data output
