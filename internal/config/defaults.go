package config

// RequiredFirewallDomains is the default list of domains allowed through the firewall.
// These are essential for Claude Code and common development tools.
var RequiredFirewallDomains = []string{
	"api.anthropic.com",
	"sentry.io",
	"statsig.anthropic.com",
	"statsig.com",
	"registry-1.docker.io",
	"production.cloudflare.docker.com",
	"docker.io",
}

// DefaultProject returns a Project with sensible default values
func DefaultProject() *Project {
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

// DefaultSettings returns a Settings with sensible default values.
// This is the single source of truth for all settings defaults.
func DefaultSettings() *Settings {
	return &Settings{
		Logging: LoggingConfig{
			FileEnabled: boolPtr(true),
			MaxSizeMB:   50,
			MaxAgeDays:  7,
			MaxBackups:  3,
			Compress:    boolPtr(true),
			Otel: OtelConfig{
				Enabled:               boolPtr(true),
				TimeoutSeconds:        5,
				MaxQueueSize:          2048,
				ExportIntervalSeconds: 5,
			},
		},
		Monitoring: MonitoringConfig{
			OtelCollectorPort:     4318,
			OtelCollectorHost:     "localhost",
			OtelCollectorInternal: "otel-collector",
			OtelGRPCPort:          4317,
			LokiPort:              3100,
			PrometheusPort:        9090,
			JaegerPort:            16686,
			GrafanaPort:           3000,
			PrometheusMetricsPort: 8889,
			Telemetry: TelemetryConfig{
				MetricsPath:            "/v1/metrics",
				LogsPath:               "/v1/logs",
				MetricExportIntervalMs: 10000,
				LogsExportIntervalMs:   5000,
				LogToolDetails:         boolPtr(true),
				LogUserPrompts:         boolPtr(true),
				IncludeAccountUUID:     boolPtr(true),
				IncludeSessionID:       boolPtr(true),
			},
		},
	}
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

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
#   compress: true
#   otel:
#     enabled: true
#     timeout_seconds: 5
#     max_queue_size: 2048
#     export_interval_seconds: 5

# Monitoring stack ports (override if defaults conflict)
# monitoring:
#   otel_collector_port: 4318
#   otel_collector_host: localhost
#   otel_collector_internal: otel-collector
#   otel_grpc_port: 4317
#   grafana_port: 3000
#   jaeger_port: 16686
#   prometheus_port: 9090
#   loki_port: 3100
#   prometheus_metrics_port: 8889
#   telemetry:
#     # URL paths appended to collector URL for per-signal OTLP endpoints
#     # Constructed as: http://<otel_collector_internal>:<otel_collector_port><path>
#     metrics_path: "/v1/metrics"
#     logs_path: "/v1/logs"
#     metric_export_interval_ms: 10000
#     logs_export_interval_ms: 5000
#     log_tool_details: true
#     log_user_prompts: true
#     include_account_uuid: true
#     include_session_id: true
`

// DefaultRegistryYAML returns the default registry template
const DefaultRegistryYAML = `# Clawker ProjectCfg Registry
# Managed by 'clawker init' — do not edit manually
projects: {}
`

// DefaultIgnoreFile returns the default .clawkerignore content
const DefaultIgnoreFile = `# Clawker Ignore File
# Snapshot mode: matching files/directories are excluded from the copy
# Bind mode: matching directories are masked with empty tmpfs overlays
#            (file-level patterns like *.env cannot be enforced in bind mode)
# Syntax is similar to .gitignore (negation patterns not yet supported)

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
