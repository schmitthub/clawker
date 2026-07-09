package bundler

import (
	"fmt"
	"slices"
	"text/template"
)

// DeclaredBlocks returns the slot names the master Dockerfile template
// declares. A harness template may define any subset; defining any other
// template name is a validation error. Names are positional opportunities
// in the master's instruction ordering, never content-prescriptive.
//
// NOTE: placeholder generic names — final event-centric names TBD.
func DeclaredBlocks() []string {
	return []string{
		"block_1", // root scope, after system packages + Docker CLI, before user context — heavy stack installs
		"block_2", // root scope, after base tooling, before USER ${USERNAME}
		"block_3", // user scope, after the master's static-env section
		"block_4", // user scope, after user_run renders — version-ARG cache zone
		"block_5", // root scope, after trailing USER root, before clawker assets
		"block_6", // final instruction — CMD position
	}
}

// isReservedDefine reports whether name is a template name a harness may
// never define: the master template's own name plus the project-config
// inject-point keys, which must stay disjoint from block names forever.
func isReservedDefine(name string) bool {
	switch name {
	case "Dockerfile",
		"after_from",
		"after_packages",
		"after_user_setup",
		"after_user_switch",
		"after_claude_install",
		"after_harness_install",
		"before_entrypoint":
		return true
	}
	return false
}

func isDeclaredBlock(name string) bool {
	return slices.Contains(DeclaredBlocks(), name)
}

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
		return nil, fmt.Errorf("harness %q: parse %s: %w", b.Name, HarnessTemplateFile, parseErr)
	}

	return tmpl, nil
}

// validateDefines parses the harness fragment standalone and checks every
// defined template name against the declared block slots.
func validateDefines(b *Bundle) error {
	probe, err := template.New("harness-probe").Parse(b.Template)
	if err != nil {
		return fmt.Errorf("harness %q: parse %s: %w", b.Name, HarnessTemplateFile, err)
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
				HarnessTemplateFile,
				name,
			)
		}
		if !isDeclaredBlock(name) {
			return fmt.Errorf(
				"harness %q: %s defines unknown block %q; declared blocks: %v",
				b.Name, HarnessTemplateFile, name, DeclaredBlocks(),
			)
		}
	}
	return nil
}
