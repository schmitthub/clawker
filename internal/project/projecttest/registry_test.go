package projecttest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistrarFunc_ImplementsRegistrar(t *testing.T) {
	fn := RegistrarFunc(func(displayName, rootDir string) (string, error) {
		assert.Equal(t, "My Project", displayName)
		assert.Equal(t, "/tmp/my-project", rootDir)
		return "my-project", nil
	})

	slug, err := fn.Register("My Project", "/tmp/my-project")
	require.NoError(t, err)
	assert.Equal(t, "my-project", slug)
}

func TestRegistrarFunc_Nil(t *testing.T) {
	var fn RegistrarFunc
	slug, err := fn.Register("My Project", "/tmp/my-project")
	require.Error(t, err)
	assert.Equal(t, "", slug)
}

func TestMockRegistrar_DefaultBehavior(t *testing.T) {
	m := &MockRegistrar{}

	slug, err := m.Register("My Project", "/tmp/my-project")
	require.NoError(t, err)
	assert.Equal(t, "test-project", slug)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, "My Project", m.Calls[0].DisplayName)
	assert.Equal(t, "/tmp/my-project", m.Calls[0].RootDir)
}

func TestMockRegistrar_CustomFunc(t *testing.T) {
	m := &MockRegistrar{
		RegisterFunc: func(displayName, rootDir string) (string, error) {
			assert.Equal(t, "My Project", displayName)
			assert.Equal(t, "/tmp/my-project", rootDir)
			return "my-project", nil
		},
	}

	slug, err := m.Register("My Project", "/tmp/my-project")
	require.NoError(t, err)
	assert.Equal(t, "my-project", slug)
	require.Len(t, m.Calls, 1)
}

func TestMockRegistrar_DefaultSlug(t *testing.T) {
	m := &MockRegistrar{DefaultSlug: "custom-slug"}

	slug, err := m.Register("My Project", "/tmp/my-project")
	require.NoError(t, err)
	assert.Equal(t, "custom-slug", slug)
}

func TestMockRegistrar_Error(t *testing.T) {
	expected := errors.New("boom")
	m := &MockRegistrar{Err: expected}

	slug, err := m.Register("My Project", "/tmp/my-project")
	require.Error(t, err)
	assert.ErrorIs(t, err, expected)
	assert.Equal(t, "", slug)
	require.Len(t, m.Calls, 1)
}

func TestMockRegistrar_Reset(t *testing.T) {
	m := &MockRegistrar{
		DefaultSlug: "custom-slug",
		Err:         errors.New("boom"),
		RegisterFunc: func(displayName, rootDir string) (string, error) {
			return "x", nil
		},
	}

	_, _ = m.Register("My Project", "/tmp/my-project")
	require.Len(t, m.Calls, 1)

	m.Reset()

	assert.Empty(t, m.Calls)
	assert.Nil(t, m.RegisterFunc)
	assert.Equal(t, "", m.DefaultSlug)
	assert.Nil(t, m.Err)
}
