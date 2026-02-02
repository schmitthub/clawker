package docker

import (
	"context"

	"github.com/schmitthub/clawker/pkg/whail"
)

// Pinger is the subset of the Docker API needed for BuildKit detection.
// Deprecated: Use whail.Pinger directly.
type Pinger = whail.Pinger

// BuildKitEnabled checks whether BuildKit is available.
// Deprecated: Use whail.BuildKitEnabled directly.
func BuildKitEnabled(ctx context.Context, p Pinger) (bool, error) {
	return whail.BuildKitEnabled(ctx, p)
}
