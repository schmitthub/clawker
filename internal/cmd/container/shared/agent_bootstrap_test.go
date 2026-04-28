package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	mocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/testenv"
)

func setupAuthEnv(t *testing.T) (caCert, caKey string, signing *ecdsa.PrivateKey) {
	t.Helper()
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())

	caCert, err := consts.AuthCACertPath()
	require.NoError(t, err)
	caKey, err = consts.AuthCAKeyPath()
	require.NoError(t, err)
	signing, err = auth.LoadSigningKey()
	require.NoError(t, err)
	return caCert, caKey, signing
}

func TestGenerateAgentBootstrap_HappyPath(t *testing.T) {
	caCert, caKey, signing := setupAuthEnv(t)

	const project, agent = "alpha", "bravo"
	b, err := GenerateAgentBootstrap(caCert, caKey, auth.MustProjectSlug(project), auth.MustAgentName(agent), "https://hydra.example/oauth2/token", signing)
	require.NoError(t, err)
	require.NotNil(t, b)

	require.True(t, b.HasVerifier())
	assert.Equal(t, consts.ChallengeMethodS256, b.Method)

	// Challenge must equal base64url(sha256(verifier)) with no padding.
	// HasVerifier confirmed presence; reading the bytes through the
	// unexported field is OK in same-package tests but consume-once
	// semantics still apply if we use ConsumeVerifier — use direct
	// field access here so the assertion doesn't burn the secret.
	sum := sha256.Sum256([]byte(b.verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	assert.Equal(t, expectedChallenge, b.Challenge)

	// Cert thumbprint matches sha256(certDER).
	assert.NotEqual(t, [sha256.Size]byte{}, b.ExpectedCertThumbprint, "thumbprint must not be zero-valued")

	// Cert decodes; CN must be canonical "clawker.<project>.<agent>" —
	// composed inside MintAgentCert so the agent handler's CN cross-check
	// has a single equality to enforce.
	leaf := mustParseCert(t, b.CertPEM)
	got := sha256.Sum256(leaf.Raw)
	assert.Equal(t, "clawker.alpha.bravo", leaf.Subject.CommonName)
	assert.Equal(t, got, b.ExpectedCertThumbprint)

	// CA PEM matches the on-disk CA.
	assert.Contains(t, string(b.CACertPEM), "BEGIN CERTIFICATE")

	// Assertion is signed (non-empty JWT).
	assert.NotEmpty(t, b.Assertion)
}

func TestGenerateAgentBootstrap_EmptyProjectStillWorks(t *testing.T) {
	// 2-segment naming case: empty project, short agent. Canonical CN is
	// "clawker.<agent>" — same convention as docker.ContainerName.
	caCert, caKey, signing := setupAuthEnv(t)
	b, err := GenerateAgentBootstrap(caCert, caKey, auth.ProjectSlug{}, auth.MustAgentName("solo"), "https://h.example/o/t", signing)
	require.NoError(t, err)
	leaf := mustParseCert(t, b.CertPEM)
	assert.Equal(t, "clawker.solo", leaf.Subject.CommonName)
}

func TestGenerateAgentBootstrap_Validation(t *testing.T) {
	caCert, caKey, signing := setupAuthEnv(t)
	tests := []struct {
		name    string
		agent   auth.AgentName
		signing *ecdsa.PrivateKey
	}{
		// Zero-value AgentName mirrors the empty-input case at the
		// post-NewAgentName boundary — production callers can't
		// construct that value but tests can to exercise the guard.
		{name: "zero agent name", agent: auth.AgentName{}, signing: signing},
		{name: "nil signing key", agent: auth.MustAgentName("x"), signing: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateAgentBootstrap(caCert, caKey, auth.MustProjectSlug("proj"), tc.agent, "https://h", tc.signing)
			require.Error(t, err)
		})
	}
}

func validBootstrap() *AgentBootstrap {
	return &AgentBootstrap{
		verifier:               "verifier",
		Challenge:              "challenge",
		Method:                 consts.ChallengeMethodS256,
		CertPEM:                []byte("cert-pem"),
		KeyPEM:                 []byte("key-pem"),
		CACertPEM:              []byte("ca-pem"),
		ExpectedCertThumbprint: sha256.Sum256([]byte("thumbprint-fixture")),
		Assertion:              "assertion-jwt",
	}
}

func TestAnnounceAgent_SendsContainerID(t *testing.T) {
	// AnnounceAgent's wire contract is now minimal: just container_id.
	// Identity (thumbprint, agent_name, project) is recorded once at
	// CreateContainer time in the CLI-written agentregistry — the slot
	// reserved here is purely a CLI-attestation token consumed by
	// agentdial when CP next dials the running clawkerd.
	var captured *adminv1.AnnounceAgentRequest
	mock := &mocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, in *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			captured = in
			return &adminv1.AnnounceAgentResult{ExpiresAtUnix: 12345}, nil
		},
	}

	require.NoError(t, AnnounceAgent(context.Background(), mock, "ctr-id"))
	require.NotNil(t, captured)
	assert.Equal(t, "ctr-id", captured.ContainerId)
}

func TestAnnounceAgent_PropagatesError(t *testing.T) {
	want := errors.New("rejected")
	mock := &mocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, _ *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			return nil, want
		},
	}
	err := AnnounceAgent(context.Background(), mock, "id")
	assert.ErrorIs(t, err, want)
}

func TestAnnounceAgent_RejectsEmptyContainerID(t *testing.T) {
	mock := &mocks.AdminServiceClientMock{}
	require.Error(t, AnnounceAgent(context.Background(), mock, ""))
	// Mock should never see a request — validation happens before RPC.
	assert.Empty(t, mock.AnnounceAgentCalls())
}

// TestAgentBootstrap_RedactsViaFormatter pins the redaction contract:
// none of fmt's verb permutations may surface the per-agent private
// key, the PKCE verifier (a bearer secret to the CP slot), or the
// Hydra assertion JWT. Catches the regression where someone adds a
// log line `log.Debug().Interface("bootstrap", b)` and quietly leaks
// every secret in the struct to the on-disk log.
func TestAgentBootstrap_RedactsViaFormatter(t *testing.T) {
	b := &AgentBootstrap{
		verifier:               "VERIFIER-BEARER-SECRET",
		Challenge:              "challenge-public",
		Method:                 consts.ChallengeMethodS256,
		CertPEM:                []byte("PRIVATE-CERT-MATERIAL"),
		KeyPEM:                 []byte("PRIVATE-KEY-MATERIAL"),
		ExpectedCertThumbprint: sha256.Sum256([]byte("thumb")),
		CACertPEM:              []byte("CA-MATERIAL"),
		Assertion:              "ASSERTION-JWT-SECRET",
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			out := fmt.Sprintf(verb, b)
			assert.Contains(t, out, "<redacted>", "formatter %q must include redaction marker", verb)
			assert.NotContains(t, out, "VERIFIER-BEARER-SECRET", "formatter %q leaked Verifier", verb)
			assert.NotContains(t, out, "PRIVATE-KEY-MATERIAL", "formatter %q leaked KeyPEM", verb)
			assert.NotContains(t, out, "ASSERTION-JWT-SECRET", "formatter %q leaked Assertion", verb)
		})
	}
}

func TestWriteAgentBootstrapToContainer_TarShape(t *testing.T) {
	b := validBootstrap()
	const wantVerifier = "verifier-bytes"
	b.verifier = wantVerifier

	var (
		gotDest   string
		gotTarBuf bytes.Buffer
		gotCalls  int
	)
	copyFn := func(_ context.Context, _ string, dest string, content io.Reader) error {
		gotCalls++
		gotDest = dest
		_, err := io.Copy(&gotTarBuf, content)
		return err
	}

	require.NoError(t, WriteAgentBootstrapToContainer(context.Background(), "ctr-id", copyFn, b))
	assert.Equal(t, 1, gotCalls)
	assert.Equal(t, path.Dir(consts.BootstrapDir), gotDest)

	files := readTar(t, &gotTarBuf)
	leaf := path.Base(consts.BootstrapDir)
	expectDir(t, files, leaf+"/", 0o700)
	expectFile(t, files, leaf+"/"+consts.BootstrapCertFile, 0o400, b.CertPEM)
	expectFile(t, files, leaf+"/"+consts.BootstrapKeyFile, 0o400, b.KeyPEM)
	expectFile(t, files, leaf+"/"+consts.BootstrapCAFile, 0o400, b.CACertPEM)
	expectFile(t, files, leaf+"/"+consts.BootstrapAssertionFile, 0o400, []byte(b.Assertion))
	// Verifier was consumed by the tar build — read the snapshot we
	// captured before WriteAgentBootstrapToContainer ran. b.HasVerifier()
	// must now be false (consume-once contract).
	expectFile(t, files, leaf+"/"+consts.BootstrapVerifierFile, 0o400, []byte(wantVerifier))
	assert.False(t, b.HasVerifier(), "ConsumeVerifier must zero the in-memory copy after tar build")
}

func TestWriteAgentBootstrapToContainer_Validation(t *testing.T) {
	noopCopy := func(_ context.Context, _ string, _ string, _ io.Reader) error { return nil }

	// Nil copyFn.
	require.Error(t, WriteAgentBootstrapToContainer(context.Background(), "ctr", nil, validBootstrap()))
	// Nil bootstrap.
	require.Error(t, WriteAgentBootstrapToContainer(context.Background(), "ctr", noopCopy, nil))
	// Empty cert PEM should fail before we tar-copy partial material.
	b := validBootstrap()
	b.CertPEM = nil
	require.Error(t, WriteAgentBootstrapToContainer(context.Background(), "ctr", noopCopy, b))
}

// --- helpers ---

type tarEntry struct {
	mode os.FileMode
	dir  bool
	body []byte
}

func readTar(t *testing.T, buf *bytes.Buffer) map[string]tarEntry {
	t.Helper()
	out := map[string]tarEntry{}
	tr := tar.NewReader(buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[hdr.Name] = tarEntry{
			mode: os.FileMode(hdr.Mode),
			dir:  hdr.Typeflag == tar.TypeDir,
			body: body,
		}
	}
	return out
}

func expectDir(t *testing.T, files map[string]tarEntry, name string, mode os.FileMode) {
	t.Helper()
	entry, ok := files[name]
	require.Truef(t, ok, "tar missing dir %q", name)
	assert.True(t, entry.dir, "expected dir for %q", name)
	assert.Equal(t, mode, entry.mode, "mode for %q", name)
}

func expectFile(t *testing.T, files map[string]tarEntry, name string, mode os.FileMode, body []byte) {
	t.Helper()
	entry, ok := files[name]
	require.Truef(t, ok, "tar missing entry %q", name)
	assert.False(t, entry.dir, "expected file (not dir) for %q", name)
	assert.Equal(t, mode, entry.mode, "mode for %q", name)
	assert.Equal(t, body, entry.body, "body for %q", name)
}

func mustParseCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf
}
