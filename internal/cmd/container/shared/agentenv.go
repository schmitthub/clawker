package shared

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// userHomeDir is injectable for testing (avoids writing to real home dir in tests).
var userHomeDir = os.UserHomeDir

// ResolveAgentEnv merges the shared agent env spec with the selected
// harness's env spec into a single map. Within each spec, precedence (lowest
// to highest) is env_file < from_env < env; the harness spec as a whole
// overrides the agent base on key collision. A nil harness config applies the
// base spec only. The projectDir is used to resolve relative paths in
// env_file entries; harnessName scopes harness-layer diagnostics.
// Returns the merged env map, any warnings (e.g. unset from_env vars), and an error.
func ResolveAgentEnv(
	agent config.AgentConfig,
	harnessCfg *config.HarnessConfig,
	harnessName, projectDir string,
	log *logger.Logger,
) (map[string]string, []string, error) {
	result := make(map[string]string)

	warnings, err := applyEnvSpec(result, "agent", agent.EnvFile, agent.FromEnv, agent.Env, projectDir, log)
	if err != nil {
		return nil, nil, err
	}

	if harnessCfg != nil {
		scope := "harnesses." + harnessName
		harnessWarnings, specErr := applyEnvSpec(
			result, scope, harnessCfg.EnvFile, harnessCfg.FromEnv, harnessCfg.Env, projectDir, log)
		if specErr != nil {
			return nil, nil, specErr
		}
		warnings = append(warnings, harnessWarnings...)
	}

	if len(result) == 0 {
		return nil, warnings, nil
	}
	return result, warnings, nil
}

// applyEnvSpec layers one env spec (env_file < from_env < env) onto result.
// scope prefixes diagnostics so agent-level and harness-level issues are
// distinguishable (e.g. "agent.env_file" vs "harnesses.codex.env_file").
func applyEnvSpec(
	result map[string]string,
	scope string,
	envFile, fromEnv []string,
	env map[string]string,
	projectDir string,
	log *logger.Logger,
) ([]string, error) {
	var warnings []string

	// Layer 1: env_file (lowest precedence)
	for _, path := range envFile {
		resolved, err := resolvePath(path, projectDir)
		if err != nil {
			return nil, fmt.Errorf("%s.env_file %q: %w", scope, path, err)
		}
		fileEnv, err := parseEnvFile(resolved, log)
		if err != nil {
			return nil, fmt.Errorf("%s.env_file %q: %w", scope, path, err)
		}
		maps.Copy(result, fileEnv)
	}

	// Layer 2: from_env (overrides file values)
	for _, name := range fromEnv {
		val, ok := os.LookupEnv(name)
		if !ok {
			log.Debug().Str("var", name).Str("scope", scope).Msg("from_env: variable not set on host, skipping")
			warnings = append(warnings, fmt.Sprintf("%s.from_env: variable %q not set on host, skipping", scope, name))
			continue
		}
		result[name] = val
	}

	// Layer 3: env (highest precedence — explicit static values win)
	maps.Copy(result, env)

	return warnings, nil
}

// parseEnvFile reads an env file and returns key-value pairs.
// Format: KEY=VALUE lines, # comments, blank lines skipped.
// Bare KEY lines (no =) set the key to an empty string.
func parseEnvFile(path string, log *logger.Logger) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, hasEquals := strings.Cut(line, "=")
		if key == "" {
			continue
		}
		if hasEquals {
			result[key] = value
		} else {
			result[key] = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	log.Debug().Str("file", path).Int("count", len(result)).Msg("loaded env file")
	return result, nil
}

// resolvePath expands a path using standard Unix conventions:
// - ~ and ~/ expand to the user's home directory
// - $VAR and ${VAR} expand environment variables (unset vars are an error)
// - Relative paths are resolved against projectDir
func resolvePath(path, projectDir string) (string, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return expanded, nil
	}
	return filepath.Join(projectDir, expanded), nil
}

// expandPath expands ~ and environment variables in a path.
// Returns an error if ~ expansion fails or an env var is not set.
func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding ~ in %q: %w", path, err)
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, path[2:])
	}

	// Strict env var expansion: unset variables are an error.
	var expandErr error
	expanded := os.Expand(path, func(key string) string {
		val, ok := os.LookupEnv(key)
		if !ok && expandErr == nil {
			expandErr = fmt.Errorf("environment variable %q not set in path %q", key, path)
		}
		return val
	})
	if expandErr != nil {
		return "", expandErr
	}
	return expanded, nil
}
