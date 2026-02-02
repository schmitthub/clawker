package buildkit

import (
	"context"
	"fmt"
	"sync"

	bkclient "github.com/moby/buildkit/client"

	"github.com/schmitthub/clawker/pkg/whail"
)

// NewImageBuilder returns a closure that builds images using BuildKit's Solve
// API. The closure is intended to be set on Engine.BuildKitImageBuilder.
//
// If apiClient is nil, the returned closure always returns an error.
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
	if apiClient == nil {
		return func(_ context.Context, _ whail.ImageBuildKitOptions) error {
			return fmt.Errorf("buildkit: nil API client")
		}
	}

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
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			drainProgress(statusCh, opts.SuppressOutput)
		}()

		// Solve returns errors for failed vertices. The drainProgress goroutine logs
		// per-vertex errors from the status channel for diagnostics, but Solve's
		// return value is the authoritative error source.
		_, err = bkClient.Solve(ctx, nil, solveOpt, statusCh)
		wg.Wait()
		if err != nil {
			return fmt.Errorf("buildkit: solve: %w", err)
		}
		return nil
	}
}
