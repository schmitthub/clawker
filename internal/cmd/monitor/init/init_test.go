package init

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/internal/testenv"
)

// renderMonitorConfigs runs initRun against an isolated testenv and
// returns the rendered compose.yaml and otel-config.yaml as strings.
// Used by the security-regression pinning tests below — each test
// asserts a distinct invariant of the rendered output.
func renderMonitorConfigs(t *testing.T) (compose, otelCfg string) {
	t.Helper()
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
	otelBytes, err := os.ReadFile(filepath.Join(monitorDir, monitor.OtelConfigFileName))
	require.NoError(t, err)
	return string(composeBytes), string(otelBytes)
}

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
	compose, _ := renderMonitorConfigs(t)

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
// config. The receiver's `grpc.tls` block must declare
// `client_ca_file` — its presence is what flips configtls to
// tls.RequireAndVerifyClientCert. Without it, the collector still
// terminates TLS using its server keypair but accepts any client, so
// any peer with network reach to the port can push records onto the
// trusted lane.
//
// Companion to TestInitRun_OtelInfraCAHostPath: that test pins WHERE
// the trust anchor is sourced from on the host (compose bind-mount).
// This test pins THAT the receiver still consumes one (nested under
// the receiver's tls block, not loose anywhere in the file).
func TestInitRun_OtelInfraReceiverRequiresClientCert(t *testing.T) {
	_, otelCfg := renderMonitorConfigs(t)

	tls := otelInfraGRPCTLS(t, otelCfg)
	caFile, ok := tls["client_ca_file"].(string)
	require.True(t, ok && caFile != "",
		"otlp/infra grpc.tls must declare client_ca_file — removing it leaves server-auth TLS only, accepting any client peer")
	require.Equal(t, "/etc/otel/tls/ca.pem", caFile,
		"client_ca_file path must match the bind-mount target in compose.yaml — drift would break the handshake or silently load the wrong anchor")
}

// TestInitRun_OtelInfraTrustedPipelineIsolated pins the receivers list
// on the `logs/in_trusted` pipeline to exactly `[otlp/infra]`. Adding
// the unauth `otlp` receiver here — even by accident during a refactor
// — would route records ingested over the plaintext lane into the
// `routing/trusted` connector, where sender-declared service.name is
// honored. That is the same agent-spoof shape the receiver mTLS gate
// exists to prevent, just one layer up the pipeline.
func TestInitRun_OtelInfraTrustedPipelineIsolated(t *testing.T) {
	_, otelCfg := renderMonitorConfigs(t)

	pipelines := otelMap(t, otelCfg, "service", "pipelines")
	trusted, ok := pipelines["logs/in_trusted"].(map[string]any)
	require.True(t, ok, "logs/in_trusted pipeline must exist — it is the only path into routing/trusted")

	receivers, ok := trusted["receivers"].([]any)
	require.True(t, ok, "logs/in_trusted.receivers must be a list")
	require.Equal(t, []any{"otlp/infra"}, receivers,
		"logs/in_trusted must receive only from otlp/infra — adding the unauth otlp receiver routes spoofed records into routing/trusted")
}

// TestInitRun_OtelUntrustedRoutingAllowlist pins the `routing/untrusted`
// connector's table to exactly the two `service.name` values that may
// legitimately push from the unauth lane: `claude-code` and
// `clawker-cli`. Adding `clawker-cp`, `envoy`, or `coredns` to this
// table would let an agent container forge a trusted identity from
// the plaintext OTLP port — the trusted indices (clawker-cp,
// clawker-envoy, clawker-coredns) must remain reachable only via
// `routing/trusted` behind the mTLS gate.
func TestInitRun_OtelUntrustedRoutingAllowlist(t *testing.T) {
	_, otelCfg := renderMonitorConfigs(t)

	untrusted := otelMap(t, otelCfg, "connectors", "routing/untrusted")
	tableRaw, ok := untrusted["table"].([]any)
	require.True(t, ok, "routing/untrusted.table must be a list")

	conditions := make([]string, 0, len(tableRaw))
	for _, entry := range tableRaw {
		e, ok := entry.(map[string]any)
		require.True(t, ok, "routing/untrusted.table entry must be a map")
		cond, _ := e["condition"].(string)
		conditions = append(conditions, cond)
	}
	require.ElementsMatch(t,
		[]string{
			`attributes["service.name"] == "claude-code"`,
			`attributes["service.name"] == "clawker-cli"`,
		},
		conditions,
		"routing/untrusted must allow ONLY claude-code and clawker-cli — adding clawker-cp/envoy/coredns conditions opens spoofed trusted identities from the unauth lane")
}

// otelMap parses otel-config.yaml and walks the given key path into a
// nested map. Fails the test if any key is missing or not a map. The
// otel-collector config keys (`otlp/infra`, `routing/untrusted`,
// `logs/in_trusted`) contain `/` characters; yaml.v3 unmarshals these
// as ordinary string map keys.
func otelMap(t *testing.T, otelCfg string, path ...string) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(otelCfg), &doc),
		"rendered otel-config.yaml must be valid YAML")
	cur := doc
	for i, key := range path {
		next, ok := cur[key].(map[string]any)
		require.Truef(t, ok, "expected map at otel-config path %v (failed at %q, index %d)", path, key, i)
		cur = next
	}
	return cur
}

// otelInfraGRPCTLS walks otel-config.yaml to the `otlp/infra`
// receiver's `grpc.tls` block. Centralized so the receiver-gate test
// asserts nesting (not just a loose substring match).
func otelInfraGRPCTLS(t *testing.T, otelCfg string) map[string]any {
	t.Helper()
	return otelMap(t, otelCfg, "receivers", "otlp/infra", "protocols", "grpc", "tls")
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
