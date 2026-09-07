package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestWireExecutor_NilBus pins the CP-resilience contract: a
// regression that swaps the `if err != nil { return nil }` for
// `panic(err)` would crash CP and strand eBPF programs unsupervised,
// silently breaking the firewall enforcement boundary. The structured
// `event=<subsystem>_unavailable` log line is the only triage surface
// operators have once the wrapper degrades.
func TestWireExecutor_NilBus(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWriter(&buf)

	exec := wireExecutor(nil, nil, log)

	require.Nil(t, exec, "nil bus must yield nil Executor (degrade), not crash")
	require.Contains(t, buf.String(), "agent_executor_unavailable",
		"degraded path must emit the structured event so operators can triage")
}

// TestLogHostIdentity pins the structured-event contract — the only
// place an operator can correlate downstream userStage EACCES with
// the CLI's CLAWKER_HOST_UID/GID env-drop. Renaming the event,
// dropping the emit, or swapping warn for debug must trip this test.
func TestLogHostIdentity(t *testing.T) {
	t.Run("happy path emits nothing", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		logHostIdentity(log,
			consts.HostIDResolution{Env: "CLAWKER_HOST_UID", Value: 1234, Fallback: false},
			consts.HostIDResolution{Env: "CLAWKER_HOST_GID", Value: 1234, Fallback: false},
		)
		require.Empty(t, buf.String(), "non-fallback resolutions must produce zero log lines")
	})

	t.Run("fallback emits structured warn per env", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		logHostIdentity(
			log,
			consts.HostIDResolution{Env: "CLAWKER_HOST_UID", Raw: "", Value: 1001, Fallback: true, Reason: "unset"},
			consts.HostIDResolution{
				Env:      "CLAWKER_HOST_GID",
				Raw:      "bad",
				Value:    1001,
				Fallback: true,
				Reason:   "malformed",
				Err:      assert.AnError,
			},
		)
		// NewWriter is a zerolog JSON writer (one record per line).
		// Parse rather than substring-grep so a field rename or
		// formatter swap surfaces as a structured failure, not a
		// silently-passing test.
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		require.Len(t, lines, 2, "exactly one record per degraded env, got: %s", buf.String())

		want := []struct {
			env, reason string
			err         bool
		}{
			{"CLAWKER_HOST_UID", "unset", false},
			{"CLAWKER_HOST_GID", "malformed", true},
		}
		for i, line := range lines {
			var rec map[string]any
			decoder := json.NewDecoder(strings.NewReader(line))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&rec),
				"record %d must be valid JSON: %q", i, line)
			require.Equal(t, "warn", rec["level"], "must use warn severity, not debug/info")
			require.Equal(
				t,
				"host_id_unavailable",
				rec["event"],
				"structured event name is the operator's triage anchor (env field disambiguates UID vs GID)",
			)
			require.Equal(t, want[i].env, rec["env"])
			require.Equal(t, want[i].reason, rec["reason"])
			require.Equal(
				t,
				json.Number("1001"),
				rec["fallback"],
				"fallback value must surface so operators know which UID userStage will drop to",
			)
			if want[i].err {
				require.NotEmpty(t, rec["error"], "malformed reason must surface the underlying parse error")
			} else {
				_, hasErr := rec["error"]
				require.False(t, hasErr, "unset reason must not synthesize an error field")
			}
		}
	})
}

// TestRunDrainStage pins the CP §3.4 drain-resilience contract: a panicking
// teardown stage MUST be contained (recovered, not propagated) and reported as
// an error, so the linear drain sequence in run() falls through to
// ebpfMgr.FlushAll instead of unwinding the whole function and stranding eBPF.
// A clean stage runs its body and reports no error and no log noise. The
// structured event name is the only operator triage surface, so a rename or a
// dropped emit must trip this test.
func TestRunDrainStage(t *testing.T) {
	t.Parallel()

	t.Run("clean stage runs body, returns nil, logs nothing", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		ran := false
		err := runDrainStage(log, "pre-flush teardown", "drain_preflush_panic", func() {
			ran = true
		})
		require.NoError(t, err, "a non-panicking stage must report no error")
		require.True(t, ran, "the stage body must execute")
		require.Empty(t, buf.String(), "a clean stage must emit no log line")
	})

	t.Run("panicking stage is contained and reported, not propagated", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		var err error
		// The whole point of the recover: a panic in one teardown stage must
		// NOT unwind run() (which would skip FlushAll and strand eBPF).
		require.NotPanics(t, func() {
			err = runDrainStage(log, "pre-flush teardown", "drain_preflush_panic", func() {
				panic("stack stop blew up")
			})
		})
		require.Error(t, err, "a panicking stage must surface as an error so the drain exits non-zero")
		require.Contains(t, err.Error(), "pre-flush teardown panic",
			"the error must name the stage so the aggregate drain error is triagable")
		require.Contains(t, buf.String(), "drain_preflush_panic",
			"a contained panic must emit the structured event — the only operator triage surface")
	})

	t.Run("a panicking stage still lets the caller reach a later stage", func(t *testing.T) {
		// This is the load-bearing FlushAll-still-runs guarantee, modeled as
		// the call site does it: two stages in sequence, the first panics,
		// the second (FlushAll's stand-in) must still run.
		log := logger.Nop()
		flushed := false
		_ = runDrainStage(log, "pre-flush teardown", "drain_preflush_panic", func() {
			panic("pre-flush blew up")
		})
		// Control returned here despite the panic — the caller proceeds to the
		// next stage exactly as drainCallbackBody does.
		_ = runDrainStage(log, "ebpf flush", "drain_ebpf_flush_panic", func() {
			flushed = true
		})
		require.True(t, flushed, "FlushAll-equivalent stage must run after a prior stage panics")
	})
}

func TestStopSDSServer(t *testing.T) {
	for _, tt := range []struct {
		name   string
		active bool
	}{
		{name: "idle", active: false},
		{name: "active delta stream", active: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, clientTLS := newSDSStopTestServer(t)
			t.Cleanup(server.Stop)
			if tt.active {
				openSDSStream(t, server, clientTLS)
			}

			var output bytes.Buffer
			done := make(chan struct{})
			go func() {
				stopSDSServer(server, logger.NewWriter(&output))
				close(done)
			}()
			timer := time.NewTimer(2 * defaultShutdownWait)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				t.Fatal("SDS shutdown did not return while its stream was open")
			}
			if tt.active {
				assert.Contains(t, output.String(), "sds_graceful_stop_timeout")
				assert.Contains(t, output.String(), "firewall.sds")
			} else {
				assert.Empty(t, output.String())
			}
		})
	}
}

func newSDSStopTestServer(t *testing.T) (*grpc.Server, *tls.Config) {
	t.Helper()
	const hostname = "stop.sds.test"
	caCert, caKey, err := firewall.EnsureCA(t.TempDir())
	require.NoError(t, err)
	certPEM, keyPEM, err := firewall.GenerateDomainCert(caCert, caKey, hostname)
	require.NoError(t, err)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	clientTLS := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: hostname,
		RootCAs:    x509.NewCertPool(),

		Rand:                                nil, //nolint:staticcheck // exhaustruct requires this deprecated field; its zero value keeps the TLS default.
		Time:                                nil,
		Certificates:                        nil,
		NameToCertificate:                   nil, //nolint:staticcheck // exhaustruct requires this deprecated field; its zero value keeps the TLS default.
		GetCertificate:                      nil,
		GetClientCertificate:                nil,
		GetConfigForClient:                  nil,
		VerifyPeerCertificate:               nil,
		VerifyConnection:                    nil,
		NextProtos:                          nil,
		ClientAuth:                          0,
		ClientCAs:                           nil,
		InsecureSkipVerify:                  false,
		CipherSuites:                        nil,
		PreferServerCipherSuites:            true, //nolint:staticcheck // exhaustruct requires this deprecated field; Go ignores its value.
		SessionTicketsDisabled:              false,
		SessionTicketKey:                    [32]byte{}, //nolint:staticcheck // exhaustruct requires this deprecated field; its zero value keeps the TLS default.
		ClientSessionCache:                  nil,
		UnwrapSession:                       nil,
		WrapSession:                         nil,
		MaxVersion:                          0,
		CurvePreferences:                    nil,
		DynamicRecordSizingDisabled:         false,
		Renegotiation:                       tls.RenegotiateNever,
		KeyLogWriter:                        nil,
		EncryptedClientHelloConfigList:      nil,
		EncryptedClientHelloRejectionVerify: nil,
		GetEncryptedClientHelloKeys:         nil,
		EncryptedClientHelloKeys:            nil,
	}
	clientTLS.RootCAs.AddCert(caCert)
	return grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&cert))), clientTLS
}

func openSDSStream(t *testing.T, server *grpc.Server, clientTLS *tls.Config) {
	t.Helper()
	store, err := firewall.NewRulesStoreFromString("rules: []")
	require.NoError(t, err)
	certDir := t.TempDir()
	ca, err := firewall.NewCAStore(func() (string, error) { return certDir, nil })
	require.NoError(t, err)
	sds, err := firewall.NewSDSServer(firewall.SDSServerDeps{
		Store: store,
		CA:    ca,
		Log:   logger.Nop(),
	})
	require.NoError(t, err)
	secretservice.RegisterSecretDiscoveryServiceServer(server, sds)
	listener := bufconn.Listen(1 << 20)
	t.Cleanup(func() { assert.NoError(t, listener.Close()) })
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve SDS: %v", serveErr)
		}
	}()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })
	stream, err := secretservice.NewSecretDiscoveryServiceClient(conn).DeltaSecrets(t.Context())
	require.NoError(t, err)
	req := new(discovery.DeltaDiscoveryRequest)
	req.TypeUrl = firewall.SDSSecretTypeURL
	req.ResourceNamesSubscribe = []string{"probe.example.com"}
	require.NoError(t, stream.Send(req))
	_, err = stream.Recv()
	require.NoError(t, err)
}

// leafWithSAN builds a bare parsed-cert stand-in carrying the given DNS
// SANs. requireSDSClientSAN only reads DNSNames from the already-verified
// leaf, so no signing is needed here — chain validity is the TLS stack's
// job (ClientAuth: RequireAndVerifyClientCert) and is not re-tested.
func leafWithSAN(sans ...string) *x509.Certificate {
	return &x509.Certificate{
		DNSNames:                sans,
		Raw:                     nil,
		RawTBSCertificate:       nil,
		RawSubjectPublicKeyInfo: nil,
		RawSubject:              nil,
		RawIssuer:               nil,
		RawSignatureAlgorithm:   nil,
		Signature:               nil,
		SignatureAlgorithm:      x509.UnknownSignatureAlgorithm,
		PublicKeyAlgorithm:      x509.UnknownPublicKeyAlgorithm,
		PublicKey:               nil,
		Version:                 0,
		SerialNumber:            nil,
		Issuer: pkix.Name{
			Country:            nil,
			Organization:       nil,
			OrganizationalUnit: nil,
			Locality:           nil,
			Province:           nil,
			StreetAddress:      nil,
			PostalCode:         nil,
			SerialNumber:       "",
			CommonName:         "",
			Names:              nil,
			ExtraNames:         nil,
		},
		Subject: pkix.Name{
			Country:            nil,
			Organization:       nil,
			OrganizationalUnit: nil,
			Locality:           nil,
			Province:           nil,
			StreetAddress:      nil,
			PostalCode:         nil,
			SerialNumber:       "",
			CommonName:         "",
			Names:              nil,
			ExtraNames:         nil,
		},
		NotBefore:                   time.Time{},
		NotAfter:                    time.Time{},
		KeyUsage:                    0,
		Extensions:                  nil,
		ExtraExtensions:             nil,
		UnhandledCriticalExtensions: nil,
		ExtKeyUsage:                 nil,
		UnknownExtKeyUsage:          nil,
		BasicConstraintsValid:       false,
		IsCA:                        false,
		MaxPathLen:                  0,
		MaxPathLenZero:              false,
		SubjectKeyId:                nil,
		AuthorityKeyId:              nil,
		OCSPServer:                  nil,
		IssuingCertificateURL:       nil,
		EmailAddresses:              nil,
		IPAddresses:                 nil,
		URIs:                        nil,
		PermittedDNSDomainsCritical: false,
		PermittedDNSDomains:         nil,
		ExcludedDNSDomains:          nil,
		PermittedIPRanges:           nil,
		ExcludedIPRanges:            nil,
		PermittedEmailAddresses:     nil,
		ExcludedEmailAddresses:      nil,
		PermittedURIDomains:         nil,
		ExcludedURIDomains:          nil,
		CRLDistributionPoints:       nil,
		PolicyIdentifiers:           nil,
		Policies:                    nil,
		InhibitAnyPolicy:            0,
		InhibitAnyPolicyZero:        false,
		InhibitPolicyMapping:        0,
		InhibitPolicyMappingZero:    false,
		RequireExplicitPolicy:       0,
		RequireExplicitPolicyZero:   false,
		PolicyMappings:              nil,
	}
}

// TestRequireSDSClientSAN pins the SDS listener's per-service authz: chain
// validation admits ANY infra-intermediate-signed leaf, so the pin must
// separate the dedicated SDS identity from every other infra client —
// most importantly the telemetry lane's envoy-otel-client leaf, which is
// signed by the same intermediate and would otherwise be able to fetch
// minted MITM certificates.
func TestRequireSDSClientSAN(t *testing.T) {
	t.Run("dedicated SDS leaf admitted", func(t *testing.T) {
		chains := [][]*x509.Certificate{{leafWithSAN(consts.EnvoySDSClientName)}}
		require.NoError(t, requireSDSClientSAN(chains))
	})

	t.Run("telemetry lane leaf refused", func(t *testing.T) {
		chains := [][]*x509.Certificate{{leafWithSAN("envoy-otel-client")}}
		err := requireSDSClientSAN(chains)
		require.Error(t, err)
		assert.Contains(t, err.Error(), consts.EnvoySDSClientName)
	})

	t.Run("no SANs refused", func(t *testing.T) {
		require.Error(t, requireSDSClientSAN([][]*x509.Certificate{{leafWithSAN()}}))
	})

	t.Run("empty chain set refused", func(t *testing.T) {
		require.Error(t, requireSDSClientSAN(nil))
	})
}
