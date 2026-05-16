package cmdutil

import (
	"strings"
	"unicode"
)

// ProjectSlugify normalizes a raw project-name candidate (typically
// filepath.Base(cwd) or a --name flag value) into a slug suitable for
// use as a clawker project identifier.
//
// Transform:
//   - lowercase
//   - whitespace runs (incl. tab/newline) → single `-`
//   - control chars (\x00-\x1F not whitespace, plus \x7F) stripped so
//     the result is safe for x509 URI SAN encoding
//   - leading/trailing `-` trimmed (Docker rejects names that don't
//     start with an alnum char)
//
// Everything else (dots, underscores, unicode, etc.) passes through
// unchanged. Downstream consumers (Docker container/volume create,
// x509 cert mint, gRPC IdentityInterceptor) enforce their own
// constraints and produce their own errors at op time. This helper
// never errors and never refuses input.
func ProjectSlugify(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	var prevDash bool
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r):
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		case r == 0x7F || r < 0x20:
			// strip control chars (non-whitespace below 0x20, plus DEL)
		default:
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		}
	}
	return strings.Trim(b.String(), "-")
}
