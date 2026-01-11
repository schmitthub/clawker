# CLI Best Practices (Cobra/Viper)

## Command Structure

Every command should have:
1. `Use` - Command name and args pattern
2. `Short` - One-line description
3. `Long` - Detailed description (optional)
4. `Example` - Formatted usage examples (REQUIRED)
5. `RunE` - Error-returning run function

## Example Field Format

```go
Example: `  # Basic usage
  claucker <cmd>

  # With flags
  claucker <cmd> --flag value`,
```

Note: Indent examples with 2 spaces for proper help formatting.

## Output Routing

- **stderr**: All status messages, progress, errors, warnings
  - `fmt.Fprintln(os.Stderr, "Starting...")`
  - `cmdutil.PrintError(...)`, `cmdutil.PrintWarning(...)`
  - `cmdutil.PrintNextSteps(...)`

- **stdout**: Only structured data output for scripting
  - `ls` command table output
  - JSON/YAML data output

## Error Handling

1. Use `PersistentPreRunE` not `PersistentPreRun`
2. Never use `logger.Fatal()` or `os.Exit()` in Cobra hooks
3. Return errors properly - Cobra will format them
4. Use `cmdutil.HandleError(err)` for DockerError rich formatting

## Flag Validation

```go
// After defining flags:
cmd.MarkFlagsOneRequired("name", "project")    // At least one
cmd.MarkFlagsMutuallyExclusive("bind", "snapshot")  // Not both
cmd.MarkFlagRequired("config")                 // Always required
```

## Common Patterns Fixed

1. **root.go**: Changed `PersistentPreRun` → `PersistentPreRunE`
2. **ls.go**: Changed manual error formatting → `cmdutil.HandleError()`
3. **rm.go**: Added `MarkFlagsOneRequired("name", "project")`
4. **All commands**: Added `Example` field
5. **All commands**: Changed `fmt.Printf` → `fmt.Fprintf(os.Stderr, ...)`
