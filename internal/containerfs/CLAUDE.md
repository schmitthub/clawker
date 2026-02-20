# ContainerFS Package

Prepares host Claude Code configuration for container injection. Receives `config.Config` interface for `ContainerUID()`/`ContainerGID()` methods.

## Key Functions

| Function | Purpose |
|----------|---------|
| `ResolveHostConfigDir() (string, error)` | Find host ~/.claude/ dir ($CLAUDE_CONFIG_DIR or default) |
| `PrepareClaudeConfig(hostConfigDir, containerHomeDir, containerWorkDir string) (stagingDir string, cleanup func(), err error)` | Stage host config for volume copy (settings, plugins, agents, etc.) |
| `PrepareCredentials(hostConfigDir string) (stagingDir string, cleanup func(), err error)` | Stage credentials from keyring or file fallback |
| `PrepareOnboardingTar(cfg config.Config, containerHomeDir string) (io.Reader, error)` | Create tar with ~/.claude.json onboarding marker |
| `PreparePostInitTar(cfg config.Config, script string) (io.Reader, error)` | Create tar with .clawker/post-init.sh (bash shebang + set -e + user script); extracts at /home/claude |

## Dependencies

Imports: `internal/config`, `internal/keyring`, `internal/logger`, stdlib only. No docker imports.

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

### PreparePostInitTar
Returns an `io.Reader` containing a tar archive with:
```
.clawker/post-init.sh   (#!/bin/bash + set -e + user script, mode 0755)
```

## Credential Resolution Order

1. OS keyring via `keyring.GetClaudeCodeCredentials()`
2. File fallback: `<hostConfigDir>/.credentials.json`
3. Error with actionable message if neither source available

## Testing

```bash
go test ./internal/containerfs/... -v
```

All tests use `t.TempDir()` for isolation, `keyring.MockInit()` for keyring tests, and `config.NewBlankConfig()` for Config interface stubs.
