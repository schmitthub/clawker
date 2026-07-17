package cmdutil

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RunBundleAutoUpdate fires the opt-in bundle auto-update check at the start of a
// bundle-consuming command (build, container run/create, monitor up) and prints
// any advisories to stderr. It is warn-and-proceed by contract: it NEVER blocks
// the command — a manager-construction failure or an internal check failure is
// surfaced as a warning, not an error, so an unreachable bundle source can never
// stop a build or run that could otherwise proceed on the cached content.
func RunBundleAutoUpdate(
	ctx context.Context, bundleManager func() (*bundle.Manager, error), ios *iostreams.IOStreams,
) {
	if bundleManager == nil {
		return
	}
	mgr, err := bundleManager()
	if err != nil {
		fmt.Fprintf(ios.ErrOut, "%s bundle auto-update skipped: %v\n", ios.ColorScheme().WarningIcon(), err)
		return
	}
	for _, w := range mgr.AutoUpdateCheck(ctx) {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().WarningIcon(), w.Message)
	}
}
