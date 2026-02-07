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
