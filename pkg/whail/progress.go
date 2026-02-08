package whail

import (
	"fmt"
	"strings"
	"time"
)

// IsInternalStep returns true for BuildKit housekeeping vertices that should
// be hidden in progress displays (load build definition, load .dockerignore, etc.).
func IsInternalStep(name string) bool {
	return strings.HasPrefix(name, "[internal]")
}

// CleanStepName strips BuildKit noise from a step name for display.
// Removes --mount=type=cache and similar flags from RUN commands and collapses whitespace.
func CleanStepName(name string) string {
	if idx := strings.Index(name, "RUN --mount="); idx >= 0 {
		rest := name[idx:]
		for strings.HasPrefix(rest, "RUN --mount=") {
			if sp := strings.IndexByte(rest[4:], ' '); sp >= 0 {
				rest = "RUN" + rest[4+sp:]
			} else {
				break
			}
		}
		name = name[:idx] + rest
	}
	parts := strings.Fields(name)
	return strings.Join(parts, " ")
}

// ParseBuildStage extracts the build stage name from a step name like "[stage-2 3/7] RUN ...".
// Returns empty string if no stage bracket is found.
func ParseBuildStage(name string) string {
	if !strings.HasPrefix(name, "[") {
		return ""
	}
	end := strings.Index(name, "]")
	if end < 0 {
		return ""
	}
	inner := name[1:end]
	if sp := strings.IndexByte(inner, ' '); sp > 0 {
		return inner[:sp]
	}
	return inner
}

// FormatBuildDuration returns a compact duration string with sub-second precision.
func FormatBuildDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := d.Seconds()
	switch {
	case secs < 60:
		return fmt.Sprintf("%.1fs", secs)
	case secs < 3600:
		m := int(secs) / 60
		s := int(secs) % 60
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		h := int(secs) / 3600
		m := (int(secs) % 3600) / 60
		return fmt.Sprintf("%dh %dm", h, m)
	}
}
