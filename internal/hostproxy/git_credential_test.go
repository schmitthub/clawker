package hostproxy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestFormatGitCredentialInput(t *testing.T) {
	tests := []struct {
		name     string
		req      *agentv1.GitCredentialRequest
		expected string
	}{
		{
			name:     "minimal request",
			req:      gitReq(agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET, "https", "github.com", "", "", ""),
			expected: "protocol=https\nhost=github.com\n\n",
		},
		{
			name: "with path",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
				"https",
				"github.com",
				"user/repo.git",
				"",
				"",
			),
			expected: "protocol=https\nhost=github.com\npath=user/repo.git\n\n",
		},
		{
			name: "with username",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
				"https",
				"github.com",
				"",
				"testuser",
				"",
			),
			expected: "protocol=https\nhost=github.com\nusername=testuser\n\n",
		},
		{
			name: "with password (for store)",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE,
				"https",
				"github.com",
				"",
				"testuser",
				"secretpass",
			),
			expected: "protocol=https\nhost=github.com\nusername=testuser\npassword=secretpass\n\n",
		},
		{
			name: "full request",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE,
				"https",
				"github.com",
				"user/repo.git",
				"testuser",
				"secretpass",
			),
			expected: "protocol=https\nhost=github.com\npath=user/repo.git\nusername=testuser\npassword=secretpass\n\n",
		},
		{
			name: "strips newlines from host",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
				"https",
				"evil.com\nusername=victim\npassword=stolen",
				"",
				"",
				"",
			),
			expected: "protocol=https\nhost=evil.comusername=victimpassword=stolen\n\n",
		},
		{
			name: "strips carriage returns",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
				"https",
				"evil.com\r\nusername=victim",
				"",
				"",
				"",
			),
			expected: "protocol=https\nhost=evil.comusername=victim\n\n",
		},
		{
			name: "strips null bytes",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
				"https\x00injected",
				"github.com",
				"",
				"",
				"",
			),
			expected: "protocol=httpsinjected\nhost=github.com\n\n",
		},
		{
			name: "strips newlines from all fields",
			req: gitReq(
				agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE,
				"https\nhost=evil.com",
				"github.com\npassword=stolen",
				"repo\nusername=hacker",
				"user\npassword=pwned",
				"pass\nhost=attacker.com",
			),
			expected: "protocol=httpshost=evil.com\nhost=github.compassword=stolen\npath=repousername=hacker\nusername=userpassword=pwned\npassword=passhost=attacker.com\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGitCredentialInput(tt.req)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParseGitCredentialOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected gitCredentials
	}{
		{
			name:   "empty output",
			output: "",
			expected: gitCredentials{
				Protocol: "",
				Host:     "",
				Username: "",
				Password: "",
			},
		},
		{
			name:   "minimal credentials",
			output: "protocol=https\nhost=github.com\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "",
				Password: "",
			},
		},
		{
			name:   "full credentials",
			output: "protocol=https\nhost=github.com\nusername=testuser\npassword=secretpass\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "secretpass",
			},
		},
		{
			name:   "with trailing blank line",
			output: "protocol=https\nhost=github.com\nusername=testuser\npassword=secretpass\n\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "secretpass",
			},
		},
		{
			name:   "ignores unknown keys",
			output: "protocol=https\nhost=github.com\nunknown=value\nusername=testuser\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "",
			},
		},
		{
			name:   "ignores malformed lines",
			output: "protocol=https\nhost=github.com\nmalformed line without equals\nusername=testuser\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "",
			},
		},
		{
			name:   "handles value with equals sign",
			output: "protocol=https\nhost=github.com\npassword=secret=with=equals\n",
			expected: gitCredentials{
				Protocol: "https",
				Host:     "github.com",
				Password: "secret=with=equals",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGitCredentialOutput(tt.output, logger.Nop())

			if result.Protocol != tt.expected.Protocol {
				t.Errorf("Protocol: expected %q, got %q", tt.expected.Protocol, result.Protocol)
			}
			if result.Host != tt.expected.Host {
				t.Errorf("Host: expected %q, got %q", tt.expected.Host, result.Host)
			}
			if result.Username != tt.expected.Username {
				t.Errorf("Username: expected %q, got %q", tt.expected.Username, result.Username)
			}
			if result.Password != tt.expected.Password {
				t.Errorf("Password: expected %q, got %q", tt.expected.Password, result.Password)
			}
		})
	}
}

// TestPrecheckGitCredential pins the error split: exit 1 (no credential.*
// key anywhere) is the one benign "not configured" outcome; a broken config
// file must surface git's own fatal instead of masquerading as
// not-configured; a configured helper passes.
func TestPrecheckGitCredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	tests := []struct {
		name              string
		globalConfig      string
		wantErr           bool
		wantNotConfigured bool   // errors.Is(err, errNoGitCredentialHelper)
		wantInError       string // substring the error must carry
	}{
		{
			name:              "no helper configured",
			globalConfig:      "",
			wantErr:           true,
			wantNotConfigured: true,
			wantInError:       "",
		},
		{
			name:              "broken config surfaces the git fatal",
			globalConfig:      "[credential\nhelper = broken",
			wantErr:           true,
			wantNotConfigured: false,
			wantInError:       "bad config",
		},
		{
			name:              "configured helper passes",
			globalConfig:      "[credential]\n\thelper = store\n",
			wantErr:           false,
			wantNotConfigured: false,
			wantInError:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := precheckGitEnv(t, tt.globalConfig)
			err := m.PrecheckGitCredential(context.Background())

			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v, got %v", tt.wantErr, err)
			}
			if errors.Is(err, errNoGitCredentialHelper) != tt.wantNotConfigured {
				t.Errorf("wantNotConfigured=%v, got %v", tt.wantNotConfigured, err)
			}
			if tt.wantInError != "" && !strings.Contains(err.Error(), tt.wantInError) {
				t.Errorf("want error containing %q, got %v", tt.wantInError, err)
			}
		})
	}
}

// precheckGitEnv points every git config source at a scratch file and moves
// cwd outside any repository, so the host's real config and the repo's
// .git/config cannot leak credential.* keys into the probe.
func precheckGitEnv(t *testing.T, globalConfig string) *Manager {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfgPath, []byte(globalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", cfgPath)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Chdir(dir)
	return &Manager{}
}
