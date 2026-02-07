package tui

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFireHook_NilHook(t *testing.T) {
	cfg := &ProgressDisplayConfig{}
	result := cfg.fireHook("progress", "before_complete")
	assert.True(t, result.Continue)
	assert.Empty(t, result.Message)
	assert.NoError(t, result.Err)
}

func TestFireHook_Continue(t *testing.T) {
	var capturedComponent, capturedEvent string
	cfg := &ProgressDisplayConfig{
		OnLifecycle: func(component, event string) HookResult {
			capturedComponent = component
			capturedEvent = event
			return HookResult{Continue: true}
		},
	}

	result := cfg.fireHook("progress", "before_complete")
	assert.True(t, result.Continue)
	assert.Equal(t, "progress", capturedComponent)
	assert.Equal(t, "before_complete", capturedEvent)
}

func TestFireHook_Abort(t *testing.T) {
	cfg := &ProgressDisplayConfig{
		OnLifecycle: func(_, _ string) HookResult {
			return HookResult{Continue: false, Message: "user quit"}
		},
	}

	result := cfg.fireHook("progress", "before_complete")
	assert.False(t, result.Continue)
	assert.Equal(t, "user quit", result.Message)
	assert.NoError(t, result.Err)
}

func TestFireHook_Error(t *testing.T) {
	hookErr := fmt.Errorf("hook failed")
	cfg := &ProgressDisplayConfig{
		OnLifecycle: func(_, _ string) HookResult {
			return HookResult{Continue: false, Err: hookErr}
		},
	}

	result := cfg.fireHook("progress", "before_complete")
	assert.False(t, result.Continue)
	assert.ErrorIs(t, result.Err, hookErr)
}
