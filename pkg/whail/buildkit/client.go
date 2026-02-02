// Package buildkit provides BuildKit client connectivity for whail.
//
// This subpackage imports moby/buildkit and its transitive dependencies (gRPC,
// protobuf, containerd, opentelemetry). Consumers who only need whail's
// label-based Docker wrapper do not pay this cost — only importing this
// subpackage adds the dependency tree.
//
// Usage:
//
//	engine, _ := whail.New(ctx)
//	engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)
package buildkit

import (
	"context"
	"fmt"
	"net"

	bkclient "github.com/moby/buildkit/client"
)

// DockerDialer abstracts the DialHijack capability on the moby client.
// Engine.APIClient (which embeds *client.Client) satisfies this interface.
type DockerDialer interface {
	DialHijack(ctx context.Context, url, proto string, meta map[string][]string) (net.Conn, error)
}

// NewBuildKitClient creates a BuildKit client connected to Docker's embedded
// buildkitd via the /grpc and /session hijack endpoints. This is the same
// connection pattern used by docker/buildx internally.
//
// The caller is responsible for calling Close() on the returned client.
func NewBuildKitClient(ctx context.Context, apiClient DockerDialer) (*bkclient.Client, error) {
	c, err := bkclient.New(ctx, "",
		bkclient.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return apiClient.DialHijack(ctx, "/grpc", "h2c", nil)
		}),
		bkclient.WithSessionDialer(func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return apiClient.DialHijack(ctx, "/session", proto, meta)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("buildkit: failed to create client: %w", err)
	}
	return c, nil
}

// VerifyConnection checks that the BuildKit client can communicate with the
// daemon and at least one worker is available. This is a spike/diagnostic
// function — production code should just call Solve and handle errors.
func VerifyConnection(ctx context.Context, c *bkclient.Client) error {
	workers, err := c.ListWorkers(ctx)
	if err != nil {
		return fmt.Errorf("buildkit: failed to list workers: %w", err)
	}
	if len(workers) == 0 {
		return fmt.Errorf("buildkit: no workers available")
	}
	return nil
}
