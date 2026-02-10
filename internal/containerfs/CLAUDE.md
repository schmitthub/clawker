# ContainerFS Package

Leaf package for preparing host Claude Code configuration for container injection.

## Key Functions

| Function | Purpose |
|----------|---------|
| `ResolveHostConfigDir() (string, error)` | Find host ~/.claude/ dir ($CLAUDE_CONFIG_DIR or default) |
| `PrepareClaudeConfig(hostConfigDir, containerHomeDir, containerWorkDir string) (stagingDir string, cleanup func(), err error)` | Stage host config for volume copy (settings, plugins, agents, etc.) |
| `PrepareCredentials(hostConfigDir string) (stagingDir string, cleanup func(), err error)` | Stage credentials from keyring or file fallback |
| `PrepareOnboardingTar(containerHomeDir string) (io.Reader, error)` | Create tar with ~/.claude.json onboarding marker |

## Dependencies

Imports: `internal/keyring`, `internal/logger`, stdlib only. No docker imports (leaf package).

## Copy Logic

- settings.json: Only `enabledPlugins` key extracted
- agents/, skills/, commands/: Full recursive copy, symlinks resolved
- plugins/: Full recursive copy including cache/, minus install-counts-cache.json
- known_marketplaces.json: `installPath` and `installLocation` values rewritten for container paths
- installed_plugins.json: `installPath` rewritten for container paths, `projectPath` replaced with `containerWorkDir`
- Missing files/dirs: logged and skipped (not errors)

## Path Rewriting

Uses `pathRewriteRule` and `rewriteJSONFile` to generalize host-to-container path rewriting:
- **Prefix swap** (`hostPrefix != ""`): replaces host prefix with container prefix (e.g., `installPath`, `installLocation`)
- **Full replacement** (`hostPrefix == ""`): replaces entire value (e.g., `projectPath` → `/workspace`)

## Staging Directory Structure

Each `Prepare*` function returns a temp directory with this layout:

### PrepareClaudeConfig
```
<tmpdir>/.claude/
  settings.json      (if existed on host)
  agents/            (if existed on host)
  skills/            (if existed on host)
  commands/          (if existed on host)
  plugins/           (if existed on host, including cache/)
```

### PrepareCredentials
```
<tmpdir>/.claude/
  .credentials.json
```

### PrepareOnboardingTar
Returns an `io.Reader` containing a tar archive with:
```
.claude.json         ({"hasCompletedOnboarding": true})
```

## Credential Resolution Order

1. OS keyring via `keyring.GetClaudeCodeCredentials()`
2. File fallback: `<hostConfigDir>/.credentials.json`
3. Error with actionable message if neither source available

## Testing

```bash
go test ./internal/containerfs/... -v
```

All tests use `t.TempDir()` for isolation and `keyring.MockInit()` for keyring tests.
