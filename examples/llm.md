# clawker.yaml — LLM Authoring Guide

Reference for LLM agents creating `clawker.yaml` configuration files.

## Schema Structure

```yaml
version: "1"                    # Always "1"
build:                          # Container image build configuration
  image: "base:tag"             # Base Docker image
  packages: [...]               # APT/APK packages to install
  inject:                       # Raw Dockerfile lines at specific insertion points
    after_packages: |           # After package install, before root_run (runs as root)
    after_user_switch: |        # After USER switch, before user_run (runs as claude)
  instructions:                 # Typed build instructions
    root_run:                   # RUN commands as root (system-wide installs)
      - cmd: "..."
    user_run:                   # RUN commands as claude user (user-space tools)
      - cmd: "..."
agent:                          # Container runtime agent settings
  from_env: [...]               # Host env vars forwarded at `docker run`
  env: {}                       # Static env vars set at `docker run`
  post_init: |                  # Shell script run once on first container start
workspace:
  remote_path: "/workspace"     # Mount point inside container
  default_mode: "bind"          # "bind" (live mount) or "snapshot" (ephemeral copy)
security:
  firewall:
    enable: true
    add_domains: [...]          # Extra domains to allow through firewall
    ip_range_sources:           # Cloud provider IP ranges to allow
      - name: github            # Built-in: github, google, google-cloud, cloudflare, aws
  docker_socket: false
  git_credentials:
    forward_https: true
    forward_ssh: true
    forward_gpg: true
    copy_git_config: true
loop:                           # Optional autonomous loop settings
  max_loops: 50
  stagnation_threshold: 3
  timeout_minutes: 15
```

## Critical: inject vs build.instructions.env

`build.instructions.env` sets ENV vars at **container runtime** (`docker run`). These vars do **NOT** exist during `docker build`. Any build step (`root_run`/`user_run`) referencing them will fail.

Use `build.inject.*` to emit `ENV` directives directly into the Dockerfile at build time.

## Dockerfile Template Order

```
FROM base:image
  ↓ inject.after_from
Package installation (apt-get/apk)
  ↓ inject.after_packages          ← ENV vars needed by root_run go here
instructions.root_run              ← runs as root
User creation (claude user, UID 1001)
  ↓ inject.after_user_setup
Directory setup, firewall, tooling
USER claude
Static ENV (PATH, SHELL, TERM, etc.)
  ↓ inject.after_user_switch       ← ENV vars needed by user_run go here
Zsh setup
instructions.user_run              ← runs as claude user
Claude Code installation
  ↓ inject.after_claude_install
HEALTHCHECK
  ↓ inject.before_entrypoint
ENTRYPOINT
```

## Placement Rules

| ENV var used by | Place in |
|---|---|
| `root_run` | `inject.after_packages` |
| `user_run` | `inject.after_user_switch` |
| Container runtime only | `agent.env` or `build.instructions.env` |

## inject Syntax

Each inject point accepts a YAML block scalar (`|`) containing raw Dockerfile lines:

```yaml
build:
  inject:
    after_packages: |
      ENV GO_VERSION="1.24.1"
    after_user_switch: |
      ENV GOPATH="/home/claude/go"
      ENV GOBIN="/home/claude/go/bin"
```

Multiple lines are supported. Each line becomes a Dockerfile instruction.

## Patterns

### System-wide tool install (root_run)

```yaml
build:
  image: "buildpack-deps:bookworm-scm"
  packages: [ca-certificates]
  inject:
    after_packages: |
      ENV TOOL_VERSION="1.0"
  instructions:
    root_run:
      - cmd: |
          curl -fsSL "https://example.com/tool-${TOOL_VERSION}.tar.gz" \
            | tar -C /usr/local -xzf -
```

### User-space tool install (user_run)

```yaml
build:
  inject:
    after_user_switch: |
      ENV TOOL_HOME="/home/claude/.tool"
  instructions:
    user_run:
      - cmd: |
          curl -sSf https://example.com/install.sh | sh
      - cmd: |
          echo 'export PATH="$TOOL_HOME/bin:$PATH"' >> ~/.bashrc
```

### Pre-built image (no inject needed)

When the base image already has the toolchain, skip inject entirely:

```yaml
build:
  image: "golang:1.24-bookworm"
  instructions:
    user_run:
      - cmd: "go install golang.org/x/tools/gopls@latest"
```

### OS-variant commands

```yaml
instructions:
  root_run:
    - alpine: "apk add libfoo-dev"
      debian: "apt-get install -y libfoo-dev"
```

### Firewall domains

Always add domains for package registries your build/runtime depends on:

```yaml
security:
  firewall:
    enable: true
    add_domains:
      - "registry.npmjs.org"
      - "pypi.org"
    ip_range_sources:
      - name: github                     # Always include
      - name: google                     # Only if needed (e.g., proxy.golang.org)
```

### post_init (first-start script)

Runs once on first container start. Use for project-specific setup:

```yaml
agent:
  post_init: |
    cd /workspace
    if [ -f package.json ]; then npm install; fi
```

## Examples

See sibling files for complete, working configurations:

- `node.yaml` — Node.js via nvm + pnpm
- `go.yaml` — Go toolchain + dev tools
- `python.yaml` — Python via uv
- `rust.yaml` — Rust via rustup
- `php.yaml` — PHP via sury.org + Composer
- `csharp.yaml` — .NET SDK via Microsoft repo

## Common Mistakes

1. Using `build.instructions.env` for build-time vars (use `build.inject.*` instead)
2. Putting user-space ENV in `after_packages` (use `after_user_switch` instead — keeps ENV declarations near the `user_run` commands that reference them, improving readability and cache efficiency)
3. Forgetting firewall `add_domains` for package registries
4. Missing `ca-certificates` in packages (needed for HTTPS downloads)
5. Forgetting to quote ENV values containing spaces or special characters (`ENV FOO="bar baz"` not `ENV FOO=bar baz`)
