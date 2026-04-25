package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
)

// AgentBootstrap is the per-agent registration package the CLI delivers
// to a managed container at boot. It collects the PKCE pair, the mTLS
// leaf cert and key, the CA cert clawkerd uses to trust the CP server,
// and the Hydra client_assertion JWT. Material is meant to be tarred
// directly to the container — never persisted on the host.
type AgentBootstrap struct {
	Verifier               string
	Challenge              string
	Method                 string // "S256" only
	CertPEM                []byte
	KeyPEM                 []byte
	ExpectedCertThumbprint string // lowercase-hex SHA-256 of cert DER
	CACertPEM              []byte
	Assertion              string
}

// GenerateAgentBootstrap mints all material the CLI needs to announce
// + start one agent: a fresh 32-byte PKCE verifier, the matching S256
// challenge, the per-agent mTLS leaf cert + key signed by the CLI CA,
// the CP server-trust CA cert, and a Hydra client_assertion for the
// clawker-agent OAuth2 client. caCertPath/caKeyPath identify the CLI
// CA on disk (typically `consts.AuthCACertPath()` /
// `consts.AuthCAKeyPath()`); hydraTokenURL is the audience of the
// assertion (the CP's Hydra `/oauth2/token` endpoint as clawkerd will
// see it from inside the container).
func GenerateAgentBootstrap(caCertPath, caKeyPath, agentName, hydraTokenURL string, signingKey *ecdsa.PrivateKey) (*AgentBootstrap, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name required")
	}
	if signingKey == nil {
		return nil, fmt.Errorf("signing key required")
	}

	verifier, challenge, err := newPKCEPair()
	if err != nil {
		return nil, fmt.Errorf("pkce: %w", err)
	}

	cert, err := auth.MintAgentCert(caCertPath, caKeyPath, agentName)
	if err != nil {
		return nil, fmt.Errorf("mint agent cert: %w", err)
	}

	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	assertion, err := auth.BuildAgentAssertion(hydraTokenURL, signingKey)
	if err != nil {
		return nil, fmt.Errorf("build agent assertion: %w", err)
	}

	return &AgentBootstrap{
		Verifier:               verifier,
		Challenge:              challenge,
		Method:                 "S256",
		CertPEM:                cert.CertPEM,
		KeyPEM:                 cert.KeyPEM,
		ExpectedCertThumbprint: cert.ThumbprintHex,
		CACertPEM:              caPEM,
		Assertion:              assertion,
	}, nil
}

// AnnounceAgent reserves a registration slot on the CP for the given
// container before docker start. CP slot stores the canonical agent
// name, the Docker container ID CLI just received, the cert thumbprint
// CLI minted, and the PKCE challenge. clawkerd consumes the matching
// verifier at Register; if the slot expires (60s) clawkerd's Register
// fails fail-closed.
func AnnounceAgent(ctx context.Context, admin adminv1.AdminServiceClient, b *AgentBootstrap, agentName, containerID string) error {
	if err := b.validate(); err != nil {
		return fmt.Errorf("announce agent %q: %w", agentName, err)
	}
	if agentName == "" {
		return fmt.Errorf("announce agent: agent name required")
	}
	if containerID == "" {
		return fmt.Errorf("announce agent %q: container id required", agentName)
	}
	if _, err := admin.AnnounceAgent(ctx, &adminv1.AnnounceAgentRequest{
		AgentName:              agentName,
		ContainerId:            containerID,
		ExpectedCertThumbprint: b.ExpectedCertThumbprint,
		CodeChallenge:          b.Challenge,
		CodeChallengeMethod:    b.Method,
	}); err != nil {
		return fmt.Errorf("announce agent %q (container %s): %w", agentName, containerID, err)
	}
	return nil
}

// validate ensures every load-bearing bootstrap field is populated
// before the CLI commits to a Hydra-published slot or a tar copy. Empty
// values would let an Announce slot reserve with no PKCE binding or a
// container start with empty cert/key files — both fail later but with
// confusing diagnostics, so reject up front.
func (b *AgentBootstrap) validate() error {
	if b == nil {
		return fmt.Errorf("bootstrap is nil")
	}
	switch {
	case b.Method != "S256":
		return fmt.Errorf("bootstrap challenge method must be S256, got %q", b.Method)
	case b.Challenge == "":
		return fmt.Errorf("bootstrap challenge is empty")
	case b.Verifier == "":
		return fmt.Errorf("bootstrap verifier is empty")
	case b.ExpectedCertThumbprint == "":
		return fmt.Errorf("bootstrap cert thumbprint is empty")
	case len(b.CertPEM) == 0:
		return fmt.Errorf("bootstrap cert PEM is empty")
	case len(b.KeyPEM) == 0:
		return fmt.Errorf("bootstrap key PEM is empty")
	case len(b.CACertPEM) == 0:
		return fmt.Errorf("bootstrap CA PEM is empty")
	case b.Assertion == "":
		return fmt.Errorf("bootstrap assertion is empty")
	}
	return nil
}

// WriteAgentBootstrapToContainer streams the bootstrap material as a
// tar archive into the container's filesystem at consts.BootstrapDir.
// Files are 0400 root:root inside the archive; the directory itself
// is 0700 root:root. Caller passes the same CopyToContainerFn used by
// InjectPostInitScript, so behavior matches existing post-create
// injection patterns.
//
// Note: the destination is currently a regular path inside the
// container's writable layer rather than a tmpfs mount. Docker's
// CopyToContainer cannot pre-populate tmpfs mounts (tmpfs is mounted
// at start time, shadowing any contents written via cp before start),
// so the pragmatic B4 placement uses the writable layer with strict
// permissions. The container layer is destroyed on `--rm` or when
// the container is removed; for non-`--rm` containers the material
// stays in the writable layer until removal but is only useful
// against this container's identity.
func WriteAgentBootstrapToContainer(ctx context.Context, containerID string, copyFn CopyToContainerFn, b *AgentBootstrap) error {
	if copyFn == nil {
		return fmt.Errorf("WriteAgentBootstrapToContainer: CopyToContainerFn is required")
	}
	if err := b.validate(); err != nil {
		return fmt.Errorf("WriteAgentBootstrapToContainer: %w", err)
	}

	tarBuf, err := bootstrapTar(b)
	if err != nil {
		return fmt.Errorf("build bootstrap tar: %w", err)
	}

	// Copy to the parent of consts.BootstrapDir so the tar's leading
	// directory entry creates BootstrapDir itself with the right
	// permissions. Mirrors how InjectPostInitScript writes
	// `~/.clawker/post-init.sh` by tar'ing the parent.
	parent, _ := bootstrapParentAndLeaf()
	if err := copyFn(ctx, containerID, parent, tarBuf); err != nil {
		return fmt.Errorf("copy bootstrap into container: %w", err)
	}
	return nil
}

func newPKCEPair() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func bootstrapTar(b *AgentBootstrap) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	now := time.Now()
	_, leafName := bootstrapParentAndLeaf()

	// Directory header — 0700 root:root.
	if err := tw.WriteHeader(&tar.Header{
		Name:     leafName + "/",
		Mode:     0o700,
		Typeflag: tar.TypeDir,
		ModTime:  now,
	}); err != nil {
		return nil, err
	}

	files := []struct {
		name string
		body []byte
	}{
		{consts.BootstrapCertFile, b.CertPEM},
		{consts.BootstrapKeyFile, b.KeyPEM},
		{consts.BootstrapCAFile, b.CACertPEM},
		{consts.BootstrapAssertionFile, []byte(b.Assertion)},
		{consts.BootstrapVerifierFile, []byte(b.Verifier)},
	}
	for _, f := range files {
		hdr := &tar.Header{
			Name:    leafName + "/" + f.name,
			Mode:    0o400,
			Size:    int64(len(f.body)),
			ModTime: now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

// bootstrapParentAndLeaf splits consts.BootstrapDir into the parent
// directory (CopyToContainer's destination) and the leaf segment (the
// directory name written into the tar archive).
func bootstrapParentAndLeaf() (parent, leaf string) {
	parent, leaf = path.Split(strings.TrimSuffix(consts.BootstrapDir, "/"))
	return strings.TrimSuffix(parent, "/"), leaf
}
