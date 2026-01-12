package config

// DefaultConfig returns a Config with sensible default values
func DefaultConfig() *Config {
	return &Config{
		Version: "1",
		Build: BuildConfig{
			Image:    "node:20-slim",
			Packages: []string{"git", "curl", "ripgrep"},
		},
		Agent: AgentConfig{
			Includes: []string{},
			Env:      map[string]string{},
		},
		Workspace: WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: SecurityConfig{
			EnableFirewall: true,  // Enabled by default for safety
			DockerSocket:   false, // Disabled by default, opt-in
			CapAdd:         []string{"NET_ADMIN", "NET_RAW"},
		},
	}
}

// DefaultConfigYAML returns the default configuration as YAML for scaffolding
const DefaultConfigYAML = `# Clawker Configuration
# Documentation: https://github.com/schmitthub/clawker

version: "1"
project: "%s"

build:
  # Base image for the container
  image: "node:20-slim"
  # Optional: path to custom Dockerfile (relative to project root)
  # dockerfile: "./.devcontainer/Dockerfile"
  # System packages to install (apt-get on Debian, apk on Alpine)
  packages:
    - git
    - curl
    - ripgrep

agent:
  # Files to make available to Claude (prompts, docs, memory)
  includes:
    - "./README.md"
    # - "./.claude/memory.md"
  # Environment variables for the agent
  env:
    # NODE_ENV: "development"

workspace:
  # Container path where your code is mounted
  remote_path: "/workspace"
  # Default mode: "bind" (live sync) or "snapshot" (isolated copy)
  default_mode: "bind"

security:
  # Enable network firewall (blocks outbound traffic by default)
  enable_firewall: true
  # Mount Docker socket for Docker-in-Docker (security risk if enabled)
  docker_socket: false
  # Domains allowed through firewall (only when enable_firewall: true)
  # allowed_domains:
  #   - "api.github.com"
  #   - "registry.npmjs.org"
`

// DefaultIgnoreFile returns the default .clawkerignore content
const DefaultIgnoreFile = `# Clawker Ignore File
# Files matching these patterns will not be copied in snapshot mode
# Syntax follows .gitignore conventions

# Dependencies
node_modules/
vendor/
.venv/
__pycache__/

# Build outputs
dist/
build/
*.o
*.a
*.so
*.dylib

# IDE and editor files
.idea/
.vscode/
*.swp
*.swo
*~

# OS files
.DS_Store
Thumbs.db

# Git
.git/

# Secrets (never copy these)
.env
.env.*
*.pem
*.key
credentials.json

# Large files
*.zip
*.tar
*.tar.gz
*.tgz
*.rar
*.7z

# Logs
*.log
logs/
`
