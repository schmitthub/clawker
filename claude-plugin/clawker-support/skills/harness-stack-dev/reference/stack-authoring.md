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
- Name grammar (unified rule, shared with harnesses): `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`,
  max 32 chars — lowercase letters, digits, internal hyphens, no leading/trailing
  hyphen. It is a registry key, a directory name (the dir base name IS the name
  unless `--name` overrides at register time), and a token in `build.stacks` lists.

Fragments render against the same Dockerfile context as harness blocks —
`{{.BuildKitEnabled}}` for cache-mount gating is the one field shipped
fragments use; `${USERNAME}`, `${ZSH_ENV}` ARGs are in scope at the user
anchor.

## Per-lineage lookup chain

A declared name resolves through a **lineage lookup chain** — the closest
layer that defines it wins **wholesale** (never merged). There is no global
namespace; the chain depends on where the name renders:

| Renders in | Lookup chain |
|---|---|
| Base image (project `build.stacks`) | project `stacks:` registry → shipped |
| Harness image (bundle `stacks:` or `build.harnesses.<h>.stacks`) | project `stacks:` registry → the selected bundle's `stacks/<name>/` dir → shipped |

- **Shipped** definitions (`go`, `node`, `python`, `rust`) are embedded in the
  clawker binary and load straight from it — no materialization, no config-dir
  copies. They are the floor of every chain.
- **Project registry** — `stacks: {<name>: {path: <dir>}}` in the project's
  `clawker.yaml`, written by `clawker stack register`. Paths are project-root-
  relative or absolute (no `~`/`$VAR`). A project entry under a shipped name
  shadows shipped everywhere.
- **Bundle-embedded** — a harness bundle's `stacks/<name>/` subdir, resolved
  only in that harness's lineage. Because sibling harness images don't share a
  chain, two bundles embedding the same name never collide.

When a closer layer shadows a farther one, the build output prints a
provenance line (`stack node ← project (./stacks/node) shadows shipped`) — the
substitution is never silent. An unresolvable name errors, naming the searched
lineage and the `clawker stack register <path>` remedy.

## Placement semantics: who declares, where it renders

**Declared, never installed.** A declaration puts the fragments at fixed
anchors; earliest stage wins:

| Declaration | Renders in | Anchors |
|---|---|---|
| Project `build.stacks: [name]` (clawker.yaml) | **Shared base image** | root fragments before the project's `root_run`; user fragments before `user_run` — so project instructions can rely on them |
| Harness manifest `stacks: [name]` (+ project overlay `build.harnesses.<h>.stacks`) | **Harness image** — always, with its lineage-resolved definition, even when the project declared the same name in the base | root fragments before `block_1`; user fragments before `block_3` |

The two strata **never interact** (there is no cross-stratum dedup): project
`node` renders in the base, a harness's `node` renders ADDITIONALLY in the
harness image. The engine never judges whether the base render "satisfies"
the harness declaration — that would be an implicit taxonomy. Satisfaction is
the fragment's job (self-guard skips when the runtime is already present, apt
idempotence, PATH shadowing by later layers). Extra rules:

- Project declarations resolve from the project registry + shipped **only** —
  a bundle-embedded definition can never leak into the harness-agnostic base.
- Overlay stacks (`build.harnesses.<h>.stacks`) render AFTER the bundle's own
  installer stacks (installer → overlay). A name repeated across the two
  sources renders once, at its installer position.
- Duplicate names within one declaration list error
  (`build.stacks: duplicate stack declaration`).

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

1. Create the definition dir (e.g. `./stacks/<name>/`; bespoke-to-a-harness:
   `<bundle>/stacks/<name>/`).
2. Write `stack.yaml` (`description:`) + fragment(s) with the
   self-guard idiom.
3. Register per-project (skip for bundle-embedded):
   ```bash
   clawker stack register ./stacks/myname          # name = dir base name
   clawker stack register ./vendor/foo --name myname
   ```
   Writes `stacks.myname.path` in the project's `clawker.yaml`. Run it inside
   an initialized project; from an unregistered dir it writes to the user-level
   `clawker.yaml` and prints where it wrote.
4. Declare it from a project (`build.stacks: [myname]`), a harness manifest
   (`stacks: [myname]`), or a project overlay (`build.harnesses.<h>.stacks`)
   and build. Load errors name the file and rule; resolution errors name the
   searched lineage.
