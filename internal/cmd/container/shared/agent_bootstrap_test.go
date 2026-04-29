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
	"fmt"
	"io"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
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

// TestInstallAgentBootstrapMaterial_DoesNotTouchRegistry verifies the
// material-delivery half of the create-time install does not open or
// write to the registry DB. The split is the C7 fix: registry row must
// be the LAST step so a post-init failure cannot orphan a row.
func TestInstallAgentBootstrapMaterial_DoesNotTouchRegistry(t *testing.T) {
	caCert, caKey, signing := setupAuthEnv(t)

	var copied bool
	copyFn := func(_ context.Context, _ string, _ string, _ io.Reader) error {
		copied = true
		return nil
	}

	// Pass a registry DB path that would FAIL to open if touched (file
	// path under a non-existent directory). Material delivery must not
	// open the registry, so this path must never be dereferenced.
	bogusDB := path.Join(t.TempDir(), "does-not-exist-dir", "agents.db")

	bootstrap, err := InstallAgentBootstrapMaterial(context.Background(), caCert, caKey, signing, InstallAgentBootstrapOptions{
		Project:            auth.MustProjectSlug("alpha"),
		Agent:              auth.MustAgentName("bravo"),
		ContainerID:        "ctr-id",
		HydraTokenAudience: "https://hydra.example/oauth2/token",
		CopyToContainer:    copyFn,
		RegistryDBPath:     bogusDB,
	})
	require.NoError(t, err)
	require.NotNil(t, bootstrap)
	assert.True(t, copied, "WriteAgentBootstrapToContainer must run")

	// Bogus DB path must not have been opened — no parent dir was created.
	_, statErr := os.Stat(path.Dir(bogusDB))
	assert.True(t, os.IsNotExist(statErr), "registry DB parent dir must not be created during material delivery")
}

// TestRegisterAgentInRegistry_AddSucceeds_NoErr exercises the happy path:
// a real sqlite DB, a synthetic bootstrap, and verification that the row
// reads back via LookupByContainerID.
func TestRegisterAgentInRegistry_AddSucceeds_NoErr(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "agents.db")
	bootstrap := validBootstrap()
	opts := InstallAgentBootstrapOptions{
		Project:        auth.MustProjectSlug("alpha"),
		Agent:          auth.MustAgentName("bravo"),
		ContainerID:    "ctr-register-ok",
		RegistryDBPath: dbPath,
	}

	require.NoError(t, RegisterAgentInRegistry(context.Background(), opts, bootstrap))

	// Verify row landed by reopening the DB.
	r, err := agentregistry.NewSQLiteWriter(dbPath, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		if c, ok := r.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	})
	entry, err := r.LookupByContainerID("ctr-register-ok")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "bravo", entry.AgentName)
	assert.Equal(t, "alpha", entry.Project)
	assert.Equal(t, bootstrap.ExpectedCertThumbprint, entry.Thumbprint)
}

// TestRegisterAgentInRegistry_DBFailure_ReturnsErr verifies that a failed
// sqlite open surfaces as a returned error rather than a panic. Path
// points inside a non-existent parent directory so the driver cannot
// create the file.
func TestRegisterAgentInRegistry_DBFailure_ReturnsErr(t *testing.T) {
	bogusDB := path.Join(t.TempDir(), "does-not-exist-dir", "agents.db")
	err := RegisterAgentInRegistry(context.Background(), InstallAgentBootstrapOptions{
		Project:        auth.MustProjectSlug("alpha"),
		Agent:          auth.MustAgentName("bravo"),
		ContainerID:    "ctr",
		RegistryDBPath: bogusDB,
	}, validBootstrap())
	require.Error(t, err)
}

// TestRegisterAgentInRegistry_RejectsNilBootstrap guards the explicit
// nil-check at the top of RegisterAgentInRegistry — a bug in the caller
// that forgets to thread the bootstrap through must not segfault.
func TestRegisterAgentInRegistry_RejectsNilBootstrap(t *testing.T) {
	err := RegisterAgentInRegistry(context.Background(), InstallAgentBootstrapOptions{
		ContainerID:    "ctr",
		RegistryDBPath: path.Join(t.TempDir(), "agents.db"),
	}, nil)
	require.Error(t, err)
}

func mustParseCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return leaf
}
