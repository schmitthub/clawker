package consts

import (
	"fmt"
	"regexp"
)

// NameMaxLength is the maximum length for a name governed by the unified
// naming rule below.
const NameMaxLength = 32

// nameRe is the unified naming rule shared by every clawker-registered
// dev-stack surface: stack names, harness names, and the registry/overlay
// keys that key them (a stacks:/harnesses: registry entry, or a
// build.harnesses.<name> overlay key). One rule everywhere a name becomes a
// directory name, a registry key, and — for harnesses — a Docker image tag
// segment: lowercase letters, digits, and single internal hyphens, no
// leading or trailing hyphen.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName rejects a name that does not match the unified naming rule
// (lowercase kebab-case, 1-NameMaxLength characters). This is the single
// naming rule for stack names, harness names, stack/harness registry keys,
// and build.harnesses overlay keys — never a per-surface regex.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > NameMaxLength {
		return fmt.Errorf("name %q must be between 1 and %d characters", name, NameMaxLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf(
			"name %q is invalid: must match %s (lowercase letters, digits, and single hyphens; no leading or trailing hyphen)",
			name,
			nameRe.String(),
		)
	}
	return nil
}

// ValidateHarnessName applies ValidateName plus the harness-specific
// reservation: a harness registry key doubles as its built image's tag, so
// it may not collide with a reserved tag alias.
func ValidateHarnessName(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	switch name {
	case ImageTagDefaultAlias, ImageTagLatest, ImageTagBase:
		return fmt.Errorf("name %q is reserved as an image tag alias", name)
	}
	return nil
}
