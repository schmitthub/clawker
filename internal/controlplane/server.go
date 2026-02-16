package controlplane

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	mobyclient "github.com/moby/moby/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Config holds control plane server configuration.
type Config struct {
	// Secret is the shared secret agents must present to register.
	Secret string
	// InitSpec is the init specification to send to agents after registration.
	InitSpec *v1.RunInitRequest
	// DockerClient is used to inspect containers for IP resolution.
	DockerClient *docker.Client
}

// Server is the control plane gRPC server.
// It implements AgentReportingService (agents register here) and on
// successful registration, connects back to the agent's AgentCommandService
// to call RunInit.
type Server struct {
	v1.UnimplementedAgentReportingServiceServer

	config   Config
	registry *Registry
	grpc     *grpc.Server
}

// NewServer creates a new control plane server.
func NewServer(cfg Config) *Server {
	s := &Server{
		config:   cfg,
		registry: NewRegistry(),
		grpc:     grpc.NewServer(),
	}
	v1.RegisterAgentReportingServiceServer(s.grpc, s)
	return s
}

// Serve starts serving gRPC on the given listener.
// Blocks until Stop is called or the listener is closed.
func (s *Server) Serve(lis net.Listener) error {
	logger.Info().Str("addr", lis.Addr().String()).Msg("control plane serving")
	return s.grpc.Serve(lis)
}

// Stop gracefully stops the gRPC server and closes agent connections.
func (s *Server) Stop() {
	s.grpc.GracefulStop()
	s.registry.Close()
}

// Registry returns the agent registry for inspection.
func (s *Server) Registry() *Registry {
	return s.registry
}

// Register implements AgentReportingService.Register.
// Validates the secret, registers the agent, resolves the container's IP
// via Docker inspect, then connects back to the agent's AgentCommandService
// to call RunInit asynchronously.
func (s *Server) Register(ctx context.Context, req *v1.RegisterRequest) (*v1.RegisterResponse, error) {
	logger.Info().
		Str("container_id", req.ContainerId).
		Uint32("listen_port", req.ListenPort).
		Str("version", req.Version).
		Msg("agent registering")

	// Validate secret.
	if req.Secret != s.config.Secret {
		logger.Warn().
			Str("container_id", req.ContainerId).
			Msg("agent registration rejected: invalid secret")
		return &v1.RegisterResponse{
			Accepted: false,
			Reason:   "invalid secret",
		}, nil
	}

	// Register the agent.
	s.registry.Register(req.ContainerId, req.ListenPort, req.Version)

	// Resolve container IP via Docker inspect.
	agentAddr, err := s.resolveAgentAddress(ctx, req.ContainerId, req.ListenPort)
	if err != nil {
		logger.Error().Err(err).
			Str("container_id", req.ContainerId).
			Msg("failed to resolve container IP")
		return &v1.RegisterResponse{
			Accepted: false,
			Reason:   fmt.Sprintf("failed to resolve container IP: %v", err),
		}, nil
	}

	logger.Info().
		Str("container_id", req.ContainerId).
		Str("agent_addr", agentAddr).
		Msg("agent registered, connecting back for RunInit")

	// Connect back to the agent's gRPC server and call RunInit asynchronously.
	go s.runInitOnAgent(req.ContainerId, agentAddr)

	return &v1.RegisterResponse{Accepted: true}, nil
}

// resolveAgentAddress inspects the container to determine how to reach the
// agent's gRPC server. It first checks for a host port mapping (required on
// macOS/Docker Desktop where container IPs aren't routable from the host),
// then falls back to the container's network IP.
func (s *Server) resolveAgentAddress(ctx context.Context, containerID string, port uint32) (string, error) {
	result, err := s.config.DockerClient.ContainerInspect(ctx, containerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", containerID, err)
	}

	// Check for host port mapping first (works on all platforms).
	portKey := fmt.Sprintf("%d/tcp", port)
	for p, bindings := range result.Container.NetworkSettings.Ports {
		if p.String() == portKey && len(bindings) > 0 {
			hostPort := bindings[0].HostPort
			if hostPort != "" {
				addr := fmt.Sprintf("127.0.0.1:%s", hostPort)
				logger.Debug().
					Str("container_id", containerID).
					Str("addr", addr).
					Msg("resolved agent via port mapping")
				return addr, nil
			}
		}
	}

	// Fallback: use container IP directly (works on Linux where host can reach container IPs).
	for networkName, endpoint := range result.Container.NetworkSettings.Networks {
		if endpoint.IPAddress.IsValid() {
			logger.Debug().
				Str("container_id", containerID).
				Str("network", networkName).
				Str("ip", endpoint.IPAddress.String()).
				Msg("resolved container IP (direct)")
			return fmt.Sprintf("%s:%d", endpoint.IPAddress, port), nil
		}
	}

	return "", fmt.Errorf("no valid address found for container %s", containerID)
}

// runInitOnAgent connects to the agent's gRPC server and calls RunInit.
func (s *Server) runInitOnAgent(containerID, agentAddr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Connect to the agent's gRPC server.
	conn, err := grpc.NewClient(
		agentAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		logger.Error().Err(err).
			Str("container_id", containerID).
			Str("agent_addr", agentAddr).
			Msg("failed to connect to agent")
		s.registry.SetInitFailed(containerID)
		return
	}
	s.registry.SetClientConn(containerID, conn)

	client := v1.NewAgentCommandServiceClient(conn)

	// Send the init spec.
	initSpec := s.config.InitSpec
	if initSpec == nil {
		initSpec = &v1.RunInitRequest{}
	}

	stream, err := client.RunInit(ctx, initSpec)
	if err != nil {
		logger.Error().Err(err).
			Str("container_id", containerID).
			Msg("RunInit RPC failed")
		s.registry.SetInitFailed(containerID)
		return
	}

	// Consume the progress stream.
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Error().Err(err).
				Str("container_id", containerID).
				Msg("RunInit stream error")
			s.registry.SetInitFailed(containerID)
			return
		}

		logger.Info().
			Str("container_id", containerID).
			Str("step", event.StepName).
			Str("status", event.Status.String()).
			Str("output", event.Output).
			Msg("init event")

		s.registry.AppendInitEvent(containerID, event)

		if event.Status == v1.InitEventStatus_INIT_EVENT_STATUS_READY {
			s.registry.SetInitCompleted(containerID)
			logger.Info().
				Str("container_id", containerID).
				Msg("agent init completed")
			return
		}

		if event.Status == v1.InitEventStatus_INIT_EVENT_STATUS_FAILED {
			s.registry.SetInitFailed(containerID)
			logger.Error().
				Str("container_id", containerID).
				Str("step", event.StepName).
				Str("error", event.Error).
				Msg("agent init step failed")
			return
		}
	}

	// Stream ended without READY event — mark as completed if we got here cleanly.
	s.registry.SetInitCompleted(containerID)
}
