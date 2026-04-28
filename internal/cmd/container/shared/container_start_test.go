package shared

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	cpbootmocks "github.com/schmitthub/clawker/internal/controlplane/cpboot/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

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

// prepareAgentBootstrap was deleted when material delivery moved to
// CreateContainer (and the AnnounceAgent call moved to
// BootstrapServicesPreStart). The bootstrap mint + tar + registry
// write is now exercised by InstallAgentBootstrap; AnnounceAgent's
// minimal wire shape is exercised in agent_bootstrap_test.go.
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
