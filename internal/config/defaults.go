package config

// requiredFirewallRules is the canonical list of required egress rules.
// These are essential for Claude Code operation inside the container.
//
// Docker registry domains (docker.io, registry-1.docker.io, etc.) are NOT
// included because image pulls are performed by the host-side Docker daemon,
// outside the container's network namespace — container egress rules do not
// apply to them.
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
// generated from `default` struct tags on schema types via
// storage.GenerateDefaultsYAML[T](). See schema.go for the tag definitions.
// Consumers use storage.WithDefaultsFromStruct[T]() to inject defaults into
// a Store[T] as a merge layer.

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
