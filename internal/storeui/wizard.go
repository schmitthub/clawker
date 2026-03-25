package storeui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
)

// WizardOption configures the Wizard function.
type WizardOption func(*wizardOptions)

type wizardOptions struct {
	title     string
	fields    []string   // dotted paths to include, in wizard step order
	overrides []Override // domain-specific field overrides
	writePath string     // target file path for store.Write
}

// WithWizardFields specifies which fields to include in the wizard, in order.
// Only fields matching these dotted paths appear as wizard steps.
// The order of paths determines the wizard step order.
func WithWizardFields(paths ...string) WizardOption {
	return func(o *wizardOptions) {
		o.fields = paths
	}
}

// WithWizardOverrides provides domain-specific field overrides.
// These work identically to Edit[T] overrides — they can change Kind, Options,
// Label, etc. Hidden overrides are applied before wizard field generation.
func WithWizardOverrides(overrides ...Override) WizardOption {
	return func(o *wizardOptions) {
		o.overrides = overrides
	}
}

// WithWizardTitle sets the title used for wizard step labels.
func WithWizardTitle(title string) WizardOption {
	return func(o *wizardOptions) {
		o.title = title
	}
}

// WithWizardWritePath sets the file path where the store is written after
// the wizard completes. If not set, the caller is responsible for writing.
func WithWizardWritePath(path string) WizardOption {
	return func(o *wizardOptions) {
		o.writePath = path
	}
}

// Wizard runs a step-by-step guided editor for a storage.Store[T].
//
// When the wizard is submitted but no values changed (user accepted all
// defaults), Result.Saved is false and store.Write is NOT called. Callers
// in init-style workflows where a file must always be written should check
// for !Result.Cancelled and write the store themselves if Result.SavedCount == 0.
// Each store field is presented as a wizard step with its current value
// pre-populated as the default. The user can accept or modify each value.
//
// The orchestration flow:
//  1. store.Read() → snapshot
//  2. WalkFields(snapshot) → discover all fields
//  3. enrichWithSchema → apply struct tag metadata (labels, descriptions, kinds)
//  4. ApplyOverrides → apply domain-specific customizations
//  5. Filter + order by WithWizardFields paths
//  6. Map storeui.Field → tui.WizardField (kind mapping + value pre-population)
//  7. TUI.RunWizard → collect user input
//  8. Apply changed values → store.Set(SetFieldValue) + optional store.Write
//  9. Return Result
func Wizard[T storage.Schema](t *tui.TUI, store *storage.Store[T], opts ...WizardOption) (Result, error) {
	cfg := wizardOptions{
		title: "Setup",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Read current snapshot and discover fields.
	snapshot := store.Read()
	fields := WalkFields(snapshot)
	enrichWithSchema(fields, (*snapshot).Fields())
	fields = ApplyOverrides(fields, cfg.overrides)

	// Filter and order fields by the requested paths.
	if len(cfg.fields) > 0 {
		fields = filterAndOrder(fields, cfg.fields)
	}

	if len(fields) == 0 {
		return Result{}, fmt.Errorf("wizard: no fields to display")
	}

	// Map storeui fields to wizard fields.
	var wizardFields []tui.WizardField
	var mappedPaths []string // tracks which paths are in the wizard (parallel to wizardFields)
	originalValues := make(map[string]string)

	for _, f := range fields {
		wf, ok := fieldToWizardField(f)
		if !ok {
			continue // skip unmappable kinds
		}
		wizardFields = append(wizardFields, wf)
		mappedPaths = append(mappedPaths, f.Path)
		originalValues[f.Path] = f.Value
	}

	if len(wizardFields) == 0 {
		return Result{}, fmt.Errorf("wizard: no mappable fields found")
	}

	// Run the wizard.
	result, err := t.RunWizard(wizardFields)
	if err != nil {
		return Result{}, fmt.Errorf("wizard: %w", err)
	}

	if !result.Submitted {
		return Result{Cancelled: true}, nil
	}

	// Apply changed values back to the store. All values were validated
	// by the wizard's per-field validators before submission, so SetFieldValue
	// failures here indicate programming errors (wrong path, type mismatch).
	// If a SetFieldValue fails mid-loop, earlier fields are applied in-memory
	// but store.Write is skipped — the caller should treat the error as fatal.
	var changed int
	var setErr error
	if err := store.Set(func(t *T) {
		for i, path := range mappedPaths {
			fieldID := wizardFields[i].ID
			newVal, ok := result.Values[fieldID]
			if !ok {
				continue
			}
			if newVal == originalValues[path] {
				continue
			}
			if err := SetFieldValue(t, path, newVal); err != nil {
				setErr = fmt.Errorf("setting %s: %w", path, err)
				return
			}
			changed++
		}
	}); err != nil {
		return Result{}, fmt.Errorf("wizard: updating store: %w", err)
	}
	if setErr != nil {
		return Result{}, fmt.Errorf("wizard: %w", setErr)
	}

	// Write to the target path if specified and there were changes.
	if cfg.writePath != "" && changed > 0 {
		if err := store.Write(storage.ToPath(cfg.writePath)); err != nil {
			return Result{}, fmt.Errorf("wizard: writing to %s: %w", ShortenHome(cfg.writePath), err)
		}
	}

	return Result{
		Saved:      changed > 0,
		SavedCount: changed,
	}, nil
}

// filterAndOrder returns only the fields matching the given paths, in the
// order specified by paths. Fields not in the path list are excluded.
func filterAndOrder(fields []Field, paths []string) []Field {
	idx := make(map[string]Field, len(fields))
	for _, f := range fields {
		idx[f.Path] = f
	}

	result := make([]Field, 0, len(paths))
	for _, p := range paths {
		if f, ok := idx[p]; ok {
			result = append(result, f)
		}
	}
	return result
}

// fieldToWizardField maps a storeui Field to a tui WizardField.
// Returns (field, true) if the kind is mappable, (zero, false) if the field
// should be skipped (KindMap, KindStructSlice, consumer-defined kinds).
func fieldToWizardField(f Field) (tui.WizardField, bool) {
	wf := tui.WizardField{
		ID:    f.Path,
		Title: f.Label,
	}

	// Use description as prompt, fall back to label.
	if f.Description != "" {
		wf.Prompt = f.Description
	} else {
		wf.Prompt = f.Label
	}

	switch f.Kind {
	case KindText:
		wf.Kind = tui.FieldText
		wf.Default = f.Value
		wf.Validator = f.Validator
		wf.Required = f.Required

	case KindBool:
		wf.Kind = tui.FieldConfirm
		wf.DefaultYes = f.Value == "true"

	case KindInt:
		wf.Kind = tui.FieldText
		wf.Default = f.Value
		wf.Validator = chainValidators(f.Validator, validateInt)

	case KindDuration:
		wf.Kind = tui.FieldText
		wf.Default = f.Value
		wf.Validator = chainValidators(f.Validator, validateDuration)

	case KindSelect:
		wf.Kind = tui.FieldSelect
		wf.Options = make([]tui.FieldOption, len(f.Options))
		for i, opt := range f.Options {
			wf.Options[i] = tui.FieldOption{Label: opt}
		}
		// Find the default index matching the current value.
		for i, opt := range f.Options {
			if opt == f.Value {
				wf.DefaultIdx = i
				break
			}
		}

	case KindStringSlice:
		wf.Kind = tui.FieldText
		wf.Default = f.Value // already comma-separated from WalkFields
		wf.Validator = f.Validator

	case KindMap, KindStructSlice:
		return tui.WizardField{}, false

	default:
		// Consumer-defined kinds (> KindLast) and any unrecognized kinds — skip.
		return tui.WizardField{}, false
	}

	return wf, true
}

// validateInt is a validator for integer fields.
func validateInt(val string) error {
	if val == "" {
		return nil
	}
	if _, err := strconv.ParseInt(val, 10, 64); err != nil {
		return fmt.Errorf("must be a valid integer")
	}
	return nil
}

// validateDuration is a validator for duration fields.
func validateDuration(val string) error {
	if val == "" {
		return nil
	}
	if _, err := time.ParseDuration(val); err != nil {
		return fmt.Errorf("must be a valid duration (e.g. 30s, 5m, 1h)")
	}
	return nil
}

// chainValidators combines an optional domain validator with a type validator.
// The domain validator runs first; if it passes, the type validator runs.
func chainValidators(domain, typeVal func(string) error) func(string) error {
	if domain == nil {
		return typeVal
	}
	return func(val string) error {
		if err := domain(val); err != nil {
			return err
		}
		return typeVal(val)
	}
}
