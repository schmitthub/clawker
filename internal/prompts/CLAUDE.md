# Prompts Package

Interactive user prompts with TTY and CI awareness. Access via `f.Prompter()` from Factory.

## Types

`Prompter` -- main prompt handler, wraps `*iostreams.IOStreams` for testable I/O
`NewPrompter(ios) *Prompter` -- constructor
`PromptConfig` -- configures string prompts: `Message`, `Default`, `Required`, `Validator func(string) error`
`SelectOption` -- selection item: `Label`, `Description`

## Prompter Methods

`String(cfg PromptConfig) (string, error)` -- prompt for string value with optional default/validation
`Confirm(message string, defaultYes bool) (bool, error)` -- y/N confirmation prompt
`Select(message string, options []SelectOption, defaultIdx int) (int, error)` -- numbered selection, returns index

## Standalone Functions

`PromptForConfirmation(in io.Reader, message string) bool` -- **Deprecated**: simple y/N prompt writing to os.Stderr. Use `Prompter.Confirm()` instead.

## Non-Interactive Behavior

In CI or non-TTY environments (checked via `ios.IsInteractive()`):
- `String`: returns default (or error if `Required` with no default)
- `Confirm`: returns the `defaultYes` value
- `Select`: returns `defaultIdx`

## Usage

```go
prompter := f.Prompter()  // prompts.NewPrompter(ios)

name, err := prompter.String(prompts.PromptConfig{
    Message:  "Project name",
    Default:  "my-project",
    Required: true,
})

proceed, err := prompter.Confirm("Continue?", false)

idx, err := prompter.Select("Choose base image", []prompts.SelectOption{
    {Label: "Debian Bookworm", Description: "Recommended"},
    {Label: "Alpine 3.22", Description: "Smaller image"},
}, 0)
```

## Testing

```go
ios := iostreams.NewTestIOStreams()
ios.SetInteractive(true)
ios.InBuf.SetInput("y\n")

prompter := prompts.NewPrompter(ios.IOStreams)
result, err := prompter.Confirm("Continue?", false)
// result == true, ios.ErrBuf.String() contains "Continue?"
```

## Gotchas

- Always use `f.Prompter()` for proper IOStreams configuration
- All prompts write to stderr (keeps stdout clean for data output)
- `Required: true` with no default fails in non-interactive mode
- `Select` with empty options returns `(-1, error)`
- `Select` clamps out-of-range `defaultIdx` to 0

## Tests

`prompts_test.go` -- unit tests for all prompt types and non-interactive fallback
