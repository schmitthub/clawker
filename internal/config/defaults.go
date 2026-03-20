package config

// requiredFirewallRules is the canonical list of required egress rules.
// These are essential for Claude Code and container image pulls.
//
// Claude Code OAuth requires platform.claude.com (token exchange) and
// claude.ai (alternative authorize URL). These use SNI-based filtering,
// so each domain must be listed explicitly even if they share IPs with
// api.anthropic.com.
var requiredFirewallRules = []EgressRule{
	// Claude Code — API and OAuth
	{Dst: "api.anthropic.com", Proto: "tls", Action: "allow"},
	{Dst: "platform.claude.com", Proto: "tls", Action: "allow"},
	{Dst: "claude.ai", Proto: "tls", Action: "allow"},
	// Claude Code — telemetry
	{Dst: "sentry.io", Proto: "tls", Action: "allow"},
	{Dst: "statsig.anthropic.com", Proto: "tls", Action: "allow"},
	{Dst: "statsig.com", Proto: "tls", Action: "allow"},
	// Container image pulls
	{Dst: "registry-1.docker.io", Proto: "tls", Action: "allow"},
	{Dst: "production.cloudflare.docker.com", Proto: "tls", Action: "allow"},
	{Dst: "docker.io", Proto: "tls", Action: "allow"},
}

// requiredFirewallDomains is derived from requiredFirewallRules for backwards compatibility.
//
// Deprecated: Use RequiredFirewallRules() instead.
var requiredFirewallDomains []string

func init() {
	requiredFirewallDomains = make([]string, len(requiredFirewallRules))
	for i, r := range requiredFirewallRules {
		requiredFirewallDomains[i] = r.Dst
	}
}

// defaultProjectYAML is the base-layer defaults for project configuration.
// Always loaded via storage.WithDefaults() in NewConfig() as the lowest
// priority layer — guarantees critical values like security.firewall.enable
// are present even with zero files on disk.
const defaultProjectYAML = `
build:
  packages:
    - git
    - curl
    - ripgrep

agent:
  includes: []
  env: {}

workspace:
  default_mode: "bind"

security:
  docker_socket: false
  cap_add:
    - NET_ADMIN
    - NET_RAW
`

// defaultSettingsYAML is the base-layer defaults for settings configuration.
// Always loaded via storage.WithDefaults() in NewConfig() as the lowest
// priority layer.
const defaultSettingsYAML = `
logging:
  file_enabled: true
  max_size_mb: 50
  max_age_days: 7
  max_backups: 3
  compress: true
  otel:
    enabled: true
    timeout_seconds: 5
    max_queue_size: 2048
    export_interval_seconds: 5

host_proxy:
  manager:
    port: 18374
  daemon:
    port: 18374
    poll_interval: 30s
    grace_period: 60s
    max_consecutive_errs: 10

firewall:
  enable: true

monitoring:
  otel_collector_port: 4318
  otel_collector_host: "localhost"
  otel_collector_internal: "otel-collector"
  otel_grpc_port: 4317
  loki_port: 3100
  prometheus_port: 9090
  jaeger_port: 16686
  grafana_port: 3000
  prometheus_metrics_port: 8889
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    metric_export_interval_ms: 10000
    logs_export_interval_ms: 5000
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`

// DefaultConfigYAML is the commented template written to disk by clawker init.
// This is a user-facing scaffold — different from defaultProjectYAML which is
// the programmatic base layer. The template contains comments to guide users.
const DefaultConfigYAML = `# Clawker Configuration
# Documentation: https://github.com/schmitthub/clawker

build:
  #image: "buildpack-deps:bookworm-scm"
  packages: # apt-get on Debian, apk on Alpine
    - git
    - curl
    - ripgrep

agent:
  #env_file: # load env vars from files (Docker env-file format)
  #  - ".env"
  #  - "~/.secrets/api-keys.env"
  #from_env: # passthrough host env vars by name (warns if unset)
  #  - "ANTHROPIC_API_KEY"
  #  - "GITHUB_TOKEN"
  env: {}
  claude_code:
    config:
      strategy: "copy" # "copy" (host ~/.claude/) or "fresh" (clean)
    use_host_auth: true # keyring or ~/.claude/.credentials.json
  enable_shared_dir: false # read-only mount at ~/.clawker-share
  #post_init: | # runs once after init (set -e), failure aborts startup
  #  claude mcp add -- npx -y @anthropic-ai/claude-code-mcp
  #  npm install -g typescript

workspace:
  default_mode: "bind" # "bind" (live sync) or "snapshot" (isolated copy)

security:
  firewall:
    add_domains: # additional allowed domains
      - "github.com"
      - "gitlab.com"
      - "bitbucket.org"
  docker_socket: false # mount Docker socket (security risk if enabled)

#loop: # autonomous loop settings (clawker loop iterate / clawker loop tasks)
#  max_loops: 50 # maximum iterations per session
#  stagnation_threshold: 3 # iterations without progress before circuit trips
#  timeout_minutes: 15 # per-iteration timeout
#  loop_delay_seconds: 3 # delay between iterations
#  calls_per_hour: 100 # API rate limit (0 to disable)
#  skip_permissions: false # allow all tools without prompting
#  hooks_file: "" # custom hooks file (overrides defaults)
#  append_system_prompt: "" # additional system prompt instructions
#  same_error_threshold: 5 # consecutive identical errors before trip
#  output_decline_threshold: 70 # output shrink percentage before trip
#  max_consecutive_test_loops: 3 # test-only iterations before trip
#  safety_completion_threshold: 5 # completion indicators without exit before trip
#  completion_threshold: 2 # indicators required for strict completion
#  session_expiration_hours: 24 # session TTL
`

// DefaultSettingsYAML is the commented template written to disk by clawker init.
const DefaultSettingsYAML = `# Clawker User Settings
# Documentation: https://github.com/schmitthub/clawker

firewall:
  enable: true # enables container network firewall

#logging:
#  file_enabled: true
#  max_size_mb: 50
#  max_age_days: 7
#  max_backups: 3
#  compress: true
#  otel:
#    enabled: true
#    timeout_seconds: 5
#    max_queue_size: 2048
#    export_interval_seconds: 5

#host_proxy:
#  manager:
#    port: 18374
#  daemon:
#    port: 18374
#    poll_interval: 30s
#    grace_period: 60s
#    max_consecutive_errs: 10

#monitoring: # override if defaults conflict
#  otel_collector_port: 4318
#  otel_collector_host: localhost
#  otel_collector_internal: otel-collector
#  otel_grpc_port: 4317
#  grafana_port: 3000
#  jaeger_port: 16686
#  prometheus_port: 9090
#  loki_port: 3100
#  prometheus_metrics_port: 8889
#  telemetry:
#    metrics_path: "/v1/metrics" # appended to http://<collector>:<port>
#    logs_path: "/v1/logs"
#    metric_export_interval_ms: 10000
#    logs_export_interval_ms: 5000
#    log_tool_details: true
#    log_user_prompts: true
#    include_account_uuid: true
#    include_session_id: true
`

// DefaultRegistryYAML is the commented template for the project registry.
const DefaultRegistryYAML = `# Clawker ProjectCfg Registry
# Managed by 'clawker init' — do not edit manually
projects: []
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

`
