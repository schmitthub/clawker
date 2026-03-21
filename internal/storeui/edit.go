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

// SaveTarget represents a location where changes can be persisted.
// This is a storeui concept — it maps a user-visible label to a filesystem
// path for store.WriteTo(). The tui layer only sees BrowserSaveTarget
// (label + description, no path).
type SaveTarget struct {
	Label       string // Display label (e.g. "User settings", "Project local")
	Description string // Short description shown in the save dialog
	Path        string // Full filesystem path for store.WriteTo(), or "" for provenance routing
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
// Orchestration flow:
//  1. store.Read() → snapshot
//  2. WalkFields(snapshot) → fields
//  3. Filter skip paths, ApplyOverrides
//  4. Map storeui.Field → tui.BrowserField, run tui.FieldBrowserModel
//  5. If saved: store.Set(func(t *T) { SetFieldValue(t, ...) }) then store.Write/WriteTo
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
				Path:        l.Path,
			})
		}
	}

	// 4. Map to tui types and run the interactive browser.
	browserFields := fieldsToBrowserFields(fields)
	browserTargets := targetsToBrowserTargets(saveTargets)

	model := tui.NewFieldBrowser(tui.BrowserConfig{
		Title:       cfg.title,
		Fields:      browserFields,
		SaveTargets: browserTargets,
	})
	finalModel, err := tui.RunProgram(ios, model, tui.WithAltScreen(true))
	if err != nil {
		return Result{}, err
	}

	browser := finalModel.(*tui.FieldBrowserModel)
	br := browser.Result()
	result := Result{
		Saved:     br.Saved,
		Cancelled: br.Cancelled,
		Modified:  br.Modified,
	}

	if !br.Saved || len(br.Modified) == 0 {
		return result, nil
	}

	// 5. Apply changes via store.Set and persist.
	var setErrs []error
	err = store.Set(func(t *T) {
		for path, val := range br.Modified {
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

	// Persist: provenance routing by default (each section goes back to
	// its original file). When the user selected a specific target path,
	// write the entire tree to that path via WriteTo.
	if br.SaveTargetIndex >= 0 && br.SaveTargetIndex < len(saveTargets) {
		target := saveTargets[br.SaveTargetIndex]
		if target.Path != "" {
			if err := store.WriteTo(target.Path); err != nil {
				return result, err
			}
		} else {
			if err := store.Write(); err != nil {
				return result, err
			}
		}
	} else {
		if err := store.Write(); err != nil {
			return result, err
		}
	}

	return result, nil
}

// fieldsToBrowserFields maps storeui fields to tui browser fields.
func fieldsToBrowserFields(fields []Field) []tui.BrowserField {
	out := make([]tui.BrowserField, len(fields))
	for i, f := range fields {
		out[i] = tui.BrowserField{
			Path:        f.Path,
			Label:       f.Label,
			Description: f.Description,
			Kind:        fieldKindToBrowserKind(f.Kind),
			Value:       f.Value,
			Default:     f.Default,
			Options:     f.Options,
			Validator:   f.Validator,
			Required:    f.Required,
			ReadOnly:    f.ReadOnly,
			Order:       f.Order,
		}
	}
	return out
}

// targetsToBrowserTargets maps storeui save targets to tui browser targets.
func targetsToBrowserTargets(targets []SaveTarget) []tui.BrowserSaveTarget {
	out := make([]tui.BrowserSaveTarget, len(targets))
	for i, t := range targets {
		out[i] = tui.BrowserSaveTarget{
			Label:       t.Label,
			Description: t.Description,
		}
	}
	return out
}

// fieldKindToBrowserKind maps storeui FieldKind to tui BrowserFieldKind.
func fieldKindToBrowserKind(k FieldKind) tui.BrowserFieldKind {
	switch k {
	case KindText:
		return tui.BrowserText
	case KindBool:
		return tui.BrowserBool
	case KindTriState:
		return tui.BrowserTriState
	case KindSelect:
		return tui.BrowserSelect
	case KindInt:
		return tui.BrowserInt
	case KindStringSlice:
		return tui.BrowserStringSlice
	case KindDuration:
		return tui.BrowserDuration
	case KindComplex:
		return tui.BrowserComplex
	default:
		return tui.BrowserComplex
	}
}
