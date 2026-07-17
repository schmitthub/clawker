# ContainerFS Package

Harness config staging: interprets the selected harness bundle's staging manifest (`config.Staging`) to prepare host state for container injection. Executes the manifest's explicit `staging.copy` directives (glob-capable src, JSON key allowlist, per-file skips, JSON path rewrites) into a temp staging mirror that callers copy into the harness config volume. Only host state OUTSIDE the workspace is staged — the workspace arrives via mount. Credentials are never copied from the host: the user authenticates inside the container and the token family persists in the config volume.

Leaf package: imports `internal/config`, `internal/consts`, `internal/logger`, `doublestar` (globbing), and stdlib. No docker imports.

## Key Functions

| Function | Purpose |
|----------|---------|
| `ResolveHostMountSource(src string) (string, bool, error)` | Expand a manifest `staging.mounts` src (`~`, `$VAR`, `${VAR:-fallback}` via `config.ExpandHostPath`) and stat it. Returns `("", false, nil)` when the dir is absent (caller soft-skips the bind); expansion errors, stat errors, and path-is-file come back as errors. Symlinks resolve via `os.Stat`. Never creates the dir. |
| `PrepareConfig(log *logger.Logger, staging config.Staging, containerHomeDir, containerWorkDir, hostProjectRoot string) (stagingDir string, cleanup func(), err error)` | Run every `staging.copy` directive into a temp staging mirror for volume copy. |
| `PrepareHookTar(cfg config.Config, shell, script, name string) (io.Reader, error)` | Create tar with `.clawker/<name>.sh` (shell shebang + `set -e` + user script, mode 0755); extracts at the container home. Empty script → bare no-op wrapper (lets callers always-deliver, overwriting stale content). Tar headers carry `cfg.ContainerUID()`/`cfg.ContainerGID()`. |

## Copy Directive Semantics (`stageCopy`)

Each `config.CopySpec` is one explicit host→container copy:

- **Src expansion**: `~`, `$VAR`, `${VAR:-fallback}`, then glob fan-out (`doublestar`) when the pattern has glob meta; literal paths stat. No matches = debug-logged soft skip (not an error).
- **Workspace guard**: any match inside `hostProjectRoot` is rejected — the workspace is mounted, never staged.
- **Dest placement**: dest is container-home-relative and must fall under a bundle-declared volume. A glob, multi-match, or trailing-slash dest lands each match UNDER dest; a single literal src copies TO dest exactly.
- **`json_keys`**: allowlist — only the listed top-level keys are extracted from the src JSON (e.g. the claude bundle stages only `enabledPlugins` from `settings.json`).
- **`skip`**: per-file skip list applied during directory copies (e.g. `install-counts-cache.json`).
- **`json_rewrites`**: `{file, key, rewrite}` rules applied to named JSON files in a copied tree. Rewrite tokens: `prefix-swap` (host prefix → container prefix, e.g. `installPath`) and `replace-with-workdir` (entire value → `containerWorkDir`, e.g. `projectPath`).
- Directories copy recursively with symlinks resolved; broken symlinks are warn-logged and skipped.

## Testing

```bash
go test ./internal/containerfs/... -v
```

Tests use `t.TempDir()` for isolation and `configmocks.NewBlankConfig()` for Config interface stubs (`import configmocks "github.com/schmitthub/clawker/internal/config/mocks"`).
