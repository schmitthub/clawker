# Prompts Package

Interactive user prompts with TTY and CI awareness. Access via `f.Prompter()` from Factory.

## API

```go
prompter := f.Prompter()  // prompts.NewPrompter(ios)

// String prompt
name, err := prompter.String(prompts.PromptConfig{
    Message:   "Project name",
    Default:   "my-project",
    Required:  true,                    // Error if empty in non-interactive mode
    Validator: func(s string) error {}, // Optional validation
})

// Confirmation
proceed, err := prompter.Confirm("Continue?", false)  // [y/N]

// Selection
options := []prompts.SelectOption{
    {Label: "Debian Bookworm", Description: "Recommended"},
    {Label: "Alpine 3.22", Description: "Smaller image"},
}
idx, err := prompter.Select("Choose base image", options, 0)  // 0 = default
```

## Non-Interactive Behavior

In CI or non-TTY environments:
- `String`: returns default (or error if `Required` with no default)
- `Confirm`: returns the default value
- `Select`: returns `defaultIdx`

## Testing

```go
ios := iostreams.NewTestIOStreams()
ios.SetInteractive(true)
ios.InBuf.SetInput("y\n")

prompter := prompts.NewPrompter(ios.IOStreams)
result, err := prompter.Confirm("Continue?", false)
// result == true
// ios.ErrBuf.String() contains "Continue?"
```

## Gotchas

- Always use `f.Prompter()` for proper IOStreams configuration
- Prompts write to stderr (keeps stdout clean for data)
- `Required: true` with no default fails in non-interactive mode
- Legacy `PromptForConfirmation()` is deprecated; use `Prompter.Confirm()`
