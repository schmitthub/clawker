package docker

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/schmitthub/clawker/internal/config"
)

// RuntimeEnv returns environment variables derived from project config that should
// be set at container creation time rather than baked into the image. This keeps
// the Dockerfile (and its content hash) stable across config-only changes like
// firewall domains, editor preferences, and user-defined env vars.
//
// Precedence (last wins): base defaults (EDITOR/VISUAL) → agent env → build instruction env.
// The result is sorted by key for deterministic ordering.
func RuntimeEnv(cfg *config.Project) ([]string, error) {
	// Build a map so later sources override earlier ones.
	// Precedence: base defaults → agent env → build instruction env (last wins).
	m := make(map[string]string)

	// Base defaults: editor/visual preferences
	editor := cfg.Agent.Editor
	if editor == "" {
		editor = "nano"
	}
	visual := cfg.Agent.Visual
	if visual == "" {
		visual = "nano"
	}
	m["EDITOR"] = editor
	m["VISUAL"] = visual

	// Firewall domains (consumed by entrypoint/init-firewall.sh)
	if cfg.Security.FirewallEnabled() {
		domains := cfg.Security.Firewall.GetFirewallDomains(config.DefaultFirewallDomains)

		jsonBytes, err := json.Marshal(domains)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal firewall domains: %w", err)
		}
		m["CLAWKER_FIREWALL_DOMAINS"] = string(jsonBytes)

		if cfg.Security.Firewall.IsOverrideMode() {
			m["CLAWKER_FIREWALL_OVERRIDE"] = "true"
		}
	}

	// Agent env vars (override base defaults)
	for k, v := range cfg.Agent.Env {
		m[k] = v
	}

	// User-defined env from build instructions (highest precedence)
	if cfg.Build.Instructions != nil {
		for k, v := range cfg.Build.Instructions.Env {
			m[k] = v
		}
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
