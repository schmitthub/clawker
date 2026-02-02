package docker

import (
	"encoding/json"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// RuntimeEnv returns environment variables derived from project config that should
// be set at container creation time rather than baked into the image. This keeps
// the Dockerfile (and its content hash) stable across config-only changes like
// firewall domains, editor preferences, and user-defined env vars.
func RuntimeEnv(cfg *config.Config) []string {
	var env []string

	// Editor/Visual preferences
	editor := cfg.Agent.Editor
	if editor == "" {
		editor = "nano"
	}
	visual := cfg.Agent.Visual
	if visual == "" {
		visual = "nano"
	}
	env = append(env, "EDITOR="+editor, "VISUAL="+visual)

	// Firewall domains (consumed by entrypoint/init-firewall.sh)
	if cfg.Security.FirewallEnabled() {
		var domains []string
		if cfg.Security.Firewall != nil {
			domains = cfg.Security.Firewall.GetFirewallDomains(config.DefaultFirewallDomains)
		} else {
			domains = config.DefaultFirewallDomains
		}

		jsonBytes, err := json.Marshal(domains)
		if err != nil {
			logger.Error().Err(err).Msg("failed to marshal firewall domains to JSON")
		} else {
			env = append(env, "CLAWKER_FIREWALL_DOMAINS="+string(jsonBytes))
		}

		if cfg.Security.Firewall != nil && cfg.Security.Firewall.IsOverrideMode() {
			env = append(env, "CLAWKER_FIREWALL_OVERRIDE=true")
		}
	}

	// Agent env vars
	for k, v := range cfg.Agent.Env {
		env = append(env, k+"="+v)
	}

	// User-defined env from build instructions
	if cfg.Build.Instructions != nil {
		for k, v := range cfg.Build.Instructions.Env {
			env = append(env, k+"="+v)
		}
	}

	return env
}
