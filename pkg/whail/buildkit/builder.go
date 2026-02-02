package buildkit

import (
	"context"
	"fmt"

	bkclient "github.com/moby/buildkit/client"

	"github.com/schmitthub/clawker/pkg/whail"
)

// NewImageBuilder returns a closure that builds images using BuildKit's Solve
// API. The closure is intended to be set on Engine.BuildKitImageBuilder.
//
// Each invocation creates a fresh BuildKit client connection via DialHijack,
// runs Solve, and closes the connection. Label enforcement is handled by
// Engine.ImageBuildKit before the closure is called — the closure receives
// already-merged labels.
//
// Usage:
//
//	engine, _ := whail.New(ctx)
//	engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
func NewImageBuilder(apiClient DockerDialer) func(context.Context, whail.ImageBuildKitOptions) error {
	return func(ctx context.Context, opts whail.ImageBuildKitOptions) error {
		bkClient, err := NewBuildKitClient(ctx, apiClient)
		if err != nil {
			return fmt.Errorf("buildkit: connect: %w", err)
		}
		defer bkClient.Close()

		solveOpt, err := toSolveOpt(opts)
		if err != nil {
			return err
		}

		statusCh := make(chan *bkclient.SolveStatus)
		go drainProgress(statusCh, opts.SuppressOutput)

		_, err = bkClient.Solve(ctx, nil, solveOpt, statusCh)
		if err != nil {
			return fmt.Errorf("buildkit: solve: %w", err)
		}
		return nil
	}
}
