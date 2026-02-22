# Project Command Package

Project lifecycle management (initialization and registration).

## Files

| File | Purpose |
|------|---------|
| `project.go` | `NewCmdProject(f)` — parent command |
| `init/init.go` | `NewCmdProjectInit(f, runF)` — initialize project |
| `register/register.go` | `NewCmdProjectRegister(f, runF)` — register existing project |

## Subcommands

- `project init` — initialize new project in current directory (creates `.clawker.yaml` dotfile and `cfg.ClawkerIgnoreName()`). Uses `scaffoldProjectConfig()` based on `config.DefaultConfigYAML`. Optionally prompts to save as user-level default in configDir.
- `project register` — register existing project in user's registry (`cfg.ProjectRegistryFileName()`)

## Key Symbols

```go
func NewCmdProject(f *cmdutil.Factory) *cobra.Command

type ProjectInitOptions struct {
    IOStreams *iostreams.IOStreams
    Prompter  func() *prompter.Prompter
    Config    func() (config.Config, error)
    Name      string // positional arg
    Force     bool
    Yes       bool
}
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command

type RegisterOptions struct {
    IOStreams *iostreams.IOStreams
    Prompter  func() *prompter.Prompter
    Config    func() (config.Config, error)
    Name      string // positional arg
    Yes       bool
}
func NewCmdProjectRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command
```

`NewCmdProject` is the parent (no RunE). Both subcommands accept `runF` for test injection.

## Config Access Pattern

Both commands use `config.Config` interface. `project init` uses `config.DefaultConfigYAML` as scaffold template, `config.UserProjectConfigFilePath()` for user-level default, and `project.ProjectManager` for registry.
