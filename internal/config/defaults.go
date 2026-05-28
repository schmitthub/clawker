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
// claude.ai (authorization/downloads). SNI matching selects per-domain TLS
// filter chains in Envoy, so each domain must be listed explicitly even if
// they share IPs with api.anthropic.com.
var requiredFirewallRules = []EgressRule{
	// Claude Code — API and OAuth
	{Dst: "api.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	{Dst: "claude.com", Proto: "https", Port: 443, Action: "allow"},
	{Dst: "platform.claude.com", Proto: "https", Port: 443, Action: "allow"},
	// .claude.ai serves both Claude Code OAuth + Anthropic-hosted UGC. The
	// host-scope allow is required for login; the explicit deny PathRules
	// scope out documented UGC surfaces so an injected prompt can't pivot
	// an agent into fetching attacker-authored content from a trusted
	// origin (public artifacts render HTML/JS; shared chats are UGC by
	// definition). PathDefault is left empty so EffectivePathDefault
	// returns "allow" — denylist mode keeps OAuth/login flows intact under
	// `/` and `/login` (the only Allow patterns in claude.ai's robots.txt).
	{
		Dst: ".claude.ai", Proto: "https", Port: 443, Action: "allow",
		PathRules: []PathRule{
			{Path: "/public/", Action: "deny"},
			{Path: "/share/", Action: "deny"},
		},
	},
	// Claude Code — MCP proxy
	{Dst: "mcp-proxy.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	// Node.js / npm — registry for `npm install -g` (presets + user_run) and
	// runtime hook deps. Node is baked into every image; without registry
	// access the unprivileged user cannot install global packages.
	{Dst: "registry.npmjs.org", Proto: "https", Port: 443, Action: "allow"},
	// Claude Code — telemetry
	{Dst: "sentry.io", Proto: "https", Port: 443, Action: "allow"},
	{Dst: "statsig.anthropic.com", Proto: "https", Port: 443, Action: "allow"},
	{Dst: "statsig.com", Proto: "https", Port: 443, Action: "allow"},
	{Dst: ".datadoghq.com", Proto: "https", Port: 443, Action: "allow"},
	{Dst: ".datadoghq.eu", Proto: "https", Port: 443, Action: "allow"},
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

// DefaultIgnoreFile is the default .clawkerignore content.
// All entries are commented out — users opt in to what they need.
const DefaultIgnoreFile = `# Clawker Ignore File
#
# In bind mode, listed directories are masked with empty tmpfs overlays
# so the host's platform-specific binaries (e.g. macOS Darwin node_modules/.bin)
# don't bleed into the Linux container. The container installs its own
# dependencies into the tmpfs, which is ephemeral.
#
# In snapshot mode, listed directories are simply excluded from the copy —
# they don't exist in the container at all, allowing it to create its own.
#
# Syntax is similar to .gitignore. Negation patterns are not yet supported.
# File-level patterns (*.env, *.pem) cannot be enforced in bind mode —
# only directory-level masking works.
#
# Uncomment the lines relevant to your stack:

# ── JavaScript / TypeScript ──
# node_modules/
# .next/
# .nuxt/

# ── Python ──
# .venv/
# __pycache__/
# .mypy_cache/

# ── Go ──
# vendor/

# ── Ruby ──
# vendor/bundle/

# ── Rust ──
# target/

# ── Java / Kotlin ──
# .gradle/
# build/

# ── .NET ──
# bin/
# obj/

# ── PHP ──
# vendor/

# ── Build outputs ──
# dist/
# build/
# out/
`
