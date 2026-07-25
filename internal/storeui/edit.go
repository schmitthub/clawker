package storeui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

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

// unreadableValue is the browse-list marker for a field whose stored value
// could not be decoded in any shape. It is deliberately not blank: blank is
// what an unset field renders, and conflating the two would let an operator
// save over data the editor never showed them.
const unreadableValue = "<unreadable>"

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
		fields := schemaFields(store)

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

		if err := stageFieldValue(store, fieldPath, value); err != nil {
			return err
		}

		// Flush ONLY this field: the store may carry other staged mutations
		// (a preset store's seed marks, edits routed to other targets), and a
		// whole-store WriteTo would dump them all into this field's target.
		if werr := store.WriteFieldTo(target.Path, fieldKey(fieldPath)...); werr != nil {
			return fmt.Errorf("writing to %s: %w", ShortenHome(target.Path), werr)
		}
		return nil
	}

	onFieldDeleted := func(fieldPath string, targetIdx int) error {
		if targetIdx < 0 || targetIdx >= len(cfg.layerTargets) {
			return fmt.Errorf("invalid layer target index: %d", targetIdx)
		}
		target := cfg.layerTargets[targetIdx]

		key := fieldKey(fieldPath)
		// An already-unset field is nothing to delete, not a failure: the row
		// the user pressed "d" on was showing a default or a lower layer.
		if err := store.Remove(key...); err != nil && !errors.Is(err, storage.ErrKeyNotFound) {
			return fmt.Errorf("deleting from store: %w", err)
		}

		if werr := store.WriteFieldTo(target.Path, key...); werr != nil {
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
//  1. schemaFields(store) → fields (schema metadata + one storage.Get per field)
//  2. Filter skip paths, ApplyOverrides
//  3. Map storeui.Field → tui.BrowserField, run tui.FieldBrowserModel
//  4. OnFieldSaved callback: store.Set + store.WriteFieldTo(target) per field
//  5. Return Result
func Edit[T storage.Schema](ios *iostreams.IOStreams, store *storage.Store[T], opts ...Option) (Result, error) {
	model, err := BuildBrowser(store, opts...)
	if err != nil {
		return Result{}, err
	}

	finalModel, err := tui.RunProgram(ios, model, tui.WithAltScreen(true))
	if err != nil {
		return Result{}, fmt.Errorf("running field editor: %w", err)
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

// fieldKey splits a dotted schema path into storage key segments. Schema field
// names never contain a literal dot, so the split is lossless.
func fieldKey(path string) []string {
	return strings.Split(path, ".")
}

// schemaFields builds the editable field list for a store: the schema owns the
// metadata (path, label, description, kind, default) and the store owns the
// values, one storage.Get per declared leaf. There is no whole-struct read.
func schemaFields[T storage.Schema](store *storage.Store[T]) []Field {
	var zero T
	all := zero.Fields().All()
	fields := make([]Field, 0, len(all))
	for i, sf := range all {
		read := fieldValue(store, sf.Kind(), fieldKey(sf.Path()))
		value, editValue := read.value, read.editValue
		desc := sf.Description()
		def := sf.Default()
		readOnly := false
		switch {
		case read.err != nil:
			// The field holds something the store cannot decode in any shape.
			// Render it as unreadable — visibly distinct from the blank an unset
			// field shows — and lock the row: an operator must not overwrite a
			// value they were never shown. Clearing the key with "d" still works.
			value, editValue = unreadableValue, ""
			desc = fmt.Sprintf("%s (%s: %v)", desc, unreadableValue, read.err)
			def = ""
			readOnly = true
		case read.set && value == "":
			// Explicitly set to empty (`key: ""`, `key: []`) is a real value that
			// masks lower layers — the default is NOT in effect, so the row must
			// not render "<default> (default)". An unset key keeps it.
			def = ""
		}
		fields = append(fields, Field{
			Path:        sf.Path(),
			Label:       sf.Label(),
			Description: desc,
			Kind:        sf.Kind(),
			Value:       value,
			EditValue:   editValue,
			Default:     def,
			Required:    sf.Required(),
			Order:       i,
			ReadOnly:    readOnly,
			// Presentation-only concerns the schema does not describe; domain
			// adapters supply them through ApplyOverrides.
			Options:   nil,
			Validator: nil,
			Editor:    nil,
		})
	}
	return fields
}

// fieldValue reads one field from the store and renders it for display. The
// FieldKind picks the Go shape the merged YAML is decoded into.
func fieldValue[T storage.Schema](store *storage.Store[T], kind FieldKind, key []string) fieldRead {
	switch kind {
	case KindText, KindSelect:
		return readField(store, key, func(v string) (string, string) { return v, "" })
	case KindBool:
		return readField(store, key, func(v bool) (string, string) { return strconv.FormatBool(v), "" })
	case KindInt:
		return readField(store, key, func(v int64) (string, string) { return strconv.FormatInt(v, 10), "" })
	case KindDuration:
		return readField(store, key, func(v time.Duration) (string, string) { return v.String(), "" })
	case KindTime:
		return readField(store, key, formatTime)
	case KindStringSlice:
		return readField(store, key, func(v []string) (string, string) { return strings.Join(v, ", "), "" })
	case KindMap:
		return readField(store, key, func(v map[string]string) (string, string) {
			return countSummary(len(v), "entry", "entries"), marshalYAMLValue(reflect.ValueOf(v))
		})
	case KindStructSlice:
		return readField(store, key, func(v []any) (string, string) {
			return countSummary(len(v), "item", "items"), marshalYAMLValue(reflect.ValueOf(v))
		})
	default:
		// KindStructMap and consumer-defined kinds (> KindLast) have no dedicated
		// Go shape: decode untyped and edit as a YAML blob.
		return readField(store, key, func(v any) (string, string) {
			return summarizeValue(v), marshalYAMLValue(reflect.ValueOf(v))
		})
	}
}

// fieldRead is one field's read result: how it renders in the browse list, what
// pre-populates its editor, whether the key carries a value at all (false =
// unset, so the schema default shows through), and the read error when the
// field could not be decoded in any shape.
type fieldRead struct {
	value     string
	editValue string
	set       bool
	err       error
}

// readField decodes the field at key into V and renders it with format,
// falling back to rawFieldValue when the decode does not produce V.
func readField[V any, T storage.Schema](
	store *storage.Store[T],
	key []string,
	format func(V) (string, string),
) fieldRead {
	v, err := storage.Get[V](store, key...)
	if err != nil {
		return rawFieldValue(store, key, err)
	}
	value, editValue := format(v)
	return fieldRead{value: value, editValue: editValue, set: true, err: nil}
}

// formatTime renders an RFC3339Nano scalar; the zero time is the schema's
// "never happened" and displays blank rather than as a sentinel date.
func formatTime(v time.Time) (string, string) {
	if v.IsZero() {
		return "", ""
	}
	return v.Format(time.RFC3339Nano), ""
}

// summarizeValue renders the compact browse summary for an untyped value: a
// container gets a count, anything else its YAML scalar form.
func summarizeValue(v any) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Map {
		return countSummary(rv.Len(), "entry", "entries")
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return countSummary(rv.Len(), "item", "items")
	}
	return marshalYAMLValue(rv)
}

// rawFieldValue is the fallback for a Get that did not produce the kind's Go
// shape. An absent key is simply unset. Anything else means the merged value
// contradicts its declared kind — structurally impossible after the
// construction-time strict decode, but display it untyped rather than lying
// about it being unset.
//
// When even the untyped read fails the field is unreadable: it holds something,
// but nothing this process can render. That error is returned, never folded —
// rendering an unreadable field as unset would invite the operator to save over
// data they never saw.
func rawFieldValue[T storage.Schema](store *storage.Store[T], key []string, err error) fieldRead {
	if errors.Is(err, storage.ErrKeyNotFound) {
		return fieldRead{}
	}
	v, gerr := storage.Get[any](store, key...)
	if gerr != nil {
		return fieldRead{value: "", editValue: "", set: false, err: gerr}
	}
	y := marshalYAMLValue(reflect.ValueOf(v))
	return fieldRead{value: y, editValue: y, set: true, err: nil}
}

// countSummary renders the compact browse summary for container fields
// ("3 entries"); an empty container renders blank so the default shows.
func countSummary(n int, one, many string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + one
	default:
		return fmt.Sprintf("%d %s", n, many)
	}
}

// stageFieldValue coerces a TUI string into T's typed value and stages it on
// the store: Set for a value, Remove when the editor produced nothing at all
// (a cleared map or struct blob), since storage.Set rejects nil and Remove is
// the one unset verb. An explicit empty scalar or list is a real value and is
// staged as such.
func stageFieldValue[T storage.Schema](store *storage.Store[T], fieldPath, value string) error {
	var fresh T
	if err := SetFieldValue(&fresh, fieldPath, value); err != nil {
		return fmt.Errorf("setting field %s: %w", fieldPath, err)
	}
	typed, err := GetFieldValue(&fresh, fieldPath)
	if err != nil {
		return fmt.Errorf("setting field %s: %w", fieldPath, err)
	}

	key := fieldKey(fieldPath)
	if isNilValue(typed) {
		if rerr := store.Remove(key...); rerr != nil && !errors.Is(rerr, storage.ErrKeyNotFound) {
			return fmt.Errorf("clearing field %s: %w", fieldPath, rerr)
		}
		return nil
	}
	if serr := store.Set(key, typed); serr != nil {
		return fmt.Errorf("updating store: %w", serr)
	}
	return nil
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
