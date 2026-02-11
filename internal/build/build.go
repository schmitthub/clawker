// Package build holds build-time metadata injected via ldflags.
// It is the single source of truth for version, date, and display info.
//
// Leaf package: stdlib only, no internal imports.
package build

import "runtime/debug"

// Variables injected via ldflags at build time.
// Defaults are used for development builds (go run / go build without flags).
var (
	Version = "DEV"
	Date    = "" // YYYY-MM-DD, empty for dev builds
)

func init() {
	if Version == "DEV" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "(devel)" {
			Version = info.Main.Version
		}
	}
}
