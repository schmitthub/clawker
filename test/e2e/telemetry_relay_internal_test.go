package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
)

// relayProbeMetric is the gauge the test sends through the relay and
// then finds in Prometheus.
const relayProbeMetric = "clawker_e2e_relay_probe"

// TestTelemetryRelay_E2E sends one OTLP metrics batch from inside an agent
// container to the clawkerd relay on 127.0.0.1:4318 and finds it in
// Prometheus. The batch crosses the real path: relay, mTLS AgentService on
// the CP (identity gate and registry), OTel gateway, collector agent lane.
// The batch claims a false identity; the series must carry the identity the
// CP resolved for the container.
func TestTelemetryRelay_E2E(t *testing.T) {
	h := newFirewallHarness(t)

	upRes := h.Run("monitor", "up")
	require.NoError(t, upRes.Err, "monitor up failed\nstdout: %s\nstderr: %s", upRes.Stdout, upRes.Stderr)
	t.Cleanup(func() {
		if res := h.Run("monitor", "down", "--volumes"); res.Err != nil {
			t.Logf("monitor down: %v\nstderr: %s", res.Err, res.Stderr)
		}
	})

	agent := "relay-" + randomHex(t, 4)
	nonce := randomHex(t, 8)
	startRes := h.Run("container", "run", "--detach", "--agent", agent, "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container run failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	body, err := proto.Marshal(relayProbeBatch(nonce))
	require.NoError(t, err)
	sendRes := h.ExecInContainer(agent, "sh", "-c", fmt.Sprintf(
		`echo %s | base64 -d | curl -sS --max-time 20 -o /dev/null -w "%%{http_code}" `+
			`-H "Content-Type: application/x-protobuf" --data-binary @- http://%s/v1/metrics`,
		base64.StdEncoding.EncodeToString(body), consts.AgentOTLPRelayAddr))
	require.NoError(t, sendRes.Err, "relay send failed\nstdout: %s\nstderr: %s", sendRes.Stdout, sendRes.Stderr)
	require.Equal(t, "200", strings.TrimSpace(sendRes.Stdout), "relay must accept the batch")

	containerName, err := docker.ContainerName("testproject", agent)
	require.NoError(t, err)
	idOut, err := exec.CommandContext(t.Context(), "docker", "inspect", "-f", "{{.Id}}", containerName).Output()
	require.NoError(t, err, "docker inspect %s", containerName)
	containerID := strings.TrimSpace(string(idOut))

	cfg, err := config.NewConfig()
	require.NoError(t, err)
	promBase := "http://" + consts.Localhost + ":" + strconv.Itoa(cfg.MonitoringConfig().PrometheusPort)

	var labels map[string]string
	require.Eventually(t, func() bool {
		series, qerr := queryPrometheus(t.Context(), promBase, relayProbeMetric+`{nonce="`+nonce+`"}`)
		if qerr != nil {
			t.Logf("prometheus query: %v", qerr)
			return false
		}
		if len(series) != 1 {
			return false
		}
		labels = series[0]
		return true
	}, 90*time.Second, 3*time.Second, "probe series with nonce %s never reached Prometheus", nonce)

	assert.Equal(t, "testproject", labels["project"], "project must be the CP-resolved value")
	assert.Equal(t, agent, labels["agent"], "agent must be the CP-resolved value")
	assert.Equal(t, containerID, labels["container_id"], "container_id must be the CP-resolved value")
	assert.Equal(t, "cp_gateway", labels["ingest_source"])
}

// relayProbeBatch is one gauge point whose resource claims a false
// identity. The CP replaces these values with its own.
func relayProbeBatch(nonce string) *colmetrics.ExportMetricsServiceRequest {
	point := new(metricsv1.NumberDataPoint)
	point.Attributes = []*commonv1.KeyValue{stringAttr("nonce", nonce)}
	point.TimeUnixNano = uint64(time.Now().UnixNano())
	point.Value = &metricsv1.NumberDataPoint_AsDouble{AsDouble: 1}
	gauge := new(metricsv1.Gauge)
	gauge.DataPoints = []*metricsv1.NumberDataPoint{point}
	metric := new(metricsv1.Metric)
	metric.Name = relayProbeMetric
	metric.Data = &metricsv1.Metric_Gauge{Gauge: gauge}
	scope := new(metricsv1.ScopeMetrics)
	scope.Metrics = []*metricsv1.Metric{metric}
	res := new(resourcev1.Resource)
	res.Attributes = []*commonv1.KeyValue{
		stringAttr("project", "evil"),
		stringAttr("agent", "spoof"),
		stringAttr("container_id", "forged"),
	}
	rm := new(metricsv1.ResourceMetrics)
	rm.Resource = res
	rm.ScopeMetrics = []*metricsv1.ScopeMetrics{scope}
	req := new(colmetrics.ExportMetricsServiceRequest)
	req.ResourceMetrics = []*metricsv1.ResourceMetrics{rm}
	return req
}

func stringAttr(key, value string) *commonv1.KeyValue {
	v := new(commonv1.AnyValue)
	v.Value = &commonv1.AnyValue_StringValue{StringValue: value}
	kv := new(commonv1.KeyValue)
	kv.Key = key
	kv.Value = v
	return kv
}

// queryPrometheus runs an instant query and returns the label set of each
// result series.
func queryPrometheus(ctx context.Context, base, query string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/v1/query?query="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus status %d", res.StatusCode)
	}
	var out struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if derr := json.NewDecoder(res.Body).Decode(&out); derr != nil {
		return nil, derr
	}
	series := make([]map[string]string, 0, len(out.Data.Result))
	for _, r := range out.Data.Result {
		series = append(series, r.Metric)
	}
	return series, nil
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}
