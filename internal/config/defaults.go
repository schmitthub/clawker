package config

import "github.com/schmitthub/clawker/internal/storage"

// requiredFirewallRules is the canonical list of required egress rules.
// These are essential for Claude Code and container image pulls.
//
// Claude Code OAuth requires platform.claude.com (token exchange) and
// claude.ai (alternative authorize URL). These use SNI-based filtering,
// so each domain must be listed explicitly even if they share IPs with
// api.anthropic.com.
var requiredFirewallRules = []EgressRule{
	// Claude Code — API and OAuth
	{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	{Dst: "platform.claude.com", Proto: "tls", Port: 443, Action: "allow"},
	{Dst: "claude.ai", Proto: "tls", Port: 443, Action: "allow"},
	// Claude Code — telemetry
	{Dst: "sentry.io", Proto: "tls", Port: 443, Action: "allow"},
	{Dst: "statsig.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	{Dst: "statsig.com", Proto: "tls", Port: 443, Action: "allow"},
	// Container image pulls
	{Dst: "registry-1.docker.io", Proto: "tls", Port: 443, Action: "allow"},
	{Dst: "production.cloudflare.docker.com", Proto: "tls", Port: 443, Action: "allow"},
	{Dst: "docker.io", Proto: "tls", Port: 443, Action: "allow"},
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

// Programmatic base-layer defaults for project and settings configuration are
// now generated from `default` struct tags on schema types via
// storage.GenerateDefaultsYAML[T](). See schema.go for the tag definitions.
// The legacy YAML constants (defaultProjectYAML, defaultSettingsYAML) have
// been removed — struct tags are the single source of truth.

// NewProjectWithDefaults returns a Project populated with all default-tagged
// values from struct tags. Used by init scaffolding — callers can override
// fields before marshaling to YAML.
func NewProjectWithDefaults() *Project {
	store, err := storage.NewFromString[Project](storage.GenerateDefaultsYAML[Project]())
	if err != nil {
		panic("config.NewProjectWithDefaults: " + err.Error())
	}
	return store.Read()
}

// NewSettingsWithDefaults returns a Settings populated with all default-tagged
// values from struct tags.
func NewSettingsWithDefaults() *Settings {
	store, err := storage.NewFromString[Settings](storage.GenerateDefaultsYAML[Settings]())
	if err != nil {
		panic("config.NewSettingsWithDefaults: " + err.Error())
	}
	return store.Read()
}

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
