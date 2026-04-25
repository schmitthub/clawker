package main

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// bearerInterceptor returns a unary client interceptor that attaches
// "authorization: Bearer <token>" on every outgoing RPC. clawkerd holds
// one token for the lifetime of the process — Register is the only RPC
// in B4, so no refresh loop is wired.
func bearerInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
