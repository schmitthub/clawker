package storeui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

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
func Edit[T any](ios *iostreams.IOStreams, store *storage.Store[T], opts ...Option) (Result, error) {
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

	// 4. Map to tui types and run the interactive browser.
	provMap := store.ProvenanceMap()
	browserFields := fieldsToBrowserFields(fields, provMap)
	browserLayers := layersToBrowserLayers(store.Layers())
	browserTargets := layerTargetsToBrowserTargets(cfg.layerTargets)

	// Build field kind lookup for type-aware YAML writes.
	fieldKinds := make(map[string]FieldKind, len(fields))
	for _, f := range fields {
		fieldKinds[f.Path] = f.Kind
	}

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

		// Persist single field to the target file.
		kind := fieldKinds[fieldPath]
		if err := writeFieldToFile(target.Path, fieldPath, value, kind); err != nil {
			return fmt.Errorf("writing to %s: %w", ShortenHome(target.Path), err)
		}

		return nil
	}

	model := tui.NewFieldBrowser(tui.BrowserConfig{
		Title:        cfg.title,
		Fields:       browserFields,
		LayerTargets: browserTargets,
		Layers:       browserLayers,
		OnFieldSaved: onFieldSaved,
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

// writeFieldToFile persists a single field value to a YAML file.
// If the file doesn't exist, it is created with just that field.
// If it exists, the field is merged into the existing content.
func writeFieldToFile(filePath, fieldPath, value string, kind FieldKind) error {
	// Ensure parent directory exists.
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	// Read existing file (or start with empty map).
	existing := make(map[string]any)
	data, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading existing %s: %w", filePath, err)
	}
	if err == nil && len(data) > 0 {
		if err := yaml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing %s: %w", filePath, err)
		}
	}
	if existing == nil {
		existing = make(map[string]any)
	}

	// Build nested map from dotted path and merge.
	nested := buildNestedMap(fieldPath, typedYAMLValue(value, kind))
	mergeMap(existing, nested)

	// Preserve existing file permissions, default to 0644 for new files.
	perm := os.FileMode(0o644)
	if info, err := os.Stat(filePath); err == nil {
		perm = info.Mode().Perm()
	}

	// Atomic write: temp + rename.
	tmp, err := os.CreateTemp(dir, ".clawker-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	enc := yaml.NewEncoder(tmp)
	enc.SetIndent(2)
	if err := enc.Encode(existing); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("marshaling YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("flushing YAML encoder: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("syncing %s: %w", tmpName, err)
	}
	tmp.Close()

	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("setting permissions on %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming %s to %s: %w", tmpName, filePath, err)
	}

	return nil
}

// buildNestedMap converts "build.image" + value into {"build": {"image": value}}.
func buildNestedMap(dottedPath string, value any) map[string]any {
	segments := strings.Split(dottedPath, ".")
	result := make(map[string]any)
	current := result
	for i, seg := range segments {
		if i == len(segments)-1 {
			current[seg] = value
		} else {
			next := make(map[string]any)
			current[seg] = next
			current = next
		}
	}
	return result
}

// mergeMap recursively merges src into dst. Nested maps are merged;
// all other values are overwritten.
func mergeMap(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeMap(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

// typedYAMLValue converts a string value to the appropriate Go type based on
// the field's known kind, so YAML output uses native types (bool, int, []string).
//
// SetFieldValue validates the value before this function is called, so the
// parse-failure fallthrough (returning the raw string) should be unreachable.
// If it is reached, the raw string is still valid YAML — just not the expected type.
func typedYAMLValue(s string, kind FieldKind) any {
	switch kind {
	case KindBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	case KindInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case KindStringSlice:
		parts := strings.Split(s, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				result = append(result, t)
			}
		}
		return result
	}
	// KindText, KindSelect, KindDuration, KindComplex — all stored as strings.
	return s
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
		out[i] = tui.BrowserLayer{
			Label: ShortenHome(l.Path),
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
