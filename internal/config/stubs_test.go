package config

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBlankConfig(t *testing.T) {
	cfg := NewBlankConfig()
	require.NotNil(t, cfg)
	assert.NotEmpty(t, cfg.Domain())
	assert.NotEmpty(t, cfg.LabelManaged())
	assert.NotNil(t, cfg.Project())
}

func TestNewFromString_PanicsOnInvalidYAML(t *testing.T) {
	assert.Panics(t, func() {
		_ = NewFromString("not: [valid")
	})
}

func TestNewFromString_DelegatesReadMethods(t *testing.T) {
	cfg := NewFromString(`build: { image: "alpine:3.20" }`)
	require.NotNil(t, cfg)
	assert.Equal(t, "alpine:3.20", cfg.Project().Build.Image)
}

func TestNewFromString_MutationPanics(t *testing.T) {
	cfg := NewFromString("")

	assert.Panics(t, func() {
		_ = cfg.Set("build.image", "nope")
	})
	assert.Panics(t, func() {
		_ = cfg.Write(WriteOptions{})
	})
}

func TestNewIsolatedTestConfig_WritesAreIsolated(t *testing.T) {
	cfg, read := NewIsolatedTestConfig(t)
	require.NotNil(t, cfg)
	require.NotNil(t, read)

	require.NoError(t, cfg.Set("projects", []any{
		map[string]any{"name": "demo", "root": "/tmp/demo"},
	}))
	require.NoError(t, cfg.Write(WriteOptions{Scope: ScopeRegistry}))

	var settingsBuf, projectBuf, registryBuf bytes.Buffer
	read(&settingsBuf, &projectBuf, &registryBuf)
	assert.Contains(t, registryBuf.String(), "name: demo")
	assert.Contains(t, registryBuf.String(), "root: /tmp/demo")
}
