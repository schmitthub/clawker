package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	cpbootmocks "github.com/schmitthub/clawker/internal/controlplane/cpboot/mocks"
	controlplanemocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// extractTarMember reads a tar stream and returns the contents of the
// first entry whose name matches `name` (last path component). Used by
// audience-claim test to fish out assertion.jwt from the bootstrap tar.
func extractTarMember(r io.Reader, name string) ([]byte, error) {
	// CopyToContainer requires a Reader, but our tar may have been
	// produced into a bytes.Buffer; either way Reader is fine.
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if path.Base(hdr.Name) == name {
			// Bound the read so a malformed/oversized tar entry (e.g. a
			// zip-bomb-style member) can't blow up the test process.
			// 1 MiB is generous — bootstrap files (PEM + JWT + verifier)
			// are all sub-kB.
			var buf bytes.Buffer
			if _, err := io.CopyN(&buf, tr, 1<<20); err != nil && err != io.EOF {
				return nil, err
			}
			return buf.Bytes(), nil
		}
	}
	return nil, errors.New("tar member not found: " + name)
}

// noopCPManager returns a CP manager mock whose EnsureRunning is a no-op.
// Bootstrap tests need it because CP is unconditionally brought up in
// BootstrapServicesPreStart (CP is core infra, not a firewall feature).
func noopCPManager() func() cpboot.Manager {
	m := &cpbootmocks.ManagerMock{
		EnsureRunningFunc: func(context.Context) error { return nil },
	}
	return func() cpboot.Manager { return m }
}

func TestBootstrapServices_ErrorHandlingAndNilSafety(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	tests := []struct {
		name    string
		cmdOpts CommandOpts
		wantErr string
	}{
		{
			name:    "nil config provider",
			cmdOpts: CommandOpts{},
			wantErr: "bootstrapping services: config provider is nil",
		},
		{
			name: "config provider returns error",
			cmdOpts: CommandOpts{
				Config: func() (config.Config, error) { return nil, errBoom },
			},
			wantErr: "bootstrapping services: loading config: boom",
		},
		{
			name: "config provider returns nil config",
			cmdOpts: CommandOpts{
				Config: func() (config.Config, error) { return nil, nil },
			},
			wantErr: "bootstrapping services: config is nil",
		},
		{
			name: "logger init error is wrapped",
			cmdOpts: CommandOpts{
				Config:       testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
				Logger:       func() (*logger.Logger, error) { return nil, errors.New("logger boom") },
				ControlPlane: noopCPManager(),
			},
			wantErr: "bootstrapping services: initializing logger: logger boom",
		},
		{
			name: "missing control plane manager is an error",
			cmdOpts: CommandOpts{
				Config: testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
			},
			wantErr: "bootstrapping services: no control plane manager provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := BootstrapServicesPreStart(context.Background(), "ctr", tt.cmdOpts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestBootstrapServices_MissingOptionalProvidersAreSkipped(t *testing.T) {
	t.Parallel()

	err := BootstrapServicesPreStart(context.Background(), "ctr", CommandOpts{
		Config:       testRuntimeConfig("", `firewall: { enable: false }`),
		ControlPlane: noopCPManager(),
	})
	if err != nil {
		t.Fatalf("expected nil error when optional providers are omitted, got %v", err)
	}
}

func TestBootstrapServices_NilProjectAndSettingsDoNotPanic(t *testing.T) {
	t.Parallel()

	cfg := configmocks.NewBlankConfig()
	cfg.ProjectFunc = func() *config.Project { return nil }
	cfg.SettingsFunc = func() *config.Settings { return nil }

	err := BootstrapServicesPreStart(context.Background(), "ctr", CommandOpts{
		Config:       func() (config.Config, error) { return cfg, nil },
		ControlPlane: noopCPManager(),
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestContainerStart_ClientValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil client provider", func(t *testing.T) {
		t.Parallel()

		_, err := ContainerStart(context.Background(), CommandOpts{
			Config:       testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
			ControlPlane: noopCPManager(),
		}, docker.ContainerStartOptions{ContainerID: "ctr"})
		if err == nil || !strings.Contains(err.Error(), "starting container: docker client provider is nil") {
			t.Fatalf("expected nil client provider error, got %v", err)
		}
	})

	t.Run("client provider returns nil client", func(t *testing.T) {
		t.Parallel()

		_, err := ContainerStart(context.Background(), CommandOpts{
			Config:       testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
			Client:       func(context.Context) (*docker.Client, error) { return nil, nil },
			ControlPlane: noopCPManager(),
		}, docker.ContainerStartOptions{ContainerID: "ctr"})
		if err == nil || !strings.Contains(err.Error(), "starting container: docker client is nil") {
			t.Fatalf("expected nil client error, got %v", err)
		}
	})
}

func testRuntimeConfig(projectYAML, settingsYAML string) func() (config.Config, error) {
	return func() (config.Config, error) {
		return configmocks.NewFromString(projectYAML, settingsYAML), nil
	}
}

// --- prepareAgentBootstrap tests ---

func TestPrepareAgentBootstrap_HappyPath(t *testing.T) {
	// testenv + EnsureAuthMaterial set up the CA + signing key on disk.
	// Without them GenerateAgentBootstrap would fail before reaching
	// AnnounceAgent. The helper resolves CA paths internally via
	// consts.AuthCACertPath() / AuthCAKeyPath(), so the side effect is
	// what we want here — discard the returned values.
	setupAuthEnv(t)

	var (
		announced *adminv1.AnnounceAgentRequest
		copyDest  string
	)
	admin := &controlplanemocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, in *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			announced = in
			// Order check: AnnounceAgent must run BEFORE WriteAgentBootstrap.
			// If copyFn already fired, copyDest would be non-empty here —
			// fail loudly so a future refactor that swaps the call order is
			// caught immediately.
			if copyDest != "" {
				t.Fatal("AnnounceAgent must run BEFORE Write")
			}
			return &adminv1.AnnounceAgentResult{ExpiresAtUnix: 99}, nil
		},
	}
	copyFn := func(_ context.Context, _, dest string, _ io.Reader) error {
		copyDest = dest
		return nil
	}

	cmdOpts := CommandOpts{
		Config:      testRuntimeConfig("", ""),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) { return admin, nil },
		AgentName:   "bravo",
		Project:     "alpha",
	}
	err := prepareAgentBootstrap(context.Background(), cmdOpts, "ctr-id-123", copyFn)
	require.NoError(t, err)

	// AnnounceAgent saw the right request shape — short (project, agent)
	// pair (NOT the canonical form), container ID round-trip through the
	// wire. Composing the canonical CN is the CP's job.
	require.NotNil(t, announced)
	assert.Equal(t, "bravo", announced.AgentName)
	assert.Equal(t, "alpha", announced.Project)
	assert.Equal(t, "ctr-id-123", announced.ContainerId)
	assert.Equal(t, "S256", announced.CodeChallengeMethod)
	assert.NotEmpty(t, announced.CodeChallenge)
	assert.NotEmpty(t, announced.ExpectedCertThumbprint)

	// Write landed at the parent of consts.BootstrapDir — the tar's
	// leading directory entry creates BootstrapDir itself with the
	// right perms inside the container. Use path.Dir(consts.BootstrapDir)
	// so a future BootstrapDir relocation doesn't silently break this
	// assertion at the wrong layer.
	assert.Equal(t, path.Dir(consts.BootstrapDir), copyDest)
}

func TestPrepareAgentBootstrap_AnnounceErrorBlocksWrite(t *testing.T) {
	// If AnnounceAgent fails, the bootstrap MUST NOT be written into
	// the container — partial states (slot announced but bootstrap
	// missing, or bootstrap present but slot absent) are unreachable.
	setupAuthEnv(t)

	wantErr := errors.New("slot already exists")
	admin := &controlplanemocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, _ *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			return nil, wantErr
		},
	}
	copyFn := func(_ context.Context, _, _ string, _ io.Reader) error {
		t.Fatal("copyFn must NOT run when AnnounceAgent fails")
		return nil
	}

	cmdOpts := CommandOpts{
		Config:      testRuntimeConfig("", ""),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) { return admin, nil },
		AgentName:   "x",
		Project:     "p",
	}
	err := prepareAgentBootstrap(context.Background(), cmdOpts, "ctr", copyFn)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestPrepareAgentBootstrap_WriteErrorPropagates(t *testing.T) {
	setupAuthEnv(t)

	admin := &controlplanemocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, _ *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			return &adminv1.AnnounceAgentResult{}, nil
		},
	}
	wantErr := errors.New("docker copy failed")
	copyFn := func(_ context.Context, _, _ string, _ io.Reader) error {
		return wantErr
	}

	cmdOpts := CommandOpts{
		Config:      testRuntimeConfig("", ""),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) { return admin, nil },
		AgentName:   "x",
		Project:     "p",
	}
	err := prepareAgentBootstrap(context.Background(), cmdOpts, "ctr", copyFn)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestPrepareAgentBootstrap_NilAdminClientFails(t *testing.T) {
	setupAuthEnv(t)
	cmdOpts := CommandOpts{
		Config:    testRuntimeConfig("", ""),
		AgentName: "x",
		Project:   "p",
		// AdminClient intentionally nil.
	}
	err := prepareAgentBootstrap(context.Background(), cmdOpts, "ctr", func(_ context.Context, _, _ string, _ io.Reader) error {
		t.Fatal("copyFn must NOT run when admin client is nil")
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin client provider is nil")
}

// TestHydraTokenAudienceFromPort_PinnedTo127001 locks the canonical
// `aud` claim format. Hot-fix fd475fb1 pinned this format after a
// regression where the audience matched the docker-DNS hostname
// (`https://clawker-controlplane:<port>/...`) and Hydra rejected the
// assertion at token exchange time. A regression that re-derives the
// audience from the network endpoint clawkerd dials would compile and
// pass every other unit test until manual UAT, so the test exists.
func TestHydraTokenAudienceFromPort_PinnedTo127001(t *testing.T) {
	assert.Equal(t, "https://127.0.0.1:4444/oauth2/token", hydraTokenAudienceFromPort(4444))
	// Different ports thread through unchanged — the constant is the
	// 127.0.0.1 host + /oauth2/token path, not the port itself.
	assert.Equal(t, "https://127.0.0.1:7777/oauth2/token", hydraTokenAudienceFromPort(7777))
}

// TestPrepareAgentBootstrap_AssertionAudienceUsesCanonicalFormat
// decodes the assertion JWT generated for the agent and asserts the
// `aud` claim matches Hydra's canonical 127.0.0.1 form. Guards against
// a refactor that bypasses hydraTokenAudienceFromPort and signs with
// the docker-DNS hostname instead.
func TestPrepareAgentBootstrap_AssertionAudienceUsesCanonicalFormat(t *testing.T) {
	setupAuthEnv(t)

	var capturedAssertion string
	admin := &controlplanemocks.AdminServiceClientMock{
		AnnounceAgentFunc: func(_ context.Context, in *adminv1.AnnounceAgentRequest, _ ...grpc.CallOption) (*adminv1.AnnounceAgentResult, error) {
			// The assertion isn't on AnnounceAgentRequest — the CLI
			// signs a fresh one each time inside GenerateAgentBootstrap.
			// To get at it we tap the bootstrap via the copyFn below.
			_ = in
			return &adminv1.AnnounceAgentResult{}, nil
		},
	}
	copyFn := func(_ context.Context, _, _ string, payload io.Reader) error {
		// Walk the tar payload and find assertion.jwt.
		assertion, err := extractTarMember(payload, "assertion.jwt")
		if err != nil {
			return err
		}
		capturedAssertion = string(assertion)
		return nil
	}

	cmdOpts := CommandOpts{
		Config:      testRuntimeConfig("", "control_plane: { hydra_public_port: 4444 }"),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) { return admin, nil },
		AgentName:   "alpha",
		Project:     "myapp",
	}
	require.NoError(t, prepareAgentBootstrap(context.Background(), cmdOpts, "ctr", copyFn))
	require.NotEmpty(t, capturedAssertion)

	// Decode the JWT body and assert aud claim shape.
	parts := strings.Split(capturedAssertion, ".")
	require.Len(t, parts, 3, "assertion must be a 3-segment JWS")
	bodyJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims struct {
		Aud any `json:"aud"`
	}
	require.NoError(t, json.Unmarshal(bodyJSON, &claims))
	// `aud` may be a string or []string per RFC 7519; accept either.
	switch a := claims.Aud.(type) {
	case string:
		assert.Equal(t, "https://127.0.0.1:4444/oauth2/token", a)
	case []any:
		require.Len(t, a, 1)
		assert.Equal(t, "https://127.0.0.1:4444/oauth2/token", a[0])
	default:
		t.Fatalf("unexpected aud type %T", claims.Aud)
	}
}
