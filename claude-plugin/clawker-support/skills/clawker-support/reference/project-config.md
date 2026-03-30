# Project Config ((.)clawker.yaml)

Project config controls everything about a clawker agent container: build
instructions, agent behavior, firewall rules, workspace mode, and security
settings. It uses walk-up file discovery with multi-layer merge.

## Discovery

Clawker discovers project config by walking up from CWD to the project root,
probing each directory for config files. The user-level config dir is added
as the lowest-priority layer.

**Dot-prefix required in repos.** Walk-up discovery only finds files whose
relative path starts with a dot: `.clawker.yaml` (flat dotfile) or
`.clawker/clawker.yaml` (dir form). A bare `clawker.yaml` in a repo
directory will NOT be discovered.

**Dir form wins.** If a `.clawker/` directory exists in a given directory,
only files inside it are checked. The flat dotfile form is only probed when
no `.clawker/` dir exists.

**`.yml` accepted.** Both `.yaml` and `.yml` extensions are discovered
(`.yaml` checked first). This applies at all levels.

**User-level config dir is different.** `~/.config/clawker/` uses direct
filename lookup (no dot prefix) — the directory itself is the namespace.

## Layering

Closer files win over farther ones. Within the same directory, local
overrides win over project config.

Fetch `https://docs.clawker.dev/configuration` for the current merge
behavior, field-level precedence details, and available merge strategies.

## How to get the current schema

**Never guess at project config field names or types.** The project config
schema is deterministically documented at:

`https://docs.clawker.dev/configuration`

**Always fetch this page** before recommending any project config changes.

### Reading the YAML schema

The configuration page includes a full YAML schema block for both project
config and user settings. Every field uses a type placeholder as its value
and an inline comment with metadata:

```yaml
# Description of the field
some_field: <type>  # default: value | required: true
```

- **`<type>`** — The field's type: `<string>`, `<integer>`, `<boolean>`,
  `<duration>`, or a nested structure.
- **`default: value`** — The default applied when the field is omitted.
  `default: n/a` means no default exists (the field is unset unless the
  user provides a value, though getter methods may apply runtime fallbacks).
- **`required: true|false`** — Whether the field must have a value.

Do not treat type placeholders as literal values. Do not infer defaults
from the placeholder — always read the inline `# default:` comment.

## Troubleshooting

### Build failed

User reports errors during `clawker build` or first `clawker run` (which
triggers a build).

1. **Check for user-level config conflicts FIRST**: This is the #1 hidden
   cause of build failures. User-level config (`~/.config/clawker/clawker.yaml`)
   is merged into every project. If user-level config has build-related fields
   written for a different distro than the project's base image, the build will
   fail with confusing errors.
   ```bash
   cat ~/.config/clawker/clawker.yaml
   ```
   Look for:
   - Distro-specific package names that don't match the project's base image
   - Package manager commands targeting the wrong distro
   - Shell commands assuming tools or behaviors not present on the base image
   - Any build config at user level that isn't universally distro-agnostic

   **If found**: Move the offending entries to the project-level config
   where they belong, or remove them from user-level config entirely.

2. **Identify which layer failed**: The build output shows which Dockerfile
   step failed. Read the Dockerfile template (`reference/Dockerfile.tmpl`) to
   map the failing step to the config section that produced it. Look at
   execution order and root vs user context.

3. **Package not found**: Different base images use different package managers
   with different package names. Check the project's base image, then research
   the correct package name for that distro — do not guess.

4. **Network error during build**: The build runs outside the firewall
   (it needs to pull packages). But if using a custom registry or proxy,
   ensure network access is available during build.

5. **COPY file not found**: Copy instruction paths are relative to the build
   context (project root). Verify the source file exists at the specified path.

6. **Rebuild from scratch**:
   ```bash
   clawker build --no-cache
   ```

### Config not taking effect

User changed their clawker config but the change doesn't seem to apply.

1. **Config layering precedence**: Closer files win over farther ones. Local
   overrides win over project config, which wins over parent dirs, which win
   over user-level defaults. Fetch `https://docs.clawker.dev/configuration`
   for the current merge behavior and precedence details.

2. **Check which file is active**: Use `clawker project edit` to see the
   merged project config with provenance (which file each value comes from).

3. **Build-time vs runtime**: Build-related config changes require a rebuild
   (`clawker build --no-cache`). Agent and firewall config changes take
   effect on next container creation. Fetch the current schema to check
   which fields are build-time vs runtime.

4. **Local override hiding changes**: Check if a local override file exists
   and shadows the field you changed.
