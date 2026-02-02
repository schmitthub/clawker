package docker

import (
	"context"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/buildkit"
)

// Pinger is the subset of the Docker API needed for BuildKit detection.
// Deprecated: Use whail.Pinger directly.
type Pinger = whail.Pinger

// BuildKitEnabled checks whether BuildKit is available.
// Deprecated: Use whail.BuildKitEnabled directly.
func BuildKitEnabled(ctx context.Context, p Pinger) (bool, error) {
	return whail.BuildKitEnabled(ctx, p)
}

// WireBuildKit sets up the BuildKit image builder on the given Client.
// This encapsulates the buildkit subpackage dependency so callers don't
// need to import pkg/whail/buildkit directly.
func WireBuildKit(c *Client) {
	c.BuildKitImageBuilder = buildkit.NewImageBuilder(c.APIClient)
}
