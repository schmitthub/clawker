package hostproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// containsCredentialInjectionChars returns true if s contains characters that
// could inject additional key=value pairs into the git credential protocol.
func containsCredentialInjectionChars(s string) bool {
	return strings.ContainsAny(s, "\n\r\x00")
}

// credentialFieldSanitizer strips newline and null bytes from git credential
// field values. This is defense-in-depth — the handler rejects such requests
// before reaching this point.
var credentialFieldSanitizer = strings.NewReplacer("\n", "", "\r", "", "\x00", "")

// formatGitCredentialInput formats a request as git credential protocol input.
// Format is: key=value\n pairs ending with a blank line.
// All field values are sanitized to prevent newline injection attacks.
func formatGitCredentialInput(req *agentv1.GitCredentialRequest) string {
	var sb strings.Builder

	sb.WriteString("protocol=")
	sb.WriteString(credentialFieldSanitizer.Replace(req.GetProtocol()))
	sb.WriteString("\n")

	sb.WriteString("host=")
	sb.WriteString(credentialFieldSanitizer.Replace(req.GetHost()))
	sb.WriteString("\n")

	path := credentialFieldSanitizer.Replace(req.GetPath())
	if path != "" {
		sb.WriteString("path=")
		sb.WriteString(path)
		sb.WriteString("\n")
	}

	username := credentialFieldSanitizer.Replace(req.GetUsername())
	if username != "" {
		sb.WriteString("username=")
		sb.WriteString(username)
		sb.WriteString("\n")
	}

	password := credentialFieldSanitizer.Replace(req.GetPassword())
	if password != "" {
		sb.WriteString("password=")
		sb.WriteString(password)
		sb.WriteString("\n")
	}

	// Terminate with blank line
	sb.WriteString("\n")

	return sb.String()
}

// gitCredentials holds parsed git credential output.
type gitCredentials struct {
	Protocol string
	Host     string
	Username string
	Password string
}

// parseGitCredentialOutput parses git credential protocol output.
func parseGitCredentialOutput(output string, log *logger.Logger) gitCredentials {
	var creds gitCredentials
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		switch key {
		case "protocol":
			creds.Protocol = value
		case "host":
			creds.Host = value
		case "username":
			creds.Username = value
		case "password":
			creds.Password = value
		}
	}

	if err := scanner.Err(); err != nil {
		log.Debug().Err(err).Msg("error scanning git credential output")
	}

	return creds
}

// gitConfigExitNoMatch is git-config's documented exit status for "no value
// matched" — the outcome when no credential.* key is set. Every other
// failure is a real fault (128 = unreadable or malformed config).
const gitConfigExitNoMatch = 1

// errNoGitCredentialHelper is the one benign precheck outcome: git ran
// cleanly and found no credential.* key, so a headless `git credential fill`
// has no credential source.
var errNoGitCredentialHelper = errors.New("no git credential helper configured on host")

// PrecheckGitCredential implements Service. It checks the exact host
// prerequisites Actions.gitCredential relies on: the `git` binary it execs,
// and a configured credential helper — without one, a headless
// `git credential fill` has no credential source.
func (m *Manager) PrecheckGitCredential(ctx context.Context) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found on host: %w", err)
	}
	// --get-regexp also matches URL-scoped helpers (credential.<url>.helper).
	out, err := exec.CommandContext(ctx, "git", "config", "--get-regexp", `^credential\.`).Output()
	if err != nil {
		return classifyGitConfigError(err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return errNoGitCredentialHelper
	}
	return nil
}

// classifyGitConfigError splits the one benign git-config failure — exit 1,
// no credential.* key matched — from real faults (broken config, killed
// process, exec failure), which surface with git's own stderr detail,
// mirroring what Actions.gitCredential reports at request time.
func classifyGitConfigError(err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("reading host git credential config: %w", err)
	}
	if exitErr.ExitCode() == gitConfigExitNoMatch {
		return errNoGitCredentialHelper
	}
	if detail := strings.TrimSpace(string(exitErr.Stderr)); detail != "" {
		return fmt.Errorf("reading host git credential config: %w: %s", err, detail)
	}
	return fmt.Errorf("reading host git credential config: %w", err)
}
