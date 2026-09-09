package tui

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/iostreams"
	cltext "github.com/schmitthub/clawker/internal/text"
)

// TUI provides the interactive presentation layer.
// Constructed once via Factory; hooks registered separately after construction.
// This enables pointer-sharing: commands capture *TUI eagerly at NewCmd time,
// while hooks are registered later (e.g., in PersistentPreRunE after flag parsing).
type TUI struct {
	ios   *iostreams.IOStreams
	hooks []LifecycleHook
}

// NewTUI creates a TUI bound to the given IOStreams.
func NewTUI(ios *iostreams.IOStreams) *TUI {
	if ios == nil {
		panic("NewTUI: IOStreams must not be nil")
	}
	return &TUI{ios: ios}
}

// RegisterHooks appends one or more lifecycle hooks.
// Hooks fire in registration order; the first non-continue result short-circuits.
func (t *TUI) RegisterHooks(hooks ...LifecycleHook) {
	t.hooks = append(t.hooks, hooks...)
}

// RunProgress displays a multi-step progress view, delegating to the package-level
// RunProgress function. Registered hooks are injected into cfg.OnLifecycle if the
// caller has not already set one.
func (t *TUI) RunProgress(mode string, cfg ProgressDisplayConfig, ch <-chan ProgressStep) ProgressResult {
	if len(t.hooks) > 0 && cfg.OnLifecycle == nil {
		cfg.OnLifecycle = t.composedHook()
	}
	return RunProgress(t.ios, mode, cfg, ch)
}

// IOStreams returns the underlying IOStreams for callers that need direct access.
func (t *TUI) IOStreams() *iostreams.IOStreams {
	return t.ios
}

// RenderDetails renders aligned labels and plain values with the active color
// scheme. It returns an empty string when there are no rows.
func (t *TUI) RenderDetails(pairs []KeyValuePair) string {
	if len(pairs) == 0 {
		return ""
	}
	maxKeyLen := 0
	for _, pair := range pairs {
		if width := cltext.CountVisibleWidth(pair.Key); width > maxKeyLen {
			maxKeyLen = width
		}
	}
	cs := t.ios.ColorScheme()
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		label := fmt.Sprintf("%-*s:", maxKeyLen, pair.Key)
		lines = append(lines, cs.Primary(label)+" "+pair.Value)
	}
	return strings.Join(lines, "\n")
}

// RunWizard runs a multi-step wizard using the given steps.
// Each step wraps a pre-constructed WizardPage — the wizard is a generic
// page sequencer that delegates rendering and input to each page model.
// Returns the collected values and whether the wizard was submitted (vs cancelled).
func (t *TUI) RunWizard(steps []WizardStep) (WizardResult, error) {
	if len(steps) == 0 {
		return WizardResult{}, fmt.Errorf("wizard requires at least one step")
	}
	model := newWizardModel(steps)
	finalModel, err := RunProgram(t.ios, &model, WithAltScreen(true))
	if err != nil {
		return WizardResult{}, err
	}
	if wm, ok := finalModel.(*wizardModel); ok {
		return WizardResult{
			Values:    wm.values,
			Submitted: wm.submitted,
		}, nil
	}
	return WizardResult{}, fmt.Errorf("unexpected model type %T from wizard program", finalModel)
}

// composedHook returns a single LifecycleHook that chains all registered hooks.
// Hooks fire in order; the first abort (Continue=false) or error short-circuits.
func (t *TUI) composedHook() LifecycleHook {
	if len(t.hooks) == 1 {
		return t.hooks[0]
	}
	hooks := t.hooks // capture slice
	return func(component, event string) HookResult {
		for _, h := range hooks {
			result := h(component, event)
			if !result.Continue || result.Err != nil {
				return result
			}
		}
		return HookResult{Continue: true}
	}
}
