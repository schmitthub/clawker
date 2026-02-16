package config

import (
	"maps"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// partialProject is a lightweight struct for raw YAML parsing.
// It uses yaml tags (not mapstructure) to read the original YAML values
// before viper's lossy merge/unmarshal processing.
type partialProject struct {
	Agent struct {
		FromEnv  []string          `yaml:"from_env"`
		Includes []string          `yaml:"includes"`
		EnvFile  []string          `yaml:"env_file"`
		Env      map[string]string `yaml:"env"`
	} `yaml:"agent"`
	Build struct {
		BuildArgs map[string]string `yaml:"build_args"`
	} `yaml:"build"`
	Security struct {
		Firewall struct {
			AddDomains []string `yaml:"add_domains"`
		} `yaml:"firewall"`
	} `yaml:"security"`
}

// postMerge reconciles viper's lossy merge behavior after unmarshal.
//
// Viper's MergeInConfig replaces slices entirely instead of unioning them,
// and lowercases map keys. This function re-reads both raw YAML sources and
// computes:
//   - Slice unions (deduplicated, sorted) for accumulating fields
//   - Map merges (project wins conflicts) with original case preserved
//   - Env var map overrides (CLAWKER_AGENT_ENV_<KEY>=val)
//   - Env var list appends (CLAWKER_SECURITY_FIREWALL_ADD_DOMAINS=a,b)
func postMerge(cfg *Project, userYAML, projectYAML []byte) error {
	var user, project partialProject

	if len(userYAML) > 0 {
		if err := yaml.Unmarshal(userYAML, &user); err != nil {
			return err
		}
	}
	if len(projectYAML) > 0 {
		if err := yaml.Unmarshal(projectYAML, &project); err != nil {
			return err
		}
	}

	// --- Slice unions (accumulate across user + project) ---
	cfg.Agent.FromEnv = sortedUnion(user.Agent.FromEnv, project.Agent.FromEnv)
	cfg.Agent.Includes = sortedUnion(user.Agent.Includes, project.Agent.Includes)
	cfg.Agent.EnvFile = sortedUnion(user.Agent.EnvFile, project.Agent.EnvFile)
	if cfg.Security.Firewall != nil {
		cfg.Security.Firewall.AddDomains = sortedUnion(
			user.Security.Firewall.AddDomains,
			project.Security.Firewall.AddDomains,
		)
	}

	// --- Map merges (project wins conflicts, case preserved) ---
	cfg.Agent.Env = mergeMaps(user.Agent.Env, project.Agent.Env)
	cfg.Build.BuildArgs = mergeMaps(user.Build.BuildArgs, project.Build.BuildArgs)

	// --- Env var overrides ---
	applyEnvMapOverrides(cfg.Agent.Env, "CLAWKER_AGENT_ENV_")
	applyEnvMapOverrides(cfg.Build.BuildArgs, "CLAWKER_BUILD_BUILD_ARGS_")

	// --- Env var list appends ---
	cfg.Agent.FromEnv = applyEnvSliceAppend(cfg.Agent.FromEnv, "CLAWKER_AGENT_FROM_ENV")
	cfg.Agent.Includes = applyEnvSliceAppend(cfg.Agent.Includes, "CLAWKER_AGENT_INCLUDES")
	cfg.Agent.EnvFile = applyEnvSliceAppend(cfg.Agent.EnvFile, "CLAWKER_AGENT_ENV_FILE")
	if cfg.Security.Firewall != nil {
		cfg.Security.Firewall.AddDomains = applyEnvSliceAppend(
			cfg.Security.Firewall.AddDomains,
			"CLAWKER_SECURITY_FIREWALL_ADD_DOMAINS",
		)
	}

	return nil
}

// sortedUnion returns a deduplicated, sorted union of two string slices.
// Nil inputs are treated as empty. Returns nil if both inputs are empty.
func sortedUnion(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		seen[s] = struct{}{}
	}

	result := make([]string, 0, len(seen))
	for s := range seen {
		result = append(result, s)
	}
	sort.Strings(result)
	return result
}

// mergeMaps deep-merges two maps. Override wins conflicts.
// Returns nil if both inputs are nil/empty.
func mergeMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	result := make(map[string]string, len(base)+len(override))
	maps.Copy(result, base)
	maps.Copy(result, override)
	return result
}

// applyEnvMapOverrides scans environment variables with the given prefix
// and overrides matching keys in the target map.
// For example, with prefix "CLAWKER_AGENT_ENV_" and env var
// CLAWKER_AGENT_ENV_FOO=bar, it sets target["FOO"] = "bar".
func applyEnvMapOverrides(target map[string]string, envPrefix string) {
	if target == nil {
		return
	}
	for _, kv := range os.Environ() {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, envPrefix) {
			continue
		}
		mapKey := key[len(envPrefix):]
		if mapKey == "" {
			continue
		}
		target[mapKey] = val
	}
}

// applyEnvSliceAppend checks for an environment variable and unions its
// comma-separated values with the existing slice. Returns the updated slice.
func applyEnvSliceAppend(existing []string, envKey string) []string {
	val := os.Getenv(envKey)
	if val == "" {
		return existing
	}

	parts := strings.Split(val, ",")
	trimmed := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			trimmed = append(trimmed, p)
		}
	}

	return sortedUnion(existing, trimmed)
}
