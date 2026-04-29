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
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/logger"
)

// AgentBootstrap is the per-agent registration package the CLI delivers
// to a managed container at boot. It collects the PKCE pair, the mTLS
// leaf cert and key, the CA cert clawkerd uses to trust the CP server,
// and the Hydra client_assertion JWT. Material is meant to be tarred
// directly to the container — never persisted on the host.
//
// The String/GoString methods deliberately redact every field so the
// struct (which holds the PKCE verifier, the per-agent private key,
// and the Hydra assertion JWT) cannot leak via fmt verbs or zerolog's
// interface logger. Callers needing the raw fields must read them
// directly.
//
// The PKCE verifier is held in an unexported field and exposed only
// via ConsumeVerifier, which returns it AND zeros the in-memory copy.
// The verifier is a single-use bearer secret for the CP slot — once
// it has been written into the container's bootstrap tar, the host
// process has no legitimate reason to read it again. The consume-once
// gate makes accidental misuse a compile error: external callers
// cannot read the field directly, and even in-package callers go
// through a method whose return-value-and-zero semantics surface in
// the call site instead of being implicit on a public string field.
type AgentBootstrap struct {
	// verifier is the PKCE secret. Access only via ConsumeVerifier or
	// the in-package internal helpers below. NEVER make this exported.
	verifier  string
	Challenge string
	// Method is the PKCE challenge method announced over the wire.
	// Typed for safety; today only consts.ChallengeMethodS256 is
	// accepted by the CP, and the bootstrap helpers reject anything
	// else before it can reach the wire.
	Method                 consts.ChallengeMethod
	CertPEM                []byte
	KeyPEM                 []byte
	ExpectedCertThumbprint [sha256.Size]byte // SHA-256 over cert DER
	CACertPEM              []byte
	Assertion              string
}

// ConsumeVerifier returns the PKCE verifier ONCE and zeros the
// in-memory copy. Subsequent calls return the empty string. Callers
// that need to inspect verifier state (e.g. validate non-empty before
// committing to a Hydra-published slot) must use HasVerifier — reading
// the secret implies consuming it.
func (b *AgentBootstrap) ConsumeVerifier() string {
	if b == nil {
		return ""
	}
	v := b.verifier
	b.verifier = ""
	return v
}

// HasVerifier reports whether the verifier is still populated. Used by
// validate() so the caller can confirm the bootstrap is complete
// without burning the single-use secret.
func (b *AgentBootstrap) HasVerifier() bool {
	return b != nil && b.verifier != ""
}

// String redacts every field so AgentBootstrap can never accidentally
// leak the per-agent private key, the PKCE verifier (a bearer secret
// for the CP slot), or the Hydra assertion JWT via fmt.Sprintf("%v",
// b) or zerolog.
func (*AgentBootstrap) String() string { return "AgentBootstrap{<redacted>}" }

// GoString redacts so fmt.Sprintf("%#v", b) (and any logger that uses
// Go-syntax representation) also does not leak Verifier / KeyPEM /
// Assertion.
func (*AgentBootstrap) GoString() string { return "AgentBootstrap{<redacted>}" }

// GenerateAgentBootstrap mints all material the CLI needs to announce
// + start one agent: a fresh 32-byte PKCE verifier, the matching S256
// challenge, the per-agent mTLS leaf cert + key signed by the CLI CA,
// the CP server-trust CA cert, and a Hydra client_assertion for the
// clawker-agent OAuth2 client. caCertPath/caKeyPath identify the CLI
// CA on disk (typically `consts.AuthCACertPath()` /
// `consts.AuthCAKeyPath()`); hydraTokenURL is the audience of the
// assertion (the CP's Hydra `/oauth2/token` endpoint as clawkerd will
// see it from inside the container).
//
// project + agent are the user-typed short identifiers (e.g. "myapp",
// "dev") — never the canonical "clawker.project.agent" form. The cert's
// CN is composed inside MintAgentCert via auth.CanonicalAgentCN so every
// CLI caller produces the same canonical shape and the agent handler's
// peer-cert CN cross-check has a single equality to enforce.
//
// The signature uses the typed auth.ProjectSlug / auth.AgentName so
// the caller has gone through NewProjectSlug / NewAgentName at the CLI
// flag boundary — a raw `string` cannot reach this function.
func GenerateAgentBootstrap(caCertPath, caKeyPath string, project auth.ProjectSlug, agent auth.AgentName, hydraTokenURL string, signingKey *ecdsa.PrivateKey) (*AgentBootstrap, error) {
	if agent.IsZero() {
		return nil, fmt.Errorf("agent name required")
	}
	if signingKey == nil {
		return nil, fmt.Errorf("signing key required")
	}

	verifier, challenge, err := newPKCEPair()
	if err != nil {
		return nil, fmt.Errorf("pkce: %w", err)
	}

	cert, err := auth.MintAgentCert(caCertPath, caKeyPath, project, agent)
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
		verifier:               verifier,
		Challenge:              challenge,
		Method:                 consts.ChallengeMethodS256,
		CertPEM:                cert.CertPEM,
		KeyPEM:                 cert.KeyPEM,
		ExpectedCertThumbprint: cert.Thumbprint,
		CACertPEM:              caPEM,
		Assertion:              assertion,
	}, nil
}

// AnnounceAgent reserves a registration slot on the CP for the given
// container before docker start. CP slot stores the (project, agent)
// composite identity, the Docker container ID CLI just received, the
// cert thumbprint CLI minted, and the PKCE challenge. clawkerd consumes
// the matching verifier at Connect; if the slot expires
// (consts.AgentSlotTTL elapses) clawkerd's Connect fails fail-closed.
//
// project + agent travel as separate wire fields (not assembled into a
// canonical name) so the CP composite slot key can include both without
// re-parsing on the server side. The thumbprint is sent over the wire as
// lowercase hex because the proto field is a free-form `string` —
// internally we keep the byte-array form to avoid carrying around a
// redundantly-encoded representation.
//
// Typed (auth.ProjectSlug, auth.AgentName) inputs — the constructors
// already validated charset/length/no-canonical-prefix at the CLI flag
// boundary, so this function trusts the values it receives.
// AnnounceAgent reserves a CLI-attestation slot on the CP for the
// given container_id. Called by every container start path
// (run/create/start/restart/loop) via BootstrapServicesPreStart, so
// the slot's existence on the CP side is the data point that says
// "this start was initiated by the clawker CLI, not raw `docker
// start`". Identity verification (thumbprint, CN) flows separately
// through agentregistry when CP dials the running clawkerd.
func AnnounceAgent(ctx context.Context, admin adminv1.AdminServiceClient, containerID string) error {
	if containerID == "" {
		return fmt.Errorf("announce agent: container id required")
	}
	if _, err := admin.AnnounceAgent(ctx, &adminv1.AnnounceAgentRequest{
		ContainerId: containerID,
	}); err != nil {
		return fmt.Errorf("announce agent (container %s): %w", containerID, err)
	}
	return nil
}

// InstallAgentBootstrapOptions bundles the inputs InstallAgentBootstrap
// needs at container CREATE time — fresh cert+key minted per container,
// CopyToContainered into the writable layer, and the registry row
// landed on the host-side sqlite DB.
type InstallAgentBootstrapOptions struct {
	// Project + Agent are the typed (project, agent_name) identity
	// validated upstream at the CLI flag boundary. Used to compose the
	// canonical CN in the leaf cert and the registry row's identity
	// fields.
	Project auth.ProjectSlug
	Agent   auth.AgentName
	// ContainerID is the ID returned by client.ContainerCreate.
	ContainerID string
	// HydraTokenAudience is the `aud` claim in the Hydra
	// client_assertion. Resolved by callers via
	// hydraTokenAudienceFromPort(cfg.Settings().ControlPlane.HydraPublicPort).
	HydraTokenAudience string
	// CopyToContainer streams the bootstrap tar into ContainerID's
	// writable layer at consts.BootstrapDir.
	CopyToContainer CopyToContainerFn
	// RegistryDBPath is the host-fs path to the agentregistry sqlite
	// DB. The CLI is the sole authoritative writer; the row is
	// inserted before this function returns so AnnounceAgent / agentdial
	// can read it on the next start.
	RegistryDBPath string
	// Logger receives a single info line on success and any audit
	// breadcrumbs from agentregistry. Required.
	Logger *logger.Logger
}

// InstallAgentBootstrapMaterial is step 1+2 of the create-time agent
// install: mint PKCE/cert/key/CA/assertion (GenerateAgentBootstrap) and
// tar them into the container's writable layer at consts.BootstrapDir
// (WriteAgentBootstrapToContainer). Returns the bootstrap struct so the
// caller can hand it to RegisterAgentInRegistry once the container is
// fully ready (i.e. after any post-init injection has succeeded).
//
// No DB I/O. The registry row is intentionally NOT written here so the
// caller can sequence "deliver material → run post-init → write registry
// row" with the row signifying "container fully ready" instead of merely
// "bootstrapped, may not be ready". Any failure here is recovered by the
// caller's ContainerRemove without orphaning a registry row.
func InstallAgentBootstrapMaterial(ctx context.Context, caCertPath, caKeyPath string, signingKey *ecdsa.PrivateKey, opts InstallAgentBootstrapOptions) (*AgentBootstrap, error) {
	if opts.ContainerID == "" {
		return nil, fmt.Errorf("install agent bootstrap material: container id required")
	}
	if opts.CopyToContainer == nil {
		return nil, fmt.Errorf("install agent bootstrap material: copy-to-container fn required")
	}

	bootstrap, err := GenerateAgentBootstrap(caCertPath, caKeyPath, opts.Project, opts.Agent, opts.HydraTokenAudience, signingKey)
	if err != nil {
		return nil, fmt.Errorf("install agent bootstrap material: generate: %w", err)
	}

	if err := WriteAgentBootstrapToContainer(ctx, opts.ContainerID, opts.CopyToContainer, bootstrap); err != nil {
		return nil, fmt.Errorf("install agent bootstrap material: write: %w", err)
	}
	return bootstrap, nil
}

// RegisterAgentInRegistry is step 3 of the create-time agent install:
// open the host-side agentregistry sqlite DB and INSERT the (thumbprint,
// container_id, agent_name, project, canonical_cn) row. This is the
// LAST step of container creation so the registry row signifies "container
// fully ready". The caller is responsible for ContainerRemove on failure;
// the dockerevents reaper would otherwise resurface the orphaned container.
func RegisterAgentInRegistry(ctx context.Context, opts InstallAgentBootstrapOptions, bootstrap *AgentBootstrap) error {
	if bootstrap == nil {
		return fmt.Errorf("register agent in registry: bootstrap is nil")
	}
	if opts.ContainerID == "" {
		return fmt.Errorf("register agent in registry: container id required")
	}
	if opts.RegistryDBPath == "" {
		return fmt.Errorf("register agent in registry: registry db path required")
	}
	log := opts.Logger
	if log == nil {
		log = logger.Nop()
	}

	reg, err := agentregistry.NewSQLiteWriter(opts.RegistryDBPath, log)
	if err != nil {
		return fmt.Errorf("register agent in registry: open registry: %w", err)
	}
	closer, _ := reg.(interface{ Close() error })
	defer func() {
		if closer != nil {
			_ = closer.Close()
		}
	}()

	now := time.Now()
	if err := reg.Add(agentregistry.Entry{
		AgentName:    opts.Agent.String(),
		Project:      opts.Project.String(),
		ContainerID:  opts.ContainerID,
		Thumbprint:   bootstrap.ExpectedCertThumbprint,
		RegisteredAt: now,
		LastSeen:     now,
	}); err != nil {
		return fmt.Errorf("register agent in registry: %w", err)
	}

	log.Info().
		Str("container_id", opts.ContainerID).
		Str("agent", opts.Agent.String()).
		Str("project", opts.Project.String()).
		Msg("agent registry row written")
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
	case b.Method != consts.ChallengeMethodS256:
		return fmt.Errorf("bootstrap challenge method must be %s, got %q", consts.ChallengeMethodS256, b.Method)
	case b.Challenge == "":
		return fmt.Errorf("bootstrap challenge is empty")
	case !b.HasVerifier():
		return fmt.Errorf("bootstrap verifier is empty (or already consumed)")
	case b.ExpectedCertThumbprint == [sha256.Size]byte{}:
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

	// ConsumeVerifier zeros the in-memory copy after returning. This is
	// the ONE legitimate read of the verifier in the host process —
	// after the tar lands inside the container, clawkerd consumes the
	// verifier from disk at Connect and the host has no further need
	// for it. Future code that tries to read the secret again gets the
	// empty string (and validate() catches it).
	files := []struct {
		name string
		body []byte
	}{
		{consts.BootstrapCertFile, b.CertPEM},
		{consts.BootstrapKeyFile, b.KeyPEM},
		{consts.BootstrapCAFile, b.CACertPEM},
		{consts.BootstrapAssertionFile, []byte(b.Assertion)},
		{consts.BootstrapVerifierFile, []byte(b.ConsumeVerifier())},
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
