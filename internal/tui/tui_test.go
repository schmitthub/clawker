package tui

import (
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTUI(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	require.NotNil(t, tu)
	assert.Equal(t, tio.IOStreams, tu.IOStreams())
	assert.Empty(t, tu.hooks)
}

func TestTUI_RegisterHooks(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	h1 := LifecycleHook(func(_, _ string) HookResult { return HookResult{Continue: true} })
	h2 := LifecycleHook(func(_, _ string) HookResult { return HookResult{Continue: true} })

	tu.RegisterHooks(h1)
	assert.Len(t, tu.hooks, 1)

	tu.RegisterHooks(h2)
	assert.Len(t, tu.hooks, 2)
}

func TestTUI_RunProgress_InjectsHook(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	var hookFired bool
	tu.RegisterHooks(func(component, event string) HookResult {
		hookFired = true
		assert.Equal(t, "progress", component)
		assert.Equal(t, "before_complete", event)
		return HookResult{Continue: true}
	})

	ch := make(chan ProgressStep, 1)
	ch <- ProgressStep{ID: "1", Name: "test step", Status: StepComplete}
	close(ch)

	result := tu.RunProgress("plain", ProgressDisplayConfig{
		Title: "Test",
	}, ch)

	assert.NoError(t, result.Err)
	assert.True(t, hookFired, "registered hook should fire via RunProgress")
}

func TestTUI_RunProgress_NoHooks(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	ch := make(chan ProgressStep, 1)
	ch <- ProgressStep{ID: "1", Name: "test step", Status: StepComplete}
	close(ch)

	result := tu.RunProgress("plain", ProgressDisplayConfig{
		Title: "Test",
	}, ch)

	assert.NoError(t, result.Err)
}

func TestTUI_RunProgress_DoesNotOverrideExplicitHook(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	var registeredFired, explicitFired bool
	tu.RegisterHooks(func(_, _ string) HookResult {
		registeredFired = true
		return HookResult{Continue: true}
	})

	ch := make(chan ProgressStep, 1)
	ch <- ProgressStep{ID: "1", Name: "test step", Status: StepComplete}
	close(ch)

	// Explicit OnLifecycle takes precedence.
	result := tu.RunProgress("plain", ProgressDisplayConfig{
		Title: "Test",
		OnLifecycle: func(_, _ string) HookResult {
			explicitFired = true
			return HookResult{Continue: true}
		},
	}, ch)

	assert.NoError(t, result.Err)
	assert.True(t, explicitFired, "explicit hook should fire")
	assert.False(t, registeredFired, "registered hook should NOT fire when explicit hook set")
}

func TestTUI_ComposedHook_ShortCircuitsOnAbort(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	var h1Fired, h2Fired bool
	tu.RegisterHooks(
		func(_, _ string) HookResult {
			h1Fired = true
			return HookResult{Continue: false, Message: "aborted"}
		},
		func(_, _ string) HookResult {
			h2Fired = true
			return HookResult{Continue: true}
		},
	)

	hook := tu.composedHook()
	result := hook("test", "event")

	assert.True(t, h1Fired, "first hook should fire")
	assert.False(t, h2Fired, "second hook should NOT fire after abort")
	assert.False(t, result.Continue)
	assert.Equal(t, "aborted", result.Message)
}

func TestTUI_ComposedHook_ShortCircuitsOnError(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	hookErr := fmt.Errorf("hook failed")
	var h2Fired bool
	tu.RegisterHooks(
		func(_, _ string) HookResult {
			return HookResult{Continue: false, Err: hookErr}
		},
		func(_, _ string) HookResult {
			h2Fired = true
			return HookResult{Continue: true}
		},
	)

	hook := tu.composedHook()
	result := hook("test", "event")

	assert.False(t, h2Fired, "second hook should NOT fire after error")
	assert.ErrorIs(t, result.Err, hookErr)
}

func TestTUI_ComposedHook_SingleHookDirect(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	tu := NewTUI(tio.IOStreams)

	var capturedComponent, capturedEvent string
	tu.RegisterHooks(func(component, event string) HookResult {
		capturedComponent = component
		capturedEvent = event
		return HookResult{Continue: true}
	})

	hook := tu.composedHook()
	result := hook("progress", "before_complete")

	assert.True(t, result.Continue)
	assert.Equal(t, "progress", capturedComponent)
	assert.Equal(t, "before_complete", capturedEvent)
}
