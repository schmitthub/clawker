package controlplane_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/test/bufconn"
)

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
