# Contributing to Clawker

> **LLM Memory Document**: Development guidelines for adding features and maintaining documentation.

## Adding a New CLI Command

1. **Create the command file**: `internal/cmd/<cmdname>/<cmdname>.go`

2. **Define options struct and constructor**:

   ```go
   type Options struct {
       Force bool
   }

   func NewCmd<Name>(f *cmdutil.Factory) *cobra.Command {
       opts := &Options{}

       cmd := &cobra.Command{
           Use:   "mycommand",
           Short: "One-line description",
           Long: `Detailed description.`,
           Example: `  # Basic usage
     clawker mycommand

     # With flags
     clawker mycommand --force`,
           RunE: func(cmd *cobra.Command, args []string) error {
               return runMyCommand(f, opts)
           },
       }

       cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force the operation")
       return cmd
   }
   ```

3. **Use cmdutil output functions**:

   ```go
   // Configuration not found
   if config.IsConfigNotFound(err) {
       cmdutil.PrintError("No clawker.yaml found in current directory")
       cmdutil.PrintNextSteps(
           "Run 'clawker init' to create a configuration",
           "Or change to a directory with clawker.yaml",
       )
       return err
   }

   // Docker errors (rich formatting)
   if err != nil {
       cmdutil.HandleError(err)
       return err
   }
   ```

4. **Register in `internal/cmd/root/root.go`**

5. **Update documentation**:
   - Add to `.claude/docs/CLI-VERBS.md`
   - Update `README.md` CLI Commands table

## Modifying Dockerfile Generation

1. **Edit template**: `pkg/build/templates/Dockerfile.tmpl`

2. **Update context struct**: `pkg/build/dockerfile.go`
   - `DockerfileContext` for base images
   - `ProjectGenerator.buildContext` for project builds

3. **Add new config fields**: `internal/config/schema.go`

4. **Add validation**: `internal/config/validator.go`

## Adding New Build Instructions

1. **Add type**: `internal/config/schema.go`

   ```go
   type NewInstruction struct {
       Name    string `yaml:"name"`
       Default string `yaml:"default,omitempty"`
   }
   ```

2. **Add field**: `DockerfileInstructions` in `pkg/build/dockerfile.go`

3. **Add template logic**: `pkg/build/templates/Dockerfile.tmpl`
   Insert at appropriate injection point.

4. **Add validation**: `internal/config/validator.go`

5. **Add tests**: `generator_test.go` and `validator_test.go`

---

## Updating README.md

**CRITICAL: Keep README.md synchronized with code changes.**

### When to Update

1. **New CLI commands or flags** - Update CLI Commands table and add usage examples
2. **Configuration changes** - Update the `clawker.yaml` example and field descriptions
3. **New features** - Add to appropriate section (Quick Start, Workspace Modes, Security, etc.)
4. **Authentication changes** - Update Authentication section with new env vars or methods
5. **Behavior changes** - Update affected sections to reflect new behavior
6. **Security defaults** - Update Security section if defaults change

### Writing Guidelines

- **User-first language** - Write for new users, not developers
- **Complete examples** - Show full commands with common flags
- **Concise descriptions** - One sentence per feature when possible
- **Practical use cases** - Explain WHEN to use a feature, not just HOW
- **Tables for reference** - Use tables for commands, flags, and env vars
- **No implementation details** - Avoid internals like package names or function calls

### README Structure

1. Quick Start - Get users running in 5 minutes
2. Authentication - How to pass API keys
3. CLI Commands - Reference table + detailed usage
4. Configuration - Full `clawker.yaml` spec with comments
5. Workspace Modes - bind vs snapshot explained
6. Security - Defaults and opt-in dangerous features
7. Ignore Patterns - `.clawkerignore` behavior
8. Development - Build instructions for contributors

---

## Updating CLAUDE.md

**CRITICAL: Keep CLAUDE.md current with architectural and implementation changes.**

### When to Update

1. **New packages or modules** - Update Repository Structure with purpose
2. **Architectural changes** - Update Key Concepts table
3. **New abstractions or interfaces** - Add to `ARCHITECTURE.md`
4. **Build/test commands** - Update Build Commands section
5. **Important behaviors** - Add to Important Gotchas if non-obvious
6. **Design decisions** - Document reasoning in Design Decisions
7. **Directory structure changes** - Update Repository Structure tree
8. **Common task patterns** - Add to `CONTRIBUTING.md`

### Writing Guidelines

- **Developer-focused** - Assume reader knows Go and Docker
- **Implementation details** - Include package names, interfaces, key types
- **Architectural reasoning** - Explain WHY, not just WHAT
- **Code patterns** - Show idioms and conventions used in the codebase
- **Gotchas and pitfalls** - Document non-obvious behaviors that cause bugs
- **Keep structure updated** - Repository Structure must match actual layout

---

## New Command Checklist

Before adding a new command, verify:

- [ ] Has `Example` field with 2+ examples
- [ ] Uses `PersistentPreRunE` (not `PersistentPreRun`)
- [ ] Routes status messages to stderr (`fmt.Fprintf(os.Stderr, ...)`)
- [ ] Uses `cmdutil.HandleError(err)` for Docker errors
- [ ] Uses `cmdutil.PrintNextSteps()` for guidance
- [ ] Registered in `internal/cmd/root/root.go`
- [ ] Updates `README.md` with user-facing docs
- [ ] Updates `.claude/docs/CLI-VERBS.md`
- [ ] Uses standard flag names from CLI-VERBS.md Flag Conventions
- [ ] Validates input early (before state changes)
- [ ] Has tests in `*_test.go` file
- [ ] Handles Ctrl+C gracefully (`term.SetupSignalContext`)
