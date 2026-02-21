package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHarness_Defaults(t *testing.T) {
	h := NewHarness(t)

	// Check directories exist
	assert.DirExists(t, h.ProjectDir)
	assert.DirExists(t, h.ConfigDir)

	// Check config is set with minimal valid defaults
	require.NotNil(t, h.Config)
	assert.Equal(t, "test-project", h.Config.Project)
	assert.Equal(t, "test-project", h.Project)

	// Check clawker.yaml was written
	assert.FileExists(t, h.ConfigPath())
}

func TestNewHarness_WithProject(t *testing.T) {
	h := NewHarness(t, WithProject("my-project"))

	assert.Equal(t, "my-project", h.Project)
	assert.Equal(t, "my-project", h.Config.Project)
}

func TestNewHarness_WithConfig(t *testing.T) {
	cfg := &config.Project{
		Version: "1",
		Project: "custom-project",
		Build: config.BuildConfig{
			Image: "custom:image",
		},
	}

	h := NewHarness(t, WithConfig(cfg))

	assert.Equal(t, "custom-project", h.Project)
	assert.Equal(t, "custom:image", h.Config.Build.Image)
}

func TestNewHarness_WithConfigBuilder(t *testing.T) {
	h := NewHarness(t,
		WithConfigBuilder(
			builders.NewConfigBuilder().
				WithProject("builder-project").
				WithBuild(builders.AlpineBuild()),
		),
	)

	assert.Equal(t, "builder-project", h.Project)
	assert.Equal(t, "alpine:latest", h.Config.Build.Image)
}

func TestHarness_SetEnv(t *testing.T) {
	// Save original value
	origValue := os.Getenv("TEST_HARNESS_VAR")
	defer os.Setenv("TEST_HARNESS_VAR", origValue)

	// Set initial value
	os.Setenv("TEST_HARNESS_VAR", "initial")

	// Create harness in a subtest so cleanup runs
	t.Run("inner", func(t *testing.T) {
		h := NewHarness(t)
		h.SetEnv("TEST_HARNESS_VAR", "modified")

		assert.Equal(t, "modified", os.Getenv("TEST_HARNESS_VAR"))
	})

	// After subtest completes, cleanup should have restored
	assert.Equal(t, "initial", os.Getenv("TEST_HARNESS_VAR"))
}

func TestHarness_SetEnv_NewVar(t *testing.T) {
	// Make sure the var doesn't exist
	os.Unsetenv("TEST_HARNESS_NEW_VAR")

	// Create harness in a subtest so cleanup runs
	t.Run("inner", func(t *testing.T) {
		h := NewHarness(t)
		h.SetEnv("TEST_HARNESS_NEW_VAR", "new_value")

		assert.Equal(t, "new_value", os.Getenv("TEST_HARNESS_NEW_VAR"))
	})

	// After subtest completes, var should be unset
	assert.Empty(t, os.Getenv("TEST_HARNESS_NEW_VAR"))
}

func TestHarness_UnsetEnv(t *testing.T) {
	// Set initial value
	os.Setenv("TEST_HARNESS_UNSET", "exists")
	defer os.Unsetenv("TEST_HARNESS_UNSET")

	t.Run("inner", func(t *testing.T) {
		h := NewHarness(t)
		h.UnsetEnv("TEST_HARNESS_UNSET")

		assert.Empty(t, os.Getenv("TEST_HARNESS_UNSET"))
	})

	// After subtest completes, should be restored
	assert.Equal(t, "exists", os.Getenv("TEST_HARNESS_UNSET"))
}

func TestHarness_Chdir(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)

	t.Run("inner", func(t *testing.T) {
		h := NewHarness(t)

		// Change to project dir
		h.Chdir()

		// Verify we're in project dir
		currentDir, err := os.Getwd()
		require.NoError(t, err)
		assert.Equal(t, h.ProjectDir, currentDir)
	})

	// After subtest completes, should be back to original
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, origDir, currentDir)
}

func TestHarness_ContainerName(t *testing.T) {
	h := NewHarness(t, WithProject("myapp"))

	assert.Equal(t, "clawker.myapp.dev", h.ContainerName("dev"))
	assert.Equal(t, "clawker.myapp.worker", h.ContainerName("worker"))
}

func TestHarness_ImageName(t *testing.T) {
	tests := []struct {
		name         string
		config       *config.Project
		expectedName string
	}{
		{
			name: "with default image",
			config: &config.Project{
				Project:      "myapp",
				DefaultImage: "custom:v1",
			},
			expectedName: "custom:v1",
		},
		{
			name: "without default image",
			config: &config.Project{
				Project: "myapp",
			},
			expectedName: "clawker-myapp:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHarness(t, WithConfig(tt.config))
			assert.Equal(t, tt.expectedName, h.ImageName())
		})
	}
}

func TestHarness_VolumeName(t *testing.T) {
	h := NewHarness(t, WithProject("myapp"))

	assert.Equal(t, "clawker.myapp.dev-workspace", h.VolumeName("dev", "workspace"))
	assert.Equal(t, "clawker.myapp.dev-claude", h.VolumeName("dev", "claude"))
	assert.Equal(t, "clawker.myapp.dev-history", h.VolumeName("dev", "history"))
}

func TestHarness_NetworkName(t *testing.T) {
	h := NewHarness(t)
	assert.Equal(t, "clawker-net", h.NetworkName())
}

func TestHarness_WriteFile(t *testing.T) {
	h := NewHarness(t)

	// Write a simple file
	h.WriteFile("test.txt", "hello world")

	// Verify it exists
	content, err := os.ReadFile(filepath.Join(h.ProjectDir, "test.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

func TestHarness_WriteFile_NestedDir(t *testing.T) {
	h := NewHarness(t)

	// Write to nested path (should create directories)
	h.WriteFile("a/b/c/test.txt", "nested content")

	// Verify it exists
	content, err := os.ReadFile(filepath.Join(h.ProjectDir, "a/b/c/test.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested content", string(content))
}

func TestHarness_ReadFile(t *testing.T) {
	h := NewHarness(t)

	// Write a file first
	h.WriteFile("data.txt", "some data")

	// Read it back
	content := h.ReadFile("data.txt")
	assert.Equal(t, "some data", content)
}

func TestHarness_FileExists(t *testing.T) {
	h := NewHarness(t)

	// Should not exist initially
	assert.False(t, h.FileExists("nothere.txt"))

	// Create it
	h.WriteFile("exists.txt", "content")

	// Now should exist
	assert.True(t, h.FileExists("exists.txt"))
}

func TestHarness_UpdateConfig(t *testing.T) {
	h := NewHarness(t, WithProject("original"))

	// Update the config
	h.UpdateConfig(func(cfg *config.Project) {
		cfg.Build.Image = "new:image"
	})

	// Verify in-memory config updated
	assert.Equal(t, "new:image", h.Config.Build.Image)

	// Verify file was rewritten - reload and check
	data, err := os.ReadFile(h.ConfigPath())
	require.NoError(t, err)
	reloaded, err := config.ReadFromString(string(data))
	require.NoError(t, err)
	assert.Equal(t, "new:image", reloaded.Project().Build.Image)
}

func TestHarness_ConfigPath(t *testing.T) {
	h := NewHarness(t)

	expected := filepath.Join(h.ProjectDir, _blankCfg.ProjectConfigFileName())
	assert.Equal(t, expected, h.ConfigPath())
}

func TestHarness_Isolation(t *testing.T) {
	// Create two harnesses - they should be completely isolated
	h1 := NewHarness(t, WithProject("project1"))
	h2 := NewHarness(t, WithProject("project2"))

	// Directories should be different
	assert.NotEqual(t, h1.ProjectDir, h2.ProjectDir)
	assert.NotEqual(t, h1.ConfigDir, h2.ConfigDir)

	// Write file in h1
	h1.WriteFile("h1.txt", "from h1")

	// Should not exist in h2
	assert.False(t, h2.FileExists("h1.txt"))
}
