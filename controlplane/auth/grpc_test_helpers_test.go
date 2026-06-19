package auth_test

import (
	"context"
	"net"
	"testing"

	cpfw "github.com/schmitthub/clawker/internal/controlplane/firewall"
	"google.golang.org/grpc/test/bufconn"
)

// nopContainerResolver is a ContainerResolver that always reports the
// container as alive at a fixed cgroup path. Suitable for tests that don't
// exercise Docker verification — the returned cgroup path resolves to
// /sys/fs/cgroup/cgroup.procs whose inode is stable inside the container.
var nopContainerResolver cpfw.ContainerResolver = func(_ context.Context, ref string) (string, string, bool, error) {
	return ref, "/sys/fs/cgroup/cgroup.procs", true, nil
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
