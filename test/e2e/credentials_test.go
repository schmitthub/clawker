package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

func boolPtr(b bool) *bool { return &b }

// parseEnvLines parses `env` output into a map.
func parseEnvLines(output string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			m[line[:i]] = line[i+1:]
		}
	}
	return m
}

func TestCredentialEnvInjection(t *testing.T) {
	tests := []struct {
		name            string
		setupConfig     func(p *config.Project)
		wantSSHAuthSock bool
		wantGPGConfig   bool
		wantSSHSocket   bool
		wantGPGSocket   bool
	}{
		{
			name: "ssh_only",
			setupConfig: func(p *config.Project) {
				p.Security.GitCredentials = &config.GitCredentialsConfig{
					ForwardSSH: boolPtr(true),
					ForwardGPG: boolPtr(false),
				}
			},
			wantSSHAuthSock: true,
			wantGPGConfig:   false,
			wantSSHSocket:   true,
			wantGPGSocket:   false,
		},
		{
			name: "gpg_only",
			setupConfig: func(p *config.Project) {
				p.Security.GitCredentials = &config.GitCredentialsConfig{
					ForwardSSH: boolPtr(false),
					ForwardGPG: boolPtr(true),
				}
			},
			wantSSHAuthSock: false,
			wantGPGConfig:   true,
			wantSSHSocket:   false,
			wantGPGSocket:   true,
		},
		{
			name: "both",
			setupConfig: func(p *config.Project) {
				p.Security.GitCredentials = &config.GitCredentialsConfig{
					ForwardSSH: boolPtr(true),
					ForwardGPG: boolPtr(true),
				}
			},
			wantSSHAuthSock: true,
			wantGPGConfig:   true,
			wantSSHSocket:   true,
			wantGPGSocket:   true,
		},
		{
			name:            "nil_defaults",
			setupConfig:     nil, // no git_credentials section — validates the nil guard fix
			wantSSHAuthSock: true,
			wantGPGConfig:   false,
			wantSSHSocket:   true,
			wantGPGSocket:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio, _, out, errOut := iostreams.Test()
			f := &cmdutil.Factory{
				Version:   "test",
				IOStreams: tio,
				Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
				TUI:       tui.NewTUI(tio),
				Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio) },
				Config: func() (config.Config, error) {
					return config.NewConfig()
				},
				Client: func(ctx context.Context) (*docker.Client, error) {
					cfg, err := config.NewConfig()
					if err != nil {
						return nil, err
					}
					c, err := docker.NewClient(ctx, cfg, nil,
						docker.WithLabels(docker.TestLabelConfig(cfg, t.Name())))
					if err != nil {
						return nil, err
					}
					docker.WireBuildKit(c)
					return c, nil
				},
				ProjectManager: func() (project.ProjectManager, error) {
					cfg, err := config.NewConfig()
					if err != nil {
						return nil, err
					}
					return project.NewProjectManager(cfg, logger.Nop(), nil)
				},
			}
			h := &harness.Harness{T: t, Factory: f}
			result := h.NewIsolatedFS(nil)

			// Override HOME so IsOutsideHome(".") returns false
			// and create a minimal ~/.claude/ dir for container init.
			t.Setenv("HOME", result.BaseDir)
			require.NoError(t, os.MkdirAll(filepath.Join(result.BaseDir, ".claude"), 0o755))

			// Scaffold project config + register via CLI.
			initRes := h.Run("project", "init", "testproject", "--yes")
			require.NoError(t, initRes.Err, "project init failed\nstdout: %s\nstderr: %s",
				out.String(), errOut.String())
			out.Reset()
			errOut.Reset()

			// Configure project: disable host auth + apply per-test git credentials.
			cfg, err := config.NewConfig()
			require.NoError(t, err)
			require.NoError(t, cfg.ProjectStore().Set(func(p *config.Project) {
				p.Agent.ClaudeCode = &config.ClaudeCodeConfig{
					UseHostAuth: boolPtr(false),
				}
				if tt.setupConfig != nil {
					tt.setupConfig(p)
				}
			}))
			require.NoError(t, cfg.ProjectStore().Write())

			// Build image.
			buildRes := h.Run("build")
			require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
				out.String(), errOut.String())
			out.Reset()
			errOut.Reset()

			// Run container detached with firewall disabled.
			agentName := "cred-" + tt.name
			runRes := h.Run("container", "run", "--detach",
				"--disable-firewall", "--agent", agentName, "@", "sleep", "infinity")
			require.NoError(t, runRes.Err, "run failed\nstdout: %s\nstderr: %s",
				out.String(), errOut.String())
			out.Reset()
			errOut.Reset()

			t.Cleanup(func() {
				h.Run("container", "stop", "--agent", agentName)
				h.Run("container", "rm", "--agent", agentName, "--volumes")
			})

			// Exec `env` inside the container.
			execRes := h.Run("container", "exec", "--agent", agentName, "env")
			require.NoError(t, execRes.Err, "exec env failed\nstdout: %s\nstderr: %s",
				out.String(), errOut.String())

			envMap := parseEnvLines(out.String())
			out.Reset()
			errOut.Reset()

			// SSH_AUTH_SOCK
			if tt.wantSSHAuthSock {
				assert.Contains(t, envMap, "SSH_AUTH_SOCK",
					"SSH_AUTH_SOCK should be set when SSH forwarding is enabled")
			} else {
				assert.NotContains(t, envMap, "SSH_AUTH_SOCK",
					"SSH_AUTH_SOCK should not be set when SSH forwarding is disabled")
			}

			// GIT_CONFIG (GPG program injection)
			if tt.wantGPGConfig {
				assert.Equal(t, "1", envMap["GIT_CONFIG_COUNT"],
					"GIT_CONFIG_COUNT should be 1 when GPG is enabled")
				assert.Equal(t, "gpg.program", envMap["GIT_CONFIG_KEY_0"])
				assert.Equal(t, "/usr/bin/gpg", envMap["GIT_CONFIG_VALUE_0"])
			} else {
				assert.NotContains(t, envMap, "GIT_CONFIG_COUNT",
					"GIT_CONFIG_COUNT should not be set when GPG is disabled")
			}

			// CLAWKER_REMOTE_SOCKETS
			remoteSockets := envMap["CLAWKER_REMOTE_SOCKETS"]
			if tt.wantSSHSocket {
				assert.Contains(t, remoteSockets, "ssh-agent",
					"CLAWKER_REMOTE_SOCKETS should contain ssh-agent")
			} else {
				assert.NotContains(t, remoteSockets, "ssh-agent",
					"CLAWKER_REMOTE_SOCKETS should not contain ssh-agent")
			}
			if tt.wantGPGSocket {
				assert.Contains(t, remoteSockets, "gpg-agent",
					"CLAWKER_REMOTE_SOCKETS should contain gpg-agent")
			} else {
				assert.NotContains(t, remoteSockets, "gpg-agent",
					"CLAWKER_REMOTE_SOCKETS should not contain gpg-agent")
			}
		})
	}
}

func TestSSHKeySigning(t *testing.T) {
	// Skip if no SSH agent available on host.
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		t.Skip("SSH_AUTH_SOCK not set — no SSH agent available")
	}
	if keyOut, err := exec.Command("ssh-add", "-L").Output(); err != nil || len(keyOut) == 0 {
		t.Skip("no SSH keys loaded in agent (ssh-add -L failed)")
	}

	tio, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		Version:   "test",
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		TUI:       tui.NewTUI(tio),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		Config: func() (config.Config, error) {
			return config.NewConfig()
		},
		Client: func(ctx context.Context) (*docker.Client, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			c, err := docker.NewClient(ctx, cfg, nil,
				docker.WithLabels(docker.TestLabelConfig(cfg, t.Name())))
			if err != nil {
				return nil, err
			}
			docker.WireBuildKit(c)
			return c, nil
		},
		ProjectManager: func() (project.ProjectManager, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			return project.NewProjectManager(cfg, logger.Nop(), nil)
		},
	}
	h := &harness.Harness{T: t, Factory: f}
	result := h.NewIsolatedFS(nil)
	t.Setenv("HOME", result.BaseDir)
	require.NoError(t, os.MkdirAll(filepath.Join(result.BaseDir, ".claude"), 0o755))

	// Scaffold project + disable host auth. SSH forwarding is enabled by default.
	initRes := h.Run("project", "init", "testproject", "--yes")
	require.NoError(t, initRes.Err, "project init failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())
	out.Reset()
	errOut.Reset()

	cfg, err := config.NewConfig()
	require.NoError(t, err)
	require.NoError(t, cfg.ProjectStore().Set(func(p *config.Project) {
		p.Agent.ClaudeCode = &config.ClaudeCodeConfig{
			UseHostAuth: boolPtr(false),
		}
	}))
	require.NoError(t, cfg.ProjectStore().Write())

	// Build image.
	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())
	out.Reset()
	errOut.Reset()

	// Run container detached — capture short container ID from stdout.
	runRes := h.Run("container", "run", "--detach",
		"--disable-firewall", "--agent", "sshsign", "@", "sleep", "infinity")
	require.NoError(t, runRes.Err, "run failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())
	containerID := strings.TrimSpace(out.String())
	require.NotEmpty(t, containerID, "container run --detach should print container ID")
	out.Reset()
	errOut.Reset()

	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "sshsign")
		h.Run("container", "rm", "--agent", "sshsign", "--volumes")
	})

	// Start socket bridge daemon in a goroutine.
	// Bridge serve is self-contained (own logger, config, Docker client) —
	// does NOT use Factory IOStreams. Root command has SilenceErrors: true,
	// so no concurrent writes to shared buffers.
	go h.Run("bridge", "serve", "--container", containerID)

	// Wait for the forwarded SSH agent to become available inside the container.
	require.Eventually(t, func() bool {
		out.Reset()
		errOut.Reset()
		res := h.Run("container", "exec", "--agent", "sshsign", "ssh-add", "-L")
		return res.Err == nil && strings.Contains(out.String(), "ssh-")
	}, 30*time.Second, 1*time.Second, "forwarded SSH agent not available in container")
	out.Reset()
	errOut.Reset()

	// Sign a git commit using the forwarded host SSH key and verify signature.
	signingScript := `set -e
KEY=$(ssh-add -L | head -1)
echo "test@test.com $KEY" > /tmp/allowed_signers
git config --global gpg.format ssh
git config --global user.signingKey "$KEY"
git config --global gpg.ssh.allowedSignersFile /tmp/allowed_signers
git config --global user.name "Test"
git config --global user.email "test@test.com"
git init /tmp/test-repo
cd /tmp/test-repo
git commit --allow-empty -S -m "signed test"
git log --show-signature -1`

	signRes := h.Run("container", "exec", "--agent", "sshsign", "sh", "-c", signingScript)
	require.NoError(t, signRes.Err, "signing failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())

	assert.Contains(t, out.String(), "Good \"git\" signature",
		"git log --show-signature should verify the SSH signature")
}
