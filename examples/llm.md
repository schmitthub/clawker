# clawker.yaml — LLM Authoring Guide

Comprehensive reference for LLM agents creating `clawker.yaml` configuration files.
Read this entire document before generating any configuration.

---

## 1. What Is Clawker?

Clawker is container orchestration for running Claude Code agents in isolated Docker containers.
A single `clawker.yaml` file in your project root drives three phases:

1. **Image building** — Clawker renders an embedded Dockerfile template with your config fields, then builds the image.
2. **Container creation** — workspace mounted, environment variables resolved, config volumes initialized, Docker network connected.
3. **Container start** — entrypoint runs: firewall setup, config initialization, git configuration, SSH known hosts, `post_init` script (once), then Claude Code launches.

Key facts:

- You do **NOT** write a Dockerfile. Clawker uses a parameterized template internally.
- The container runs as unprivileged user `claude` (UID 1001, GID 1001) with a `zsh` shell.
- The project workspace is mounted at `/workspace` by default.
- A network firewall is enabled by default, blocking all outbound traffic except allowed domains.
- Git credential forwarding (SSH, GPG, HTTPS) is enabled by default.
- A host proxy enables browser-based OAuth flows from inside the container.

---

## 2. What clawker.yaml Does

The config file has two phases of effect:

- **Build-time** fields (`build.*`) control what goes into the Docker image.
- **Runtime** fields (`agent.*`, `workspace.*`, `security.*`, `loop.*`) control container creation and startup behavior.

`version: "1"` is always required as the first field.

Config merge chain (lowest to highest precedence): hardcoded defaults, user-level `~/.local/clawker/clawker.yaml`, project-level `clawker.yaml`, `CLAWKER_*` environment variables.

---

## 3. What the Base Image Already Provides

The Clawker template installs a comprehensive set of packages automatically.
**Do NOT add these to `build.packages`** — they are already present.

### Debian Images (bookworm, trixie)

| Package | Purpose |
|---------|---------|
| less | Pager |
| procps | Process utilities (ps, top) |
| sudo | Privilege escalation (firewall only) |
| fzf | Fuzzy finder |
| gcc | C compiler |
| git | Version control |
| libc6-dev | C standard library headers |
| zsh | Default shell |
| make | Build tool |
| man-db | Manual pages |
| unzip | Archive extraction |
| gnupg2 | GPG encryption |
| iptables | Firewall backend |
| ipset | IP set management |
| iproute2 | Network utilities |
| dnsutils | DNS tools (dig, nslookup) |
| aggregate | CIDR aggregation |
| jq | JSON processor |
| nano | Text editor |
| vim | Text editor |
| wget | HTTP downloader |
| curl | HTTP client |
| gh | GitHub CLI |
| locales, locales-all | Locale support |
| docker-ce-cli | Docker CLI (no daemon) |
| docker-buildx-plugin | BuildKit plugin |
| docker-compose-plugin | Compose plugin |
| git-delta | Diff viewer |

### Alpine Images (alpine3.22, alpine3.23)

| Package | Purpose |
|---------|---------|
| bash | Bourne-again shell |
| less | Pager |
| procps | Process utilities |
| sudo | Privilege escalation (firewall only) |
| fzf | Fuzzy finder |
| gcc | C compiler |
| git | Version control |
| musl-dev | C library headers |
| zsh | Default shell |
| make | Build tool |
| man-db | Manual pages |
| unzip | Archive extraction |
| gnupg | GPG encryption |
| iptables | Firewall backend |
| ipset | IP set management |
| iproute2 | Network utilities |
| bind-tools | DNS tools |
| jq | JSON processor |
| nano | Text editor |
| vim | Text editor |
| wget | HTTP downloader |
| curl | HTTP client |
| github-cli | GitHub CLI |
| musl-locales | Locale support |
| musl-locales-lang | Locale language data |
| docker-cli | Docker CLI (no daemon) |
| docker-cli-buildx | BuildKit plugin |
| docker-cli-compose | Compose plugin |
| delta | Diff viewer |

### Also Pre-Installed (Both Variants)

- **zsh** with plugins: `git`, `fzf` (key bindings + completion)
- **Claude Code** (latest or pinned version)
- **Bash history** persistence across sessions
- **SSH known hosts** for github.com, gitlab.com, bitbucket.org (added at container start)

### Pre-Configured Environment Variables

These are set in the image and available at build time (after `USER claude`):

| Variable | Value | Purpose |
|----------|-------|---------|
| `SHELL` | `/bin/zsh` | Default shell |
| `PATH` | `/home/claude/.local/bin:$PATH` | User-local binaries |
| `BROWSER` | `/usr/local/bin/host-open` | Opens URLs on the host |
| `TERM` | `xterm-256color` | Terminal type |
| `COLORTERM` | `truecolor` | Color support |
| `LANG` | `en_US.UTF-8` | Locale |

---

## 4. Annotated Dockerfile Template

This is a simplified view of the Dockerfile template showing exactly where each config field is injected. `<<<<` markers indicate injection points you control via `clawker.yaml`.

```dockerfile
# Builder stages (callback-forwarder, socket-server — internal, not configurable)

FROM <build.image> AS final                          <<<< build.image

ARG TZ=UTC

# User-defined ARGs                                  <<<< build.instructions.args
ARG MY_ARG="default"

# ── inject.after_from ──                            <<<< build.inject.after_from

# Install system packages
RUN apt-get install -y \                             (Debian variant shown)
    less procps sudo fzf gcc git libc6-dev           (built-in packages)
    zsh make man-db unzip gnupg2 iptables ipset
    iproute2 dnsutils aggregate jq nano vim
    wget curl gh locales locales-all
    <build.packages>                                 <<<< build.packages

# ── inject.after_packages ──                        <<<< build.inject.after_packages
#    ENV vars needed by root_run go HERE

# Install Docker CLI (automatic)

# Locale setup (automatic)

# Root-mode RUN commands                             <<<< build.instructions.root_run
RUN <root_run[0].cmd>
RUN <root_run[1].cmd>

# Create non-root user: claude (UID 1001, GID 1001)
# Create docker group, add claude to it

# ── inject.after_user_setup ──                      <<<< build.inject.after_user_setup

# Create directories: ~/.claude, ~/.gnupg, ~/.ssh, /workspace, /var/run/clawker
# Persist bash history

WORKDIR /workspace                                   (or build.instructions.workdir)

# Install git-delta (automatic)

# Firewall script + host proxy scripts (automatic)

# Switch to non-root user
USER claude

# Static ENV: SHELL, PATH, BROWSER, TERM, COLORTERM, LANG
# Telemetry ENV (OTEL — automatic)

# ── inject.after_user_switch ──                     <<<< build.inject.after_user_switch
#    ENV vars needed by user_run go HERE

# Zsh setup with plugins: git, fzf (automatic)

# User-mode RUN commands                             <<<< build.instructions.user_run
RUN <user_run[0].cmd>
RUN <user_run[1].cmd>

# Install Claude Code (automatic)

# Stage config files (automatic)

# COPY instructions                                  <<<< build.instructions.copy
COPY --chown=claude:claude <src> <dest>

# ── inject.after_claude_install ──                  <<<< build.inject.after_claude_install

# HEALTHCHECK (automatic, or override)               <<<< build.instructions.healthcheck

# ── inject.before_entrypoint ──                     <<<< build.inject.before_entrypoint

ENTRYPOINT ["entrypoint.sh"]
CMD ["claude"]
```

### Injection Point Summary

| Injection Point | Runs As | Use For |
|----------------|---------|---------|
| `after_from` | root | Early ARGs, base image modifications |
| `after_packages` | root | ENV vars needed by `root_run`, additional apt sources |
| `after_user_setup` | root | Permissions, directory creation after user exists |
| `after_user_switch` | claude | ENV vars needed by `user_run`, PATH additions |
| `after_claude_install` | claude | Post-install tweaks, additional COPY layering |
| `before_entrypoint` | claude | Final labels, EXPOSE, last-mile config |

---

## 5. Container Lifecycle

### Phase 1: BUILD (image creation)

Triggered by `clawker image build` or automatically on first `clawker run`.

1. Template renders `Dockerfile` from `build.*` fields.
2. `build.packages` are installed via `apt-get` (Debian) or `apk add` (Alpine).
3. `build.inject.*` lines are emitted at their respective injection points.
4. `build.instructions.root_run` commands execute as root.
5. User `claude` (UID 1001) is created.
6. `build.instructions.user_run` commands execute as claude.
7. Claude Code is installed.
8. `build.instructions.copy` files are added.
9. Image is tagged `clawker-<project>:sha-<hash>` with `:latest` alias.

### Phase 2: CREATE (container creation)

Triggered by `clawker run`, `clawker container create`, or `clawker start`.

1. Workspace mounted: `bind` (live host mount) or `snapshot` (ephemeral copy).
2. Config volumes created for `~/.claude` (settings, history) and `/commandhistory`.
3. Environment variables resolved and injected (see precedence below).
4. `build.instructions.env` values set as container environment variables.
5. Container connected to `clawker-net` Docker network.
6. Host proxy started if enabled.

### Phase 3: START / ENTRYPOINT (container startup)

The entrypoint script runs in this exact order:

1. **Firewall initialization** — if `security.firewall.enable: true` and the container has `NET_ADMIN`/`NET_RAW` capabilities, iptables rules are applied using `CLAWKER_FIREWALL_DOMAINS` and `CLAWKER_FIREWALL_IP_RANGE_SOURCES`.
2. **Config volume initialization** — copies `statusline.sh` and `settings.json` from image defaults if not already present on the config volume; merges settings if the volume already has a `settings.json`.
3. **Git configuration** — copies host `.gitconfig` (filtering out `[credential]` section), configures `git-credential-clawker` helper if HTTPS forwarding is enabled.
4. **SSH known hosts** — adds public keys for github.com, gitlab.com, and bitbucket.org to `~/.ssh/known_hosts`.
5. **post_init script** — runs `agent.post_init` content **once** on first start. A marker file (`~/.claude/post-initialized`) prevents re-execution on subsequent starts. To re-run, delete the marker or recreate the config volume.
6. **Ready signal** — writes `/var/run/clawker/ready` for health check.
7. **exec** — hands off to Claude Code (or custom CMD).

### Environment Variable Precedence

At container creation, env vars are merged from these sources (lowest to highest priority):

1. `agent.env_file` — Docker env-file format, loaded from host paths
2. `agent.from_env` — host environment variables forwarded by name
3. `agent.env` — static key-value pairs in clawker.yaml

A variable defined in `agent.env` overrides the same name from `agent.from_env` or `agent.env_file`.

**Important distinction**: `build.instructions.env` sets env vars on the **container** (at `docker create` time via Docker API). These are **NOT available during `docker build`**. Use `build.inject.*` with `ENV` directives for build-time variables.

---

## 6. Complete Schema Reference

### Top-Level

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `version` | string | — | **Required.** Always `"1"`. |
| `project` | string | — | Project name (injected by loader from registry, not persisted). |

### `build` — Image Build Configuration

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `build.image` | string | `"node:20-slim"` | build | Base Docker image. Use a built-in flavor or any Docker Hub image. |
| `build.packages` | list[string] | `["git", "curl", "ripgrep"]` | build | Additional system packages (`apt-get` on Debian, `apk` on Alpine). |
| `build.dockerfile` | string | `""` | build | Path to a custom Dockerfile (relative to project root). Skips template entirely. |
| `build.context` | string | `""` | build | Custom build context directory (relative to project root). |
| `build.build_args` | map[string, string] | `{}` | build | Docker build arguments (`--build-arg`). |

#### Built-In Base Image Flavors

| Flavor | Maps To |
|--------|---------|
| `bookworm` | `buildpack-deps:bookworm-scm` (Debian stable, recommended) |
| `trixie` | `buildpack-deps:trixie-scm` (Debian testing) |
| `alpine3.22` | `alpine:3.22` |
| `alpine3.23` | `alpine:3.23` |

You can also use any Docker Hub image directly (e.g., `golang:1.24-bookworm`, `node:22-bookworm`).

### `build.instructions` — Typed Dockerfile Instructions

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `instructions.root_run` | list[RunInstruction] | `[]` | build | RUN commands executed as root, after packages. |
| `instructions.user_run` | list[RunInstruction] | `[]` | build | RUN commands executed as `claude` user. |
| `instructions.env` | map[string, string] | `{}` | **runtime** | **Container env vars** set at `docker create`. NOT available during build. |
| `instructions.copy` | list[CopyInstruction] | `[]` | build | COPY instructions (after Claude Code install, for cache efficiency). |
| `instructions.args` | list[ArgDefinition] | `[]` | build | ARG instructions (emitted early, before `inject.after_from`). |
| `instructions.expose` | list[ExposePort] | `[]` | build | EXPOSE instructions. |
| `instructions.labels` | map[string, string] | `{}` | build | LABEL instructions. |
| `instructions.volumes` | list[string] | `[]` | build | VOLUME instructions. |
| `instructions.workdir` | string | `""` | build | Override WORKDIR (default: `workspace.remote_path`). |
| `instructions.healthcheck` | HealthcheckConfig | (auto) | build | Override the default HEALTHCHECK. |
| `instructions.shell` | list[string] | `[]` | build | Override SHELL instruction. |

#### RunInstruction Format

Each `root_run` / `user_run` entry supports three forms:

```yaml
# Generic (both Debian and Alpine):
- cmd: "curl -fsSL https://example.com/install.sh | bash"

# OS-specific (only the matching variant runs):
- alpine: "apk add libfoo-dev"
  debian: "apt-get install -y libfoo-dev"

# Multi-line with shell continuation:
- cmd: |
    ARCH=$(dpkg --print-architecture) && \
    curl -fsSL "https://go.dev/dl/go1.24.linux-${ARCH}.tar.gz" \
      | tar -C /usr/local -xzf -
```

#### CopyInstruction Format

```yaml
instructions:
  copy:
    - src: "./config/my.conf"
      dest: "/home/claude/.config/my.conf"
      chown: "claude:claude"    # optional
      chmod: "0644"             # optional
```

#### ArgDefinition Format

```yaml
instructions:
  args:
    - name: "MY_VERSION"
      default: "1.0"           # optional
```

#### ExposePort Format

```yaml
instructions:
  expose:
    - port: 8080
      protocol: "tcp"          # optional, defaults to "tcp"
```

#### HealthcheckConfig Format

```yaml
instructions:
  healthcheck:
    cmd: ["CMD-SHELL", "curl -f http://localhost:8080/health"]
    interval: "30s"            # optional
    timeout: "5s"              # optional
    start_period: "10s"        # optional
    retries: 3                 # optional
```

### `build.inject` — Raw Dockerfile Injection Points

Each field accepts a YAML list of strings. Each string becomes a Dockerfile instruction at that point.

| Field | Type | Default | Runs As | Description |
|-------|------|---------|---------|-------------|
| `inject.after_from` | list[string] | `[]` | root | After `FROM`, before package install. |
| `inject.after_packages` | list[string] | `[]` | root | After package install, before `root_run`. |
| `inject.after_user_setup` | list[string] | `[]` | root | After user creation, before directory setup. |
| `inject.after_user_switch` | list[string] | `[]` | claude | After `USER claude`, before `user_run`. |
| `inject.after_claude_install` | list[string] | `[]` | claude | After Claude Code install. |
| `inject.before_entrypoint` | list[string] | `[]` | claude | Before ENTRYPOINT instruction. |

**Syntax**: Use YAML block scalar (`|`) for multi-line. Each line is a raw Dockerfile instruction:

```yaml
build:
  inject:
    after_packages: |
      ENV GO_VERSION="1.24.1"
    after_user_switch: |
      ENV GOPATH="/home/claude/go"
      ENV GOBIN="/home/claude/go/bin"
```

### `agent` — Agent Runtime Settings

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `agent.includes` | list[string] | `[]` | runtime | Files to make available to Claude (prompts, docs). |
| `agent.env_file` | list[string] | `[]` | runtime | Host paths to Docker env-file format files. Supports `~` and `$VAR` expansion. |
| `agent.from_env` | list[string] | `[]` | runtime | Host env var names to forward. Warns if unset. |
| `agent.env` | map[string, string] | `{}` | runtime | Static env vars. Highest precedence. |
| `agent.post_init` | string | `""` | entrypoint | Shell script run **once** on first container start (`set -e`). |
| `agent.memory` | string | `""` | runtime | Memory/context for the agent. |
| `agent.editor` | string | `""` | runtime | Editor preference. |
| `agent.visual` | string | `""` | runtime | Visual editor preference. |
| `agent.shell` | string | `""` | runtime | Override default shell. |
| `agent.enable_shared_dir` | bool | `false` | runtime | Mount `~/.clawker-share` read-only from host. |
| `agent.claude_code.config.strategy` | string | `"copy"` | runtime | `"copy"` copies host `~/.claude/` config; `"fresh"` starts clean. |
| `agent.claude_code.use_host_auth` | bool | `true` | runtime | Use host authentication tokens in container. |

### `workspace` — Workspace Configuration

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `workspace.remote_path` | string | `"/workspace"` | runtime | Mount point inside the container. |
| `workspace.default_mode` | string | `"bind"` | runtime | `"bind"` (live sync) or `"snapshot"` (isolated copy). |

### `security` — Security Configuration

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `security.docker_socket` | bool | `false` | runtime | Mount Docker socket (security risk). |
| `security.cap_add` | list[string] | `["NET_ADMIN", "NET_RAW"]` | runtime | Linux capabilities to add. |
| `security.enable_host_proxy` | bool | `true` | runtime | Enable host proxy for browser auth + HTTPS git credentials. |

#### `security.firewall` — Network Firewall

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `firewall.enable` | bool | `true` | runtime | Enable outbound firewall. |
| `firewall.add_domains` | list[string] | `[]` | runtime | Domains to add to the default allowlist. |
| `firewall.remove_domains` | list[string] | `[]` | runtime | Domains to remove from the default allowlist. |
| `firewall.override_domains` | list[string] | `[]` | runtime | Replace the entire allowlist (ignores add/remove, skips IP range fetching). |
| `firewall.ip_range_sources` | list[IPRangeSource] | `[{name: github}]` | runtime | Cloud provider IP ranges to allow. |

**Default firewall allowlist** (always present unless overridden):

```
api.anthropic.com
docker.io
marketplace.visualstudio.com
production.cloudflare.docker.com
registry-1.docker.io
registry.npmjs.org
sentry.io
statsig.anthropic.com
statsig.com
update.code.visualstudio.com
vscode.blob.core.windows.net
```

**Built-in IP range sources:**

| Name | Purpose | Default? |
|------|---------|----------|
| `github` | GitHub API, web, git, actions, packages, copilot | Yes (required) |
| `google-cloud` | Google Cloud Platform IPs only | No |
| `google` | All Google IPs (includes GCS, Firebase — see security warning below) | No |
| `cloudflare` | Cloudflare CDN IPs | No |
| `aws` | All AWS IPs | No |

**Security warning**: The `google` source allows traffic to all Google IPs, including Google Cloud Storage and Firebase Hosting which can serve user-generated content. Only add `google` if your project requires it (e.g., Go modules via `proxy.golang.org`). Prefer `google-cloud` for tighter scoping.

**Custom IP range source:**

```yaml
firewall:
  ip_range_sources:
    - name: github
    - name: custom
      url: "https://example.com/ranges.json"
      jq_filter: ".cidrs[]"
      required: false          # optional, default: false (true for github)
```

#### `security.git_credentials` — Git Credential Forwarding

| Field | Type | Default | Phase | Description |
|-------|------|---------|-------|-------------|
| `git_credentials.forward_https` | bool | follows `enable_host_proxy` | runtime | HTTPS credential forwarding via host proxy. |
| `git_credentials.forward_ssh` | bool | `true` | runtime | SSH agent forwarding. |
| `git_credentials.forward_gpg` | bool | `true` | runtime | GPG agent forwarding. |
| `git_credentials.copy_git_config` | bool | `true` | runtime | Copy host `.gitconfig` (filtering `[credential]` section). |

### `loop` — Autonomous Loop Settings (Optional)

The `loop` section is entirely optional. Omit it if you don't need autonomous looping.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `loop.max_loops` | int | `50` | Maximum iterations per session. |
| `loop.stagnation_threshold` | int | `3` | Iterations without progress before circuit trips. |
| `loop.timeout_minutes` | int | `15` | Per-iteration timeout. |
| `loop.loop_delay_seconds` | int | `0` | Delay between iterations. |
| `loop.calls_per_hour` | int | `0` | API rate limit (0 = disabled). |
| `loop.skip_permissions` | bool | `false` | Allow all tools without prompting. |
| `loop.hooks_file` | string | `""` | Custom hooks file path. |
| `loop.append_system_prompt` | string | `""` | Additional system prompt instructions. |
| `loop.same_error_threshold` | int | `0` | Consecutive identical errors before trip. |
| `loop.output_decline_threshold` | int | `0` | Output shrink percentage before trip. |
| `loop.max_consecutive_test_loops` | int | `0` | Test-only iterations before trip. |
| `loop.safety_completion_threshold` | int | `0` | Completion indicators without exit before trip. |
| `loop.completion_threshold` | int | `0` | Indicators required for strict completion. |
| `loop.session_expiration_hours` | int | `0` | Session TTL. |

---

## 7. Placement Rules

Where to put things and why:

| What You Need | Where to Put It | Why |
|---------------|-----------------|-----|
| ENV var used by `root_run` | `build.inject.after_packages` | Available during build, before root commands |
| ENV var used by `user_run` | `build.inject.after_user_switch` | Available during build, after USER switch |
| ENV var needed only at container runtime | `agent.env` or `build.instructions.env` | Set at `docker create`, not baked into image |
| Secrets (API keys, tokens) | `agent.from_env` | Forwarded from host at runtime, never in image |
| Secret files (.env format) | `agent.env_file` | Loaded from host at runtime |
| System package (apt/apk) | `build.packages` | Added to the template's package install step |
| System-wide binary install | `build.instructions.root_run` | Runs as root during build |
| User-space tool install | `build.instructions.user_run` | Runs as claude during build |
| Package registry domain | `security.firewall.add_domains` | Allows runtime network access |
| Cloud provider IP ranges | `security.firewall.ip_range_sources` | IP-based allowlisting for CDN/API endpoints |
| First-start project setup | `agent.post_init` | Runs once in container, not during build |
| Files to copy into image | `build.instructions.copy` | COPY instruction in Dockerfile |
| Custom Dockerfile lines | `build.inject.*` | Raw Dockerfile instructions at injection points |

---

## 8. Patterns

### Pattern 1: System-Wide Tool Install (root_run)

Install a binary as root, with a build-time version variable:

```yaml
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - ca-certificates                              # needed for HTTPS downloads
  inject:
    after_packages: |
      ENV TOOL_VERSION="1.0"                       # available to root_run below
  instructions:
    root_run:
      - cmd: |
          curl -fsSL "https://example.com/tool-${TOOL_VERSION}.tar.gz" \
            | tar -C /usr/local -xzf -
```

### Pattern 2: User-Space Tool Install (user_run)

Install tooling as the claude user:

```yaml
build:
  inject:
    after_user_switch: |
      ENV TOOL_HOME="/home/claude/.tool"           # available to user_run below
  instructions:
    user_run:
      - cmd: |
          curl -sSf https://example.com/install.sh | sh
      - cmd: |
          echo 'export PATH="$TOOL_HOME/bin:$PATH"' >> ~/.bashrc
```

### Pattern 3: Pre-Built Image (No Inject Needed)

When the base image already has the toolchain, skip inject entirely:

```yaml
build:
  image: "golang:1.24-bookworm"
  instructions:
    user_run:
      - cmd: "go install golang.org/x/tools/gopls@latest"
```

### Pattern 4: OS-Variant Commands

When your project needs to support both Debian and Alpine images:

```yaml
build:
  instructions:
    root_run:
      - alpine: "apk add libfoo-dev"
        debian: "apt-get install -y libfoo-dev"
```

Only the matching variant's command runs. `cmd` is for commands that work on both.

### Pattern 5: Firewall Domains for Package Registries

Always add domains for registries your runtime depends on:

```yaml
security:
  firewall:
    enable: true
    add_domains:
      - "registry.npmjs.org"     # already in defaults, but shown for clarity
      - "pypi.org"
      - "files.pythonhosted.org"
    ip_range_sources:
      - name: github             # always include — required by default
      - name: google             # only if needed (e.g., proxy.golang.org uses GCS)
```

**Note**: The default allowlist already includes `registry.npmjs.org`, Docker registries, and Anthropic services. Only add domains that aren't in the default list.

### Pattern 6: post_init (First-Start Script)

Runs once on first container start. Use for project-specific setup that depends on the mounted workspace:

```yaml
agent:
  post_init: |
    cd /workspace
    if [ -f package.json ]; then npm install; fi
```

Key details:
- Runs with `set -e` — any command failure aborts startup.
- A marker file (`~/.claude/post-initialized`) prevents re-execution.
- To re-run, delete the marker or recreate the config volume.
- Runs **after** firewall, git config, and SSH setup — network access is available (within firewall rules).

### Pattern 7: Environment Variable Layering

Combine multiple env sources with clear precedence:

```yaml
agent:
  env_file:
    - "~/.secrets/common.env"    # lowest precedence
    - ".env.local"               # overrides common.env
  from_env:
    - ANTHROPIC_API_KEY          # from host, overrides env_file
    - GITHUB_TOKEN
  env:
    NODE_ENV: "development"      # highest precedence, overrides all above
```

### Pattern 8: Multiple RUN Steps vs Single Step

Use separate `user_run` entries when steps are logically independent (better cache reuse):

```yaml
instructions:
  user_run:
    # Step 1: Install toolchain
    - cmd: "curl -sSf https://sh.rustup.rs | sh -s -- -y"
    # Step 2: Install components (depends on step 1)
    - cmd: |
        . "$HOME/.cargo/env" && \
        rustup component add rust-analyzer clippy
    # Step 3: Configure shell (independent)
    - cmd: 'echo '"'"'. "$HOME/.cargo/env"'"'"' >> ~/.bashrc'
```

Each entry becomes a separate `RUN` layer. Combine with `&&` only when steps must share shell state.

---

## 9. Validating Your Config

Always validate after writing a `clawker.yaml`:

```bash
# Validate clawker.yaml in current directory
clawker config check

# Validate a specific file
clawker config check --file path/to/clawker.yaml
```

**What it checks:**
- YAML syntax errors
- Unknown or misspelled fields (strict mode — no extra keys allowed)
- Required fields (`version`)
- Valid values (e.g., `default_mode` must be `"bind"` or `"snapshot"`)
- File existence for referenced paths (`agent.env_file`, `agent.includes`)

**Success output:** `<checkmark> /path/to/clawker.yaml is valid` (exit code 0)

**Failure output:** Lists errors with `<cross>` prefix (exit code 1)

---

## 10. Common Mistakes

1. **Using `build.instructions.env` for build-time variables.** These are set at `docker create` (runtime), NOT during `docker build`. Use `build.inject.after_packages` or `build.inject.after_user_switch` with `ENV` directives instead.

2. **Putting user-space ENV in `inject.after_packages` instead of `inject.after_user_switch`.** The `after_packages` point runs as root before user creation. Put user-specific ENV in `after_user_switch` to keep it near the `user_run` commands that use it.

3. **Forgetting `security.firewall.add_domains` for runtime package registries.** If `post_init` runs `npm install` but `registry.npmjs.org` isn't in the allowlist, it fails. (Note: `registry.npmjs.org` IS in the default list, but other registries like `pypi.org` are not.)

4. **Adding packages that are already pre-installed.** Check Section 3 before adding to `build.packages`. Adding duplicates wastes build time and image space.

5. **Missing `ca-certificates` for HTTPS downloads.** If your base image is minimal (e.g., `buildpack-deps:*-scm`), you may need `ca-certificates` in `build.packages` for `curl`/`wget` to work with HTTPS URLs in `root_run`/`user_run`.

6. **Hardcoding secrets in `agent.env` or `build.inject`.** Secrets baked into the image or config file are visible in `docker inspect` and image layers. Use `agent.from_env` to forward host env vars at runtime.

7. **Expecting `post_init` to run on every container start.** It runs **once** — a marker file prevents re-execution. For commands that must run every start, consider a custom entrypoint.

8. **Not sourcing env in subsequent RUN steps.** Each `RUN` is a new shell. If step 1 installs a tool that modifies the shell profile, step 2 must explicitly source it:
    ```yaml
    user_run:
      - cmd: "curl -sSf https://sh.rustup.rs | sh -s -- -y"
      - cmd: '. "$HOME/.cargo/env" && cargo install foo'    # must source!
    ```

9. **Missing `google` IP range source for Go modules.** `proxy.golang.org` resolves to Google Cloud IPs. Without `ip_range_sources: [{name: google}]`, Go module downloads fail behind the firewall. Also add `golang.org`, `go.dev`, `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` to `add_domains`.

10. **Using `override_domains` without including essential defaults.** `override_domains` replaces the entire allowlist AND skips IP range fetching. You must include `api.anthropic.com` and other Claude Code essentials, or the agent won't function. Prefer `add_domains`/`remove_domains` unless you need full control.

---

## 11. Example References

See sibling files for complete, tested configurations:

- `node.yaml` — Node.js via nvm + pnpm
- `go.yaml` — Go toolchain + dev tools (gopls, delve, golangci-lint)
- `python.yaml` — Python via uv + ruff
- `rust.yaml` — Rust via rustup + cargo-watch
- `php.yaml` — PHP via sury.org + Composer
- `csharp.yaml` — .NET SDK via Microsoft repo
