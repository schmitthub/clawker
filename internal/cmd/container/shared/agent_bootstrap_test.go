package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
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

	const project, agent, containerID = "alpha", "bravo", "abcdef0123456789"
	b, err := GenerateAgentBootstrap(caCert, caKey, auth.MustProjectSlug(project), auth.MustAgentName(agent), containerID, "https://hydra.example/oauth2/token", signing)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Cert decodes; CN is the deterministic clawkerd binary identity.
	// The per-agent canonical lives in the urn:clawker:agent: URI SAN
	// so long random docker.GenerateRandomName outputs don't push the
	// cert past x509's 64-byte CN limit.
	leaf := mustParseCert(t, b.CertPEM)
	assert.Equal(t, consts.ContainerClawkerd, leaf.Subject.CommonName)

	// Canonical agent identity rides in the agent URI SAN.
	gotAgentFullName, ok := auth.AgentFullNameFromCert(leaf)
	require.True(t, ok, "cert must carry agent URI SAN")
	assert.Equal(t, "clawker.alpha.bravo", gotAgentFullName)

	// Container_id must be embedded as a URI SAN — the load-bearing
	// binding the Register handler reads to identify which container
	// the request is about.
	gotID, ok := auth.ContainerIDFromCert(leaf)
	require.True(t, ok, "cert must carry container_id URI SAN")
	assert.Equal(t, containerID, gotID)

	// CA PEM matches the on-disk CA.
	assert.Contains(t, string(b.CACertPEM), "BEGIN CERTIFICATE")

	// Assertion is signed (non-empty JWT).
	assert.NotEmpty(t, b.Assertion)
}

func TestGenerateAgentBootstrap_EmptyProjectStillWorks(t *testing.T) {
	// 2-segment naming case: empty project, short agent. The agent
	// SAN encodes "clawker.<agent>" (matching docker.ContainerName);
	// CN remains the binary literal.
	caCert, caKey, signing := setupAuthEnv(t)
	b, err := GenerateAgentBootstrap(caCert, caKey, auth.ProjectSlug{}, auth.MustAgentName("solo"), "fedcba9876543210", "https://h.example/o/t", signing)
	require.NoError(t, err)
	leaf := mustParseCert(t, b.CertPEM)
	assert.Equal(t, consts.ContainerClawkerd, leaf.Subject.CommonName)
	gotAgentFullName, ok := auth.AgentFullNameFromCert(leaf)
	require.True(t, ok)
	assert.Equal(t, "clawker.solo", gotAgentFullName)
}

func TestGenerateAgentBootstrap_Validation(t *testing.T) {
	caCert, caKey, signing := setupAuthEnv(t)
	tests := []struct {
		name        string
		agent       auth.AgentName
		containerID string
		signing     *ecdsa.PrivateKey
	}{
		{name: "zero agent name", agent: auth.AgentName{}, containerID: "ctr", signing: signing},
		{name: "empty container id", agent: auth.MustAgentName("x"), containerID: "", signing: signing},
		{name: "nil signing key", agent: auth.MustAgentName("x"), containerID: "ctr", signing: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateAgentBootstrap(caCert, caKey, auth.MustProjectSlug("proj"), tc.agent, tc.containerID, "https://h", tc.signing)
			require.Error(t, err)
		})
	}
}

func validBootstrap() *AgentBootstrap {
	return &AgentBootstrap{
		CertPEM:   []byte("cert-pem"),
		KeyPEM:    []byte("key-pem"),
		CACertPEM: []byte("ca-pem"),
		Assertion: "assertion-jwt",
	}
}

// TestAgentBootstrap_RedactsViaFormatter pins the redaction contract:
// none of fmt's verb permutations may surface the per-agent private
// key or the Hydra assertion JWT. Catches the regression where someone
// adds a log line `log.Debug().Interface("bootstrap", b)` and quietly
// leaks every secret in the struct to the on-disk log.
func TestAgentBootstrap_RedactsViaFormatter(t *testing.T) {
	b := &AgentBootstrap{
		CertPEM:   []byte("PRIVATE-CERT-MATERIAL"),
		KeyPEM:    []byte("PRIVATE-KEY-MATERIAL"),
		CACertPEM: []byte("CA-MATERIAL"),
		Assertion: "ASSERTION-JWT-SECRET",
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			out := fmt.Sprintf(verb, b)
			assert.Contains(t, out, "<redacted>", "formatter %q must include redaction marker", verb)
			assert.NotContains(t, out, "PRIVATE-KEY-MATERIAL", "formatter %q leaked KeyPEM", verb)
			assert.NotContains(t, out, "ASSERTION-JWT-SECRET", "formatter %q leaked Assertion", verb)
		})
	}
}

func TestWriteAgentBootstrapToContainer_TarShape(t *testing.T) {
	b := validBootstrap()

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
	// Verifier file was retired alongside PKCE — only 4 entries
	// (1 dir + 3 normal files including assertion would be 5; verify
	// total file count).
	assert.Equal(t, 5, len(files), "expected exactly the directory + 4 files (cert, key, ca, assertion)")
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

// TestInstallAgentBootstrapMaterial_DoesNotTouchRegistry verifies the
// material-delivery path does not open or write to the registry DB.
// CP is the sole sqlite writer; the CLI only delivers material into
// the container's writable layer.
func TestInstallAgentBootstrapMaterial_DoesNotTouchRegistry(t *testing.T) {
	caCert, caKey, signing := setupAuthEnv(t)

	var copied bool
	copyFn := func(_ context.Context, _ string, _ string, _ io.Reader) error {
		copied = true
		return nil
	}

	err := InstallAgentBootstrapMaterial(context.Background(), caCert, caKey, signing, InstallAgentBootstrapOptions{
		Project:            auth.MustProjectSlug("alpha"),
		Agent:              auth.MustAgentName("bravo"),
		ContainerID:        "deadbeefcafef00d",
		HydraTokenAudience: "https://hydra.example/oauth2/token",
		CopyToContainer:    copyFn,
	})
	require.NoError(t, err)
	assert.True(t, copied, "WriteAgentBootstrapToContainer must run")
}

func mustParseCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf
}

// stub usage to keep import; thumbprint helper retained for any future test.
var _ = sha256.Sum256
