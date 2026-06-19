package netlogger

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// captureLogger returns a logger writing JSON lines into buf so a test
// can assert the structured fields of the netlogger_unavailable line.
func captureLogger(buf *bytes.Buffer) *logger.Logger {
	return logger.NewWriter(buf)
}

// lastLogLine decodes the final JSON log object written to buf.
func lastLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no log lines captured; buffer=%q", buf.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("decode log line %q: %v", lines[len(lines)-1], err)
	}
	return m
}

// TestStart_DegradesWhenOtelCertsAbsent proves the highest-priority
// degrade branch: a nil OtelCerts returns (nil, nil) and logs at Error
// (not Warn — absent trusted-lane material is a real fault, not the
// benign unconfigured shape). This is the LANDMINE branch — the nil
// check must fire before anything dereferences the provisioner.
func TestStart_DegradesWhenOtelCertsAbsent(t *testing.T) {
	// Endpoint set so the empty-endpoint branch can't mask the
	// otelcerts-nil branch we're targeting.
	t.Setenv(consts.EnvOTLPLogsEndpoint, "https://collector:4317")

	var buf bytes.Buffer
	svc, provider := Start(context.Background(), StartDeps{
		Log:       captureLogger(&buf),
		OtelCerts: nil,
	})

	if svc != nil || provider != nil {
		t.Fatalf("expected (nil,nil) on absent otelcerts; got svc=%v provider=%v", svc, provider)
	}
	line := lastLogLine(t, &buf)
	if line["event"] != "netlogger_unavailable" {
		t.Fatalf("event = %v; want netlogger_unavailable", line["event"])
	}
	if line["level"] != "error" {
		t.Fatalf("level = %v; want error (absent material is a real fault)", line["level"])
	}
	if line["step"] != "otelcerts unavailable" {
		t.Fatalf("step = %v; want %q", line["step"], "otelcerts unavailable")
	}
}

// TestStart_DegradesToNilWhenUnconfigured proves the no-endpoint path
// still returns (nil,nil) (eBPF never stranded) and emits the
// netlogger_unavailable event. The Warn-vs-Error discrimination for
// the unconfigured shape requires a real *otelcerts.Service to clear
// the higher-priority otelcerts gate; that cert-fixture machinery is
// out of scope for a netlogger unit test, so this test fixes only the
// (nil,nil) safety contract, which is the load-bearing invariant.
func TestStart_DegradesToNilWhenUnconfigured(t *testing.T) {
	t.Setenv(consts.EnvOTLPLogsEndpoint, "")
	t.Setenv(consts.EnvOTLPEndpoint, "")

	var buf bytes.Buffer
	svc, provider := Start(context.Background(), StartDeps{
		Log:       captureLogger(&buf),
		OtelCerts: nil,
	})

	if svc != nil || provider != nil {
		t.Fatalf("expected (nil,nil) when unconfigured; got svc=%v provider=%v", svc, provider)
	}
	line := lastLogLine(t, &buf)
	if line["event"] != "netlogger_unavailable" {
		t.Fatalf("event = %v; want netlogger_unavailable", line["event"])
	}
}
