# harness.yaml Field Reference

The manifest holds everything clawker's engine consumes outside template
rendering. Every field below is verified against the manifest types and
their load-time validators. All top-level sections are optional except that
a useful bundle almost always has `version` and `volumes`.

```yaml
version:      # how {{.HarnessVersion}} is resolved
stacks:   # stack definitions this harness's blocks require
volumes:      # persisted dirs (each becomes a named volume)
seeds:        # first-boot files applied into volumes by CP's init step
staging:      # create-time host→container copies + live bind mounts
egress:       # firewall floor the harness needs to function
```

Validation happens at the **load front door** — the first command that
loads the bundle (build, create, firewall sync) fails with a named
`harness "<name>": ...` error. There are no silent skips for vocabulary
errors.

## version

```yaml
version:
  resolver: npm | github-release | none   # empty = none
  package: "@scope/pkg"                   # npm name, or GitHub owner/repo
  tag_prefix: rust-v                      # github-release only
```

| Field | Rules |
|---|---|
| `resolver` | `npm`: resolve the package's `latest` dist-tag from the npm registry. `github-release`: resolve the repo's latest release tag via the GitHub API. `none` (or empty): render the floating default `latest`. Any other token errors at build: `unsupported version resolver`. |
| `package` | npm package name (`@anthropic-ai/claude-code`) or GitHub `owner/repo` (`openai/codex`), per resolver. |
| `tag_prefix` | github-release only. Stripped from the release tag to obtain the bare version (`rust-v` turns `rust-v0.50.0` into `0.50.0`). A latest tag that does not carry the prefix **fails resolution**. |

The resolved value feeds `{{.HarnessVersion}}` at template render time.
Resolution failure is a **warning, not fatal** — the build proceeds with the
literal `latest`, so templates should treat the value as either a concrete
version or a floating tag (both shipped installers accept either).

## stacks

```yaml
stacks:
  - node
```

A list of stack definition names this harness's blocks require. Names
must match the unified rule `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` (max 32);
duplicates error at load (`duplicate stack declaration`). Whether a name
*resolves* is checked at generation time against the harness lineage
(project `stacks:` registry → this bundle's `stacks/<name>/` → shipped),
which the bundle can't fully see at load. Resolved fragments render in the
**harness image** before your blocks — always, with their lineage-resolved
definition, even when the project also declared the same name in the base
(both strata render; the fragment self-guard owns the overlap). See
`stack-authoring.md`.

## volumes

```yaml
volumes:
  - name: config       # volume-name suffix
    path: .claude      # container-home-relative mount dir
```

Each entry becomes a named docker volume `clawker.<project>.<agent>-<name>`
mounted at `~/<path>`. Every persisted dir is an explicit declaration —
clawker assumes nothing about where a harness keeps state. The harness
image pre-creates each path with correct ownership so the volume mount
inherits it.

| Rule | Error |
|---|---|
| `name` must match `[a-zA-Z0-9][a-zA-Z0-9_.-]{0,40}` (embedded verbatim in the volume name) | `volume name ... must match ...` |
| `name` must not be `history`, `workspace`, or `clawker` (reserved for clawker infrastructure) | `volume name ... is reserved for clawker infrastructure` |
| `path` must be a valid container-home-relative directory (non-empty, not `.`, no escaping) | `path ... must be a container-home-relative directory` |
| names and (normalized) paths must be unique within the manifest | `duplicate volume name` / `duplicate volume path` |

## seeds

```yaml
seeds:
  - file: assets/statusline.sh        # bundle-relative, MUST be under assets/
    dest: .claude/statusline.sh       # home-relative, MUST be under a declared volume
    apply: copy-if-missing            # copy-if-missing | copy-if-missing-or-empty | json-merge
```

Seeds solve the volume-shadowing problem: a named volume mounted over an
image path hides whatever the image baked there. Instead, the harness-image
template stages each seed to `~/.clawker/seed/<dest>` plus a generated
`seed-manifest` (apply-token lines), and the control plane's **generic
seed-apply init step** places them into the harness config dir on first
boot of the volume.

| Rule | Error |
|---|---|
| `file` must be a valid path with the `assets/` prefix (assets/ is what gets staged into the build context) | `seed file ... must be a path under assets/ inside the bundle` |
| `file` must exist in the bundle | `seed file ...: <stat error>` |
| `dest` must be home-relative and fall under a declared volume path | `dest ... is not under any declared volume path — declare the persisted dir in the volumes list` |
| `apply` must be one of the three tokens | `unknown apply strategy ... (want copy-if-missing, copy-if-missing-or-empty, or json-merge)` |

Apply strategies:

- `copy-if-missing` — place the file only when dest does not exist.
- `copy-if-missing-or-empty` — also replace an existing zero-length dest.
- `json-merge` — deep-merge the seed JSON into the existing dest JSON
  (seed fills gaps; existing user values win).

## staging

Create-time host→container copies plus live bind mounts, for harness state
that lives **outside the workspace** (the workspace arrives via bind mount
or snapshot and is never staged). Every entry is an explicit, deliberate
src→dest directive — nothing is copied by naming convention.

```yaml
staging:
  copy:
    - src: ${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json
      dest: .claude/settings.json
      json_keys: [enabledPlugins]        # allowlist of keys to keep (single-file src only)
    - src: ~/.claude/plugins
      dest: .claude/plugins
      skip: [install-counts-cache.json]  # basenames to skip during tree copy
      json_rewrites:
        - { file: known_marketplaces.json, key: installPath, rewrite: prefix-swap }
        - { file: installed_plugins.json,  key: projectPath, rewrite: replace-with-workdir }
  mounts:
    - src: ${CLAUDE_CONFIG_DIR:-~/.claude}/projects
      dest: .claude/projects
```

### `copy` entries

| Field | Rules |
|---|---|
| `src` | Host path or doublestar glob. Expansion vocabulary: leading `~`, `$VAR` / `${VAR}`, and shell-style `${VAR:-fallback}` defaults — expanded before matching; a relative result resolves against the CWD. A src resolving to a directory copies recursively; a glob matching multiple entries lands each under dest as a directory. **Missing sources skip silently** (staging is best-effort per entry). **Sources inside the project workspace are rejected at stage time.** |
| `dest` | Container-home-relative; **must fall under a declared volume** (copies land in the volume at create time — a non-persisted dest is a config error, caught at load). |
| `json_keys` | Allowlist filter: keep only these top-level keys of a JSON file. Requires a single-file src — combining with a glob errors: `json_keys requires a single-file src, not a glob`. Use for host files that mix portable config with secrets/host state. |
| `skip` | File basenames to omit during a directory-tree copy. |
| `json_rewrites` | Rewrite one JSON key's path value per matching `file` during tree staging. `rewrite` must be `prefix-swap` (map the host tree prefix onto the in-container tree prefix) or `replace-with-workdir` (substitute the whole value with the container workspace path); anything else errors: `unknown json rewrite ... (want prefix-swap or replace-with-workdir)`. |

Both `src` and `dest` are required (`staging copy entries require explicit
src and dest`).

### `mounts` entries

Live bind mounts instead of copies — host changes stay visible both ways.
`src` expands like copy src but **must be a literal path, no globs**
(`mount src ... must be a literal path, not a glob`); `dest` follows the
same under-a-declared-volume rule as copy dests.

### What NOT to stage

Credentials (see `security-egress.md` — in-container auth is the model),
anything inside the workspace, and host files that embed host-path-keyed
state a container cannot use (the codex bundle deliberately skips
`config.toml` for exactly this reason — and TOML has no `json_keys`
equivalent).

## egress

The firewall floor the harness needs to **function at runtime**, composed
with the project's `security.firewall` rules at create/sync time (floor
first, then project rules). Rule shape mirrors the project-config egress
rule:

```yaml
egress:
  - dst: api.example.com          # exact host; leading dot (.example.com) = wildcard incl. apex
    proto: https                  # optional; empty gets protocol defaults server-side
    port: "443"                   # optional; empty = proto default
    action: allow                 # optional; allow is the default
    path_rules:                   # https/http/ws/wss only
      - path: /api/
        action: allow
        methods: [GET, POST]      # optional verb gate
    path_default: deny            # optional; empty + an allow path rule = allowlist mode
```

Empty `proto`/`port`/`action` pass through untouched — the firewall's
normalization applies protocol defaults server-side, exactly as it does for
project rules. `insecure_skip_tls_verify` is **not** part of the harness
vocabulary (floors never skip verification).

Design guidance — minimal floor, path scoping, UGC-sink denial, MITM/SNI
implications — lives in `security-egress.md`. Read it before writing any
`egress:` section.

## Registration (project clawker.yaml)

Not part of the manifest, but a custom bundle is inert without it. Shipped
bundles (`claude`, `codex`) need no registration — they resolve from the
binary. Register a custom bundle per-project:

```bash
clawker harness register /path/to/bundle --name myharness
```

which writes:

```yaml
# In: the project's clawker.yaml
harnesses:
  myharness:
    path: ./relative/or/absolute/bundle   # required — resolution is registry-only
```

Resolution is a per-lineage chain — project `harnesses:` registry → shipped —
where the closest layer wins wholesale; a project entry under a shipped name
shadows it (reported in build output). The same `harnesses.<name>` entry may
also carry per-harness init config (`env`, `post_init`, `pre_run`, config
strategy); an entry with init config but no `path` is NOT a registration.

- Missing/unresolvable: `harness "<name>" is not registered` — names the
  `clawker harness register` remedy.
- Path without a manifest: `no bundle at registered path ...` — fix
  `harnesses.<name>.path` in `clawker.yaml`.
- Name grammar: unified rule `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` (max 32); the
  name is also the image tag, so `default`, `latest`, and `base` are reserved
  aliases.
