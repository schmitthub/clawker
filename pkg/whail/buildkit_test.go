package whail_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/require"
)

// fakePinger implements whail.Pinger for testing.
type fakePinger struct {
	result client.PingResult
	err    error
}

func (f *fakePinger) Ping(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
	return f.result, f.err
}

func TestBuildKitEnabled_EnvVar(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "1")

	enabled, err := whail.BuildKitEnabled(context.Background(), &fakePinger{})
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestBuildKitEnabled_EnvVarFalse(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "0")

	enabled, err := whail.BuildKitEnabled(context.Background(), &fakePinger{})
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestBuildKitEnabled_EnvVarInvalid(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "garbage")

	_, err := whail.BuildKitEnabled(context.Background(), &fakePinger{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "DOCKER_BUILDKIT environment variable expects boolean value")
}

func TestBuildKitEnabled_DaemonBuildKit(t *testing.T) {
	p := &fakePinger{
		result: client.PingResult{
			BuilderVersion: build.BuilderBuildKit,
			OSType:         "linux",
		},
	}

	enabled, err := whail.BuildKitEnabled(context.Background(), p)
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestBuildKitEnabled_DaemonV1Linux(t *testing.T) {
	p := &fakePinger{
		result: client.PingResult{
			BuilderVersion: build.BuilderV1,
			OSType:         "linux",
		},
	}

	enabled, err := whail.BuildKitEnabled(context.Background(), p)
	require.NoError(t, err)
	require.True(t, enabled, "V1 on Linux should default to BuildKit enabled")
}

func TestBuildKitEnabled_DaemonV1Windows(t *testing.T) {
	p := &fakePinger{
		result: client.PingResult{
			BuilderVersion: build.BuilderV1,
			OSType:         "windows",
		},
	}

	enabled, err := whail.BuildKitEnabled(context.Background(), p)
	require.NoError(t, err)
	require.False(t, enabled, "V1 on Windows should disable BuildKit")
}

func TestBuildKitEnabled_PingError(t *testing.T) {
	p := &fakePinger{
		err: fmt.Errorf("connection refused"),
	}

	_, err := whail.BuildKitEnabled(context.Background(), p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to ping Docker daemon")
}
