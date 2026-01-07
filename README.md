# Claude Container

Docker containers for running [Claude Code](https://claude.ai/code) in isolated environments with support for multiple programming languages.

## Features

- **Multi-language support**: Node.js/TypeScript, Python, Go, and Rust
- **Debian Bookworm base**: Modern, stable Linux distribution with comprehensive tooling
- **Multi-architecture**: Supports linux/amd64 and linux/arm64 (Apple Silicon, AWS Graviton)
- **Network isolation**: Optional firewall for controlled outbound access
- **Enhanced shell**: zsh with Oh My Zsh for improved developer experience
- **Portable customizations**: Mount your Claude Code skills, rules, and configurations
- **Automated builds**: GitHub Actions workflow for continuous deployment

## Available Images

All images are built on Debian Bookworm and include Claude Code pre-installed.

| Image Tag | Description | Included Tools |
|-----------|-------------|----------------|
| `base` | Base image with Claude Code | Node.js, npm, Claude Code, git, zsh, Oh My Zsh, git-delta, fzf, gh |
| `node` | Node.js/TypeScript development | Base + yarn, pnpm |
| `python` | Python development | Base + Python 3, pip, poetry, uv |
| `go` | Go development | Base + Go toolchain |
| `rust` | Rust development | Base + Rust, cargo, rust-analyzer, clippy |

**Note**: Image tag is `node` (not `nodejs`) to match the Dockerfile build target.

## Quick Start

## Installing claucker (Recommended)

`claucker` is a convenience wrapper script that simplifies running Claude Code containers.

### Quick Install

**Using curl:**

```bash
curl -fsSL https://raw.githubusercontent.com/schmitthub/claude-container/main/claucker -o claucker && chmod +x claucker
```

**Using wget:**

```bash
wget -O claucker https://raw.githubusercontent.com/schmitthub/claude-container/main/claucker && chmod +x claucker
```

**Manual installation:**

1. Download the script from the repository
2. Make it executable: `chmod +x claucker`
3. Optionally, add to PATH or create a symlink:

   ```bash
   # Add to PATH
   export PATH=$PATH:/path/to/claucker/directory

   # Or create symlink
   sudo ln -s /path/to/claucker /usr/local/bin/claucker
   ```

### Prerequisites

Set your DockerHub username (or the username where images are hosted):

```bash
export DOCKER_USERNAME=your-dockerhub-username
```

Or create a `.env` file in the same directory as claucker:

```bash
echo "DOCKER_USERNAME=your-dockerhub-username" > .env
```

### Usage Examples

**Basic usage (interactive base image):**

```bash
./claucker
```

**Node.js development with user configs:**

```bash
./claucker --type node --user-claude
```

**Python tests with firewall:**

```bash
./claucker -t python -f pytest tests/
```

**Run command with environment variables:**

```bash
./claucker -t node -- --env NODE_ENV=production -- npm start
```

**All available options:**

```bash
./claucker --help
```

### claucker Options

- `-t, --type TYPE` - Container type: base, node, python, go, rust (default: base)
- `-p, --project PATH` - Project directory to mount (default: current directory)
- `-u, --user-claude` - Mount ~/.claude to container
- `-s, --system-claude PATH` - Mount custom system config directory
- `-f, --firewall` - Enable network isolation
- `-n, --name NAME` - Container name
- `--no-rm` - Don't auto-remove container
- `-v, --verbose` - Show docker command before running
- `-d, --debug` - Enable debug output (implies verbose)
- `-h, --help` - Show help message

### Why Use claucker?

- **Simpler syntax**: `claucker -t node` vs `docker run -v $(pwd):/workspace -it USER/claude-container:node`
- **Smart defaults**: Automatically mounts current directory, handles image naming
- **Built-in features**: Firewall integration, config mounting, verbose mode, debug mode
- **Cross-platform**: Works on macOS and Linux without GNU getopt
- **Security**: Validates .env file permissions, prevents command injection

### Pull and Run

**Note**: If you installed claucker, you can use `claucker -t TYPE` instead of the full docker run commands below.

Replace `YOUR_USERNAME` with your DockerHub username:

```bash
# Node.js/TypeScript development
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:node

# Python development
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:python

# Go development
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:go

# Rust development
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:rust
```

### With Custom Claude Code Configurations

Mount your local Claude Code configuration directory:

```bash
docker run \
  -v $(pwd):/workspace \
  -v ~/.claude:/home/claude/.claude \
  -it YOUR_USERNAME/claude-container:node
```

This allows you to use your:

- Custom skills
- Project-specific rules
- Global configurations
- API keys and settings

## Building Locally

### Prerequisites

1. Docker installed and running
2. Set your DockerHub username:

   ```bash
   export DOCKER_USERNAME=your-dockerhub-username
   ```

### Build Commands

All images are built from a single multi-stage Dockerfile at `claude-container/Dockerfile`:

```bash
# Build all images
make build-all

# Build specific images
make build-base
make build-node
make build-python
make build-go
make build-rust
```

Manual build examples:

```bash
# Build base image
docker build -t $DOCKER_USERNAME/claude-container:base \
  --target base \
  -f claude-container/Dockerfile .

# Build Node.js image
docker build -t $DOCKER_USERNAME/claude-container:node \
  --target node \
  -f claude-container/Dockerfile .
```

### Test Interactively

```bash
# Test any image interactively
make test-node
make test-python
make test-go
make test-rust
```

### Push to DockerHub

```bash
# Push all images
make push-all

# Push specific images
make push-node
make push-python
make push-go
make push-rust
```

## GitHub Actions Setup

⚠️ **Note**: The GitHub Actions workflow is currently being refactored to implement selective builds based on changed files.

To enable automated builds and deployment to DockerHub:

### 1. Create DockerHub Access Token

1. Go to [DockerHub](https://hub.docker.com)
2. Navigate to Account Settings → Security
3. Click "New Access Token"
4. Give it a name (e.g., "claude-container-github")
5. Copy the token (you won't see it again)

### 2. Add GitHub Secrets

In your GitHub repository:

1. Go to Settings → Secrets and variables → Actions
2. Add two secrets:
   - `DOCKERHUB_USERNAME`: Your DockerHub username
   - `DOCKERHUB_TOKEN`: The access token from step 1

### 3. Trigger Builds

The workflow automatically runs on:

- Push to `main` branch
- Pull requests
- Manual trigger via "Actions" tab

## Usage Examples

**Note**: These examples show direct Docker commands. If you're using claucker, see the [Installing claucker](#installing-claucker-recommended) section for simpler syntax.

### Node.js Project

```bash
# Run in your Node.js project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:node

# Inside container:
claude
npm install
npm test
```

### Python Project

```bash
# Run in your Python project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:python

# Inside container:
claude
pip install -r requirements.txt
pytest
```

### Go Project

```bash
# Run in your Go project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:go

# Inside container:
claude
go mod download
go test ./...
```

### Rust Project

```bash
# Run in your Rust project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:rust

# Inside container:
claude
cargo build
cargo test
```

## Advanced Usage

### Custom Entry Point

The container includes a smart entry point (`docker-entrypoint.sh`) that automatically wraps commands with `claude`:

```bash
# Run a command directly - automatically wrapped with claude
docker run -v $(pwd):/workspace YOUR_USERNAME/claude-container:node npm test

# The entry point converts this to: claude npm test
```

### Network Isolation (Firewall)

For enhanced security, the containers include an optional firewall script that restricts outbound network access:

```bash
# Run with firewall enabled
docker run --cap-add=NET_ADMIN \
  -v $(pwd):/workspace \
  -it YOUR_USERNAME/claude-container:node \
  bash -c "sudo /usr/local/bin/init-firewall.sh && claude"
```

**Allowed domains**:

- GitHub (github.com, api.github.com, objects.githubusercontent.com)
- npm registry (registry.npmjs.org)
- Anthropic API (api.anthropic.com)
- Statsig (api.statsig.com)
- VS Code Marketplace (marketplace.visualstudio.com)

**Blocked**: All other outbound connections

**Testing the firewall**:

```bash
# Should be blocked
curl example.com

# Should work
curl https://api.github.com
curl https://registry.npmjs.org
```

### Environment Variables

Pass environment variables to the container:

```bash
docker run \
  -v $(pwd):/workspace \
  -e NODE_ENV=production \
  -e API_KEY=your-key \
  -it YOUR_USERNAME/claude-container:node
```

### Docker Compose

Create a `docker-compose.yml`:

```yaml
version: '3.8'
services:
  claude-node:
    image: YOUR_USERNAME/claude-container:node
    volumes:
      - .:/workspace
      - ~/.claude:/home/claude/.claude
    working_dir: /workspace
    stdin_open: true
    tty: true
```

Run with:

```bash
docker-compose run claude-node
```

## Multi-Architecture Support

All images support:

- `linux/amd64` (Intel/AMD x86_64)
- `linux/arm64` (Apple Silicon M1/M2/M3, AWS Graviton)

Docker automatically pulls the correct architecture for your system.

## Project Structure

```
claude-container/
├── claude-container/
│   ├── Dockerfile            # Multi-stage Dockerfile with all build targets
│   ├── docker-entrypoint.sh  # Smart entry point wrapper
│   └── init-firewall.sh      # Network isolation script
├── .github/
│   └── workflows/
│       └── build-test.yml    # CI/CD pipeline (in progress)
├── genMatrix.js              # Build matrix generator (needs rewrite)
├── Makefile                  # Build automation
├── .dockerignore             # Docker build exclusions
├── CLAUDE.md                 # Claude Code guidance
└── README.md                 # This file
```

## Technical Details

- **Base OS**: Debian Bookworm (bookworm)
- **User**: `claude` (uid: 1001, gid: 1001)
- **Working Directory**: `/workspace`
- **Shell**: zsh with Oh My Zsh (agnoster theme)
- **Entry Point**: `/usr/local/bin/docker-entrypoint.sh`
- **Default Command**: `claude` (interactive mode)

## Troubleshooting

### Permission Issues

If you encounter permission issues with mounted volumes:

```bash
# Linux: Run with your user ID
docker run -u $(id -u):$(id -g) -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:node
```

Note: The container runs as user `claude` (uid 1001, gid 1001) by default. If your host user has a different UID, you may need to adjust permissions or use the `-u` flag.

### Wrong Mount Path

If your Claude Code configs aren't loading, ensure you're mounting to the correct path:

```bash
# Correct path
-v ~/.claude:/home/claude/.claude

# Incorrect paths (don't use these)
-v ~/.claude:/root/.claude
-v ~/.claude:/claude/.claude
```

### Firewall Not Working

If the firewall doesn't block traffic:

1. Ensure you're using `--cap-add=NET_ADMIN`
2. Run the firewall script as root before starting Claude
3. Verify iptables rules: `sudo iptables -L -n -v`

### Build Failures

Check Docker resources:

- Ensure you have enough disk space
- For Rust builds, allocate at least 4GB RAM to Docker
- Debian images are larger than Alpine - ensure sufficient storage

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test locally with `make build-all`
5. Submit a pull request

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Related Links

- [Claude Code Documentation](https://claude.ai/code)
- [Docker Documentation](https://docs.docker.com/)
- [Debian](https://www.debian.org/)
