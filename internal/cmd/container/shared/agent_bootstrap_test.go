package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
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

	const agentName = "clawker.alpha.bravo"
	b, err := GenerateAgentBootstrap(caCert, caKey, agentName, "https://hydra.example/oauth2/token", signing)
	require.NoError(t, err)
	require.NotNil(t, b)

	require.NotEmpty(t, b.Verifier)
	assert.Equal(t, "S256", b.Method)

	// Challenge must equal base64url(sha256(verifier)) with no padding.
	sum := sha256.Sum256([]byte(b.Verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	assert.Equal(t, expectedChallenge, b.Challenge)

	// Cert thumbprint matches sha256(certDER).
	require.Len(t, b.ExpectedCertThumbprint, 64)
	thumb, err := hex.DecodeString(b.ExpectedCertThumbprint)
	require.NoError(t, err)
	require.Len(t, thumb, sha256.Size)

	// Cert decodes; CN equals agentName; thumbprint matches.
	leaf := mustParseCert(t, b.CertPEM)
	assert.Equal(t, agentName, leaf.Subject.CommonName)
	got := sha256.Sum256(leaf.Raw)
	assert.Equal(t, hex.EncodeToString(got[:]), b.ExpectedCertThumbprint)

	// CA PEM matches the on-disk CA.
	assert.Contains(t, string(b.CACertPEM), "BEGIN CERTIFICATE")

	// Assertion is signed (non-empty JWT).
	assert.NotEmpty(t, b.Assertion)
}

func TestGenerateAgentBootstrap_Validation(t *testing.T) {
	caCert, caKey, signing := setupAuthEnv(t)
	tests := []struct {
		name      string
		agentName string
		signing   *ecdsa.PrivateKey
	}{
		{name: "empty agent name", agentName: "", signing: signing},
		{name: "nil signing key", agentName: "clawker.x", signing: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateAgentBootstrap(caCert, caKey, tc.agentName, "https://h", tc.signing)
			require.Error(t, err)
		})
	}
}

func validBootstrap() *AgentBootstrap {
	return &AgentBootstrap{
		Verifier:               "verifier",
		Challenge:              "challenge",
		Method:                 "S256",
		CertPEM:                []byte("cert-pem"),
		KeyPEM:                 []byte("key-pem"),
		CACertPEM:              []byte("ca-pem"),
		ExpectedCertThumbprint: "thumbprint",
		Assertion:              "assertion-jwt",
	}
}

func TestAnnounceAgent_FieldsPropagate(t *testing.T) {
	// Captures the full wire contract — a future field rename in the
	// proto can't silently drop a security-relevant attribute.
	var captured *adminv1.AnnounceAgentRequest
	mock := &mocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, in *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			captured = in
			return &adminv1.AnnounceAgentResult{ExpiresAtUnix: 12345}, nil
		},
	}

	b := validBootstrap()
	require.NoError(t, AnnounceAgent(context.Background(), mock, b, "clawker.x.y", "ctr-id"))
	require.NotNil(t, captured)
	assert.Equal(t, "clawker.x.y", captured.AgentName)
	assert.Equal(t, "ctr-id", captured.ContainerId)
	assert.Equal(t, "thumbprint", captured.ExpectedCertThumbprint)
	assert.Equal(t, "challenge", captured.CodeChallenge)
	assert.Equal(t, "S256", captured.CodeChallengeMethod)
}

func TestAnnounceAgent_PropagatesError(t *testing.T) {
	want := errors.New("rejected")
	mock := &mocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, _ *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			return nil, want
		},
	}
	err := AnnounceAgent(context.Background(), mock, validBootstrap(), "n", "id")
	assert.ErrorIs(t, err, want)
}

func TestAnnounceAgent_RejectsInvalidBootstrap(t *testing.T) {
	mock := &mocks.AdminServiceClientMock{}
	// Empty challenge would let the slot reserve with no PKCE binding.
	b := validBootstrap()
	b.Challenge = ""
	require.Error(t, AnnounceAgent(context.Background(), mock, b, "n", "id"))

	// Empty agent name would key the slot to "" — every announce would collide.
	require.Error(t, AnnounceAgent(context.Background(), mock, validBootstrap(), "", "id"))

	// Empty container ID would skip the IP cross-check at Register.
	require.Error(t, AnnounceAgent(context.Background(), mock, validBootstrap(), "n", ""))

	// Mock should never see a request — validation happens before RPC.
	assert.Empty(t, mock.AnnounceAgentCalls())
}

func TestWriteAgentBootstrapToContainer_TarShape(t *testing.T) {
	b := validBootstrap()
	b.Verifier = "verifier-bytes"

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
	expectFile(t, files, leaf+"/"+consts.BootstrapVerifierFile, 0o400, []byte(b.Verifier))
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
