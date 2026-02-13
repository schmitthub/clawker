package docker

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
)

// RuntimeEnvOpts describes the environment variables RuntimeEnv can produce.
// Each field maps to a specific env var or category of env vars.
type RuntimeEnvOpts struct {
	// Clawker identity (consumed by statusline)
	Version         string
	Project         string
	Agent           string
	WorkspaceMode   string // "bind" or "snapshot"
	WorkspaceSource string // host path being mounted

	// Editor preferences
	Editor string
	Visual string

	// Firewall
	FirewallEnabled        bool
	FirewallDomains        []string
	FirewallOverride       bool
	FirewallIPRangeSources []config.IPRangeSource

	// Socket forwarding (consumed by socket-forwarder in container)
	GPGForwardingEnabled bool // Enable GPG agent forwarding
	SSHForwardingEnabled bool // Enable SSH agent forwarding

	// Terminal capabilities (from host)
	Is256Color bool
	TrueColor  bool

	// User-defined overrides (arbitrary pass-through)
	AgentEnv       map[string]string
	InstructionEnv map[string]string
}

// RuntimeEnv produces container environment variables from explicit options.
// Precedence (last wins): base defaults → terminal capabilities → agent env → instruction env.
// The result is sorted by key for deterministic ordering.
func RuntimeEnv(opts RuntimeEnvOpts) ([]string, error) {
	m := make(map[string]string)

	// Clawker identity (consumed by statusline)
	if opts.Project != "" {
		m["CLAWKER_PROJECT"] = opts.Project
	}
	if opts.Agent != "" {
		m["CLAWKER_AGENT"] = opts.Agent
	}
	if opts.WorkspaceMode != "" {
		m["CLAWKER_WORKSPACE_MODE"] = opts.WorkspaceMode
	}
	if opts.WorkspaceSource != "" {
		m["CLAWKER_WORKSPACE_SOURCE"] = opts.WorkspaceSource
	}
	if opts.Version != "" {
		m["CLAWKER_VERSION"] = opts.Version
	}

	// Base defaults: editor/visual
	editor := opts.Editor
	if editor == "" {
		editor = "nano"
	}
	visual := opts.Visual
	if visual == "" {
		visual = "nano"
	}
	m["EDITOR"] = editor
	m["VISUAL"] = visual

	// Terminal capabilities from host
	if opts.Is256Color {
		m["TERM"] = "xterm-256color"
	}
	if opts.TrueColor {
		m["COLORTERM"] = "truecolor"
	}

	// Firewall domains (consumed by entrypoint/init-firewall.sh)
	if opts.FirewallEnabled {
		domains := opts.FirewallDomains
		if domains == nil {
			domains = []string{}
		}
		jsonBytes, err := json.Marshal(domains)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal firewall domains: %w", err)
		}
		m["CLAWKER_FIREWALL_DOMAINS"] = string(jsonBytes)

		if opts.FirewallOverride {
			m["CLAWKER_FIREWALL_OVERRIDE"] = "true"
		}

		// IP range sources (consumed by entrypoint/init-firewall.sh)
		ipSources := opts.FirewallIPRangeSources
		if ipSources == nil {
			ipSources = []config.IPRangeSource{}
		}
		ipSourcesBytes, err := json.Marshal(ipSources)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal firewall IP range sources: %w", err)
		}
		m["CLAWKER_FIREWALL_IP_RANGE_SOURCES"] = string(ipSourcesBytes)
	}

	// Telemetry resource attributes for per-project/agent metric segmentation
	var resourceAttrs []string
	if opts.Project != "" {
		resourceAttrs = append(resourceAttrs, "project="+opts.Project)
	}
	if opts.Agent != "" {
		resourceAttrs = append(resourceAttrs, "agent="+opts.Agent)
	}
	if len(resourceAttrs) > 0 {
		m["OTEL_RESOURCE_ATTRIBUTES"] = strings.Join(resourceAttrs, ",")
	}

	// Socket forwarding (consumed by clawker-socket-server binary inside container)
	if opts.GPGForwardingEnabled || opts.SSHForwardingEnabled {
		var sockets []map[string]string
		if opts.GPGForwardingEnabled {
			sockets = append(sockets, map[string]string{
				"path": "/home/claude/.gnupg/S.gpg-agent",
				"type": "gpg-agent",
			})
		}
		if opts.SSHForwardingEnabled {
			sockets = append(sockets, map[string]string{
				"path": "/home/claude/.ssh/agent.sock",
				"type": "ssh-agent",
			})
			// SSH tools need SSH_AUTH_SOCK to find the forwarded socket
			m["SSH_AUTH_SOCK"] = "/home/claude/.ssh/agent.sock"
		}
		socketsBytes, err := json.Marshal(sockets)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal remote sockets: %w", err)
		}
		m["CLAWKER_REMOTE_SOCKETS"] = string(socketsBytes)
	}

	// Agent env vars (override base defaults and terminal)
	maps.Copy(m, opts.AgentEnv)

	// User-defined env from build instructions (highest precedence)
	maps.Copy(m, opts.InstructionEnv)

	// Sort keys for deterministic output
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+m[k])
	}
	return env, nil
}
