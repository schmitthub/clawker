package config

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// userHomeDir is injectable for testing (avoids writing to real home dir in tests).
var userHomeDir = os.UserHomeDir

// ResolveAgentEnv merges env_file, from_env, and env into a single map.
// Precedence (lowest to highest): env_file < from_env < env.
// The projectDir is used to resolve relative paths in env_file entries.
// Returns the merged env map, any warnings (e.g. unset from_env vars), and an error.
func ResolveAgentEnv(agent AgentConfig, projectDir string) (map[string]string, []string, error) {
	result := make(map[string]string)
	var warnings []string

	// Layer 1: env_file (lowest precedence)
	for _, path := range agent.EnvFile {
		resolved, err := resolvePath(path, projectDir)
		if err != nil {
			return nil, nil, fmt.Errorf("agent.env_file %q: %w", path, err)
		}
		fileEnv, err := readEnvFile(resolved)
		if err != nil {
			return nil, nil, fmt.Errorf("agent.env_file %q: %w", path, err)
		}
		maps.Copy(result, fileEnv)
	}

	// Layer 2: from_env (overrides file values)
	for _, name := range agent.FromEnv {
		val, ok := os.LookupEnv(name)
		if !ok {
			logger.Debug().Str("var", name).Msg("agent.from_env: variable not set on host, skipping")
			warnings = append(warnings, fmt.Sprintf("agent.from_env: variable %q not set on host, skipping", name))
			continue
		}
		result[name] = val
	}

	// Layer 3: env (highest precedence — explicit static values win)
	maps.Copy(result, agent.Env)

	if len(result) == 0 {
		return nil, warnings, nil
	}
	return result, warnings, nil
}

// readEnvFile reads an env file and returns key-value pairs.
// Format: KEY=VALUE lines, # comments, blank lines skipped.
// Bare KEY lines (no =) set the key to an empty string.
// Note: Docker's --env-file looks up bare KEYs from the host environment;
// clawker treats them as empty-string values instead.
func readEnvFile(path string) (map[string]string, error) {
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
	logger.Debug().Str("file", path).Int("count", len(result)).Msg("loaded env file")
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
