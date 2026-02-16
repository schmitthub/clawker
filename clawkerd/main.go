// clawkerd is the container-side agent for the clawker control plane.
//
// It starts a gRPC server implementing AgentCommandService (RunInit),
// then registers with the host-side control plane. After registration,
// the control plane connects back and calls RunInit with an init spec.
// clawkerd executes the steps, streams progress, and writes a ready file
// when init is complete.
//
// Logger initialization happens AFTER registration — the control plane delivers
// ClawkerdConfiguration (identity, OTEL config, file logging config) in the
// RegisterResponse. Pre-registration failures use fmt.Fprintf to stderr.
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
	"time"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	readyFilePath   = "/var/run/clawker/ready"
	defaultGRPCPort = "50051"

	// logsDir is the container-side log directory (Linux FHS convention for daemon logs).
	// Hardcoded — clawkerd does NOT import internal/config.
	logsDir = "/var/log/clawkerd"
)

// agentServer implements AgentCommandService.
type agentServer struct {
	v1.UnimplementedAgentCommandServiceServer
}

// RunInit executes the init steps and streams progress back.
func (s *agentServer) RunInit(req *v1.RunInitRequest, stream v1.AgentCommandService_RunInitServer) error {
	logger.Info().Str("component", "clawkerd").Int("step_count", len(req.Steps)).Msg("RunInit received")

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
			logger.Error().
				Str("component", "clawkerd").
				Str("step", step.Name).
				Err(execErr).
				Str("stderr", stderr).
				Msg("init step failed")
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

		logger.Info().
			Str("component", "clawkerd").
			Str("step", step.Name).
			Str("output", strings.TrimSpace(stdout)).
			Msg("init step completed")

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
		logger.Warn().Str("component", "clawkerd").Err(err).Msg("failed to write ready file")
	}

	// Send final READY event.
	if err := stream.Send(&v1.RunInitResponse{
		Status: v1.InitEventStatus_INIT_EVENT_STATUS_READY,
	}); err != nil {
		return fmt.Errorf("send ready event: %w", err)
	}

	logger.Info().Str("component", "clawkerd").Msg("all init steps complete, ready")
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

// fatalf writes a fatal error to stderr and exits.
// Used for pre-registration failures where the logger is not yet initialized.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[clawkerd] fatal: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	// Pre-logger phase: parse env, start gRPC server, register with CP.
	// Fatal errors in this phase go to stderr — logger is not initialized yet.
	cpAddr := os.Getenv("CLAWKER_CONTROL_PLANE")
	secret := os.Getenv("CLAWKER_CONTROL_PLANE_SECRET")
	port := os.Getenv("CLAWKER_AGENT_PORT")
	if port == "" {
		port = defaultGRPCPort
	}

	if cpAddr == "" {
		fatalf("CLAWKER_CONTROL_PLANE not set")
	}
	if secret == "" {
		fatalf("CLAWKER_CONTROL_PLANE_SECRET not set")
	}

	// Start gRPC server for AgentCommandService.
	listenAddr := "0.0.0.0:" + port
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		fatalf("failed to listen on %s: %v", listenAddr, err)
	}

	grpcServer := grpc.NewServer()
	v1.RegisterAgentCommandServiceServer(grpcServer, &agentServer{})

	// Serve in background.
	go func() {
		// Pre-logger — this goroutine starts before registration completes.
		fmt.Fprintf(os.Stderr, "[clawkerd] gRPC server listening on %s\n", lis.Addr().String())
		if err := grpcServer.Serve(lis); err != nil {
			// Post-logger may be available by now, but Serve error is fatal either way.
			logger.Error().Str("component", "clawkerd").Err(err).Msg("gRPC server error")
		}
	}()

	// Get container ID (hostname).
	containerID, err := os.Hostname()
	if err != nil {
		fatalf("failed to get hostname: %v", err)
	}

	// Resolve the listen port from the bound address.
	_, portStr, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		fatalf("failed to parse listen address: %v", err)
	}
	listenPort, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		fatalf("failed to parse port: %v", err)
	}

	// Register with control plane — returns ClawkerdConfiguration for logger init.
	resp, err := registerWithCP(cpAddr, secret, containerID, uint32(listenPort))
	if err != nil {
		fatalf("failed to register with control plane at %s — is the control plane running? (clawker monitor status): %v", cpAddr, err)
	}

	// --- Post-registration: initialize structured logger from CP-delivered config ---
	initLogger(resp.Config)
	defer logger.Close()

	logger.Info().
		Str("component", "clawkerd").
		Str("container_id", containerID).
		Str("cp_addr", cpAddr).
		Msg("registered with control plane, logger initialized")

	// Wait for SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info().Str("component", "clawkerd").Str("signal", sig.String()).Msg("shutting down")
	grpcServer.GracefulStop()
}

// registerWithCP connects to the control plane and calls Register.
// Returns the full RegisterResponse so the caller can extract ClawkerdConfiguration.
func registerWithCP(cpAddr, secret, containerID string, listenPort uint32) (*v1.RegisterResponse, error) {
	conn, err := grpc.NewClient(
		cpAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to control plane at %s: %w", cpAddr, err)
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
		return nil, fmt.Errorf("register RPC: %w", err)
	}
	if !resp.Accepted {
		return nil, fmt.Errorf("registration rejected: %s", resp.Reason)
	}

	return resp, nil
}

// initLogger initializes the structured logger from CP-delivered ClawkerdConfiguration.
// If config is nil or missing, falls back to file-only logging with defaults.
func initLogger(cfg *v1.ClawkerdConfiguration) {
	if cfg == nil {
		// No config from CP — initialize with defaults (file-only).
		logger.NewLogger(&logger.Options{
			LogsDir:     logsDir,
			ServiceName: "clawker",
			ScopeName:   "clawkerd",
			FileConfig: &logger.LoggingConfig{
				// defaults: file enabled, 50MB, 7 days, 3 backups, compress
			},
		})
		return
	}

	// Build file config from CP-delivered settings.
	var fileConfig *logger.LoggingConfig
	if fl := cfg.FileLogging; fl != nil {
		enabled := fl.Enabled
		compress := fl.Compress
		fileConfig = &logger.LoggingConfig{
			FileEnabled: &enabled,
			MaxSizeMB:   int(fl.MaxSizeMb),
			MaxAgeDays:  int(fl.MaxAgeDays),
			MaxBackups:  int(fl.MaxBackups),
			Compress:    &compress,
		}
	} else {
		// No file logging config — use defaults.
		fileConfig = &logger.LoggingConfig{}
	}

	// Build OTEL config from CP-delivered settings.
	var otelConfig *logger.OtelLogConfig
	if otel := cfg.Otel; otel != nil {
		otelConfig = &logger.OtelLogConfig{
			Endpoint:       otel.Endpoint,
			Insecure:       otel.Insecure,
			Timeout:        time.Duration(otel.TimeoutSeconds) * time.Second,
			MaxQueueSize:   int(otel.MaxQueueSize),
			ExportInterval: time.Duration(otel.ExportIntervalSeconds) * time.Second,
		}
	}

	if err := logger.NewLogger(&logger.Options{
		LogsDir:     logsDir,
		ServiceName: "clawker",
		ScopeName:   "clawkerd",
		FileConfig:  fileConfig,
		OtelConfig:  otelConfig,
	}); err != nil {
		// Logger init failure is non-fatal — fall back to nop.
		fmt.Fprintf(os.Stderr, "[clawkerd] warning: logger init failed: %v\n", err)
		logger.Init()
	}

	// Set project/agent context for all subsequent log entries.
	if cfg.Project != "" || cfg.Agent != "" {
		logger.SetContext(cfg.Project, cfg.Agent)
	}
}
