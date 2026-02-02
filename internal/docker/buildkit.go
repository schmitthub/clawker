package docker

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/moby/moby/api/types/build"
	mobytypes "github.com/moby/moby/api/types"
)

// Pinger is the subset of the Docker API needed for BuildKit detection.
type Pinger interface {
	Ping(ctx context.Context) (mobytypes.Ping, error)
}

// BuildKitEnabled checks whether BuildKit is available.
// Follows Docker CLI's detection: env var > daemon ping > OS heuristic.
func BuildKitEnabled(ctx context.Context, p Pinger) (bool, error) {
	// 1. DOCKER_BUILDKIT env var takes precedence
	if v := os.Getenv("DOCKER_BUILDKIT"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("DOCKER_BUILDKIT environment variable expects boolean value: %w", err)
		}
		return enabled, nil
	}

	// 2. Ping daemon — BuilderVersion field reports preferred builder
	ping, err := p.Ping(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to ping Docker daemon: %w", err)
	}

	if ping.BuilderVersion == build.BuilderBuildKit {
		return true, nil
	}

	// 3. Default: enabled (only disabled for Windows/WCOW)
	return ping.OSType != "windows", nil
}
