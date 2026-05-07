package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// AgentBootstrap is the per-agent registration package the CLI delivers
// to a managed container at boot. It collects the mTLS leaf cert and
// key, the CA cert clawkerd uses to trust the CP server, and the Hydra
// client_assertion JWT. Material is meant to be tarred directly to the
// container — never persisted on the host.
//
// The String/GoString methods deliberately redact every field so the
// struct (which holds the per-agent private key and the Hydra
// client_assertion JWT) cannot leak via fmt verbs or zerolog's
// interface logger. Callers needing the raw fields must read them
// directly.
//
// The cert thumbprint is intentionally NOT carried here. CP captures
// the thumbprint at Register handler entry from the live mTLS peer
// (cert.Raw → SHA-256). The CLI does not pre-stage a thumbprint
// attestation in the registry — that was the source of the
// cross-process WAL coherence bug this redesign fixes. The CLI's
// trust contribution is the cert minting itself (chained to CLI CA,
// container_id baked into the URI SAN); CP is the sole writer of
// agentregistry rows.
type AgentBootstrap struct {
	CertPEM   []byte
	KeyPEM    []byte
	CACertPEM []byte
	Assertion string
}

// String redacts every field so AgentBootstrap can never accidentally
// leak the per-agent private key or the Hydra assertion JWT via
// fmt.Sprintf("%v", b) or zerolog.
func (*AgentBootstrap) String() string { return "AgentBootstrap{<redacted>}" }

// GoString redacts so fmt.Sprintf("%#v", b) (and any logger that uses
// Go-syntax representation) also does not leak KeyPEM or Assertion.
func (*AgentBootstrap) GoString() string { return "AgentBootstrap{<redacted>}" }

// GenerateAgentBootstrap mints all material the CLI needs to start one
// agent: the per-agent mTLS leaf cert + key signed by the CLI CA (with
// containerID embedded as a URI SAN), the CP server-trust CA cert, and
// a Hydra client_assertion JWT for the clawker-agent OAuth2 client.
// caCertPath/caKeyPath identify the CLI CA on disk (typically
// `consts.AuthCACertPath()` / `consts.AuthCAKeyPath()`); hydraTokenURL
// is the audience of the assertion (the CP's Hydra `/oauth2/token`
// endpoint as clawkerd will see it from inside the container).
//
// project + agent are the user-typed short identifiers (e.g. "myapp",
// "dev") — never the canonical "clawker.project.agent" form. The cert's
// CN is composed inside MintAgentCert via auth.CanonicalAgentCN so every
// CLI caller produces the same canonical shape and the agent handler's
// peer-cert CN cross-check has a single equality to enforce.
//
// containerID is the docker container_id returned by ContainerCreate —
// MintAgentCert embeds it as a URI SAN so the CP-side Register handler
// can read the cert's binding to a specific container directly off the
// peer cert at handler entry.
//
// The signature uses the typed auth.ProjectSlug / auth.AgentName so
// the caller has gone through NewProjectSlug / NewAgentName at the CLI
// flag boundary — a raw `string` cannot reach this function.
func GenerateAgentBootstrap(caCertPath, caKeyPath string, project auth.ProjectSlug, agent auth.AgentName, containerID, hydraTokenURL string, signingKey *ecdsa.PrivateKey) (*AgentBootstrap, error) {
	if agent.IsZero() {
		return nil, fmt.Errorf("agent name required")
	}
	if containerID == "" {
		return nil, fmt.Errorf("container id required")
	}
	if signingKey == nil {
		return nil, fmt.Errorf("signing key required")
	}

	cert, err := auth.MintAgentCert(caCertPath, caKeyPath, project, agent, containerID)
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
		CertPEM:   cert.CertPEM,
		KeyPEM:    cert.KeyPEM,
		CACertPEM: caPEM,
		Assertion: assertion,
	}, nil
}

// InstallAgentBootstrapOptions bundles the inputs InstallAgentBootstrap
// needs at container CREATE time — fresh cert+key minted per container,
// CopyToContainered into the writable layer.
//
// No registry path here. CP is the sole sqlite writer; the registry row
// is written server-side at Register handler entry, not from the CLI.
type InstallAgentBootstrapOptions struct {
	// Project + Agent are the typed (project, agent_name) identity
	// validated upstream at the CLI flag boundary. Used to compose the
	// canonical CN in the leaf cert.
	Project auth.ProjectSlug
	Agent   auth.AgentName
	// ContainerID is the ID returned by client.ContainerCreate. Embedded
	// as a URI SAN in the leaf cert and read by the CP-side Register
	// handler at handler entry.
	ContainerID string
	// HydraTokenAudience is the `aud` claim in the Hydra
	// client_assertion. Resolved by callers via
	// hydraTokenAudienceFromPort(cfg.Settings().ControlPlane.HydraPublicPort).
	HydraTokenAudience string
	// CopyToContainer streams the bootstrap tar into ContainerID's
	// writable layer at consts.BootstrapDir.
	CopyToContainer CopyToContainerFn
	// Logger receives a single info line on success. Required.
	Logger *logger.Logger
}

// InstallAgentBootstrapMaterial is the create-time agent install: mint
// cert/key/CA/assertion (GenerateAgentBootstrap) and tar them into the
// container's writable layer at consts.BootstrapDir
// (WriteAgentBootstrapToContainer).
//
// No DB I/O. The agentregistry row is written CP-side at Register
// handler entry, not from the CLI. A failure here is recovered by the
// caller's ContainerRemove without orphaning anything.
func InstallAgentBootstrapMaterial(ctx context.Context, caCertPath, caKeyPath string, signingKey *ecdsa.PrivateKey, opts InstallAgentBootstrapOptions) (*AgentBootstrap, error) {
	if opts.ContainerID == "" {
		return nil, fmt.Errorf("install agent bootstrap material: container id required")
	}
	if opts.CopyToContainer == nil {
		return nil, fmt.Errorf("install agent bootstrap material: copy-to-container fn required")
	}

	bootstrap, err := GenerateAgentBootstrap(caCertPath, caKeyPath, opts.Project, opts.Agent, opts.ContainerID, opts.HydraTokenAudience, signingKey)
	if err != nil {
		return nil, fmt.Errorf("install agent bootstrap material: generate: %w", err)
	}

	if err := WriteAgentBootstrapToContainer(ctx, opts.ContainerID, opts.CopyToContainer, bootstrap); err != nil {
		return nil, fmt.Errorf("install agent bootstrap material: write: %w", err)
	}
	return bootstrap, nil
}

// validate ensures every load-bearing bootstrap field is populated
// before the CLI commits to copying material into the container. Empty
// values would let a container start with empty cert/key files —
// fail later but with confusing diagnostics, so reject up front.
func (b *AgentBootstrap) validate() error {
	if b == nil {
		return fmt.Errorf("bootstrap is nil")
	}
	switch {
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
// so the pragmatic placement uses the writable layer with strict
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
