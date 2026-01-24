package credentials

import (
	"bufio"
	"os"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// LoadDotEnv loads environment variables from a .env file
// Returns a map of key-value pairs, filtering out sensitive patterns
func LoadDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No .env file is not an error
		}
		return nil, err
	}
	defer file.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key=value
		key, value, ok := parseEnvLine(line)
		if !ok {
			logger.Debug().
				Int("line", lineNum).
				Str("content", line).
				Msg("skipping invalid .env line")
			continue
		}

		// Skip known sensitive patterns
		if isSensitiveKey(key) {
			logger.Debug().
				Str("key", key).
				Msg("skipping sensitive environment variable")
			continue
		}

		result[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	logger.Debug().
		Int("count", len(result)).
		Str("file", path).
		Msg("loaded environment variables from .env file")

	return result, nil
}

// parseEnvLine parses a single line from a .env file
func parseEnvLine(line string) (key, value string, ok bool) {
	// Handle export prefix
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimPrefix(line, "export ")
	}

	// Find the first =
	idx := strings.Index(line, "=")
	if idx < 1 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])

	// Remove surrounding quotes from value
	value = unquote(value)

	return key, value, true
}

// unquote removes surrounding quotes from a string
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}

	// Handle double quotes
	if s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}

	// Handle single quotes
	if s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}

	return s
}

// isSensitiveKey checks if an environment variable key is considered sensitive
// These are filtered out to avoid accidentally exposing credentials
func isSensitiveKey(key string) bool {
	upper := strings.ToUpper(key)

	// Allow Anthropic-specific keys (needed for Claude Code authentication in containers)
	if strings.HasPrefix(upper, "ANTHROPIC_") {
		return false
	}

	// List of sensitive key patterns
	sensitivePatterns := []string{
		"PASSWORD",
		"SECRET",
		"TOKEN",
		"API_KEY",
		"APIKEY",
		"PRIVATE_KEY",
		"PRIVATEKEY",
		"CREDENTIAL",
		"AUTH",
		"AWS_SECRET",
		"AWS_ACCESS",
		"STRIPE_",
		"GITHUB_TOKEN",
		"GITLAB_TOKEN",
		"NPM_TOKEN",
		"DOCKER_PASSWORD",
		"DATABASE_URL",
		"DB_PASSWORD",
		"ENCRYPTION_KEY",
		"SIGNING_KEY",
		"JWT_SECRET",
		"SESSION_SECRET",
		"COOKIE_SECRET",
	}

	for _, pattern := range sensitivePatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}

	return false
}

// FindDotEnvFiles finds .env files in a directory
func FindDotEnvFiles(dir string) []string {
	var files []string

	// Check for common .env file names
	envFiles := []string{
		".env",
		".env.local",
		".env.development",
		".env.development.local",
	}

	for _, name := range envFiles {
		path := dir + "/" + name
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}

	return files
}
