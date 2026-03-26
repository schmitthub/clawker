package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// sanitizePresetName converts a preset display name to a valid project name.
func sanitizePresetName(name string) string {
	s := strings.ToLower(name)
	s = strings.NewReplacer("/", "-", "+", "plus", "#", "sharp", " ", "-", ".", "").Replace(s)
	return "preset-" + s
}

// readProjectConfig reads and parses .clawker.yaml from the given directory.
func readProjectConfig(t *testing.T, dir string) *config.Project {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".clawker.yaml"))
	require.NoError(t, err, "reading .clawker.yaml")

	store, err := storage.NewFromString[config.Project](
		string(data),
		storage.WithDefaultsFromStruct[config.Project](),
	)
	require.NoError(t, err, "parsing .clawker.yaml")
	return store.Read()
}

// TestPresetBuilds_E2E verifies that every language preset produces a valid
// Dockerfile and builds successfully with real Docker. This is the E2E gate
// ensuring preset YAML → bundler → Docker build works end-to-end.
func TestPresetBuilds_E2E(t *testing.T) {
	presets := config.Presets()

	for _, preset := range presets {
		if preset.AutoCustomize {
			continue // "Build from scratch" uses the same YAML as Bare
		}

		t.Run(preset.Name, func(t *testing.T) {
			projectName := sanitizePresetName(preset.Name)

			h := &harness.Harness{
				T: t,
				Opts: &harness.FactoryOptions{
					Config:         config.NewConfig,
					Client:         docker.NewClient,
					ProjectManager: project.NewProjectManager,
				},
			}
			setup := h.NewIsolatedFS(&harness.FSOptions{
				ProjectDir: projectName,
			})

			// Init with --vcs since presets no longer include VCS domains.
			initRes := h.Run("project", "init", projectName, "--yes", "--preset", preset.Name, "--vcs", "github")
			require.NoError(t, initRes.Err,
				"init %s failed\nstdout: %s\nstderr: %s",
				preset.Name, initRes.Stdout, initRes.Stderr)

			// Verify written config has VCS domains from --vcs github.
			snap := readProjectConfig(t, setup.ProjectDir)
			assert.Contains(t, snap.Security.Firewall.AddDomains, "github.com",
				"preset %s: config should contain github.com from --vcs", preset.Name)
			assert.Contains(t, snap.Security.Firewall.AddDomains, "api.github.com",
				"preset %s: config should contain api.github.com from --vcs", preset.Name)
			assert.NotEmpty(t, snap.Build.Image,
				"preset %s: config should have build.image set", preset.Name)

			// Build the image (suppress progress output for clean test logs).
			buildRes := h.Run("build", "--progress=none")
			require.NoError(t, buildRes.Err,
				"build %s preset failed\nstdout: %s\nstderr: %s",
				preset.Name, buildRes.Stdout, buildRes.Stderr)
		})
	}
}

// TestPresetInit_VCSFlagCombinations verifies that different --vcs,
// --git-protocol, and --no-gpg flag combinations produce the correct
// config file content.
func TestPresetInit_VCSFlagCombinations(t *testing.T) {
	tests := []struct {
		name           string
		flags          []string
		wantDomains    []string
		notWantDomains []string
		wantSSHRule    string // expected SSH rule dst, empty = no SSH rule
		wantGPGFalse   bool
	}{
		{
			name:        "github https (default)",
			flags:       []string{"--vcs", "github"},
			wantDomains: []string{"github.com", "api.github.com"},
		},
		{
			name:           "gitlab https",
			flags:          []string{"--vcs", "gitlab"},
			wantDomains:    []string{"gitlab.com", "registry.gitlab.com"},
			notWantDomains: []string{"github.com"},
		},
		{
			name:           "bitbucket https",
			flags:          []string{"--vcs", "bitbucket"},
			wantDomains:    []string{"bitbucket.org", "api.bitbucket.org"},
			notWantDomains: []string{"github.com"},
		},
		{
			name:        "github ssh",
			flags:       []string{"--vcs", "github", "--git-protocol", "ssh"},
			wantDomains: []string{"github.com", "api.github.com"},
			wantSSHRule: "github.com",
		},
		{
			name:           "gitlab ssh no-gpg",
			flags:          []string{"--vcs", "gitlab", "--git-protocol", "ssh", "--no-gpg"},
			wantDomains:    []string{"gitlab.com", "registry.gitlab.com"},
			notWantDomains: []string{"github.com"},
			wantSSHRule:    "gitlab.com",
			wantGPGFalse:   true,
		},
		{
			name:         "github https no-gpg",
			flags:        []string{"--vcs", "github", "--no-gpg"},
			wantDomains:  []string{"github.com", "api.github.com"},
			wantGPGFalse: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := strings.NewReplacer(" ", "-", "(", "", ")", "").Replace(tt.name)
			projectName := "vcs-" + sanitized

			h := &harness.Harness{
				T: t,
				Opts: &harness.FactoryOptions{
					Config:         config.NewConfig,
					Client:         docker.NewClient,
					ProjectManager: project.NewProjectManager,
				},
			}
			setup := h.NewIsolatedFS(&harness.FSOptions{
				ProjectDir: projectName,
			})

			args := []string{"project", "init", projectName, "--yes", "--preset", "Bare"}
			args = append(args, tt.flags...)

			initRes := h.Run(args...)
			require.NoError(t, initRes.Err,
				"init failed\nstdout: %s\nstderr: %s",
				initRes.Stdout, initRes.Stderr)

			snap := readProjectConfig(t, setup.ProjectDir)

			// Verify expected domains are present.
			for _, d := range tt.wantDomains {
				assert.Contains(t, snap.Security.Firewall.AddDomains, d,
					"config should contain domain %s", d)
			}

			// Verify unwanted domains are absent.
			for _, d := range tt.notWantDomains {
				assert.NotContains(t, snap.Security.Firewall.AddDomains, d,
					"config should not contain domain %s", d)
			}

			// Verify SSH rule.
			if tt.wantSSHRule != "" {
				require.NotNil(t, snap.Security.Firewall, "firewall config should exist")
				found := false
				for _, r := range snap.Security.Firewall.Rules {
					if r.Dst == tt.wantSSHRule && r.Port == 22 && r.Proto == "ssh" {
						found = true
						break
					}
				}
				assert.True(t, found, "should have SSH rule for %s:22", tt.wantSSHRule)
			} else {
				// No SSH rule expected.
				if snap.Security.Firewall != nil {
					for _, r := range snap.Security.Firewall.Rules {
						assert.NotEqual(t, 22, r.Port,
							"should not have port 22 rule but found one for %s", r.Dst)
					}
				}
			}

			// Verify GPG setting.
			if tt.wantGPGFalse {
				require.NotNil(t, snap.Security.GitCredentials, "git_credentials should exist")
				require.NotNil(t, snap.Security.GitCredentials.ForwardGPG, "forward_gpg should be set")
				assert.False(t, *snap.Security.GitCredentials.ForwardGPG, "forward_gpg should be false")
			}
		})
	}
}

// TestPresetInit_SSHConnectivity verifies that --git-protocol ssh produces a
// firewall config that actually allows SSH connections to the VCS provider.
// Requires real Docker + firewall.
func TestPresetInit_SSHConnectivity(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			Firewall:       firewall.NewManager,
		},
	}
	setup := h.NewIsolatedFS(&harness.FSOptions{
		ProjectDir: "ssh-connectivity",
	})
	_ = setup

	// Init with GitHub SSH preset.
	initRes := h.Run("project", "init", "ssh-connectivity", "--yes", "--preset", "Bare", "--vcs", "github", "--git-protocol", "ssh")
	require.NoError(t, initRes.Err,
		"init failed\nstdout: %s\nstderr: %s", initRes.Stdout, initRes.Stderr)

	// Disable host auth requirement (test env has no Claude credentials).
	setup.WriteYAML(t, testenv.ProjectConfigLocal, setup.ProjectDir, `
agent:
  claude_code:
    use_host_auth: false
`)

	// Build the image.
	buildRes := h.Run("build", "--progress=none")
	require.NoError(t, buildRes.Err,
		"build failed\nstdout: %s\nstderr: %s", buildRes.Stdout, buildRes.Stderr)

	// Start a detached container so we can exec into it.
	startRes := h.Run("container", "run", "--detach", "--agent", "ssh-test", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err,
		"container start failed\nstdout: %s\nstderr: %s", startRes.Stdout, startRes.Stderr)

	// Verify git can talk to GitHub over SSH through the firewall.
	// GIT_SSH_COMMAND disables host key checking so it doesn't block on prompt.
	// git ls-remote will fail auth (no keys) but the connection itself proves
	// the firewall SSH rule works — exit code 128 with "Permission denied" means
	// TCP connected successfully; a timeout or "connection refused" means blocked.
	sshRes := h.ExecInContainer("ssh-test",
		"bash", "-c",
		`GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5" git ls-remote git@github.com:torvalds/linux.git HEAD 2>&1; exit 0`)
	// We don't check sshRes.Err — auth will fail without keys.
	// What matters: the output should show SSH connected (not a firewall timeout).
	combinedOutput := sshRes.Stdout + sshRes.Stderr
	assert.NotContains(t, combinedOutput, "Connection timed out",
		"SSH should not time out — firewall rule should allow port 22")
	assert.NotContains(t, combinedOutput, "Connection refused",
		"SSH should not be refused — firewall rule should allow port 22")
	assert.True(t,
		strings.Contains(combinedOutput, "Permission denied") ||
			strings.Contains(combinedOutput, "publickey") ||
			strings.Contains(combinedOutput, "Host key verification failed"),
		"SSH should connect and fail auth (proving TCP connectivity through firewall) — got: %s", combinedOutput)

	// Verify HTTPS git also works through the firewall.
	httpsRes := h.ExecInContainer("ssh-test",
		"git", "ls-remote", "--exit-code", "https://github.com/torvalds/linux.git", "HEAD")
	assert.NoError(t, httpsRes.Err,
		"HTTPS git ls-remote should succeed for github.com\nstdout: %s\nstderr: %s",
		httpsRes.Stdout, httpsRes.Stderr)

	// Stop the container.
	stopRes := h.Run("container", "stop", "--agent", "ssh-test")
	require.NoError(t, stopRes.Err,
		"container stop failed\nstdout: %s\nstderr: %s", stopRes.Stdout, stopRes.Stderr)
}
