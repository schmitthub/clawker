package shared

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	cpbootmocks "github.com/schmitthub/clawker/internal/controlplane/cpboot/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	mocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
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
func noopCPManager() func() cpboot.Manager {
	m := &cpbootmocks.ManagerMock{
		EnsureRunningFunc: func(context.Context) (time.Duration, error) { return 0, nil },
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
		// PreStart now needs the docker client to deliver the pre_run hook, so
		// the nil-provider guard fires there first (before ContainerStart's own).
		if err == nil || !strings.Contains(err.Error(), "bootstrapping services: docker client provider is nil") {
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
		if err == nil || !strings.Contains(err.Error(), "bootstrapping services: docker client is nil") {
			t.Fatalf("expected nil client error, got %v", err)
		}
	})
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
