// Package v1 defines the gRPC AdminService for CLI-to-CP communication.
// This is a separate proto package from the agent v1 package, enforcing the
// trust boundary between admin operations (CLI) and agent operations (clawkerd).
package v1

// ServiceName is the fully-qualified gRPC service name for AdminService.
const ServiceName = "clawker.admin.v1.AdminService"
