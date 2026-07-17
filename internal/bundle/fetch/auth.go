package fetch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/transport"
	transporthttp "github.com/go-git/go-git/v6/plumbing/transport/http"
)

// withHTTPAuthRetry runs op anonymously first and, only when an http(s) source
// answers "authentication required", shells out to the host's `git credential
// fill` (which inherits the user's configured credential helper chain) and
// retries once with HTTP basic auth. It approves the credential on success and
// rejects it on failure so the helper's cache stays accurate. ssh sources get
// agent auth plus the malformed-line-tolerant known_hosts verifier from
// sshClientOptions and never take the retry branch. No credential ever leaves
// this process boundary or lands in the cache.
func withHTTPAuthRetry(ctx context.Context, gitURL string, op func([]client.Option) error) error {
	err := op(sshClientOptions(gitURL))
	if err == nil || !isHTTPURL(gitURL) || !errors.Is(err, transport.ErrAuthenticationRequired) {
		return err
	}

	cred, credErr := credentialFill(ctx, gitURL)
	if credErr != nil {
		// Fall back to surfacing the original auth error: no usable helper
		// means we cannot authenticate, and the credential-helper failure is
		// secondary noise.
		return err
	}

	authOpt := client.WithHTTPAuth(&transporthttp.BasicAuth{
		Username: cred.Username,
		Password: cred.Password,
	})
	authErr := op([]client.Option{authOpt})
	action := "approve"
	if authErr != nil {
		action = "reject"
	}
	// Best-effort helper-cache update; the git credential outcome is advisory
	// and its failure must never mask (or replace) the fetch error.
	if updErr := credentialUpdate(ctx, action, cred); updErr != nil {
		return authErr
	}
	return authErr
}

// isHTTPURL reports whether gitURL is an http or https clone URL.
func isHTTPURL(gitURL string) bool {
	return strings.HasPrefix(gitURL, "http://") || strings.HasPrefix(gitURL, "https://")
}

// credential is one git credential-helper record.
type credential struct {
	Protocol string
	Host     string
	Path     string
	Username string
	Password string
}

// credentialFill asks the host git credential helper chain for a credential for
// gitURL by piping a request to `git credential fill` and parsing its reply.
func credentialFill(ctx context.Context, gitURL string) (credential, error) {
	u, err := url.Parse(gitURL)
	if err != nil {
		return credential{}, fmt.Errorf("parse credential url %q: %w", gitURL, err)
	}
	req := credential{
		Protocol: u.Scheme,
		Host:     u.Host,
		Path:     strings.TrimPrefix(u.Path, "/"),
		Username: "",
		Password: "",
	}
	out, err := runGitCredential(ctx, "fill", req)
	if err != nil {
		return credential{}, err
	}
	return parseCredential(req, out)
}

// credentialUpdate feeds a filled credential back to `git credential <action>`
// (approve or reject) so the helper cache reflects the fetch outcome.
func credentialUpdate(ctx context.Context, action string, cred credential) error {
	_, err := runGitCredential(ctx, action, cred)
	return err
}

// runGitCredential runs `git credential <action>`, writing the record as the
// key=value input protocol and returning the raw stdout.
func runGitCredential(ctx context.Context, action string, cred credential) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "credential", action)
	cmd.Stdin = strings.NewReader(encodeCredential(cred))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git credential %s: %w: %s", action, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// encodeCredential renders a credential as git's key=value input protocol,
// omitting empty fields, terminated by a blank line.
func encodeCredential(cred credential) string {
	var b strings.Builder
	writeField(&b, "protocol", cred.Protocol)
	writeField(&b, "host", cred.Host)
	writeField(&b, "path", cred.Path)
	writeField(&b, "username", cred.Username)
	writeField(&b, "password", cred.Password)
	b.WriteString("\n")
	return b.String()
}

// writeField appends one key=value line when value is non-empty.
func writeField(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString("=")
	b.WriteString(value)
	b.WriteString("\n")
}

// parseCredential merges the key=value output of `git credential fill` onto the
// request record, requiring at least a password (username may be embedded in the
// URL or defaulted by the helper).
func parseCredential(req credential, out string) (credential, error) {
	cred := req
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "protocol":
			cred.Protocol = value
		case "host":
			cred.Host = value
		case "path":
			cred.Path = value
		case "username":
			cred.Username = value
		case "password":
			cred.Password = value
		}
	}
	if err := sc.Err(); err != nil {
		return credential{}, fmt.Errorf("read git credential output: %w", err)
	}
	if cred.Password == "" {
		return credential{}, errors.New("git credential helper returned no password")
	}
	return cred, nil
}
