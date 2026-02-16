// clawkerd is the container-side agent for the clawker control plane.
//
// It starts a gRPC server implementing AgentCommandService (RunInit),
// then registers with the host-side control plane. After registration,
// the control plane connects back and calls RunInit with an init spec.
// clawkerd executes the steps, streams progress, and writes a ready file
// when init is complete.
package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	readyFilePath   = "/var/run/clawker/ready"
	defaultGRPCPort = "50051"
)

// agentServer implements AgentCommandService.
type agentServer struct {
	v1.UnimplementedAgentCommandServiceServer
}

// RunInit executes the init steps and streams progress back.
func (s *agentServer) RunInit(req *v1.RunInitRequest, stream v1.AgentCommandService_RunInitServer) error {
	logf("RunInit: received %d steps", len(req.Steps))

	for _, step := range req.Steps {
		// Send STARTED event.
		if err := stream.Send(&v1.RunInitResponse{
			StepName: step.Name,
			Status:   v1.InitEventStatus_INIT_EVENT_STATUS_STARTED,
		}); err != nil {
			return fmt.Errorf("send started event for %q: %w", step.Name, err)
		}

		// Execute the command.
		stdout, stderr, execErr := executeCommand(stream.Context(), step.Command)

		if execErr != nil {
			logf("RunInit: step %q failed: %v (stderr: %s)", step.Name, execErr, stderr)
			// Send FAILED event.
			if err := stream.Send(&v1.RunInitResponse{
				StepName: step.Name,
				Status:   v1.InitEventStatus_INIT_EVENT_STATUS_FAILED,
				Output:   stdout,
				Error:    fmt.Sprintf("%v: %s", execErr, stderr),
			}); err != nil {
				return fmt.Errorf("send failed event for %q: %w", step.Name, err)
			}
			// Continue to next step — don't abort the whole init on one failure.
			continue
		}

		logf("RunInit: step %q completed (output: %s)", step.Name, strings.TrimSpace(stdout))

		// Send COMPLETED event.
		if err := stream.Send(&v1.RunInitResponse{
			StepName: step.Name,
			Status:   v1.InitEventStatus_INIT_EVENT_STATUS_COMPLETED,
			Output:   stdout,
		}); err != nil {
			return fmt.Errorf("send completed event for %q: %w", step.Name, err)
		}
	}

	// Write ready file before sending the READY event.
	if err := writeReadyFile(); err != nil {
		logf("WARNING: failed to write ready file: %v", err)
	}

	// Send final READY event.
	if err := stream.Send(&v1.RunInitResponse{
		Status: v1.InitEventStatus_INIT_EVENT_STATUS_READY,
	}); err != nil {
		return fmt.Errorf("send ready event: %w", err)
	}

	logf("RunInit: all steps complete, ready")
	return nil
}

// executeCommand runs a bash command and captures stdout/stderr.
func executeCommand(ctx context.Context, command string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// writeReadyFile creates the ready signal file.
func writeReadyFile() error {
	return os.WriteFile(readyFilePath, []byte("ready\n"), 0644)
}

func main() {
	cpAddr := os.Getenv("CLAWKER_CONTROL_PLANE")
	secret := os.Getenv("CLAWKER_CONTROL_PLANE_SECRET")
	port := os.Getenv("CLAWKER_AGENT_PORT")
	if port == "" {
		port = defaultGRPCPort
	}

	if cpAddr == "" {
		logf("FATAL: CLAWKER_CONTROL_PLANE not set")
		os.Exit(1)
	}
	if secret == "" {
		logf("FATAL: CLAWKER_CONTROL_PLANE_SECRET not set")
		os.Exit(1)
	}

	// Start gRPC server for AgentCommandService.
	listenAddr := "0.0.0.0:" + port
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		logf("FATAL: failed to listen on %s: %v", listenAddr, err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	v1.RegisterAgentCommandServiceServer(grpcServer, &agentServer{})

	// Serve in background.
	go func() {
		logf("gRPC server listening on %s", lis.Addr().String())
		if err := grpcServer.Serve(lis); err != nil {
			logf("gRPC server error: %v", err)
		}
	}()

	// Get container ID (hostname).
	containerID, err := os.Hostname()
	if err != nil {
		logf("FATAL: failed to get hostname: %v", err)
		os.Exit(1)
	}

	// Resolve the listen port from the bound address.
	_, portStr, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		logf("FATAL: failed to parse listen address: %v", err)
		os.Exit(1)
	}
	listenPort, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		logf("FATAL: failed to parse port: %v", err)
		os.Exit(1)
	}

	// Register with control plane. CP resolves our container IP via Docker inspect.
	if err := registerWithCP(cpAddr, secret, containerID, uint32(listenPort)); err != nil {
		logf("FATAL: failed to register with control plane: %v", err)
		os.Exit(1)
	}

	// Wait for SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logf("received signal %v, shutting down", sig)
	grpcServer.GracefulStop()
}

// registerWithCP connects to the control plane and calls Register.
func registerWithCP(cpAddr, secret, containerID string, listenPort uint32) error {
	conn, err := grpc.NewClient(
		cpAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("connect to control plane at %s: %w", cpAddr, err)
	}
	defer conn.Close()

	client := v1.NewAgentReportingServiceClient(conn)
	resp, err := client.Register(context.Background(), &v1.RegisterRequest{
		ContainerId: containerID,
		Secret:      secret,
		Version:     "poc",
		ListenPort:  listenPort,
	})
	if err != nil {
		return fmt.Errorf("register RPC: %w", err)
	}
	if !resp.Accepted {
		return fmt.Errorf("registration rejected: %s", resp.Reason)
	}

	logf("registered with control plane at %s", cpAddr)
	return nil
}

// logf logs to stderr (container-side diagnostic output).
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[clawkerd] "+format+"\n", args...)
}
