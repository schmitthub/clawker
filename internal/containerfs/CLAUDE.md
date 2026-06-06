# ContainerFS Package

Prepares host Claude Code configuration for container injection. Receives `config.Config` interface for `ContainerUID()`/`ContainerGID()` methods.

## Key Functions

| Function | Purpose |
|----------|---------|
| `ResolveHostConfigDir() (string, error)` | Find host ~/.claude/ dir ($CLAUDE_CONFIG_DIR or default). Relative `$CLAUDE_CONFIG_DIR` is resolved to absolute via `filepath.Abs` (multi-account workflows set it relative to CWD). |
| `ResolveHostProjectsDir() (string, bool, error)` | Resolve `<hostConfigDir>/projects` for the bind-mount path; returns `("", false, nil)` only when the projects dir is absent. Stat errors (EACCES, ELOOP, path-is-file) and propagated `ResolveHostConfigDir` errors come back as `(_, false, err)` with the path included. Symlinks resolve via `os.Stat`. Never creates the dir. |
| `PrepareClaudeConfig(hostConfigDir, containerHomeDir, containerWorkDir string) (stagingDir string, cleanup func(), err error)` | Stage host config for volume copy (settings, plugins, agents, etc.) |
| `PrepareCredentials(hostConfigDir string) (stagingDir string, cleanup func(), err error)` | Stage credentials from keyring or file fallback |
| `PrepareHookTar(cfg config.Config, script, name string) (io.Reader, error)` | Create tar with .clawker/<name>.sh (bash shebang + set -e + user script); extracts at /home/claude. Empty script → bare no-op wrapper (lets callers always-deliver, overwriting stale content) |

## Dependencies

Imports: `internal/config`, `internal/keyring`, `internal/logger`, stdlib only. No docker imports.

## Copy Logic

- settings.json: Only `enabledPlugins` key extracted
- CLAUDE.md: Direct copy if present (user-level instructions)
- agents/, skills/, commands/: Full recursive copy, symlinks resolved
- plugins/: Full recursive copy including cache/, minus install-counts-cache.json
- known_marketplaces.json: `installPath` and `installLocation` values rewritten for container paths
- installed_plugins.json: `installPath` rewritten for container paths, `projectPath` replaced with `containerWorkDir`
- Missing files/dirs: logged and skipped (not errors)
- projects/: NOT staged here — handled separately as a live bind mount in `internal/workspace` (see `GetClaudeProjectsMount`). Bind mount overlays the per-agent config volume on top of `/home/claude/.claude/projects` so auto-memory and session jsonls are shared across container runs.

## Path Rewriting

Uses `pathRewriteRule` and `rewriteJSONFile` to generalize host-to-container path rewriting:
- **Prefix swap** (`hostPrefix != ""`): replaces host prefix with container prefix (e.g., `installPath`, `installLocation`)
- **Full replacement** (`hostPrefix == ""`): replaces entire value (e.g., `projectPath` → container work dir)

## Staging Directory Structure

Each `Prepare*` function returns a temp directory with this layout:

### PrepareClaudeConfig
```
<tmpdir>/.claude/
  settings.json      (if existed on host)
  CLAUDE.md          (if existed on host)
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

## Credential Resolution Order

1. OS keyring via `keyring.GetClaudeCodeCredentials()`
2. File fallback: `<hostConfigDir>/.credentials.json`
3. Error with actionable message if neither source available

## Testing

```bash
go test ./internal/containerfs/... -v
```

All tests use `t.TempDir()` for isolation, `keyring.MockInit()` for keyring tests, and `configmocks.NewBlankConfig()` for Config interface stubs (`import configmocks "github.com/schmitthub/clawker/internal/config/mocks"`).
