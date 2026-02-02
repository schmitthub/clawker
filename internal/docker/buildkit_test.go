package docker

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

// fakePinger implements the Pinger interface for testing.
type fakePinger struct {
	result client.PingResult
	err    error
}

func (f *fakePinger) Ping(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
	return f.result, f.err
}

// TestBuildKitEnabled_DelegatesToWhail verifies the docker package wrapper
// delegates to whail.BuildKitEnabled correctly.
func TestBuildKitEnabled_DelegatesToWhail(t *testing.T) {
	p := &fakePinger{
		result: client.PingResult{
			BuilderVersion: build.BuilderBuildKit,
			OSType:         "linux",
		},
	}

	enabled, err := BuildKitEnabled(context.Background(), p)
	require.NoError(t, err)
	require.True(t, enabled)
}
