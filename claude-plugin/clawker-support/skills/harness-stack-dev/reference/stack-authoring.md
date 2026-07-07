# Stack Authoring Reference

A stack definition is a named, self-guarded Dockerfile install fragment
that projects and harness bundles **declare** instead of hand-writing. One
declaration can provision a full language stack because a definition
ships up to two fragments — one per Dockerfile USER scope.

## Definition format

```
<definition>/
├── stack.yaml                    # description only
├── Dockerfile.stack-root.tmpl    # optional: renders in a root-USER region
└── Dockerfile.stack-user.tmpl    # optional: renders in the unprivileged-USER region
```

- `stack.yaml` has exactly one field: `description:` (human summary,
  shown in listings).
- At least one fragment must be present; each present fragment must be
  non-empty and parse as a Go template. Errors are named at load:
  `stack "<name>": no fragment found — a definition ships
  Dockerfile.stack-root.tmpl, Dockerfile.stack-user.tmpl, or both`
  / `... is empty` / `... parse ...`.
- Name grammar: `[a-zA-Z0-9][a-zA-Z0-9._-]{0,40}` — it is a registry key, a
  directory name, and a token in `build.stacks` lists.

Fragments render against the same Dockerfile context as harness blocks —
`{{.BuildKitEnabled}}` for cache-mount gating is the one field shipped
fragments use; `${USERNAME}`, `${ZSH_ENV}` ARGs are in scope at the user
anchor.

## Three sources, one flat namespace

Per build, definitions resolve from:

1. **Shipped** — embedded in clawker (`go`, `node`, `python`, `rust`),
   materialized copy-if-missing to `<config-dir>/stacks/<name>/`.
2. **Settings registry** — `stacks: {<name>: {path: <dir>}}` in
   settings.yaml. Every entry carries an explicit path; the shipped set is
   auto-seeded into the registry, so overriding a shipped definition =
   editing the materialized copy (or pointing its registry entry elsewhere).
3. **Bundle-embedded** — a harness bundle's `stacks/<name>/` subdir,
   for definitions bespoke to that harness.

A name claimable from the bundle AND from the registry/shipped set is a
**collision error** (`stack "<name>" is defined both by harness bundle
... and by ... — stack names share one namespace; rename the
bundle-embedded definition`). Bundle authors prefix bespoke definitions
(e.g. `myharness-runtime`), never squat generic names. An undeclarable name
errors: `unknown stack "<name>" (known: shipped [...], settings
stacks registry, or a definition embedded in the selected harness
bundle)`.

## Placement semantics: who declares, where it renders

**Declared, never installed.** A declaration puts the fragments at fixed
anchors; earliest stage wins:

| Declaration | Renders in | Anchors |
|---|---|---|
| Project `build.stacks: [name]` (clawker.yaml) | **Shared base image** | root fragments before the project's `root_run`; user fragments before `user_run` — so project instructions can rely on them |
| Harness manifest `stacks: [name]` | **Harness image** — unless the project already declared the same name (then it is already in the base the harness image builds FROM, and the harness declaration is skipped) | root fragments before `block_1`; user fragments before `block_3` |

Two extra rules:

- Project declarations resolve from the shipped set + registry **only** —
  a bundle-embedded definition can never leak into the harness-agnostic
  base.
- A bundle embedding a definition for a name the project declared is the
  same collision error (shadow check) — a bundle must never silently swap
  the definition the base actually used.
- Duplicate names within one declaration list error
  (`build.stacks: duplicate stack declaration`); a harness
  declaration duplicating a project one is silently skipped (already in
  base), not an error.

## The self-guarding idiom

Every fragment must **skip itself when the image already provides the
tool** — this is what makes the both-declared case and user-customized
bases safe. The shipped convention:

```dockerfile
RUN if command -v <tool> >/dev/null 2>&1; then \
      echo "clawker stack <name>: existing $(<tool> --version) — skipping install"; \
    else \
      <install> ; \
    fi
```

Variants in the shipped set worth copying:

- **Floor-gated skip** (node root): keep an existing install only when it
  meets a minimum (`node` major >= `NODE_MIN_MAJOR` ARG); below the floor,
  install to `/usr/local` and win PATH.
- **Presence-file skip** (node user/nvm): `[ -s "$HOME/.nvm/nvm.sh" ]`.
- **Per-user binary skip** (rust): `[ -x "$HOME/.cargo/bin/cargo" ] ||
  command -v cargo`.
- **Two independent guards in one RUN** (python: uv and python3 guarded
  separately).

Keep the `clawker stack <name>: ... — skipping install` echo — it is
the build-log signal that the guard fired.

## Root vs user fragment: choosing the scope

- **Root fragment**: system-wide installs (`/usr/local`), apt
  dependencies, ENV that must apply to every user (`ENV` in root scope
  still applies image-wide — node's `NODE_USE_SYSTEM_CA=1`, go's
  `GOPATH`/`PATH` line). ENV set here is on the static image PATH, so
  root-installed tools survive the direct-exec CMD spawn.
- **User fragment**: per-user tooling into `$HOME` (nvm, rustup). Wire
  shell availability through `${ZSH_ENV}` (zsh sources `.zshenv` on every
  invocation, interactive and non-interactive) — e.g. nvm's installer runs
  with `PROFILE=${ZSH_ENV}`. Remember: user-fragment tools reachable only
  via shell init are fine for build steps and interactive use, but a
  harness CMD cannot depend on them (see the PATH gotcha in
  `template-blocks.md`).

Install verification matters: shipped fragments GPG-verify (node tarball
via SHASUMS256.txt.asc) or sha256-verify (go via the go.dev index) what
they download, or delegate to an installer that verifies its own artifacts
(rustup, uv). Match that bar.

## Cache implications

- The **base image** is content-keyed: a hash of the rendered base
  Dockerfile decides base rebuilds. Editing a **project-declared**
  stack's fragment changes the base render → full base rebuild for
  affected projects.
- Editing a **harness-declared** stack rebuilds only harness images
  (the base is untouched).
- Within the harness image, stack fragments render above block_4's
  version ARG — a harness version roll does NOT re-run stack installs.
- Prefer `ARG`-parameterized versions inside the fragment (node's
  `NODE_VERSION`, go's `GO_VERSION`) so users can pin via
  `clawker build --build-arg` without editing the definition.

## The shipped four (summaries)

| Name | Fragments | What it provisions |
|---|---|---|
| `go` | root | Official tarball onto `/usr/local/go`, sha256-verified from the go.dev download index; floats to latest stable unless `GO_VERSION` pins. golang-image `GOPATH`/`PATH` conventions (`/go`, world-writable). Skips when `go` exists. |
| `node` | root + user | Root: prebuilt Node LTS on `/usr/local` — `NODE_VERSION` ARG names the LTS *line* (default 24), latest patch resolved per-build, GPG-verified; `NODE_USE_SYSTEM_CA=1` so node trusts the OS CA bundle (and the firewall MITM CA). Skips when existing node major ≥ `NODE_MIN_MAJOR` (22). User: nvm via canonical installer with `PROFILE=${ZSH_ENV}`; skips when `~/.nvm` exists. |
| `python` | root | uv system-wide on `/usr/local/bin` + uv-managed CPython symlinked as `python3`/`python`; shared world-writable `UV_PYTHON_INSTALL_DIR`. Independent guards for uv and python3. |
| `rust` | user | rustup stable channel into user-owned `~/.cargo`/`~/.rustup`; rustup wires `.cargo/env` into shell init. Skips when cargo present. |

## Authoring workflow

1. Create the definition dir (conventional:
   `<config-dir>/stacks/<name>/`; bespoke-to-a-harness:
   `<bundle>/stacks/<name>/`).
2. Write `stack.yaml` (`description:`) + fragment(s) with the
   self-guard idiom.
3. Register (skip for bundle-embedded):
   ```yaml
   # In: <config-dir>/settings.yaml (user settings)
   stacks:
     myname:
       path: /absolute/path/to/definition
   ```
4. Declare it from a project (`build.stacks: [myname]`) or a harness
   manifest (`stacks: [myname]`) and build. Load errors name the file
   and rule; resolution errors name the namespace searched.
