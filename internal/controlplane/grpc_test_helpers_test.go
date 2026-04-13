package controlplane_test

import (
	"context"
	"net"
	"testing"

	"github.com/schmitthub/clawker/internal/controlplane"
	"google.golang.org/grpc/test/bufconn"
)

// nopContainerResolver is a ContainerResolver that always reports the container
// as alive. Suitable for tests that don't exercise Docker verification.
var nopContainerResolver controlplane.ContainerResolver = func(_ context.Context, _ string) (string, bool, error) {
	return "/sys/fs/cgroup/cgroup.procs", true, nil
}

const bufconnSize = 1024 * 1024

func bufconnListen(t *testing.T) *bufconn.Listener {
	t.Helper()
	return bufconn.Listen(bufconnSize)
}

func bufconnDialer(lis *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
}
