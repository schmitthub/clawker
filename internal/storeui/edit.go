package storeui

import (
	"errors"
	"fmt"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
)

// Result holds the outcome of an interactive edit session.
type Result struct {
	Saved     bool              // True if the user saved changes
	Cancelled bool              // True if the user cancelled
	Modified  map[string]string // Path→value of all modified fields
}

// Option configures the Edit function.
type Option func(*editOptions)

type editOptions struct {
	title       string
	overrides   []Override
	skipPaths   map[string]bool
	saveTargets []SaveTarget
}

// WithTitle sets the editor title displayed at the top.
func WithTitle(title string) Option {
	return func(o *editOptions) {
		o.title = title
	}
}

// WithOverrides provides domain-specific field overrides.
func WithOverrides(overrides []Override) Option {
	return func(o *editOptions) {
		o.overrides = overrides
	}
}

// WithSkipPaths hides the given dotted paths from the editor.
func WithSkipPaths(paths ...string) Option {
	return func(o *editOptions) {
		for _, p := range paths {
			o.skipPaths[p] = true
		}
	}
}

// WithSaveTargets provides the list of locations the user can save to.
// Domain adapters build these using config path accessors.
func WithSaveTargets(targets []SaveTarget) Option {
	return func(o *editOptions) {
		o.saveTargets = targets
	}
}

// Edit runs an interactive field editor for a storage.Store[T].
//
// Flow:
//  1. store.Read() → snapshot
//  2. WalkFields(snapshot) → fields
//  3. Filter skip paths, ApplyOverrides
//  4. tui.RunProgram with editorModel
//  5. If saved: store.Set(func(t *T) { SetFieldValue(t, ...) }) then store.Write(target)
//  6. Return Result
func Edit[T any](ios *iostreams.IOStreams, store *storage.Store[T], opts ...Option) (Result, error) {
	cfg := editOptions{
		title:     "Configuration Editor",
		skipPaths: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// 1. Read current snapshot.
	snapshot := store.Read()

	// 2. Discover fields via reflection.
	fields := WalkFields(snapshot)

	// 3. Filter and apply overrides.
	if len(cfg.skipPaths) > 0 {
		filtered := make([]Field, 0, len(fields))
		for _, f := range fields {
			if !cfg.skipPaths[f.Path] {
				filtered = append(filtered, f)
			}
		}
		fields = filtered
	}
	fields = ApplyOverrides(fields, cfg.overrides)

	// Build save targets. If caller didn't provide any, fall back to store layers.
	saveTargets := cfg.saveTargets
	if len(saveTargets) == 0 {
		for _, l := range store.Layers() {
			saveTargets = append(saveTargets, SaveTarget{
				Label:       l.Filename,
				Description: l.Path,
				Filename:    l.Filename,
			})
		}
	}

	// 4. Run the interactive editor.
	model := newEditorModel(cfg.title, fields, saveTargets)
	finalModel, err := tui.RunProgram(ios, model, tui.WithAltScreen(true))
	if err != nil {
		return Result{}, err
	}

	editor := finalModel.(*editorModel)
	result := Result{
		Saved:     editor.saved,
		Cancelled: editor.cancelled,
		Modified:  editor.modified,
	}

	if !editor.saved || len(editor.modified) == 0 {
		return result, nil
	}

	// 5. Apply changes via store.Set and persist.
	var setErrs []error
	err = store.Set(func(t *T) {
		for path, val := range editor.modified {
			if err := SetFieldValue(t, path, val); err != nil {
				setErrs = append(setErrs, err)
			}
		}
	})
	if err != nil {
		return result, err
	}
	if len(setErrs) > 0 {
		return result, fmt.Errorf("failed to apply %d field change(s): %w", len(setErrs), errors.Join(setErrs...))
	}

	// Write using provenance routing — each top-level key goes back to the
	// layer it originally came from. New keys without provenance go to the
	// highest-priority (most local) layer.
	if err := store.Write(); err != nil {
		return result, err
	}

	return result, nil
}
