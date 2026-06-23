package agent_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/schmitthub/clawker/controlplane/agent"
	registrymock "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// resolvedCtx stuffs both a TLS peer cert and a middleware-resolved
// ResolvedContainer into ctx so the Register handler's peerLeaf +
// resolved-container reads both succeed. The peer.Addr value is an
// arbitrary loopback addr — the handler doesn't compare against it
// (the middleware already grounded the resolved container in peer IP).
func resolvedCtx(t *testing.T, leaf *x509.Certificate, resolved agent.ResolvedContainer) context.Context {
	t.Helper()
	tlsInfo := credentials.TLSInfo{
		State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{leaf},
		},
	}
	addr, err := net.ResolveTCPAddr("tcp", consts.Localhost+":52000")
	require.NoError(t, err)
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr:     addr,
		AuthInfo: tlsInfo,
	})
	return agent.WithResolvedContainer(ctx, resolved)
}

// resolvedFor is a convenience constructor for the typical resolved
// container test inputs (project/agent/container_id).
func resolvedFor(t *testing.T, project, agentName, containerID string) agent.ResolvedContainer {
	t.Helper()
	proj, err := auth.NewProjectSlug(project)
	require.NoError(t, err)
	name, err := auth.NewAgentName(agentName)
	require.NoError(t, err)
	return agent.ResolvedContainer{
		ContainerID: containerID,
		Project:     proj,
		AgentName:   name,
	}
}

// signTestLeaf produces a leaf cert chained to caCert/caKey with the
// given CN and (optionally) AgentFullName + container_id encoded as
// URI SANs. Production leaves carry CN=consts.ContainerClawkerd plus
// both SANs; tests that need to drive a specific failure path pass
// deliberately-bogus or empty values for the relevant input.
func signTestLeaf(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn, agentFullName, containerID string) *x509.Certificate {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"clawker"},
		},
		NotBefore:   time.Now().Add(-time.Minute),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	if agentFullName != "" {
		uri, err := url.Parse(auth.AgentSANScheme + agentFullName)
		require.NoError(t, err)
		tmpl.URIs = append(tmpl.URIs, uri)
	}
	if containerID != "" {
		uri, err := url.Parse(auth.ContainerSANScheme + containerID)
		require.NoError(t, err)
		tmpl.URIs = append(tmpl.URIs, uri)
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return leaf
}

// genTestCA creates a self-signed CA pair for test cert minting.
func genTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return caCert, key
}

// newTestHandler builds a Register handler with a mock registry and
// Nop logger.
func newTestHandler(reg agent.Registry) *agent.Handler {
	return &agent.Handler{
		Registry: reg,
		Log:      logger.Nop(),
		Clock:    func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
}

func TestRegister_HappyPath(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-happy-path"
	const project, agentName = "myapp", "dev"
	leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd, auth.AgentFullName(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), containerID)

	var added agent.Entry
	reg := &registrymock.RegistryMock{
		LookupByContainerIDFunc: func(string) (*agent.Entry, error) { return nil, agent.ErrUnknownAgent },
		AddFunc: func(e agent.Entry) error {
			added = e
			return nil
		},
	}
	h := newTestHandler(reg)

	ctx := resolvedCtx(t, leaf, resolvedFor(t, project, agentName, containerID))
	resp, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: agentName, Project: project})
	require.NoError(t, err)
	require.NotNil(t, resp)

	wantThumbprint := sha256.Sum256(leaf.Raw)
	assert.Equal(t, wantThumbprint, added.Thumbprint)
	assert.Equal(t, containerID, added.ContainerID)
	assert.Equal(t, agentName, added.AgentName.String())
	assert.Equal(t, project, added.Project.String())
}

// TestRegister_RequestValidation pins that request-body validation
// runs ahead of the ctx-resolved check — a malformed request must
// surface as InvalidArgument even when the wiring is broken. After
// the auth-side validators were gutted (Docker/x509 enforce their own
// constraints downstream), the only request-body check that still
// runs in the handler is "agent_name required" — which is the
// canonical malformed-input surface we keep verified here.
func TestRegister_RequestValidation(t *testing.T) {
	h := newTestHandler(&registrymock.RegistryMock{})
	cases := []struct {
		name string
		req  *agentv1.RegisterRequest
	}{
		{"empty agent_name", &agentv1.RegisterRequest{Project: "p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.Register(context.Background(), tc.req)
			require.Error(t, err)
			st, _ := status.FromError(err)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

// TestRegister_CtxGates covers the two pre-cross-check ctx gates that
// run after request validation passes: a missing resolved container
// (wiring bug, Internal) and a missing peer cert despite having a
// resolved container (defense-in-depth, PermissionDenied — the
// interceptor must have produced the resolved container from a cert,
// so reaching this branch means ctx was stripped post-resolve).
func TestRegister_CtxGates(t *testing.T) {
	resolved := resolvedFor(t, "p", "dev", "ctr-id")
	cases := []struct {
		name     string
		ctx      context.Context
		wantCode codes.Code
	}{
		{"missing resolved container", context.Background(), codes.Internal},
		{"resolved present but peer cert stripped", agent.WithResolvedContainer(context.Background(), resolved), codes.PermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&registrymock.RegistryMock{})
			_, err := h.Register(tc.ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "p"})
			require.Error(t, err)
			st, _ := status.FromError(err)
			assert.Equal(t, tc.wantCode, st.Code())
		})
	}
}

// TestRegister_IdentityCrossChecks covers the three identity gates
// the handler owns: request fields must agree with resolved labels;
// cert must carry urn:clawker:container: SAN; cert SAN must match
// resolved.ContainerID. Each row poisons exactly one input. All
// reject with PermissionDenied and must not reach Add.
func TestRegister_IdentityCrossChecks(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const goodProject, goodAgent, goodContainerID = "myapp", "dev", "ctr-resolved"
	goodAgentFullName := auth.AgentFullName(auth.MustProjectSlug(goodProject), auth.MustAgentName(goodAgent))

	cases := []struct {
		name              string
		leafContainerSAN  string // empty → no urn:clawker:container: SAN
		certAgentSAN      string // AgentFullName baked into the leaf's urn:clawker:agent: URI SAN
		reqProject        string
		reqAgent          string
		resolvedContainer string
	}{
		{
			name:              "request project disagrees with resolved",
			leafContainerSAN:  goodContainerID,
			certAgentSAN:      goodAgentFullName,
			reqProject:        "different",
			reqAgent:          goodAgent,
			resolvedContainer: goodContainerID,
		},
		{
			name:              "request agent disagrees with resolved",
			leafContainerSAN:  goodContainerID,
			certAgentSAN:      goodAgentFullName,
			reqProject:        goodProject,
			reqAgent:          "other",
			resolvedContainer: goodContainerID,
		},
		{
			name:              "cert missing container URI SAN",
			leafContainerSAN:  "",
			certAgentSAN:      goodAgentFullName,
			reqProject:        goodProject,
			reqAgent:          goodAgent,
			resolvedContainer: goodContainerID,
		},
		{
			name:              "cert container SAN disagrees with resolved",
			leafContainerSAN:  "ctr-cert-claim",
			certAgentSAN:      goodAgentFullName,
			reqProject:        goodProject,
			reqAgent:          goodAgent,
			resolvedContainer: "ctr-resolved-truth",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd, tc.certAgentSAN, tc.leafContainerSAN)
			addCalled := false
			reg := &registrymock.RegistryMock{
				LookupByContainerIDFunc: func(string) (*agent.Entry, error) { return nil, agent.ErrUnknownAgent },
				AddFunc: func(agent.Entry) error {
					addCalled = true
					return nil
				},
			}
			h := newTestHandler(reg)

			ctx := resolvedCtx(t, leaf, resolvedFor(t, goodProject, goodAgent, tc.resolvedContainer))
			_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: tc.reqAgent, Project: tc.reqProject})
			require.Error(t, err)
			st, _ := status.FromError(err)
			assert.Equal(t, codes.PermissionDenied, st.Code())
			assert.False(t, addCalled, "Add must not run when identity cross-checks fail")
		})
	}
}

// TestRegister_RegistryBranches covers the post-cross-check registry
// branches the handler decides on. Each row pins a distinct status
// code mapping (Welcome/PermissionDenied/Internal) and the
// Add-callthrough behavior. The handler's identity gates are
// pre-asserted by `TestRegister_IdentityCrossChecks` — rows here
// share the same happy-path inputs and vary only the registry-mock
// behavior.
func TestRegister_RegistryBranches(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID, project, agentName = "ctr-reg-branches", "myapp", "dev"
	leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd, auth.AgentFullName(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), containerID)
	thumb := sha256.Sum256(leaf.Raw)
	otherThumb := sha256.Sum256([]byte("a-different-cert"))

	cases := []struct {
		name            string
		lookup          func(string) (*agent.Entry, error)
		add             func(agent.Entry) error
		wantCode        codes.Code // codes.OK = success
		wantAddCalled   bool
		failOnAddCalled bool // true → Add must not be reached
	}{
		{
			name: "idempotent retry — matching thumbprint, Add skipped",
			lookup: func(string) (*agent.Entry, error) {
				return &agent.Entry{ContainerID: containerID, Thumbprint: thumb}, nil
			},
			add:           func(agent.Entry) error { return nil },
			wantCode:      codes.OK,
			wantAddCalled: false,
		},
		{
			name: "thumbprint replay — existing row, different thumbprint",
			lookup: func(string) (*agent.Entry, error) {
				return &agent.Entry{ContainerID: containerID, Thumbprint: otherThumb}, nil
			},
			wantCode:        codes.PermissionDenied,
			failOnAddCalled: true,
		},
		{
			name:            "lookup i/o error (non-ErrUnknownAgent) → Internal, Add skipped",
			lookup:          func(string) (*agent.Entry, error) { return nil, errors.New("disk i/o error") },
			wantCode:        codes.Internal,
			failOnAddCalled: true,
		},
		{
			name:          "Add i/o error → Internal",
			lookup:        func(string) (*agent.Entry, error) { return nil, agent.ErrUnknownAgent },
			add:           func(agent.Entry) error { return errors.New("UNIQUE constraint failed") },
			wantCode:      codes.Internal,
			wantAddCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addCalled := false
			reg := &registrymock.RegistryMock{
				LookupByContainerIDFunc: tc.lookup,
				AddFunc: func(e agent.Entry) error {
					addCalled = true
					if tc.failOnAddCalled {
						t.Fatalf("Add must not run for %s", tc.name)
					}
					if tc.add != nil {
						return tc.add(e)
					}
					return nil
				},
			}
			h := newTestHandler(reg)

			ctx := resolvedCtx(t, leaf, resolvedFor(t, project, agentName, containerID))
			resp, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: agentName, Project: project})

			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				require.NotNil(t, resp)
			} else {
				require.Error(t, err)
				st, _ := status.FromError(err)
				assert.Equal(t, tc.wantCode, st.Code())
			}
			assert.Equal(t, tc.wantAddCalled, addCalled, "Add call expectation mismatch")
		})
	}
}

// TestRegister_MalformedRowEvictThenRewrite pins T5's recovery path:
// when LookupByContainerID returns ErrMalformedEntry (a row whose
// agent_name / project / thumbprint failed re-validation at scan
// time), the handler must evict the row and proceed with the normal
// Add path. The Welcome surfaces to the agent; the registry ends up
// holding the middleware-resolved (and freshly validated) identity.
func TestRegister_MalformedRowEvictThenRewrite(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID, project, agentName = "ctr-malformed", "myapp", "dev"
	leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd,
		auth.AgentFullName(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), containerID)

	var (
		evicted   bool
		addCalled bool
	)
	reg := &registrymock.RegistryMock{
		LookupByContainerIDFunc: func(string) (*agent.Entry, error) {
			return nil, fmt.Errorf("agentregistry: query LookupByContainerID: %w", agent.ErrMalformedEntry)
		},
		EvictByContainerIDFunc: func(id string) error {
			evicted = true
			assert.Equal(t, containerID, id, "evict must target the resolved container_id")
			return nil
		},
		AddFunc: func(e agent.Entry) error {
			addCalled = true
			// Re-written row carries the middleware-resolved identity,
			// not whatever was malformed on the old row.
			assert.Equal(t, containerID, e.ContainerID)
			assert.Equal(t, agentName, e.AgentName.String())
			assert.Equal(t, project, e.Project.String())
			return nil
		},
	}
	h := newTestHandler(reg)

	ctx := resolvedCtx(t, leaf, resolvedFor(t, project, agentName, containerID))
	resp, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: agentName, Project: project})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, evicted, "EvictByContainerID must be called on malformed row")
	assert.True(t, addCalled, "Add must be called after evict to re-write the row")
}

// TestRegister_MalformedRowEvictFailure pins the failure branch of the
// recovery path: if the registry's evict itself fails on a malformed
// row, the handler must NOT proceed to Add — Add would hit the same
// stale row and either re-fail or land a misaligned upsert. Surface
// as Internal so the operator's structured log line carries the
// classification.
func TestRegister_MalformedRowEvictFailure(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID, project, agentName = "ctr-malformed-evict-fail", "myapp", "dev"
	leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd,
		auth.AgentFullName(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), containerID)

	reg := &registrymock.RegistryMock{
		LookupByContainerIDFunc: func(string) (*agent.Entry, error) {
			return nil, fmt.Errorf("read: %w", agent.ErrMalformedEntry)
		},
		EvictByContainerIDFunc: func(string) error { return errors.New("evict disk i/o") },
		AddFunc:                func(agent.Entry) error { t.Fatal("Add must not run after evict failure"); return nil },
	}
	h := newTestHandler(reg)

	ctx := resolvedCtx(t, leaf, resolvedFor(t, project, agentName, containerID))
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: agentName, Project: project})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}
