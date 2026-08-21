# Version Command Package

Print CLI version and build date.

## Files

| File | Purpose |
|------|---------|
| `version.go` | `NewCmdVersion(f, version, buildDate)` — version subcommand; `Format(version, buildDate)` — display string |

## Key Symbols

```go
func NewCmdVersion(f *cmdutil.Factory, version, buildDate string) *cobra.Command
func Format(version, buildDate string) string
```

`NewCmdVersion` reads formatted version from `cmd.Root().Annotations["versionInfo"]` (set by root command). `Format` strips `v` prefix and appends date in parentheses when non-empty.

## Testing

`version_test.go` covers `Format` with version-only, version+date, and dev cases. No Docker required.
