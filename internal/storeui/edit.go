package storeui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
)

// ShortenHome replaces $HOME prefix with ~ for display.
func ShortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// ResolveLocalPath determines the CWD dot-file path using dual-placement:
// if .clawker/ dir exists → .clawker/{filename}, otherwise → .{filename}.
func ResolveLocalPath(cwd, filename string) string {
	clawkerDir := filepath.Join(cwd, ".clawker")
	if info, err := os.Stat(clawkerDir); err == nil && info.IsDir() {
		return filepath.Join(clawkerDir, filename)
	}
	return filepath.Join(cwd, "."+filename)
}

// Ptr returns a pointer to a copy of the given value.
// Useful for constructing Override fields.
func Ptr[T any](v T) *T {
	return &v
}

// Result holds the outcome of an interactive edit session.
type Result struct {
	Saved      bool // True if any field was persisted
	Cancelled  bool // True if the user cancelled
	SavedCount int  // Number of fields successfully saved
}

// LayerTarget represents a save destination for a single field.
// Domain adapters build these from config accessors.
type LayerTarget struct {
	Label       string // Display label (e.g. "Original", "Local", "User")
	Description string // Shortened path for display
	Path        string // Full absolute filesystem path
}

// Option configures the Edit function.
type Option func(*editOptions)

type editOptions struct {
	title        string
	overrides    []Override
	skipPaths    map[string]bool
	layerTargets []LayerTarget
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

// WithLayerTargets provides the per-field save destinations.
// Domain adapters build these using config path accessors.
func WithLayerTargets(targets []LayerTarget) Option {
	return func(o *editOptions) {
		o.layerTargets = targets
	}
}

// Edit runs an interactive field editor for a storage.Store[T].
//
// Each field edit is saved immediately to a user-chosen layer target.
// The orchestration flow:
//  1. store.Read() → snapshot
//  2. WalkFields(snapshot) → fields
//  3. Filter skip paths, ApplyOverrides
//  4. Map storeui.Field → tui.BrowserField, run tui.FieldBrowserModel
//  5. OnFieldSaved callback: store.Set + writeFieldToFile per field
//  6. Return Result
func Edit[T storage.Schema](ios *iostreams.IOStreams, store *storage.Store[T], opts ...Option) (Result, error) {
	cfg := editOptions{
		title:     "Configuration Editor",
		skipPaths: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Validate layer targets early.
	for _, t := range cfg.layerTargets {
		if t.Path != "" && !filepath.IsAbs(t.Path) {
			return Result{}, fmt.Errorf("layer target %q has non-absolute path: %s", t.Label, t.Path)
		}
	}

	// buildBrowserState reads the current store snapshot and produces
	// the TUI field and layer representations. Called at init and after
	// every save/delete to refresh the display with winning values.
	buildBrowserState := func() ([]tui.BrowserField, []tui.BrowserLayer) {
		snapshot := store.Read()
		fields := WalkFields(snapshot)
		enrichWithSchema(fields, (*snapshot).Fields())

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

		provMap := store.ProvenanceMap()
		return fieldsToBrowserFields(fields, provMap), layersToBrowserLayers(store.Layers())
	}

	// Initial state.
	browserFields, browserLayers := buildBrowserState()
	browserTargets := layerTargetsToBrowserTargets(cfg.layerTargets)

	// Wire per-field save callback.
	onFieldSaved := func(fieldPath, value string, targetIdx int) error {
		if targetIdx < 0 || targetIdx >= len(cfg.layerTargets) {
			return fmt.Errorf("invalid layer target index: %d", targetIdx)
		}
		target := cfg.layerTargets[targetIdx]

		// Update in-memory store.
		var setFieldErr error
		if err := store.Set(func(t *T) {
			if err := SetFieldValue(t, fieldPath, value); err != nil {
				setFieldErr = err
			}
		}); err != nil {
			return fmt.Errorf("updating store: %w", err)
		}
		if setFieldErr != nil {
			return fmt.Errorf("setting field %s: %w", fieldPath, setFieldErr)
		}

		// Persist the dirty field to the target file.
		if err := store.Write(storage.ToPath(target.Path)); err != nil {
			return fmt.Errorf("writing to %s: %w", ShortenHome(target.Path), err)
		}
		return nil
	}

	// Wire per-field delete callback.
	onFieldDeleted := func(fieldPath string, targetIdx int) error {
		if targetIdx < 0 || targetIdx >= len(cfg.layerTargets) {
			return fmt.Errorf("invalid layer target index: %d", targetIdx)
		}
		target := cfg.layerTargets[targetIdx]

		// Remove from the in-memory store tree.
		if _, err := store.Delete(fieldPath); err != nil {
			return fmt.Errorf("deleting from store: %w", err)
		}

		// Persist the deletion to the target file.
		if err := store.Write(storage.ToPath(target.Path)); err != nil {
			return fmt.Errorf("deleting from %s: %w", ShortenHome(target.Path), err)
		}
		return nil
	}

	model := tui.NewFieldBrowser(tui.BrowserConfig{
		Title:          cfg.title,
		Fields:         browserFields,
		LayerTargets:   browserTargets,
		Layers:         browserLayers,
		OnFieldSaved:   onFieldSaved,
		OnFieldDeleted: onFieldDeleted,
		OnRefresh:      buildBrowserState,
	})
	finalModel, err := tui.RunProgram(ios, model, tui.WithAltScreen(true))
	if err != nil {
		return Result{}, err
	}

	browser, ok := finalModel.(*tui.FieldBrowserModel)
	if !ok {
		return Result{}, fmt.Errorf("unexpected model type from TUI: %T", finalModel)
	}
	br := browser.Result()
	return Result{
		Saved:      br.Saved,
		Cancelled:  br.Cancelled,
		SavedCount: br.SavedCount,
	}, nil
}

// enrichWithSchema replaces the schema metadata (Label, Description, Default, Kind)
// on walked fields with authoritative values from the storage.Schema.
// Runtime values (Value, Order) are preserved from the walked fields.
func enrichWithSchema(fields []Field, schema storage.FieldSet) {
	for i := range fields {
		sf := schema.Get(fields[i].Path)
		if sf == nil {
			continue
		}
		fields[i].Label = sf.Label()
		fields[i].Description = sf.Description()
		fields[i].Default = sf.Default()
		fields[i].Kind = sf.Kind()
	}
}

// fieldsToBrowserFields maps storeui fields to tui browser fields.
// provMap provides field path → source file path for provenance display.
func fieldsToBrowserFields(fields []Field, provMap map[string]string) []tui.BrowserField {
	out := make([]tui.BrowserField, len(fields))
	for i, f := range fields {
		source := resolveFieldSource(f.Path, provMap)
		out[i] = tui.BrowserField{
			Path:        f.Path,
			Label:       f.Label,
			Description: f.Description,
			Kind:        fieldKindToBrowserKind(f.Kind),
			Value:       f.Value,
			Default:     f.Default,
			Source:      source,
			Options:     f.Options,
			Validator:   f.Validator,
			Required:    f.Required,
			ReadOnly:    f.ReadOnly,
			Order:       f.Order,
		}
	}
	return out
}

// resolveFieldSource finds the source file for a field path by checking
// the provenance map for an exact match, then for a parent path match
// (e.g. "build.image" matches provenance for "build").
func resolveFieldSource(fieldPath string, provMap map[string]string) string {
	// Exact match.
	if src, ok := provMap[fieldPath]; ok {
		return ShortenHome(src)
	}
	// Walk up the path segments looking for a parent match.
	for path := fieldPath; path != ""; {
		if idx := strings.LastIndex(path, "."); idx >= 0 {
			path = path[:idx]
		} else {
			break
		}
		if src, ok := provMap[path]; ok {
			return ShortenHome(src)
		}
	}
	return ""
}

// layersToBrowserLayers maps storage LayerInfo to tui BrowserLayers.
// Layers are ordered highest→lowest priority (matching store.Layers() order).
func layersToBrowserLayers(layers []storage.LayerInfo) []tui.BrowserLayer {
	out := make([]tui.BrowserLayer, len(layers))
	for i, l := range layers {
		label := ShortenHome(l.Path)
		if l.Path == "" {
			label = "(defaults)"
		}
		out[i] = tui.BrowserLayer{
			Label: label,
			Data:  l.Data,
		}
	}
	return out
}

// layerTargetsToBrowserTargets maps storeui layer targets to tui browser targets.
func layerTargetsToBrowserTargets(targets []LayerTarget) []tui.BrowserLayerTarget {
	out := make([]tui.BrowserLayerTarget, len(targets))
	for i, t := range targets {
		out[i] = tui.BrowserLayerTarget{
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
