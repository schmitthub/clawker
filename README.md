# Claude Container

Docker containers for running [Claude Code](https://claude.ai/code) in isolated environments with support for multiple programming languages.

## Features

- **Multi-language support**: Node.js/TypeScript, Python, Go, and Rust
- **Alpine Linux base**: Minimal image sizes for efficient storage and transfer
- **Multi-architecture**: Supports linux/amd64 and linux/arm64 (Apple Silicon, AWS Graviton)
- **Portable customizations**: Mount your Claude Code skills, rules, and configurations
- **Automated builds**: GitHub Actions workflow for continuous deployment

## Available Images

All images are built on Alpine Linux for minimal size and include Claude Code pre-installed.

| Image Tag | Description | Typical Size | Included Tools |
|-----------|-------------|--------------|----------------|
| `base` | Base image with Claude Code | ~150-200MB | Node.js, npm, git, bash, curl, wget |
| `nodejs` | Node.js/TypeScript development | ~160-210MB | Base + yarn, pnpm |
| `python` | Python development | ~250-300MB | Base + Python 3, pip, poetry, uv |
| `go` | Go development | ~400-450MB | Base + Go toolchain, gopls, delve |
| `rust` | Rust development | ~500-600MB | Base + Rust, cargo, rust-analyzer, clippy |

## Quick Start

### Pull and Run

Replace `YOUR_USERNAME` with your DockerHub username:

```bash
# Node.js/TypeScript development
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:nodejs

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
  -v ~/.claude:/root/.claude \
  -it YOUR_USERNAME/claude-container:nodejs
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

```bash
# Build all images
make build-all

# Build specific images
make build-base
make build-nodejs
make build-python
make build-go
make build-rust
```

### Test Interactively

```bash
# Test any image interactively
make test-nodejs
make test-python
make test-go
make test-rust
```

### Push to DockerHub

```bash
# Push all images
make push-all

# Push specific images
make push-nodejs
make push-python
```

## GitHub Actions Setup

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

### Node.js Project

```bash
# Run in your Node.js project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:nodejs

# Inside container:
claude-code chat
npm install
npm test
```

### Python Project

```bash
# Run in your Python project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:python

# Inside container:
claude-code chat
pip install -r requirements.txt
pytest
```

### Go Project

```bash
# Run in your Go project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:go

# Inside container:
claude-code chat
go mod download
go test ./...
```

### Rust Project

```bash
# Run in your Rust project directory
docker run -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:rust

# Inside container:
claude-code chat
cargo build
cargo test
```

## Advanced Usage

### Custom Entry Point

Run a specific command instead of interactive bash:

```bash
docker run -v $(pwd):/workspace YOUR_USERNAME/claude-container:nodejs npm test
```

### Environment Variables

Pass environment variables to the container:

```bash
docker run \
  -v $(pwd):/workspace \
  -e NODE_ENV=production \
  -e API_KEY=your-key \
  -it YOUR_USERNAME/claude-container:nodejs
```

### Docker Compose

Create a `docker-compose.yml`:

```yaml
version: '3.8'
services:
  claude-nodejs:
    image: YOUR_USERNAME/claude-container:nodejs
    volumes:
      - .:/workspace
      - ~/.claude:/root/.claude
    working_dir: /workspace
    stdin_open: true
    tty: true
```

Run with:
```bash
docker-compose run claude-nodejs
```

## Multi-Architecture Support

All images support:
- `linux/amd64` (Intel/AMD x86_64)
- `linux/arm64` (Apple Silicon M1/M2/M3, AWS Graviton)

Docker automatically pulls the correct architecture for your system.

## Project Structure

```
claude-container/
├── dockerfiles/           # Dockerfile definitions
│   ├── base.Dockerfile   # Base image with Claude Code
│   ├── nodejs.Dockerfile # Node.js variant
│   ├── python.Dockerfile # Python variant
│   ├── go.Dockerfile     # Go variant
│   └── rust.Dockerfile   # Rust variant
├── .github/
│   └── workflows/
│       └── docker-build.yml  # CI/CD pipeline
├── Makefile              # Build automation
├── .dockerignore         # Docker build exclusions
├── CLAUDE.md             # Claude Code guidance
└── README.md             # This file
```

## Troubleshooting

### Permission Issues

If you encounter permission issues with mounted volumes:

```bash
# Linux: Run with your user ID
docker run -u $(id -u):$(id -g) -v $(pwd):/workspace -it YOUR_USERNAME/claude-container:nodejs
```

### Image Size Concerns

Images are optimized for size using:
- Alpine Linux base
- Multi-stage builds where applicable
- Package cache cleanup
- Minimal dependency installation

If size is critical, use the `base` image and install only what you need.

### Build Failures

Check Docker resources:
- Ensure you have enough disk space
- For Rust builds, allocate at least 4GB RAM to Docker

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
- [Alpine Linux](https://alpinelinux.org/)
