package build

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	moby "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeTags(t *testing.T) {
	tests := []struct {
		name       string
		primary    string
		additional []string
		expected   []string
	}{
		{
			name:       "primary only",
			primary:    "myapp:latest",
			additional: nil,
			expected:   []string{"myapp:latest"},
		},
		{
			name:       "primary with empty additional",
			primary:    "myapp:latest",
			additional: []string{},
			expected:   []string{"myapp:latest"},
		},
		{
			name:       "primary with one additional",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0"},
			expected:   []string{"myapp:latest", "myapp:v1.0"},
		},
		{
			name:       "primary with multiple additional",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0", "myapp:stable"},
			expected:   []string{"myapp:latest", "myapp:v1.0", "myapp:stable"},
		},
		{
			name:       "duplicate in additional is filtered",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0", "myapp:latest"},
			expected:   []string{"myapp:latest", "myapp:v1.0"},
		},
		{
			name:       "multiple duplicates filtered",
			primary:    "myapp:latest",
			additional: []string{"myapp:v1.0", "myapp:v1.0", "myapp:latest"},
			expected:   []string{"myapp:latest", "myapp:v1.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeTags(tt.primary, tt.additional)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMergeImageLabels_InternalLabelsOverrideUser(t *testing.T) {
	cfg := &config.Project{
		Project: "myproject",
		Version: "1.0.0",
		Build: config.BuildConfig{
			Instructions: &config.DockerInstructions{
				Labels: map[string]string{
					"com.clawker.project": "attacker-project", // attempt to override
					"custom-label":        "custom-value",
				},
			},
		},
	}

	b := NewBuilder(nil, cfg, "")
	labels := b.mergeImageLabels(nil)

	// Clawker internal labels must win over user labels
	assert.Equal(t, "myproject", labels["com.clawker.project"],
		"clawker internal project label should not be overridable by user labels")
	assert.Equal(t, "true", labels["com.clawker.managed"],
		"clawker managed label should be present")

	// User labels that don't conflict should still be present
	assert.Equal(t, "custom-value", labels["custom-label"],
		"non-conflicting user labels should be preserved")
}

// ensureImageTestConfig returns a minimal config for EnsureImage tests.
// It uses a standard base image and project name to produce deterministic hashes.
func ensureImageTestConfig() *config.Project {
	return &config.Project{
		Project: "testproj",
		Version: "1.0.0",
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
	}
}

func TestEnsureImage_CacheHit(t *testing.T) {
	cfg := ensureImageTestConfig()
	fake := dockertest.NewFakeClient()

	// Pre-compute the expected hash tag by generating the Dockerfile and hashing it
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	hash, err := ContentHash(dockerfile, nil, "")
	require.NoError(t, err)

	hashTag := docker.ImageTagWithHash(cfg.Project, hash)

	// Wire fake: image exists for the hash tag
	fake.SetupImageExists(hashTag, true)

	// Track TagImage calls
	var tagCalled bool
	var tagSource, tagTarget string
	fake.FakeAPI.ImageTagFn = func(_ context.Context, opts moby.ImageTagOptions) (moby.ImageTagResult, error) {
		tagCalled = true
		tagSource = opts.Source
		tagTarget = opts.Target
		return moby.ImageTagResult{}, nil
	}

	builder := NewBuilder(fake.Client, cfg, "")
	imageTag := docker.ImageTag(cfg.Project)

	err = builder.EnsureImage(context.Background(), imageTag, Options{})
	require.NoError(t, err)

	// TagImage should have been called to alias :latest → hash tag
	assert.True(t, tagCalled, "TagImage should be called on cache hit")
	assert.Equal(t, hashTag, tagSource)
	assert.Equal(t, imageTag, tagTarget)

	// ImageBuild should NOT have been called (no build on cache hit)
	fake.AssertNotCalled(t, "ImageBuild")
}

func TestEnsureImage_CacheMiss(t *testing.T) {
	cfg := ensureImageTestConfig()
	fake := dockertest.NewFakeClient()

	// Pre-compute the expected hash tag
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	hash, err := ContentHash(dockerfile, nil, "")
	require.NoError(t, err)

	hashTag := docker.ImageTagWithHash(cfg.Project, hash)

	// Wire fake: image does NOT exist
	fake.SetupImageExists(hashTag, false)

	// Wire legacy ImageBuild to succeed and capture the tags
	var buildTags []string
	fake.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		buildTags = opts.Tags
		return moby.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}

	builder := NewBuilder(fake.Client, cfg, "")
	imageTag := docker.ImageTag(cfg.Project)

	err = builder.EnsureImage(context.Background(), imageTag, Options{})
	require.NoError(t, err)

	// Build should have been called
	fake.AssertCalled(t, "ImageBuild")

	// The hash tag should be in the build tags
	assert.Contains(t, buildTags, hashTag, "hash tag should be included in build tags")
	assert.Contains(t, buildTags, imageTag, "primary tag should be included in build tags")
}

func TestEnsureImage_ForceBuild(t *testing.T) {
	cfg := ensureImageTestConfig()
	fake := dockertest.NewFakeClient()

	// Wire legacy ImageBuild to succeed
	var buildCalled bool
	fake.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, _ moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		buildCalled = true
		return moby.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}

	builder := NewBuilder(fake.Client, cfg, "")
	imageTag := docker.ImageTag(cfg.Project)

	err := builder.EnsureImage(context.Background(), imageTag, Options{ForceBuild: true})
	require.NoError(t, err)

	// Build should have been called
	assert.True(t, buildCalled, "Build should be called when ForceBuild is true")

	// ImageInspect (ImageExists) should NOT have been called
	fake.AssertNotCalled(t, "ImageInspect")
}

func TestEnsureImage_TagImageFailure(t *testing.T) {
	cfg := ensureImageTestConfig()
	fake := dockertest.NewFakeClient()

	// Pre-compute the expected hash tag
	gen := NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	hash, err := ContentHash(dockerfile, nil, "")
	require.NoError(t, err)

	hashTag := docker.ImageTagWithHash(cfg.Project, hash)

	// Wire fake: image exists (cache hit)
	fake.SetupImageExists(hashTag, true)

	// Wire TagImage to fail
	fake.FakeAPI.ImageTagFn = func(_ context.Context, _ moby.ImageTagOptions) (moby.ImageTagResult, error) {
		return moby.ImageTagResult{}, errors.New("tag failed: permission denied")
	}

	builder := NewBuilder(fake.Client, cfg, "")
	imageTag := docker.ImageTag(cfg.Project)

	err = builder.EnsureImage(context.Background(), imageTag, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag failed")
}

func TestEnsureImage_CustomDockerfileDelegatesToBuild(t *testing.T) {
	// Create a temp dir with a custom Dockerfile
	workDir := t.TempDir()
	customDockerfile := filepath.Join(workDir, "Dockerfile.custom")
	require.NoError(t, os.WriteFile(customDockerfile, []byte("FROM alpine:latest\n"), 0644))

	cfg := &config.Project{
		Project: "testproj",
		Version: "1.0.0",
		Build: config.BuildConfig{
			Image:      "buildpack-deps:bookworm-scm",
			Dockerfile: customDockerfile,
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
	}

	fake := dockertest.NewFakeClient()

	// Wire legacy ImageBuild to succeed and capture the labels
	var buildLabels map[string]string
	fake.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		buildLabels = opts.Labels
		return moby.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}

	builder := NewBuilder(fake.Client, cfg, workDir)
	imageTag := docker.ImageTag(cfg.Project)

	err := builder.EnsureImage(context.Background(), imageTag, Options{})
	require.NoError(t, err)

	// Build should have been called (custom Dockerfile delegates to Build)
	fake.AssertCalled(t, "ImageBuild")

	// mergeImageLabels should have been applied (clawker labels present)
	assert.Equal(t, "true", buildLabels[docker.LabelManaged],
		"managed label should be applied via mergeImageLabels")
	assert.Equal(t, "testproj", buildLabels[docker.LabelProject],
		"project label should be applied via mergeImageLabels")
}

func TestEnsureImage_ContentHashError(t *testing.T) {
	cfg := ensureImageTestConfig()
	cfg.Agent.Includes = []string{"nonexistent-file.txt"}
	fake := dockertest.NewFakeClient()
	builder := NewBuilder(fake.Client, cfg, t.TempDir())
	imageTag := docker.ImageTag(cfg.Project)

	err := builder.EnsureImage(context.Background(), imageTag, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compute content hash")
}

func TestWriteBuildContextToDir(t *testing.T) {
	cfg := &config.Project{
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: true},
		},
	}

	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.BuildKitEnabled = true

	dockerfile := []byte("FROM alpine:latest\nRUN echo hello\n")
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, dockerfile)
	require.NoError(t, err)

	// Verify Dockerfile was written
	content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	assert.Equal(t, dockerfile, content)

	// Verify all expected scripts exist
	expectedFiles := []string{
		"entrypoint.sh",
		"statusline.sh",
		"claude-settings.json",
		"host-open.sh",
		"callback-forwarder.sh",
		"git-credential-clawker.sh",
		"ssh-agent-proxy.go",
		"init-firewall.sh", // firewall enabled
	}
	for _, name := range expectedFiles {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected file %s to exist", name)
	}

	// Verify scripts are executable
	for _, name := range []string{"entrypoint.sh", "host-open.sh", "init-firewall.sh"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0111, "%s should be executable", name)
	}
}

func TestWriteBuildContextToDir_NoFirewall(t *testing.T) {
	cfg := &config.Project{
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{Enable: false},
		},
	}

	gen := NewProjectGenerator(cfg, t.TempDir())
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, []byte("FROM alpine\n"))
	require.NoError(t, err)

	// Firewall script should NOT be written
	_, err = os.Stat(filepath.Join(dir, "init-firewall.sh"))
	assert.True(t, os.IsNotExist(err), "init-firewall.sh should not exist when firewall disabled")
}

func TestWriteBuildContextToDir_WithIncludes(t *testing.T) {
	workDir := t.TempDir()

	// Create an include file in workDir
	includeContent := []byte("# my include file\n")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), includeContent, 0644))

	cfg := &config.Project{
		Build: config.BuildConfig{
			Image: "buildpack-deps:bookworm-scm",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath: "/workspace",
		},
		Agent: config.AgentConfig{
			Includes: []string{"CLAUDE.md"},
		},
	}

	gen := NewProjectGenerator(cfg, workDir)
	dir := t.TempDir()

	err := gen.WriteBuildContextToDir(dir, []byte("FROM alpine\n"))
	require.NoError(t, err)

	// Verify include file was copied
	content, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, includeContent, content)
}
