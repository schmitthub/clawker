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

## Command Aliases

The project config's `aliases` key defines CLI shortcuts expanded before
execution (the value is appended to `clawker`, with `$1..$N` positional
placeholders). Aliases merge key-by-key across ALL layers — walk-up files,
the user-level `clawker.yaml` in the config dir, and shipped defaults
(`go` = disposable agent run, `wt` = agent on a worktree). Managed via the
`clawker alias` command group: `set` writes the user-level file, `export`
publishes active aliases into the project's own config file, `delete`
removes a name from every file layer (shipped defaults can only be
overridden, never deleted). Fetch `https://docs.clawker.dev/aliases` for
expansion semantics and the full rules.

## Reference Config Samples

Working config samples are in standalone files to avoid bloating this
reference when loaded into context.

| File | Stack | Notes |
|------|-------|-------|
| `reference/sample-go.yaml` | Go (based on clawker's own config) | `build.stacks`, root_run checksum-verified installer, harnesses map, extensive firewall rules, path-level rules |
| `reference/sample-node.yaml` | Node.js | node stack, pnpm/typescript, env_file usage, pre_run, leaner firewall |

**Read both samples** when helping a user — even if their stack doesn't match
either one exactly. Together they demonstrate the full range of config
patterns (root_run vs user_run, env_file vs from_env, inject points,
firewall rule styles, path-level rules) that apply to any base image. Use
both to deduce the right structure for the user's specific stack.

**These samples are manually maintained** — they may lag behind the schema.
For field-level details, defaults, and types, always fetch the docs site.

## How to get the current schema

**Never guess at project config field names or types.** The project config
schema is deterministically documented at:

`https://docs.clawker.dev/configuration`

**Always fetch this page** before recommending any project config changes.
The reference samples show real-world structure; the docs site is
authoritative for field names, types, and defaults.

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
   is merged into every project. Build entries written for one project
   (packages, run steps assuming that project's tools or files) leak into
   every other project's image build.
   ```bash
   cat ~/.config/clawker/clawker.yaml
   ```
   Look for:
   - Packages or run steps that only one project needs
   - Shell commands assuming tools, files, or paths from a specific project
   - Any build config at user level that isn't genuinely universal

   **If found**: Move the offending entries to the project-level config
   where they belong, or remove them from user-level config entirely.

2. **Identify which layer failed**: The build output shows which Dockerfile
   step failed. Read the bundled Dockerfile templates
   (`reference/Dockerfile.base.tmpl`, `reference/Dockerfile.harness-image.tmpl`)
   to map the failing step to the config section
   that produced it. Look at execution order and root vs user context.
   Note there are two images: a shared per-project base image (packages,
   project stacks, instructions/inject) and a per-harness image layered
   on top (harness install, harness-declared stacks).

3. **Package not found**: Every clawker image builds from a pinned Debian
   base, so `build.packages` entries are apt package names. Research the
   correct Debian package name — do not guess. Language runtimes should come
   from `build.stacks` (e.g. `go`, `node`, `python`, `rust`) rather than
   apt packages when a shipped stack exists.

4. **Network error during build**: `clawker build` is a host process that
   asks the Docker daemon to build the agent image. Image pulls and
   `RUN` step traffic originate from the daemon itself, not from any
   agent container. The clawker firewall filters only agent-container
   egress, so it does NOT and CANNOT affect build-time traffic. **Do not recommend `clawker firewall add` or any
   firewall change to fix a build network error — it will have zero
   effect.** Diagnose as a daemon/host network problem:
   - Host DNS or corporate proxy misconfigured
   - Registry outage (Docker Hub, GHCR, distro mirror)
   - Base image tag/digest no longer exists
   - VPN MTU or TLS interception breaking package downloads
   - Docker daemon proxy settings (`~/.docker/config.json`, Docker
     Desktop proxy pane, daemon `HTTP_PROXY` env)

   See the "Firewall Scope" critical rule in `SKILL.md` for the host vs
   container split.

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
   (`clawker build --no-cache`). Agent config changes take effect on next
   container creation. Firewall egress edits (`security.firewall.add_domains`
   and `security.firewall.rules`) can be applied live with `clawker firewall
   refresh` — it re-runs the container-start sync into the rules store without
   a restart (add/update only; a rule *deleted* from the config is not pruned —
   use `clawker firewall remove` for that). Fetch the current schema to check
   which fields are build-time vs runtime.

4. **Local override hiding changes**: Check if a local override file exists
   and shadows the field you changed.
