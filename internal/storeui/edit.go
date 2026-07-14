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

// Save-destination label vocabulary. BuildLayerTargets applies the placement
// labels (Project, User); LabelLocal is exported for domain adapters that
// relabel targets whose Filename they recognize as a local override —
// which filename that is being domain knowledge storeui does not hold.
const (
	LabelProject = "Project" // walk-up target (in-play file or CWD candidate)
	LabelUser    = "User"    // configured directory candidate (config dir etc.)
	LabelLocal   = "Local"   // domain-applied: discovered local override file
)

// BuildLayerTargets builds save destinations from the store's own write
// targets (storage.Store.WriteTargets), so the editor only ever offers
// locations the store can rediscover on reload — a store without walk-up
// gets no "Project" target. The walk-up target is labeled "Project",
// directory candidates "User", and discovered layer files show their
// shortened path; each target carries the store-reported Filename so a
// domain adapter can relabel filenames it recognizes (e.g. a local override
// file). Virtual layers (defaults) are never offered.
func BuildLayerTargets[T storage.Schema](store *storage.Store[T]) ([]LayerTarget, error) {
	wts, err := store.WriteTargets()
	if err != nil {
		return nil, fmt.Errorf("resolving store write targets: %w", err)
	}
	targets := make([]LayerTarget, 0, len(wts))
	for _, wt := range wts {
		shortPath := ShortenHome(wt.Path)
		var label string
		switch wt.Source {
		case storage.TargetWalkUp:
			label = LabelProject
		case storage.TargetDir, storage.TargetPath:
			label = LabelUser
		case storage.TargetLayer:
			label = shortPath
		default: // future sources — show the path
			label = shortPath
		}
		targets = append(targets, LayerTarget{
			Label:       label,
			Description: shortPath,
			Path:        wt.Path,
			Filename:    wt.Filename,
		})
	}
	return targets, nil
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
	Label       string // Display label (e.g. "Project", "User", "Local")
	Description string // Shortened path for display
	Path        string // Full absolute filesystem path
	Filename    string // Store-configured filename this target serves (for domain relabeling)
}

// Option configures the Edit function.
type Option func(*editOptions)

type editOptions struct {
	title        string
	overrides    []Override
	skipPaths    map[string]bool
	onlyPaths    map[string]bool
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

// WithOnlyPaths restricts the editor to show only the given dotted paths.
// All other fields are excluded. When set, WithSkipPaths is ignored.
func WithOnlyPaths(paths ...string) Option {
	return func(o *editOptions) {
		if o.onlyPaths == nil {
			o.onlyPaths = make(map[string]bool, len(paths))
		}
		for _, p := range paths {
			o.onlyPaths[p] = true
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

// BuildBrowser creates a FieldBrowserModel for a storage.Store[T] without
// running it. The returned model can be wrapped as a WizardPage via
// tui.NewBrowserPage or run standalone via tui.RunProgram. All save/delete callbacks are wired.
func BuildBrowser[T storage.Schema](store *storage.Store[T], opts ...Option) (*tui.FieldBrowserModel, error) {
	cfg := editOptions{
		title:     "Configuration Editor",
		skipPaths: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	for _, t := range cfg.layerTargets {
		if t.Path != "" && !filepath.IsAbs(t.Path) {
			return nil, fmt.Errorf("layer target %q has non-absolute path: %s", t.Label, t.Path)
		}
	}

	buildBrowserState := func() ([]tui.BrowserField, []tui.BrowserLayer) {
		snapshot := store.Read()
		fields := WalkFields(snapshot)
		enrichWithSchema(fields, (*snapshot).Fields())

		if len(cfg.onlyPaths) > 0 {
			filtered := make([]Field, 0, len(cfg.onlyPaths))
			for _, f := range fields {
				if cfg.onlyPaths[f.Path] {
					filtered = append(filtered, f)
				}
			}
			fields = filtered
		} else if len(cfg.skipPaths) > 0 {
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

	browserFields, browserLayers := buildBrowserState()
	browserTargets := layerTargetsToBrowserTargets(cfg.layerTargets)

	onFieldSaved := func(fieldPath, value string, targetIdx int) error {
		if targetIdx < 0 || targetIdx >= len(cfg.layerTargets) {
			return fmt.Errorf("invalid layer target index: %d", targetIdx)
		}
		target := cfg.layerTargets[targetIdx]

		// Coerce the TUI string into the field's typed value (via a fresh T), then
		// set it on the store by path.
		var fresh T
		if err := SetFieldValue(&fresh, fieldPath, value); err != nil {
			return fmt.Errorf("setting field %s: %w", fieldPath, err)
		}
		typed, err := GetFieldValue(&fresh, fieldPath)
		if err != nil {
			return fmt.Errorf("setting field %s: %w", fieldPath, err)
		}
		if err = store.Set(fieldPath, typed); err != nil {
			return fmt.Errorf("updating store: %w", err)
		}

		prov, hasProv := store.Provenance(fieldPath)
		if hasProv && prov.Path != target.Path {
			layerVal := lookupLayerFieldValue(store.Layers(), target.Path, fieldPath)
			if normalizeLayerValue(layerVal) != value {
				if merr := store.MarkForWrite(fieldPath); merr != nil {
					return fmt.Errorf("marking %s for write: %w", fieldPath, merr)
				}
			}
		}

		// Flush ONLY this field: the store may carry other staged mutations
		// (a preset store's seed marks, edits routed to other targets), and a
		// whole-store WriteTo would dump them all into this field's target.
		if werr := store.WriteFieldTo(target.Path, fieldPath); werr != nil {
			return fmt.Errorf("writing to %s: %w", ShortenHome(target.Path), werr)
		}
		return nil
	}

	onFieldDeleted := func(fieldPath string, targetIdx int) error {
		if targetIdx < 0 || targetIdx >= len(cfg.layerTargets) {
			return fmt.Errorf("invalid layer target index: %d", targetIdx)
		}
		target := cfg.layerTargets[targetIdx]

		if _, err := store.Remove(fieldPath); err != nil {
			return fmt.Errorf("deleting from store: %w", err)
		}

		if werr := store.WriteFieldTo(target.Path, fieldPath); werr != nil {
			return fmt.Errorf("deleting from %s: %w", ShortenHome(target.Path), werr)
		}
		return nil
	}

	return tui.NewFieldBrowser(tui.BrowserConfig{
		Title:          cfg.title,
		Fields:         browserFields,
		LayerTargets:   browserTargets,
		Layers:         browserLayers,
		OnFieldSaved:   onFieldSaved,
		OnFieldDeleted: onFieldDeleted,
		OnRefresh:      buildBrowserState,
	}), nil
}

// Edit runs an interactive field editor for a storage.Store[T].
//
// Each field edit is saved immediately to a user-chosen layer target.
// The orchestration flow:
//  1. store.Read() → snapshot
//  2. WalkFields(snapshot) → fields
//  3. Filter skip paths, ApplyOverrides
//  4. Map storeui.Field → tui.BrowserField, run tui.FieldBrowserModel
//  5. OnFieldSaved callback: store.Set + store.WriteTo(target) per field
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

		if len(cfg.onlyPaths) > 0 {
			filtered := make([]Field, 0, len(cfg.onlyPaths))
			for _, f := range fields {
				if cfg.onlyPaths[f.Path] {
					filtered = append(filtered, f)
				}
			}
			fields = filtered
		} else if len(cfg.skipPaths) > 0 {
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
		// Coerce the TUI string into the field's typed value (via a fresh T), then
		// set it on the store by path.
		var fresh T
		if err := SetFieldValue(&fresh, fieldPath, value); err != nil {
			return fmt.Errorf("setting field %s: %w", fieldPath, err)
		}
		typed, err := GetFieldValue(&fresh, fieldPath)
		if err != nil {
			return fmt.Errorf("setting field %s: %w", fieldPath, err)
		}
		if err = store.Set(fieldPath, typed); err != nil {
			return fmt.Errorf("updating store: %w", err)
		}

		// When saving to a layer that isn't the provenance winner,
		// Set() may not have dirtied the path (merged value unchanged).
		// Compare against the target layer's raw data — only force a
		// write when the layer file actually needs updating.
		prov, hasProv := store.Provenance(fieldPath)
		if hasProv && prov.Path != target.Path {
			layerVal := lookupLayerFieldValue(store.Layers(), target.Path, fieldPath)
			if normalizeLayerValue(layerVal) != value {
				if merr := store.MarkForWrite(fieldPath); merr != nil {
					return fmt.Errorf("marking %s for write: %w", fieldPath, merr)
				}
			}
		}

		// Persist dirty fields to the target file. Write() remerges
		// internally, so the snapshot reflects the true merged state.
		if werr := store.WriteTo(target.Path); werr != nil {
			return fmt.Errorf("writing to %s: %w", ShortenHome(target.Path), werr)
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
		if _, err := store.Remove(fieldPath); err != nil {
			return fmt.Errorf("deleting from store: %w", err)
		}

		// Persist the deletion to the target file. Write() remerges
		// internally, so the snapshot reflects the true merged state.
		if werr := store.WriteTo(target.Path); werr != nil {
			return fmt.Errorf("deleting from %s: %w", ShortenHome(target.Path), werr)
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
		readOnly := f.ReadOnly
		// Consumer-defined kinds (> KindLast) have no specialized editor.
		// Force read-only to prevent data corruption via the raw textarea fallback.
		if f.Kind > storage.KindLast {
			readOnly = true
		}
		out[i] = tui.BrowserField{
			Path:        f.Path,
			Label:       f.Label,
			Description: f.Description,
			Kind:        fieldKindToBrowserKind(f.Kind),
			Value:       f.Value,
			EditValue:   f.EditValue,
			Default:     f.Default,
			Source:      source,
			Options:     f.Options,
			Validator:   f.Validator,
			Required:    f.Required,
			ReadOnly:    readOnly,
			Order:       f.Order,
			Editor:      f.Editor,
		}
	}
	return out
}

// normalizeLayerValue formats a raw YAML-decoded value into the same string
// representation that the TUI editor produces, enabling accurate comparison
// to avoid spurious MarkForWrite calls.
func normalizeLayerValue(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// lookupLayerFieldValue finds the raw value for a dotted field path in a
// specific layer identified by its file path. Returns nil if the layer is
// not found or the field is absent from that layer's data.
func lookupLayerFieldValue(layers []storage.LayerInfo, layerPath, fieldPath string) any {
	for _, l := range layers {
		if l.Path != layerPath {
			continue
		}
		segments := strings.Split(fieldPath, ".")
		var cur any = l.Data
		for _, seg := range segments {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur, ok = m[seg]
			if !ok {
				return nil
			}
		}
		return cur
	}
	return nil
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
	case KindTime:
		// An RFC3339 timestamp is a single-line scalar — edit it as plain text.
		// (No dedicated BrowserTime widget; SetFieldValue validates on save.)
		return tui.BrowserText
	case KindMap:
		return tui.BrowserMap
	case KindStructSlice:
		return tui.BrowserStructSlice
	case KindStructMap:
		// Edited as a YAML mapping blob — same editor surface as struct
		// slices (no dedicated struct-map widget).
		return tui.BrowserStructSlice
	default:
		// Consumer-defined kinds (> KindLast) degrade to read-only display.
		// No panic — the kind is known to storage, we just don't have an editor.
		return tui.BrowserStructSlice
	}
}
