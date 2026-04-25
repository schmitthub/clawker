// Package v1 defines the gRPC AgentService for clawkerd-to-CP communication.
//
// AgentService is the agent-side surface clawkerd dials on the CP's
// clawker-net agent listener. The Connect RPC is server-streaming and
// IS the agent's lifetime command channel; Events is a client-streaming
// telemetry channel (stub in this branch — B5 fills in payloads).
package v1

// ServiceName is the fully-qualified gRPC service name for AgentService.
const ServiceName = "clawker.agent.v1.AgentService"
