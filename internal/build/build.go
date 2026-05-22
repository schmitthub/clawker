// Package build holds build-time metadata injected via ldflags.
// It is the single source of truth for version, date, and display info.
//
// Leaf package: stdlib only, no internal imports.
package build

import "runtime/debug"

// Variables injected via ldflags at build time.
// Defaults are used for development builds (go run / go build without flags).
var (
	Version  = "DEV"
	Date     = ""        // RFC3339 commit timestamp (from GoReleaser {{.CommitDate}}), empty for dev builds
	Revision = "unknown" // Git commit SHA (from Makefile $(git rev-parse HEAD) or GoReleaser {{.FullCommit}})
)

func init() {
	if Version == "DEV" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "(devel)" {
			Version = info.Main.Version
		}
	}
	if Revision == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && s.Value != "" {
					Revision = s.Value
					break
				}
			}
		}
	}
}
