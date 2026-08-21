package start

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/shlex"
	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cpmanager "github.com/schmitthub/clawker/controlplane/manager"
	cpmanagermocks "github.com/schmitthub/clawker/controlplane/manager/mocks"
	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
	socketbridgemocks "github.com/schmitthub/clawker/internal/socketbridge/mocks"
	"github.com/schmitthub/clawker/internal/testenv"
)

func approveGrantsStartOptions() StartOptions {
	var opts StartOptions
	opts.Containers = []string{"clawker.myapp.dev"}
	opts.ApproveGrants = true
	return opts
}

var _blankCfg = configmocks.NewBlankConfig()

func TestNewCmdStart(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		wantOpts   StartOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "single container",
			args: []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Containers: []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with agent flag",
			input: "--agent",
			args:  []string{"dev"},
			wantOpts: StartOptions{
				Agent:      true,
				Containers: []string{"dev"},
			},
		},
		{
			name: "multiple containers",
			args: []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			wantOpts: StartOptions{
				Containers: []string{"clawker.myapp.dev", "clawker.myapp.writer"},
			},
		},
		{
			name:  "with attach flag",
			input: "--attach",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:     true,
				Containers: []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with shorthand attach flag",
			input: "-a",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:     true,
				Containers: []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with interactive flag",
			input: "--interactive",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with shorthand interactive flag",
			input: "-i",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "with attach and interactive flags",
			input: "-a -i",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:      true,
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:  "combined flags shorthand",
			input: "-ai",
			args:  []string{"clawker.myapp.dev"},
			wantOpts: StartOptions{
				Attach:      true,
				Interactive: true,
				Containers:  []string{"clawker.myapp.dev"},
			},
		},
		{
			name:  "agent flag with multiple containers",
			input: "--agent",
			args:  []string{"dev", "writer"},
			wantOpts: StartOptions{
				Agent:      true,
				Containers: []string{"dev", "writer"},
			},
		},
		{
			name:       "approve grants",
			input:      "--approve-grants",
			args:       []string{"clawker.myapp.dev"},
			wantOpts:   approveGrantsStartOptions(),
			wantErr:    false,
			wantErrMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() (config.Config, error) {
					return configmocks.NewBlankConfig(), nil
				},
			}

			var gotOpts *StartOptions
			cmd := NewCmdStart(f, func(_ context.Context, opts *StartOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv := tt.args
			if tt.input != "" {
				parsed, err := shlex.Split(tt.input)
				require.NoError(t, err)
				argv = append(parsed, tt.args...)
			}

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.Attach, gotOpts.Attach)
			require.Equal(t, tt.wantOpts.Interactive, gotOpts.Interactive)
			require.Equal(t, tt.wantOpts.ApproveGrants, gotOpts.ApproveGrants)
			require.Equal(t, tt.wantOpts.Containers, gotOpts.Containers)
		})
	}
}

func TestCmdStart_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStart(f, nil)

	require.Equal(t, "start [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("attach"))
	require.NotNil(t, cmd.Flags().Lookup("interactive"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
}

// --- Tier 2: Cobra+Factory integration tests (non-attach path) ---

func testStartFactory(
	t *testing.T,
	fake *mocks.FakeClient,
) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	tio, in, out, errOut := iostreams.Test()

	return &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return configmocks.NewFromString(
				`security: { enable_host_proxy: false }`,
				`firewall: { enable: false }`,
			), nil
		},
		HostProxy: func() hostproxy.Service {
			return hostproxytest.NewMockManager()
		},
		ControlPlane: func(context.Context) (cpmanager.Manager, error) {
			return &cpmanagermocks.ManagerMock{ //nolint:exhaustruct // test double: only the methods this path exercises are programmed
				StartFunc: func(context.Context) error { return nil },
			}, nil
		},
	}, in, out, errOut
}

// setupContainerStart configures the fake for the non-attach container start path.
// The default FakeClient ContainerInspectFn handles IsContainerManaged checks.
func setupContainerStart(fake *mocks.FakeClient) {
	fake.SetupNetworkExists(_blankCfg.ClawkerNetwork(), true)
	fake.FakeAPI.NetworkConnectFn = func(_ context.Context, _ string, _ mobyclient.NetworkConnectOptions) (mobyclient.NetworkConnectResult, error) {
		return mobyclient.NetworkConnectResult{}, nil
	}
	fake.SetupContainerStart()
	fake.SetupCopyToContainer() // BootstrapServicesPreStart always delivers the pre_run hook
}

func TestStartRun_DockerConnectionError(t *testing.T) {
	tio, in, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() (config.Config, error) {
			return configmocks.NewBlankConfig(), nil
		},
	}

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"mycontainer"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestStartRun_Success(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	setupContainerStart(fake)

	f, in, out, errOut := testStartFactory(t, fake)
	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, out.String(), "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerStart")
}

func TestStartRun_SocketSourceUsesConfigPathSemantics(t *testing.T) {
	env := testenv.New(t)
	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit
	home, err := os.MkdirTemp(
		"/tmp",
		"clawker-socket-start-",
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(home))
	})
	t.Setenv("HOME", home)
	t.Setenv("CLAWKER_TEST_SOCKET_ROOT", "")

	projectRoot := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(projectRoot, 0o755))
	t.Chdir(projectRoot)
	env.WriteYAML(t, testenv.ProjectConfig, projectRoot, "security: { enable_host_proxy: false }\n")
	env.WriteYAML(t, testenv.Settings, "", "firewall: { enable: false }\n")

	const harnessName = "sockettest"
	harnessDir := filepath.Join(projectRoot, consts.DotClawkerDir, bundle.ComponentHarness.Dir(), harnessName)
	require.NoError(t, os.MkdirAll(harnessDir, 0o755))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(harnessDir, bundler.HarnessManifestFile), []byte(`version: { resolver: none }
sockets:
  - source: ${CLAWKER_TEST_SOCKET_ROOT:-~/.missing}/some.sock
    target: /tmp/some.sock
    purpose: Exercise host path semantics.
`), 0o600),
	)
	require.NoError(t, os.WriteFile(filepath.Join(harnessDir, bundler.HarnessTemplateFile), []byte(`{{define "cmd"}}
CMD ["sleep", "infinity"]
{{end}}
`), 0o600))

	fallbackSource := filepath.Join(home, ".missing", "some.sock")
	fallbackPath := filepath.Join(home, "listeners", "fallback.sock")
	fallbackListener := listenUnixSocket(t, fallbackPath)
	t.Cleanup(func() {
		require.NoError(t, fallbackListener.Close())
	})
	require.NoError(t, os.MkdirAll(filepath.Dir(fallbackSource), 0o755))
	require.NoError(t, os.Symlink(fallbackPath, fallbackSource))

	cfg, err := config.NewConfig(config.WithProjectRoot(projectRoot))
	require.NoError(t, err)
	fake := mocks.NewFakeClient(cfg)
	setupContainerStart(fake)
	const containerName = "clawker.sockettest.agent"
	fake.SetupContainerInspect(
		containerName,
		container.Summary{ //nolint:exhaustruct,exhaustruct_v5 // The command reads only these inspect fields.
			ID:    containerName,
			Names: []string{"/" + containerName},
			Image: "clawker:sockettest",
			Labels: map[string]string{
				cfg.LabelManaged():  cfg.ManagedLabelValue(),
				consts.LabelHarness: harnessName,
			},
			State: "created",
		},
	)

	database, err := db.Open(filepath.Join(env.Dirs.State, "socket-grants.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, database.Close())
	})
	bridge := socketbridgemocks.NewMockManager()
	f, in, out, errOut := testStartFactory(t, fake)
	f.Config = func() (config.Config, error) { return cfg, nil }
	f.DB = func() (*db.DB, error) { return database, nil }
	f.SocketBridge = func() socketbridge.SocketBridgeManager { return bridge }

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"--approve-grants", containerName})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	require.NoError(t, cmd.Execute(), "stderr: %s", errOut.String())
	assert.Contains(t, out.String(), fallbackPath)
	require.Len(t, bridge.EnsureBridgeCalls(), 1)
	require.Len(t, bridge.EnsureBridgeCalls()[0].Opts.Sockets, 1)
	assert.Equal(t, fallbackPath, bridge.EnsureBridgeCalls()[0].Opts.Sockets[0].HostPath)
	grants, err := db.NewSocketGrantStore(database).ListSocketGrants()
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, fallbackPath, grants[0].HostPath)

	alternateRoot := filepath.Join(home, "alternate")
	alternateSource := filepath.Join(alternateRoot, "some.sock")
	alternatePath := filepath.Join(home, "listeners", "alternate.sock")
	alternateListener := listenUnixSocket(t, alternatePath)
	t.Cleanup(func() {
		require.NoError(t, alternateListener.Close())
	})
	require.NoError(t, os.MkdirAll(filepath.Dir(alternateSource), 0o755))
	require.NoError(t, os.Symlink(alternatePath, alternateSource))
	t.Setenv("CLAWKER_TEST_SOCKET_ROOT", alternateRoot)

	f, in, out, errOut = testStartFactory(t, fake)
	f.Config = func() (config.Config, error) { return cfg, nil }
	f.DB = func() (*db.DB, error) { return database, nil }
	f.SocketBridge = func() socketbridge.SocketBridgeManager { return bridge }
	cmd = NewCmdStart(f, nil)
	cmd.SetArgs([]string{containerName})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	require.ErrorIs(t, cmd.Execute(), cmdutil.SilentError)
	assert.Contains(t, errOut.String(), alternatePath)
	grants, err = db.NewSocketGrantStore(database).ListSocketGrants()
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func listenUnixSocket(t *testing.T, path string) net.Listener {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	return listener
}

// TestStartRun_PreStartFailureReapsAutoRemove pins the non-attach path: a
// pre-start bootstrap failure (via shared.ContainerStart) removes a stopped
// AutoRemove (--rm) container so its name is freed.
func TestStartRun_PreStartFailureReapsAutoRemove(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerRemove()
	fake.SetupContainerInspectReapState(true, false)

	f, in, out, errOut := testStartFactory(t, fake)
	f.ControlPlane = func(context.Context) (cpmanager.Manager, error) {
		return &cpmanagermocks.ManagerMock{ //nolint:exhaustruct // test double: only the methods this path exercises are programmed
			StartFunc: func(context.Context) error { return errors.New("cp boom") },
		}, nil
	}

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	require.Contains(t, errOut.String(), shared.ReapedNotice)
	fake.AssertCalled(t, "ContainerRemove")
	fake.AssertNotCalled(t, "ContainerStart")
}

// TestStartRun_AttachPreStartFailureReapsAutoRemove pins the attach path,
// which calls BootstrapServicesPreStart directly (not via
// shared.ContainerStart): the same pre-start failure must reap too.
func TestStartRun_AttachPreStartFailureReapsAutoRemove(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerRemove()
	fake.SetupContainerInspectReapState(true, false)

	f, in, out, errOut := testStartFactory(t, fake)
	f.ControlPlane = func(context.Context) (cpmanager.Manager, error) {
		return &cpmanagermocks.ManagerMock{ //nolint:exhaustruct // test double: only the methods this path exercises are programmed
			StartFunc: func(context.Context) error { return errors.New("cp boom") },
		}, nil
	}

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"--attach", "clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), shared.ReapedNotice)
	fake.AssertCalled(t, "ContainerRemove")
	fake.AssertNotCalled(t, "ContainerStart")
}

func TestStartRun_MultipleContainers(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	setupContainerStart(fake)

	f, in, out, errOut := testStartFactory(t, fake)
	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.writer"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	outStr := out.String()
	assert.Contains(t, outStr, "clawker.myapp.dev")
	assert.Contains(t, outStr, "clawker.myapp.writer")
	fake.AssertCalledN(t, "ContainerStart", 2)
}

func TestStartRun_PartialFailure(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupNetworkExists(_blankCfg.ClawkerNetwork(), true)
	fake.FakeAPI.NetworkConnectFn = func(_ context.Context, _ string, _ mobyclient.NetworkConnectOptions) (mobyclient.NetworkConnectResult, error) {
		return mobyclient.NetworkConnectResult{}, nil
	}
	fake.FakeAPI.ContainerStartFn = func(_ context.Context, id string, _ mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error) {
		if id == "clawker.myapp.missing" {
			return mobyclient.ContainerStartResult{}, fmt.Errorf("no such container")
		}
		return mobyclient.ContainerStartResult{}, nil
	}
	fake.SetupCopyToContainer() // BootstrapServicesPreStart always delivers the pre_run hook

	f, in, out, errOut := testStartFactory(t, fake)
	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev", "clawker.myapp.missing"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded
	assert.Contains(t, out.String(), "clawker.myapp.dev")
	// Second container had error
	assert.Contains(t, errOut.String(), "clawker.myapp.missing")
}

func TestStartRun_NilHostProxy(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	setupContainerStart(fake)

	tio, in, out, errOut := iostreams.Test()
	// Default config has host proxy enabled (EnableHostProxy = nil → true)
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return configmocks.NewFromString(
				`security: { enable_host_proxy: false }`,
				`firewall: { enable: false }`,
			), nil
		},
		HostProxy: func() hostproxy.Service { return nil },
		ControlPlane: func(context.Context) (cpmanager.Manager, error) {
			return &cpmanagermocks.ManagerMock{ //nolint:exhaustruct // test double: only the methods this path exercises are programmed
				StartFunc: func(context.Context) error { return nil },
			}, nil
		},
	}

	cmd := NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.myapp.dev"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err) // No panic, start succeeds
	assert.Contains(t, out.String(), "clawker.myapp.dev")
}
