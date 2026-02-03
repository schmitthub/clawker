package docker

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/schmitthub/clawker/internal/config"
)

// RuntimeEnvOpts describes the environment variables RuntimeEnv can produce.
// Each field maps to a specific env var or category of env vars.
type RuntimeEnvOpts struct {
	// Editor preferences
	Editor string
	Visual string

	// Firewall
	FirewallEnabled        bool
	FirewallDomains        []string
	FirewallOverride       bool
	FirewallIPRangeSources []config.IPRangeSource

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

	// Agent env vars (override base defaults and terminal)
	for k, v := range opts.AgentEnv {
		m[k] = v
	}

	// User-defined env from build instructions (highest precedence)
	for k, v := range opts.InstructionEnv {
		m[k] = v
	}

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
