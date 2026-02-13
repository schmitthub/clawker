package config

// DefaultFirewallDomains is the default list of domains allowed through the firewall.
// These are essential for Claude Code and common development tools.
var DefaultFirewallDomains = []string{
	"registry.npmjs.org",
	"api.anthropic.com",
	"sentry.io",
	"statsig.anthropic.com",
	"statsig.com",
	"marketplace.visualstudio.com",
	"vscode.blob.core.windows.net",
	"update.code.visualstudio.com",
	"registry-1.docker.io",
	"production.cloudflare.docker.com",
	"docker.io",
}

// DefaultConfig returns a Config with sensible default values
func DefaultConfig() *Project {
	return &Project{
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
			Firewall: &FirewallConfig{
				Enable: true, // Enabled by default for safety
			},
			DockerSocket: false, // Disabled by default, opt-in
			CapAdd:       []string{"NET_ADMIN", "NET_RAW"},
		},
	}
}

// TODO: making these dynamically generated while still maintaining commented
// sections is tricky. For now, we use static strings with placeholders.

// DefaultConfigYAML returns the default configuration as YAML for scaffolding
const DefaultConfigYAML = `# Clawker Configuration
# Documentation: https://github.com/schmitthub/clawker

version: "1"

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
  # Load environment variables from files (Docker env-file format)
  # env_file:
  #   - ".env"
  #   - "~/.secrets/api-keys.env"
  # Pass host environment variables by name (emits a warning if unset)
  # from_env:
  #   - "ANTHROPIC_API_KEY"
  #   - "GITHUB_TOKEN"
  # Static environment variables for the agent
  env:
    # NODE_ENV: "development"
  # Claude Code configuration
  # claude_code:
  #   config:
  #     # "copy" copies host ~/.claude/ config (default), "fresh" starts clean
  #     strategy: "copy"
  #   # Use host authentication tokens in container
  #   use_host_auth: true
  # Enable shared directory (read-only, mounted at ~/.clawker-share)
  # enable_shared_dir: false
  # Shell commands to run once inside the container after initialization.
  # Runs before the main process starts (with set -e). Any failure aborts container startup.
  # post_init: |
  #   claude mcp add -- npx -y @anthropic-ai/claude-code-mcp
  #   npm install -g typescript

workspace:
  # Container path where your code is mounted
  remote_path: "/workspace"
  # Default mode: "bind" (live sync) or "snapshot" (isolated copy)
  default_mode: "bind"

security:
  # Network firewall configuration
  firewall:
    # Enable network firewall (blocks outbound traffic by default)
    enable: true
    # Add domains to the default allowed list
    # add_domains:
    #   - "api.openai.com"
    # Remove domains from the default allowed list
    # remove_domains:
    #   - "registry.npmjs.org"
    # Override the entire allowed list (ignores add/remove, skips GitHub IP fetching)
    # override_domains:
    #   - "api.anthropic.com"
    #   - "api.github.com"
  # Mount Docker socket for Docker-in-Docker (security risk if enabled)
  docker_socket: false

# Autonomous loop settings (clawker loop iterate / clawker loop tasks)
# loop:
#   max_loops: 50                     # Maximum iterations per session
#   stagnation_threshold: 3           # Iterations without progress before circuit trips
#   timeout_minutes: 15               # Per-iteration timeout
#   loop_delay_seconds: 3             # Delay between iterations
#   calls_per_hour: 100               # API rate limit (0 to disable)
#   skip_permissions: false            # Allow all tools without prompting
#   hooks_file: ""                     # Custom hooks file (overrides defaults)
#   append_system_prompt: ""           # Additional system prompt instructions
#   # Circuit breaker tuning
#   same_error_threshold: 5            # Consecutive identical errors before trip
#   output_decline_threshold: 70       # Output shrink percentage before trip
#   max_consecutive_test_loops: 3      # Test-only iterations before trip
#   safety_completion_threshold: 5     # Completion indicators without exit signal before trip
#   completion_threshold: 2            # Indicators required for strict completion
#   session_expiration_hours: 24       # Session TTL
`

// DefaultSettingsYAML returns the default settings template for new users
const DefaultSettingsYAML = `# Clawker User Settings
# Documentation: https://github.com/schmitthub/clawker

# Logging configuration
# logging:
#   file_enabled: true
#   max_size_mb: 50
#   max_age_days: 7
#   max_backups: 3
`

// DefaultRegistryYAML returns the default registry template
const DefaultRegistryYAML = `# Clawker Project Registry
# Managed by 'clawker init' — do not edit manually
projects: {}
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
