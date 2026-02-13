# Clawker

<p align="center">
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License"></a>
  <a href="#"><img src="https://img.shields.io/badge/Platform-macOS-lightgrey?logo=apple" alt="macOS"></a>
  <a href="#"><img src="https://img.shields.io/badge/Claude-D97757?logo=claude&logoColor=fff" alt="Claude"></a>
</p>

<!-- TODO: Add background blurb -->

Claude Code in YOLO mode can wreak havoc on your system. Setting up Docker manually is tedious — Dockerfiles, volumes, networking. OAuth doesn't work because container localhost isn't host localhost. Git credentials from your keychain don't exist inside containers. And you have no visibility into what's happening.

**Clawker** (claude + docker) wraps Claude Code in Docker containers with a familiar CLI. It handles auth seamlessly via a host proxy, forwards your git credentials, and provides optional monitoring — so you can let Claude Code loose without worrying about your system.

> **Status:** Alpha (macOS tested). Issues and PRs welcome — if clawker helps you, please star the repo.

## Features

- Containerized Claude Code sessions with familiar Docker CLI
- Per-project image building from `clawker.yaml`
- Per-agent isolation — multiple agents, no interference
- Seamless auth: host proxy bridges OAuth for subscription users
- Git credential forwarding (HTTPS, SSH — zero config)
- Network firewall (outbound blocked by default, allowlist domains)
- Loop: autonomous loop engine with circuit breaker protection
- Optional monitoring stack (Prometheus + Grafana)
- Bind or snapshot workspace modes
- Label-based resource isolation (clawker never touches your other Docker resources)

## Quick Start

**Prerequisites:** Docker running, Go 1.25+

```bash
# Install
git clone https://github.com/schmitthub/clawker.git
cd clawker && go build -o ./bin/clawker ./cmd/clawker
export PATH="$PWD/bin:$PATH"

# One-time user setup
clawker init

# Start a project
cd your-project
clawker project init    # Creates clawker.yaml
# Customize your clawker.yaml (see examples/ for language-specific configs)
clawker build           # Build your project image
```

## Cheatsheet

### Creating and Using Containers

```bash
# Create a fresh container and connect interactively
# The @ symbol auto-resolves your project image (clawker-<project>:latest)
clawker run -it --agent main @

# Detach without stopping: Ctrl+P, Ctrl+Q

# Re-attach to the agent
clawker attach --agent main

# Stop the agent (Ctrl+C exits Claude Code and stops the container)
# Or from another terminal:
clawker stop --agent main

# Start a stopped agent and attach
clawker start -a -i --agent main
```

### The `@` Image Shortcut

Use `@` anywhere an image argument is expected to auto-resolve your project's image:

```bash
clawker run -it @                     # Uses clawker-<project>:latest
clawker run -it --agent dev @         # Same, with agent name
clawker container create --agent test @
```

### Working with Worktrees

Run separate agents per git worktree for parallel development:

```bash
# Create worktrees
git worktree add ../myapp-feature -b feature/auth
git worktree add ../myapp-tests -b feature/tests

# Each worktree gets its own agent containers
cd ../myapp-feature && clawker run -it --agent feature @
cd ../myapp-tests && clawker run -it --agent tests @
```

### Managing Resources

```bash
clawker ps                          # List all clawker containers
clawker container ls                # Same thing
clawker container stop --agent NAME
clawker container logs --agent NAME
clawker image ls                    # List clawker images
clawker volume ls                   # List clawker volumes
```

### Autonomous Loops (Loop)

Loop runs Claude Code in autonomous loops with stagnation detection and circuit breaker protection:

```bash
clawker loop run --agent dev --prompt "Fix all failing tests"
clawker loop status --agent dev
clawker loop reset --agent dev
```

See `clawker loop --help` for all options and configuration.

### Scripted Workflows

```bash
# Keep a container running and send prompts via exec
clawker run -it --agent worker @ -- --dangerously-skip-permissions
# Detach with Ctrl+P, Ctrl+Q

echo "Fix the tests" | clawker exec -i --agent worker claude -p
clawker stop --agent worker
```

### Passing Claude Code Options

Use `--` to separate clawker flags from Claude Code flags:

```bash
clawker run -it --agent dev @ -- --dangerously-skip-permissions
```

### Monitoring

```bash
clawker monitor start     # Starts Prometheus + Grafana
clawker monitor stop
clawker monitor status
# Dashboard at http://localhost:3000
```

## Customizing Your Build

The default image includes essentials (git, curl, vim, zsh, ripgrep). Customize for your stack in `clawker.yaml`:

```yaml
version: "1"
project: "my-react-app"
build:
  image: "buildpack-deps:bookworm-scm"
  packages: [git, curl, ripgrep]
  instructions:
    env:
      NVM_DIR: "/home/claude/.nvm"
    user_run:
      - cmd: |
          curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
      - cmd: |
          . "$NVM_DIR/nvm.sh" && nvm install 22 && npm install -g pnpm
```

See [`examples/`](./examples/) for complete configs: TypeScript, Python, Rust, Go, C#, PHP.

## CLI Reference

Complete command documentation with all flags and examples: [`docs/cli-reference/`](./docs/cli-reference/)

## Known Issues

See [GitHub Issues](https://github.com/schmitthub/clawker/issues?q=is%3Aissue+is%3Aopen+label%3Aknown-issue) for current known issues and limitations.

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and PR process.

Please read our [Code of Conduct](CODE_OF_CONDUCT.md) before participating.

## License

MIT — see [LICENSE](LICENSE)
