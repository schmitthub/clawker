package configtest

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemorySettingsLoader_ImplementsInterface(t *testing.T) {
	var _ config.SettingsLoader = (*InMemorySettingsLoader)(nil)
}

func TestInMemorySettingsLoader_Path(t *testing.T) {
	l := NewInMemorySettingsLoader()
	assert.Equal(t, "(in-memory)", l.Path())
}

func TestInMemorySettingsLoader_ProjectSettingsPath(t *testing.T) {
	l := NewInMemorySettingsLoader()
	assert.Empty(t, l.ProjectSettingsPath())
}

func TestInMemorySettingsLoader_Exists(t *testing.T) {
	l := NewInMemorySettingsLoader()
	assert.True(t, l.Exists())
}

func TestInMemorySettingsLoader_LoadDefault(t *testing.T) {
	l := NewInMemorySettingsLoader()
	settings, err := l.Load()
	require.NoError(t, err)
	require.NotNil(t, settings)

	// Should return default settings
	expected := config.DefaultSettings()
	assert.Equal(t, expected.DefaultImage, settings.DefaultImage)
}

func TestInMemorySettingsLoader_LoadWithInitial(t *testing.T) {
	initial := &config.Settings{DefaultImage: "custom:latest"}
	l := NewInMemorySettingsLoader(initial)

	settings, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, "custom:latest", settings.DefaultImage)
}

func TestInMemorySettingsLoader_SaveAndLoad(t *testing.T) {
	l := NewInMemorySettingsLoader()

	saved := &config.Settings{DefaultImage: "saved:v1"}
	err := l.Save(saved)
	require.NoError(t, err)

	loaded, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, "saved:v1", loaded.DefaultImage)
}

func TestInMemorySettingsLoader_EnsureExists(t *testing.T) {
	l := NewInMemorySettingsLoader()

	created, err := l.EnsureExists()
	require.NoError(t, err)
	assert.True(t, created, "first call should return true")

	created, err = l.EnsureExists()
	require.NoError(t, err)
	assert.False(t, created, "second call should return false")
}
