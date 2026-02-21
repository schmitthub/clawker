package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	dockerimage "github.com/moby/moby/api/types/image"
	moby "github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfig creates a config.Config from a YAML string for tests.
func testConfig(t *testing.T, yaml string) config.Config {
	t.Helper()
	cfg, err := config.ReadFromString(yaml)
	require.NoError(t, err)
	return cfg
}

func newTestClientWithConfig(cfg config.Config) (*Client, *whailtest.FakeAPIClient) {
	fakeAPI := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fakeAPI, whail.EngineOptions{
		LabelPrefix:  cfg.EngineLabelPrefix(),
		ManagedLabel: cfg.EngineManagedLabel(),
	})
	return &Client{Engine: engine, cfg: cfg}, fakeAPI
}

// managedImageInspect returns an ImageInspectResult with the managed label set,
// so that whail.Engine.isManagedImage does not panic on nil Config.
func managedImageInspect(cfg config.Config, ref string) moby.ImageInspectResult {
	return moby.ImageInspectResult{
		InspectResponse: dockerimage.InspectResponse{
			ID: ref,
			Config: &dockerspec.DockerOCIImageConfig{
				DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{},
				ImageConfig: ocispec.ImageConfig{
					Labels: map[string]string{
						cfg.EngineLabelPrefix() + "." + cfg.EngineManagedLabel(): cfg.ManagedLabelValue(),
					},
				},
			},
		},
	}
}

const ensureImageTestYAML = `
project: "testproj"
version: "1.0.0"
build:
  image: "buildpack-deps:bookworm-scm"
workspace:
  remote_path: "/workspace"
`

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
	cfg := testConfig(t, `
project: "myproject"
version: "1.0.0"
build:
  image: "buildpack-deps:bookworm-scm"
  instructions:
    labels:
      dev.clawker.project: "attacker-project"
      custom-label: "custom-value"
workspace:
  remote_path: "/workspace"
`)
	projectCfg := cfg.Project()
	client, _ := newTestClientWithConfig(cfg)
	b := NewBuilder(client, projectCfg, "")
	labels := b.mergeImageLabels(nil)

	// Clawker internal labels must win over user labels
	assert.Equal(t, "myproject", labels[cfg.LabelProject()],
		"clawker internal project label should not be overridable by user labels")
	assert.Equal(t, cfg.ManagedLabelValue(), labels[cfg.LabelManaged()],
		"clawker managed label should be present")

	// User labels that don't conflict should still be present
	assert.Equal(t, "custom-value", labels["custom-label"],
		"non-conflicting user labels should be preserved")
}

func TestEnsureImage_CacheHit(t *testing.T) {
	cfg := testConfig(t, ensureImageTestYAML)
	projectCfg := cfg.Project()
	client, fakeAPI := newTestClientWithConfig(cfg)

	// Pre-compute the expected hash tag by generating the Dockerfile and hashing it
	gen := bundler.NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	hash, err := bundler.ContentHash(dockerfile, nil, "", bundler.EmbeddedScripts())
	require.NoError(t, err)

	hashTag := ImageTagWithHash(projectCfg.Name, hash)

	// Wire fake: image exists for the hash tag (must include managed label to pass whail check)
	fakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...moby.ImageInspectOption) (moby.ImageInspectResult, error) {
		if ref == hashTag {
			return managedImageInspect(cfg, ref), nil
		}
		return moby.ImageInspectResult{}, errors.New("not found")
	}

	// Track TagImage calls
	var tagCalled bool
	var tagSource, tagTarget string
	fakeAPI.ImageTagFn = func(_ context.Context, opts moby.ImageTagOptions) (moby.ImageTagResult, error) {
		tagCalled = true
		tagSource = opts.Source
		tagTarget = opts.Target
		return moby.ImageTagResult{}, nil
	}

	builder := NewBuilder(client, projectCfg, "")
	imageTag := ImageTag(projectCfg.Name)

	err = builder.EnsureImage(context.Background(), imageTag, BuilderOptions{})
	require.NoError(t, err)

	// TagImage should have been called to alias :latest → hash tag
	assert.True(t, tagCalled, "TagImage should be called on cache hit")
	assert.Equal(t, hashTag, tagSource)
	assert.Equal(t, imageTag, tagTarget)
}

func TestEnsureImage_CacheMiss(t *testing.T) {
	cfg := testConfig(t, ensureImageTestYAML)
	projectCfg := cfg.Project()
	client, fakeAPI := newTestClientWithConfig(cfg)

	// Pre-compute the expected hash tag
	gen := bundler.NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	hash, err := bundler.ContentHash(dockerfile, nil, "", bundler.EmbeddedScripts())
	require.NoError(t, err)

	hashTag := ImageTagWithHash(projectCfg.Name, hash)

	// Wire fake: image does NOT exist
	fakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...moby.ImageInspectOption) (moby.ImageInspectResult, error) {
		return moby.ImageInspectResult{}, errors.New("not found")
	}

	// Wire legacy ImageBuild to succeed and capture the tags
	var buildTags []string
	fakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		buildTags = opts.Tags
		return moby.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}

	builder := NewBuilder(client, projectCfg, "")
	imageTag := ImageTag(projectCfg.Name)

	err = builder.EnsureImage(context.Background(), imageTag, BuilderOptions{})
	require.NoError(t, err)

	// The hash tag should be in the build tags
	assert.Contains(t, buildTags, hashTag, "hash tag should be included in build tags")
	assert.Contains(t, buildTags, imageTag, "primary tag should be included in build tags")
}

func TestEnsureImage_ForceBuild(t *testing.T) {
	cfg := testConfig(t, ensureImageTestYAML)
	projectCfg := cfg.Project()
	client, fakeAPI := newTestClientWithConfig(cfg)

	// Wire legacy ImageBuild to succeed
	var buildCalled bool
	fakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, _ moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		buildCalled = true
		return moby.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}

	builder := NewBuilder(client, projectCfg, "")
	imageTag := ImageTag(projectCfg.Name)

	err := builder.EnsureImage(context.Background(), imageTag, BuilderOptions{ForceBuild: true})
	require.NoError(t, err)

	// Build should have been called
	assert.True(t, buildCalled, "Build should be called when ForceBuild is true")
}

func TestEnsureImage_TagImageFailure(t *testing.T) {
	cfg := testConfig(t, ensureImageTestYAML)
	projectCfg := cfg.Project()
	client, fakeAPI := newTestClientWithConfig(cfg)

	// Pre-compute the expected hash tag
	gen := bundler.NewProjectGenerator(cfg, t.TempDir())
	dockerfile, err := gen.Generate()
	require.NoError(t, err)

	hash, err := bundler.ContentHash(dockerfile, nil, "", bundler.EmbeddedScripts())
	require.NoError(t, err)

	hashTag := ImageTagWithHash(projectCfg.Name, hash)

	// Wire fake: image exists (cache hit — must include managed label)
	fakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...moby.ImageInspectOption) (moby.ImageInspectResult, error) {
		if ref == hashTag {
			return managedImageInspect(cfg, ref), nil
		}
		return moby.ImageInspectResult{}, errors.New("not found")
	}

	// Wire TagImage to fail
	fakeAPI.ImageTagFn = func(_ context.Context, _ moby.ImageTagOptions) (moby.ImageTagResult, error) {
		return moby.ImageTagResult{}, errors.New("tag failed: permission denied")
	}

	builder := NewBuilder(client, projectCfg, "")
	imageTag := ImageTag(projectCfg.Name)

	err = builder.EnsureImage(context.Background(), imageTag, BuilderOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag failed")
}

func TestEnsureImage_CustomDockerfileDelegatesToBuild(t *testing.T) {
	// Create a temp dir with a custom Dockerfile
	workDir := t.TempDir()
	customDockerfile := filepath.Join(workDir, "Dockerfile.custom")
	require.NoError(t, os.WriteFile(customDockerfile, []byte("FROM alpine:latest\n"), 0644))

	cfg := testConfig(t, `
project: "testproj"
version: "1.0.0"
build:
  image: "buildpack-deps:bookworm-scm"
  dockerfile: "`+customDockerfile+`"
workspace:
  remote_path: "/workspace"
`)
	projectCfg := cfg.Project()
	client, fakeAPI := newTestClientWithConfig(cfg)

	// Wire legacy ImageBuild to succeed and capture the labels
	var buildLabels map[string]string
	fakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		buildLabels = opts.Labels
		return moby.ImageBuildResult{
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	}

	builder := NewBuilder(client, projectCfg, workDir)
	imageTag := ImageTag(projectCfg.Name)

	err := builder.EnsureImage(context.Background(), imageTag, BuilderOptions{})
	require.NoError(t, err)

	// mergeImageLabels should have been applied (clawker labels present)
	assert.Equal(t, cfg.ManagedLabelValue(), buildLabels[cfg.LabelManaged()],
		"managed label should be applied via mergeImageLabels")
	assert.Equal(t, "testproj", buildLabels[cfg.LabelProject()],
		"project label should be applied via mergeImageLabels")
}

func TestEnsureImage_ContentHashError(t *testing.T) {
	cfg := testConfig(t, `
project: "testproj"
version: "1.0.0"
build:
  image: "buildpack-deps:bookworm-scm"
workspace:
  remote_path: "/workspace"
agent:
  includes:
    - "nonexistent-file.txt"
`)
	projectCfg := cfg.Project()
	client, _ := newTestClientWithConfig(cfg)
	builder := NewBuilder(client, projectCfg, t.TempDir())
	imageTag := ImageTag(projectCfg.Name)

	err := builder.EnsureImage(context.Background(), imageTag, BuilderOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compute content hash")
}
