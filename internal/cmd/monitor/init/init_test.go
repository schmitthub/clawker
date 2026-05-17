package init

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/internal/testenv"
)

func TestNewCmdInit(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}
	if gotOpts.IOStreams != tio {
		t.Error("expected IOStreams to be set from factory")
	}
	if gotOpts.Force {
		t.Error("expected Force to default to false")
	}
}

// TestInitRun_OtelInfraCAHostPath pins the bind-mount source for the
// otel-collector container's /etc/otel/tls/ca.pem to the infra
// intermediate CA (consts.AuthInfraCACertPath), NOT the CLI root
// (consts.AuthCACertPath). Using the CLI root would let any CLI-
// signed leaf — including agent-container leaves — chain to the
// receiver's client_ca_file and forge service.name=clawker-cp
// records on the trusted forensic indices.
func TestInitRun_OtelInfraCAHostPath(t *testing.T) {
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())

	cfg, err := config.NewConfig()
	require.NoError(t, err)

	tio, _, _, _ := iostreams.Test()
	opts := &InitOptions{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Force:     true,
	}

	require.NoError(t, initRun(context.Background(), opts))

	monitorDir, err := cfg.MonitorSubdir()
	require.NoError(t, err)
	composeBytes, err := os.ReadFile(filepath.Join(monitorDir, monitor.ComposeFileName))
	require.NoError(t, err)
	compose := string(composeBytes)

	wantInfra, err := consts.AuthInfraCACertPath()
	require.NoError(t, err)
	require.Contains(t, compose, wantInfra+":/etc/otel/tls/ca.pem:ro",
		"otel-collector trust anchor must bind-mount the infra intermediate CA, not the CLI root")

	rootCA, err := consts.AuthCACertPath()
	require.NoError(t, err)
	// Sanity: the two paths must actually differ — if they collide via
	// some future const refactor, this test would pass trivially.
	require.NotEqual(t, wantInfra, rootCA, "infra and root CA host paths must be distinct")
	require.NotContains(t, compose, rootCA+":/etc/otel/tls/ca.pem:ro",
		"CLI root CA must NOT be the otel-collector trust anchor — agent leaves would chain through it")
}

// TestInitRun_OtelInfraReceiverRequiresClientCert pins the
// `otlp/infra` receiver's mTLS gate in the rendered otel-collector
// config. The receiver must declare a `client_ca_file` under its
// `tls` block — without it, the OTel collector accepts plaintext or
// any-cert TLS on the trusted lane and the spoof gate is gone.
//
// Companion to TestInitRun_OtelInfraCAHostPath: that test pins WHERE
// the trust anchor is sourced from on the host. This test pins THAT
// the receiver still references it at all.
func TestInitRun_OtelInfraReceiverRequiresClientCert(t *testing.T) {
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())

	cfg, err := config.NewConfig()
	require.NoError(t, err)

	tio, _, _, _ := iostreams.Test()
	opts := &InitOptions{
		IOStreams: tio,
		Config:    func() (config.Config, error) { return cfg, nil },
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Force:     true,
	}
	require.NoError(t, initRun(context.Background(), opts))

	monitorDir, err := cfg.MonitorSubdir()
	require.NoError(t, err)
	otelBytes, err := os.ReadFile(filepath.Join(monitorDir, monitor.OtelConfigFileName))
	require.NoError(t, err)
	otelCfg := string(otelBytes)

	require.Contains(t, otelCfg, "otlp/infra:",
		"otel-collector must define a separate otlp/infra receiver — folding it into the unauth receiver removes the mTLS gate")
	require.Contains(t, otelCfg, "client_ca_file: /etc/otel/tls/ca.pem",
		"otlp/infra receiver must require client certs against the bind-mounted CA — removing this line opens the trusted lane to any peer")
}

func TestNewCmdInit_ForceFlag(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--force"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}
	if !gotOpts.Force {
		t.Error("expected Force to be true when --force flag is set")
	}
}
