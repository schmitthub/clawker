// Package v1 defines the gRPC ClawkerdService for CP-to-clawkerd
// command dispatch.
//
// ClawkerdService is hosted by clawkerd inside each agent container
// on consts.DefaultClawkerdPort. CP is the sole legitimate caller —
// the listener requires
// an mTLS peer cert chained to the clawker CA AND pins the peer's CN
// to consts.ContainerCP, rejecting any other clawker-CA-signed cert.
//
// The Session RPC is bidi-streaming: CP streams Commands, clawkerd
// streams Responses correlated by command_id.
package v1

//go:generate moq -rm -pkg mocks -out mocks/client_mock.go . ClawkerdServiceClient

// ServiceName is the fully-qualified gRPC service name for ClawkerdService.
const ServiceName = "clawker.clawkerd.v1.ClawkerdService"
