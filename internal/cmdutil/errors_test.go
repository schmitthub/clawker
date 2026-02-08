package cmdutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlagErrorf(t *testing.T) {
	err := FlagErrorf("unknown flag: %s", "--foo")
	assert.Equal(t, "unknown flag: --foo", err.Error())

	var flagErr *FlagError
	require.True(t, errors.As(err, &flagErr))
	assert.Equal(t, "unknown flag: --foo", flagErr.Error())
}

func TestFlagErrorWrap(t *testing.T) {
	inner := fmt.Errorf("bad value")
	err := FlagErrorWrap(inner)
	assert.Equal(t, "bad value", err.Error())

	var flagErr *FlagError
	require.True(t, errors.As(err, &flagErr))
	assert.True(t, errors.Is(err, inner))
}

func TestFlagError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("wrapped")
	err := FlagErrorWrap(inner)

	var flagErr *FlagError
	require.True(t, errors.As(err, &flagErr))
	assert.Equal(t, inner, flagErr.Unwrap())
}

func TestSilentError(t *testing.T) {
	err := fmt.Errorf("something failed: %w", SilentError)
	assert.True(t, errors.Is(err, SilentError))
}

func TestSilentError_Direct(t *testing.T) {
	assert.Equal(t, "SilentError", SilentError.Error())
}

func TestExitError_Error(t *testing.T) {
	err := &ExitError{Code: 42}
	assert.Equal(t, "exit status 42", err.Error())
}

func TestExitError_ZeroCode(t *testing.T) {
	err := &ExitError{Code: 0}
	assert.Equal(t, "exit status 0", err.Error())
}

func TestExitError_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("command failed: %w", &ExitError{Code: 1})
	var exitErr *ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 1, exitErr.Code)
}

func TestFlagErrorWrap_Nil(t *testing.T) {
	assert.Nil(t, FlagErrorWrap(nil))
}

func TestFlagError_UsageTrigger(t *testing.T) {
	// FlagError is a distinct type from stdlib errors, enabling type-based dispatch.
	err := FlagErrorf("invalid --format: %s", "yaml")
	var flagErr *FlagError
	require.True(t, errors.As(err, &flagErr))
	assert.Equal(t, "invalid --format: yaml", flagErr.Error())

	// Not a SilentError.
	assert.False(t, errors.Is(err, SilentError))
}

func TestSilentError_NotFlagError(t *testing.T) {
	var flagErr *FlagError
	assert.False(t, errors.As(SilentError, &flagErr))
}
