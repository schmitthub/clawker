package storeui

import (
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
	title     string
	overrides []Override
	skipPaths map[string]bool
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

// Edit runs an interactive field editor for a storage.Store[T].
//
// Flow:
//  1. store.Read() → snapshot
//  2. WalkFields(snapshot) → fields
//  3. Filter skip paths, ApplyOverrides
//  4. tui.RunProgram with editorModel
//  5. If saved: store.Set(func(t *T) { SetFieldValue(t, ...) }) then store.Write(layer)
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

	// Build layer list from store.
	storeLayerInfos := store.Layers()
	layers := make([]string, len(storeLayerInfos))
	for i, l := range storeLayerInfos {
		layers[i] = l.Filename
	}

	// 4. Run the interactive editor.
	model := newEditorModel(cfg.title, fields, layers)
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
	err = store.Set(func(t *T) {
		for path, val := range editor.modified {
			// SetFieldValue errors are not expected here since the editor only
			// presents fields that WalkFields discovered. Log and skip on error.
			_ = SetFieldValue(t, path, val)
		}
	})
	if err != nil {
		return result, err
	}

	layer := editor.selectedLayer()
	if layer != "" {
		err = store.Write(layer)
	} else {
		err = store.Write()
	}
	if err != nil {
		return result, err
	}

	return result, nil
}
