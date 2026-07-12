package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	mobyClient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/manager"
	cpbootmocks "github.com/schmitthub/clawker/controlplane/manager/mocks"
	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	mocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// okClientProvider returns a Client provider backed by a fake whose
// CopyToContainer succeeds. PreStart unconditionally delivers the pre_run
// hook to the container, so a working docker client is now required.
func okClientProvider(t *testing.T) func(context.Context) (*docker.Client, error) {
	t.Helper()
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupCopyToContainer()
	return func(context.Context) (*docker.Client, error) { return fake.Client, nil }
}

// noopCPManager returns a CP manager mock whose EnsureRunning is a no-op.
// Bootstrap tests need it because CP is unconditionally brought up in
// BootstrapServicesPreStart (CP is core infra, not a firewall feature).
func noopCPManager() func() manager.Manager {
	m := &cpbootmocks.ManagerMock{
		EnsureRunningFunc: func(context.Context) error { return nil },
	}
	return func() manager.Manager { return m }
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
				Config: testRuntimeConfig(
					`security: { enable_host_proxy: false }`,
					`firewall: { enable: false }`,
				),
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
		Client:       okClientProvider(t),
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
		Client:       okClientProvider(t),
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// TestBootstrapServices_PreRunDelivery proves the every-start pre_run
// contract: the hook script is always copied to the container (user body
// when set, no-op wrapper when unset so a removed hook overwrites stale
// content), and a copy failure aborts the start.
func TestBootstrapServices_PreRunDelivery(t *testing.T) {
	t.Parallel()

	t.Run("delivers when pre_run set", func(t *testing.T) {
		t.Parallel()
		fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
		fake.SetupCopyToContainer()
		err := BootstrapServicesPreStart(context.Background(), "ctr", CommandOpts{
			Config:       testRuntimeConfig(`agent: { pre_run: "npm install" }`, `firewall: { enable: false }`),
			ControlPlane: noopCPManager(),
			Client:       func(context.Context) (*docker.Client, error) { return fake.Client, nil },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fake.AssertCalledN(t, "CopyToContainer", 1)
	})

	t.Run("delivers no-op when pre_run unset", func(t *testing.T) {
		t.Parallel()
		fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
		fake.SetupCopyToContainer()
		err := BootstrapServicesPreStart(context.Background(), "ctr", CommandOpts{
			Config:       testRuntimeConfig("", `firewall: { enable: false }`),
			ControlPlane: noopCPManager(),
			Client:       func(context.Context) (*docker.Client, error) { return fake.Client, nil },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fake.AssertCalledN(t, "CopyToContainer", 1)
	})

	t.Run("copy failure aborts the start", func(t *testing.T) {
		t.Parallel()
		fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
		fake.SetupCopyToContainerError(errors.New("copy boom"))
		err := BootstrapServicesPreStart(context.Background(), "ctr", CommandOpts{
			Config:       testRuntimeConfig(`agent: { pre_run: "x" }`, `firewall: { enable: false }`),
			ControlPlane: noopCPManager(),
			Client:       func(context.Context) (*docker.Client, error) { return fake.Client, nil },
		})
		if err == nil || !strings.Contains(err.Error(), "injecting pre-run script") {
			t.Fatalf("expected pre-run injection error, got %v", err)
		}
	})
}

func TestContainerStart_ClientValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil client provider", func(t *testing.T) {
		t.Parallel()

		_, err := ContainerStart(context.Background(), CommandOpts{
			Config:       testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
			ControlPlane: noopCPManager(),
		}, docker.ContainerStartOptions{ContainerID: "ctr"})
		// ContainerStart resolves the docker client BEFORE pre-start (the
		// reap-on-failed-start path needs it), so its own guard fires first.
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

func TestReapFailedStart(t *testing.T) {
	t.Parallel()

	startErr := errors.New("pre-start boom")
	inspectBoom := errors.New("inspect boom")
	removeBoom := errors.New("remove boom")

	tests := []struct {
		name       string
		hostConfig *container.HostConfig
		state      *container.State
		inspectErr error
		// goneAfterCheck makes the inspect that follows whail's managed check
		// return NotFound — the container vanished between the two calls.
		goneAfterCheck bool
		removeErr      error
		wantRemoved    bool
		wantInErr      string
		// wantBare asserts the startErr is returned verbatim — no cleanup
		// wrap, no reap notice.
		wantBare bool
		// wantWrappedErr asserts a secondary cleanup error survives the wrap.
		wantWrappedErr error
	}{
		{
			name:        "auto-remove and not running is reaped",
			hostConfig:  &container.HostConfig{AutoRemove: true},
			state:       &container.State{Running: false},
			wantRemoved: true,
			wantInErr:   ReapedNotice,
		},
		{
			name:       "non-auto-remove container is left untouched",
			hostConfig: &container.HostConfig{AutoRemove: false},
			state:      &container.State{Running: false},
			wantBare:   true,
		},
		{
			name:       "running container is never reaped",
			hostConfig: &container.HostConfig{AutoRemove: true},
			state:      &container.State{Running: true},
			wantBare:   true,
		},
		{
			name:       "nil state is unknown, never reaped",
			hostConfig: &container.HostConfig{AutoRemove: true},
			state:      nil,
			wantBare:   true,
		},
		{
			name:       "nil host config is left untouched",
			hostConfig: nil,
			state:      &container.State{Running: false},
			wantBare:   true,
		},
		{
			name:           "inspect failure surfaces both errors, no removal",
			hostConfig:     &container.HostConfig{AutoRemove: true},
			state:          &container.State{Running: false},
			inspectErr:     inspectBoom,
			wantWrappedErr: inspectBoom,
			wantInErr:      "inspecting container for cleanup failed",
		},
		{
			// A NotFound during whail's managed check collapses to
			// ErrNotManaged — the common "daemon already removed it" race.
			name:       "inspect NotFound is benign — container already gone",
			hostConfig: &container.HostConfig{AutoRemove: true},
			state:      &container.State{Running: false},
			inspectErr: fmt.Errorf("no such container: %w", cerrdefs.ErrNotFound),
			wantBare:   true,
		},
		{
			// Vanish between the managed check and the inspect call — the
			// daemon's own NotFound escapes wrapped, not collapsed.
			name:           "inspect NotFound after managed check is benign",
			hostConfig:     &container.HostConfig{AutoRemove: true},
			state:          &container.State{Running: false},
			goneAfterCheck: true,
			wantBare:       true,
		},
		{
			name:           "remove failure surfaces both errors",
			hostConfig:     &container.HostConfig{AutoRemove: true},
			state:          &container.State{Running: false},
			removeErr:      removeBoom,
			wantRemoved:    true,
			wantWrappedErr: removeBoom,
			wantInErr:      "could not be removed",
		},
		{
			name:        "remove NotFound counts as removed",
			hostConfig:  &container.HostConfig{AutoRemove: true},
			state:       &container.State{Running: false},
			removeErr:   fmt.Errorf("gone: %w", cerrdefs.ErrNotFound),
			wantRemoved: true,
			wantInErr:   ReapedNotice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
			inspectCalls := 0
			fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyClient.ContainerInspectOptions) (mobyClient.ContainerInspectResult, error) {
				if tt.inspectErr != nil {
					return mobyClient.ContainerInspectResult{}, tt.inspectErr
				}
				inspectCalls++
				if tt.goneAfterCheck && inspectCalls > 1 {
					return mobyClient.ContainerInspectResult{}, fmt.Errorf(
						"no such container: %w",
						cerrdefs.ErrNotFound,
					)
				}
				return mobyClient.ContainerInspectResult{
					Container: container.InspectResponse{
						ID: id,
						Config: &container.Config{
							Labels: map[string]string{
								fake.Cfg.LabelManaged(): fake.Cfg.ManagedLabelValue(),
							},
						},
						HostConfig: tt.hostConfig,
						State:      tt.state,
					},
				}, nil
			}
			fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyClient.ContainerRemoveOptions) (mobyClient.ContainerRemoveResult, error) {
				return mobyClient.ContainerRemoveResult{}, tt.removeErr
			}

			err := ReapFailedStart(fake.Client, "ctr", startErr)

			// The original start error must always survive.
			if !errors.Is(err, startErr) {
				t.Fatalf("returned error lost the original start error: %v", err)
			}
			if tt.wantBare && err != startErr {
				t.Fatalf("expected bare startErr, got wrapped: %v", err)
			}
			// Secondary cleanup errors are wrapped with %w too.
			if tt.wantWrappedErr != nil && !errors.Is(err, tt.wantWrappedErr) {
				t.Fatalf("returned error lost the cleanup error: %v", err)
			}
			if tt.wantInErr != "" && !strings.Contains(err.Error(), tt.wantInErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantInErr, err)
			}
			if tt.wantRemoved {
				fake.AssertCalled(t, "ContainerRemove")
			} else {
				fake.AssertNotCalled(t, "ContainerRemove")
			}
		})
	}
}

// TestContainerStart_PreStartFailureReapsAutoRemove pins the integration:
// a pre-start bootstrap failure inside ContainerStart removes an
// AutoRemove container so its name is freed for a re-run.
func TestContainerStart_PreStartFailureReapsAutoRemove(t *testing.T) {
	t.Parallel()

	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerInspectReapState(true, false)
	fake.SetupContainerRemove()

	// No ControlPlane manager → BootstrapServicesPreStart fails after the
	// client is resolved, exercising the reap path.
	res, err := ContainerStart(context.Background(), CommandOpts{
		Config: testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
		Client: func(context.Context) (*docker.Client, error) { return fake.Client, nil },
	}, docker.ContainerStartOptions{ContainerID: "ctr"})

	if err == nil || !strings.Contains(err.Error(), "pre-start bootstrapping failed") {
		t.Fatalf("expected pre-start failure, got %v", err)
	}
	if !strings.Contains(err.Error(), ReapedNotice) {
		t.Fatalf("expected reap notice in error, got %v", err)
	}
	// The Docker start call was never reached — the result must be nil, never
	// a fabricated SDK value.
	if res != nil {
		t.Fatalf("expected nil result when start was never reached, got %v", res)
	}
	fake.AssertCalled(t, "ContainerRemove")
	fake.AssertNotCalled(t, "ContainerStart")
}

// TestContainerStart_StartFailureReapsAutoRemove pins the second reap hook:
// pre-start succeeds, the Docker start call itself fails, and the AutoRemove
// container is removed so its name is freed.
func TestContainerStart_StartFailureReapsAutoRemove(t *testing.T) {
	t.Parallel()

	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerInspectReapState(true, false)
	fake.SetupContainerRemove()
	fake.SetupCopyToContainer() // pre-start delivers the pre_run hook
	fake.FakeAPI.ContainerStartFn = func(_ context.Context, _ string, _ mobyClient.ContainerStartOptions) (mobyClient.ContainerStartResult, error) {
		return mobyClient.ContainerStartResult{}, errors.New("start boom")
	}

	res, err := ContainerStart(context.Background(), CommandOpts{
		Config:       testRuntimeConfig(`security: { enable_host_proxy: false }`, `firewall: { enable: false }`),
		Client:       func(context.Context) (*docker.Client, error) { return fake.Client, nil },
		ControlPlane: noopCPManager(),
	}, docker.ContainerStartOptions{ContainerID: "ctr"})

	if err == nil || !strings.Contains(err.Error(), "start boom") {
		t.Fatalf("expected start failure, got %v", err)
	}
	if !strings.Contains(err.Error(), ReapedNotice) {
		t.Fatalf("expected reap notice in error, got %v", err)
	}
	// The Docker start call ran — the SDK result is passed through verbatim.
	if res == nil {
		t.Fatal("expected non-nil result once the start call was reached")
	}
	fake.AssertCalled(t, "ContainerStart")
	fake.AssertCalled(t, "ContainerRemove")
}

func testRuntimeConfig(projectYAML, settingsYAML string) func() (config.Config, error) {
	return func() (config.Config, error) {
		return configmocks.NewFromString(projectYAML, settingsYAML), nil
	}
}

// prepareAgentBootstrap was deleted when material delivery moved to
// CreateContainer. The bootstrap mint + tar (cert/key/ca/assertion)
// is now exercised by InstallAgentBootstrapMaterial; see
// agent_bootstrap_test.go. CP is the sole writer of the agentregistry
// row — captured at AgentService.Register handler entry from the
// live mTLS peer's cert thumbprint.
// (TestHydraTokenAudienceFromPort_PinnedTo127001 removed in test
// cleanup — the function under test is `fmt.Sprintf("https://127.0.0.1:%d/oauth2/token", port)`,
// so the test was exercising Sprintf rather than any clawker logic.
// The regression it guarded — audience mismatching what Hydra
// expects at token-exchange time — is only catchable end-to-end
// against a real Hydra; that path lives in test/e2e and the manual
// UAT flow.)

// assertHarnessResolvable enforces the stale-harness-label gate at container
// start: a qualified harness label resolves only while its bundle's source is
// declared — a cached bundle whose `bundles:` entry was deleted must refuse the
// start (the container would otherwise run against an egress floor weaker than
// it was built for). A bare floor harness always resolves.
func TestAssertHarnessResolvable_DeclarationGated(t *testing.T) {
	testenv.New(t)
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	verRoot := filepath.Join(cacheRoot, "acme", "tools", "1.0.0")
	require.NoError(t, os.MkdirAll(filepath.Join(verRoot, ".clawker-bundle"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(verRoot, ".clawker-bundle", "bundle.yaml"),
		[]byte("namespace: acme\nname: tools\nversion: 1.0.0\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(verRoot, "harnesses", "claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(verRoot, "harnesses", "claude", "harness.yaml"),
		[]byte("version:\n  resolver: none\nstacks: []\n"), 0o644))
	const url = "https://example.com/acme/tools.git"
	require.NoError(t, os.WriteFile(
		filepath.Join(cacheRoot, "acme", "tools", "source.yaml"),
		[]byte(
			"url: "+url+"\nref: v1\nversions:\n  \"1.0.0\":\n    sha: \"\"\n    fetched_at: 2026-01-01T00:00:00Z\n",
		),
		0o644,
	))

	undeclared := configmocks.NewBlankConfig()
	resolveErr := assertHarnessResolvable(undeclared, "acme.tools.claude")
	require.Error(t, resolveErr)
	require.ErrorIs(t, resolveErr, bundle.ErrNotCached)
	assert.Contains(t, resolveErr.Error(), "no longer resolves")
	assert.Contains(t, resolveErr.Error(), "clawker bundle install")

	declared := configmocks.NewBlankConfig()
	declared.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{
			{
				Source: config.BundleSource{URL: url, Ref: "v1", SHA: "", Path: "", AutoUpdate: false},
				File:   "clawker.yaml",
			},
		}
	}
	require.NoError(t, assertHarnessResolvable(declared, "acme.tools.claude"))

	require.NoError(t, assertHarnessResolvable(undeclared, "claude"),
		"a bare floor harness resolves regardless of bundle declarations")
}
