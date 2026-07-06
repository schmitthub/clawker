package harness

import (
	"fmt"
	"text/template"
)

// Compose parses the master Dockerfile template and overlays the bundle's
// {{define}} blocks onto its declared slots, returning the executable
// template set. The harness fragment is validated first: it may only define
// slot names the master declares, so a bundle cannot disturb the master's
// instruction ordering or cache architecture.
func Compose(master string, b *Bundle) (*template.Template, error) {
	if err := validateDefines(b); err != nil {
		return nil, err
	}

	tmpl, err := template.New("Dockerfile").Parse(master)
	if err != nil {
		return nil, fmt.Errorf("parse master Dockerfile template: %w", err)
	}

	if _, parseErr := tmpl.Parse(b.Template); parseErr != nil {
		return nil, fmt.Errorf("harness %q: parse %s: %w", b.Name, TemplateFile, parseErr)
	}

	return tmpl, nil
}

// validateDefines parses the harness fragment standalone and checks every
// defined template name against the declared block slots.
func validateDefines(b *Bundle) error {
	probe, err := template.New("harness-probe").Parse(b.Template)
	if err != nil {
		return fmt.Errorf("harness %q: parse %s: %w", b.Name, TemplateFile, err)
	}

	for _, t := range probe.Templates() {
		name := t.Name()
		if name == "harness-probe" {
			continue // the fragment's own root
		}
		if isReservedDefine(name) {
			return fmt.Errorf(
				"harness %q: %s defines reserved name %q (inject-point keys and the master template name cannot be overridden)",
				b.Name,
				TemplateFile,
				name,
			)
		}
		if !isDeclaredBlock(name) {
			return fmt.Errorf(
				"harness %q: %s defines unknown block %q; declared blocks: %v",
				b.Name, TemplateFile, name, DeclaredBlocks(),
			)
		}
	}
	return nil
}
