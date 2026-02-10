package create

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/stretchr/testify/require"
)

// wantOpts holds expected values for test comparisons against captured CreateOptions.
type wantOpts struct {
	Agent      string
	Name       string
	Mode       string
	Env        []string
	Volumes    []string
	User       string
	Entrypoint string
	TTY        bool
	Stdin      bool
	Network    string
	Labels     []string
	AutoRemove bool
	Image      string
	Command    []string
}

func TestNewCmdCreate(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		want       wantOpts
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:  "basic image only",
			input: "",
			args:  []string{"alpine"},
			want:  wantOpts{Image: "alpine"},
		},
		{
			name:  "image with command",
			input: "",
			args:  []string{"alpine", "echo", "hello"},
			want:  wantOpts{Image: "alpine", Command: []string{"echo", "hello"}},
		},
		{
			name:  "with agent flag",
			input: "--agent myagent",
			args:  []string{"alpine"},
			want:  wantOpts{Agent: "myagent", Image: "alpine"},
		},
		{
			name:  "with name flag",
			input: "--name mycontainer",
			args:  []string{"alpine"},
			want:  wantOpts{Name: "mycontainer", Image: "alpine"},
		},
		{
			name:  "with environment variable",
			input: "-e FOO=bar",
			args:  []string{"alpine"},
			want:  wantOpts{Env: []string{"FOO=bar"}, Image: "alpine"},
		},
		{
			name:  "with multiple env vars",
			input: "-e FOO=bar -e BAZ=qux",
			args:  []string{"alpine"},
			want:  wantOpts{Env: []string{"FOO=bar", "BAZ=qux"}, Image: "alpine"},
		},
		{
			name:  "with volume",
			input: "-v /host:/container",
			args:  []string{"alpine"},
			want:  wantOpts{Volumes: []string{"/host:/container"}, Image: "alpine"},
		},
		{
			name:  "with user",
			input: "-u nobody",
			args:  []string{"alpine"},
			want:  wantOpts{User: "nobody", Image: "alpine"},
		},
		{
			name:  "with entrypoint",
			input: "--entrypoint /bin/sh",
			args:  []string{"alpine"},
			want:  wantOpts{Entrypoint: "/bin/sh", Image: "alpine"},
		},
		{
			name:  "with tty",
			input: "-t",
			args:  []string{"alpine"},
			want:  wantOpts{TTY: true, Image: "alpine"},
		},
		{
			name:  "with interactive",
			input: "-i",
			args:  []string{"alpine"},
			want:  wantOpts{Stdin: true, Image: "alpine"},
		},
		{
			name:  "with tty and interactive",
			input: "-it",
			args:  []string{"alpine"},
			want:  wantOpts{TTY: true, Stdin: true, Image: "alpine"},
		},
		{
			name:  "with network",
			input: "--network mynet",
			args:  []string{"alpine"},
			want:  wantOpts{Network: "mynet", Image: "alpine"},
		},
		{
			name:  "with label",
			input: "-l foo=bar",
			args:  []string{"alpine"},
			want:  wantOpts{Labels: []string{"foo=bar"}, Image: "alpine"},
		},
		{
			name:  "with auto-remove",
			input: "--rm",
			args:  []string{"alpine"},
			want:  wantOpts{AutoRemove: true, Image: "alpine"},
		},
		{
			name:  "with mode bind",
			input: "--agent dev --mode=bind",
			args:  []string{"alpine"},
			want:  wantOpts{Agent: "dev", Mode: "bind", Image: "alpine"},
		},
		{
			name:  "with mode snapshot",
			input: "--agent dev --mode=snapshot",
			args:  []string{"alpine"},
			want:  wantOpts{Agent: "dev", Mode: "snapshot", Image: "alpine"},
		},
		{
			name:  "with mode and other flags",
			input: "-it --agent sandbox --mode=snapshot --rm",
			args:  []string{"alpine", "sh"},
			want:  wantOpts{TTY: true, Stdin: true, Agent: "sandbox", Mode: "snapshot", AutoRemove: true, Image: "alpine", Command: []string{"sh"}},
		},
		{
			name:  "flags after image passed as command",
			input: "-it --rm",
			args:  []string{"alpine", "--version"},
			want:  wantOpts{TTY: true, Stdin: true, AutoRemove: true, Image: "alpine", Command: []string{"--version"}},
		},
		{
			name:  "mixed clawker and container flags",
			input: "-it --rm -e FOO=bar",
			args:  []string{"alpine", "-p", "prompt"},
			want:  wantOpts{TTY: true, Stdin: true, AutoRemove: true, Env: []string{"FOO=bar"}, Image: "alpine", Command: []string{"-p", "prompt"}},
		},
		{
			name:  "claude flags passthrough",
			input: "-it --rm",
			args:  []string{"clawker-image:latest", "--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
			want:  wantOpts{TTY: true, Stdin: true, AutoRemove: true, Image: "clawker-image:latest", Command: []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"}},
		},
		{
			name:  "flags only as command with -- separator",
			input: "-it --rm --agent ralph --",
			args:  []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
			want:  wantOpts{TTY: true, Stdin: true, AutoRemove: true, Agent: "ralph", Image: "--allow-dangerously-skip-permissions", Command: []string{"-p", "Fix bugs"}},
		},
		{
			name:  "arg starting with dash treated as image after -- separator",
			input: "-it --rm --",
			args:  []string{"-unusual-image:v1"},
			want:  wantOpts{TTY: true, Stdin: true, AutoRemove: true, Image: "-unusual-image:v1"},
		},
		{
			name:  "multiple flag-value pairs after image",
			input: "-it --rm",
			args:  []string{"alpine", "--flag1", "value1", "--flag2", "value2"},
			want:  wantOpts{TTY: true, Stdin: true, AutoRemove: true, Image: "alpine", Command: []string{"--flag1", "value1", "--flag2", "value2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *CreateOptions
			cmd := NewCmdCreate(f, func(_ context.Context, opts *CreateOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
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
			require.Equal(t, tt.want.Agent, gotOpts.Agent)
			require.Equal(t, tt.want.Name, gotOpts.Name)
			require.Equal(t, tt.want.Mode, gotOpts.Mode)
			require.Equal(t, tt.want.Image, gotOpts.Image)
			require.Equal(t, tt.want.Command, gotOpts.Command)
			requireSliceEqual(t, tt.want.Env, gotOpts.Env)
			requireSliceEqual(t, tt.want.Volumes, gotOpts.Volumes)
			require.Equal(t, tt.want.User, gotOpts.User)
			require.Equal(t, tt.want.Entrypoint, gotOpts.Entrypoint)
			require.Equal(t, tt.want.TTY, gotOpts.TTY)
			require.Equal(t, tt.want.Stdin, gotOpts.Stdin)
			require.Equal(t, tt.want.Network, gotOpts.NetMode.NetworkMode())
			requireSliceEqual(t, tt.want.Labels, gotOpts.Labels)
			require.Equal(t, tt.want.AutoRemove, gotOpts.AutoRemove)
		})
	}
}

func TestCmdCreate_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdCreate(f, nil)

	// Test command basics
	require.Equal(t, "create [OPTIONS] IMAGE [COMMAND] [ARG...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("name"))
	require.NotNil(t, cmd.Flags().Lookup("mode"))
	require.NotNil(t, cmd.Flags().Lookup("env"))
	require.NotNil(t, cmd.Flags().Lookup("volume"))
	require.NotNil(t, cmd.Flags().Lookup("publish"))
	require.NotNil(t, cmd.Flags().Lookup("user"))
	require.NotNil(t, cmd.Flags().Lookup("entrypoint"))
	require.NotNil(t, cmd.Flags().Lookup("tty"))
	require.NotNil(t, cmd.Flags().Lookup("interactive"))
	require.NotNil(t, cmd.Flags().Lookup("network"))
	require.NotNil(t, cmd.Flags().Lookup("label"))
	require.NotNil(t, cmd.Flags().Lookup("rm"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("e"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("v"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("p"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("u"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("l"))
}

func TestCmdCreate_MutuallyExclusiveFlags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdCreate(f, func(_ context.Context, _ *CreateOptions) error {
		return nil
	})

	// Cobra hack-around for help flag
	cmd.Flags().BoolP("help", "x", false, "")

	// Test that --agent and --name are mutually exclusive
	cmd.SetArgs([]string{"--agent", "myagent", "--name", "myname", "alpine"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent")
	require.Contains(t, err.Error(), "name")
}

func TestBuildConfigs(t *testing.T) {
	tests := []struct {
		name    string
		opts    *copts.ContainerOptions
		wantErr bool
	}{
		{
			name: "basic config",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with tty and stdin",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				TTY:     true,
				Stdin:   true,
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with command",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Command: []string{"echo", "hello"},
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with env vars",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Env:     []string{"FOO=bar", "BAZ=qux"},
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with labels",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Labels:  []string{"foo=bar", "baz"},
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with network",
			opts: func() *copts.ContainerOptions {
				o := copts.NewContainerOptions()
				o.Image = "alpine"
				o.NetMode.Set("mynet")
				return o
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, hostCfg, _, err := tt.opts.BuildConfigs(nil, nil, config.DefaultConfig())

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)
			require.NotNil(t, hostCfg)

			// Verify basic fields
			require.Equal(t, tt.opts.Image, cfg.Image)
			require.Equal(t, tt.opts.TTY, cfg.Tty)
			require.Equal(t, tt.opts.Stdin, cfg.OpenStdin)

			// Verify command
			if len(tt.opts.Command) > 0 {
				require.Equal(t, tt.opts.Command, []string(cfg.Cmd))
			}

			// Verify env
			if len(tt.opts.Env) > 0 {
				require.Equal(t, tt.opts.Env, cfg.Env)
			}

			// Verify network mode is set in host config
			if tt.opts.NetMode.NetworkMode() != "" {
				require.Equal(t, container.NetworkMode(tt.opts.NetMode.NetworkMode()), hostCfg.NetworkMode)
			}
		})
	}
}

// requireSliceEqual compares two slices, treating nil and empty slice as equal.
func requireSliceEqual(t *testing.T, expected, actual []string) {
	t.Helper()
	if len(expected) == 0 && len(actual) == 0 {
		return // Both are empty (nil or [])
	}
	require.Equal(t, expected, actual)
}

// ---------------------------------------------------------------------------
// Tier 2 — Cobra+Factory integration tests
// ---------------------------------------------------------------------------

// testConfig returns a minimal *config.Project for Tier 2 tests.
func testConfig() *config.Project {
	hostProxyDisabled := false
	return &config.Project{
		Version: "1",
		Project: "",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: config.SecurityConfig{
			EnableHostProxy: &hostProxyDisabled,
			Firewall: &config.FirewallConfig{
				Enable: false,
			},
		},
	}
}

// testFactory builds a *cmdutil.Factory backed by a FakeClient for Tier 2 create tests.
func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()
	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(testConfig(), config.DefaultSettings())
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, fmt.Errorf("GitManager not available in test")
		},
		HostProxy: func() *hostproxy.Manager {
			return hostproxy.NewManager()
		},
		Prompter: func() *prompter.Prompter { return nil },
	}, tio
}

func TestCreateRun(t *testing.T) {
	t.Run("basic create prints container ID", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerCreate()
		fake.SetupCopyToContainer()

		f, tio := testFactory(t, fake)
		cmd := NewCmdCreate(f, nil)

		cmd.SetArgs([]string{"alpine"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		// Create prints 12-char container ID to stdout
		out := tio.OutBuf.String()
		require.Contains(t, out, "sha256:fakec")
		require.Len(t, strings.TrimSpace(out), 12)

		fake.AssertCalled(t, "ContainerCreate")
	})

	t.Run("config init runs when config volume freshly created", func(t *testing.T) {
		// Make all volumes report as not existing → EnsureVolume creates them → ConfigCreated=true
		fake := dockertest.NewFakeClient()
		fake.SetupVolumeExists("", false)
		fake.FakeAPI.VolumeCreateFn = func(_ context.Context, _ moby.VolumeCreateOptions) (moby.VolumeCreateResult, error) {
			return moby.VolumeCreateResult{}, nil
		}
		fake.SetupContainerCreate()
		fake.SetupCopyToContainer()

		// Point CLAUDE_CONFIG_DIR to a non-existent path so InitContainerConfig fails
		// (proving it WAS called when ConfigCreated=true).
		t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/nonexistent-clawker-test-dir")

		f, tio := testFactory(t, fake)
		cmd := NewCmdCreate(f, nil)

		cmd.SetArgs([]string{"alpine"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "container init")
		require.Contains(t, err.Error(), "CLAUDE_CONFIG_DIR")

		// Container should NOT have been created since init failed before ContainerCreate... wait no.
		// Actually, init runs AFTER workspace setup (which includes ContainerCreate path? No.)
		// Looking at create.go: SetupMounts → init → ContainerCreate. Init is before create.
		fake.AssertNotCalled(t, "ContainerCreate")
	})

	t.Run("config init skipped when config volume exists", func(t *testing.T) {
		// Default fake: volumes exist → ConfigCreated=false → no init
		fake := dockertest.NewFakeClient()
		fake.SetupContainerCreate()
		fake.SetupCopyToContainer()

		f, tio := testFactory(t, fake)
		cmd := NewCmdCreate(f, nil)

		cmd.SetArgs([]string{"alpine"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		// Container created successfully — no init errors
		out := tio.OutBuf.String()
		require.Len(t, strings.TrimSpace(out), 12)
		fake.AssertCalled(t, "ContainerCreate")
	})

	t.Run("onboarding injected when use_host_auth enabled", func(t *testing.T) {
		// Default config: UseHostAuth=nil → UseHostAuthEnabled()=true
		// Default fake: volumes exist → ConfigCreated=false → no init
		fake := dockertest.NewFakeClient()
		fake.SetupContainerCreate()

		copyToContainerCalled := false
		fake.FakeAPI.CopyToContainerFn = func(_ context.Context, _ string, _ moby.CopyToContainerOptions) (moby.CopyToContainerResult, error) {
			copyToContainerCalled = true
			return moby.CopyToContainerResult{}, nil
		}

		f, tio := testFactory(t, fake)
		cmd := NewCmdCreate(f, nil)

		cmd.SetArgs([]string{"alpine"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)
		require.True(t, copyToContainerCalled, "CopyToContainer should be called for onboarding injection")
		fake.AssertCalled(t, "ContainerCreate")
	})

	t.Run("onboarding failure returns error", func(t *testing.T) {
		// Default config: UseHostAuth=nil → UseHostAuthEnabled()=true
		// CopyToContainer fails → onboarding error propagates
		fake := dockertest.NewFakeClient()
		fake.SetupContainerCreate()

		fake.FakeAPI.CopyToContainerFn = func(_ context.Context, _ string, _ moby.CopyToContainerOptions) (moby.CopyToContainerResult, error) {
			return moby.CopyToContainerResult{}, fmt.Errorf("copy failed: disk full")
		}

		f, tio := testFactory(t, fake)
		cmd := NewCmdCreate(f, nil)

		cmd.SetArgs([]string{"alpine"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "inject onboarding")
	})

	t.Run("onboarding skipped when use_host_auth disabled", func(t *testing.T) {
		// Explicitly disable use_host_auth → no onboarding injection
		cfg := testConfig()
		useHostAuth := false
		cfg.Agent.ClaudeCode = &config.ClaudeCodeConfig{
			UseHostAuth: &useHostAuth,
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
		}

		fake := dockertest.NewFakeClient(
			dockertest.WithConfig(config.NewConfigForTest(cfg, config.DefaultSettings())),
		)
		fake.SetupContainerCreate()
		// No CopyToContainer setup — if called, it would panic

		tio := iostreams.NewTestIOStreams()
		f := &cmdutil.Factory{
			IOStreams: tio.IOStreams,
			Client: func(_ context.Context) (*docker.Client, error) {
				return fake.Client, nil
			},
			Config: func() *config.Config {
				return config.NewConfigForTest(cfg, config.DefaultSettings())
			},
			GitManager: func() (*git.GitManager, error) {
				return nil, fmt.Errorf("GitManager not available in test")
			},
			HostProxy: func() *hostproxy.Manager {
				return hostproxy.NewManager()
			},
			Prompter: func() *prompter.Prompter { return nil },
		}

		cmd := NewCmdCreate(f, nil)
		cmd.SetArgs([]string{"alpine"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		// Container created successfully without CopyToContainer being called
		out := tio.OutBuf.String()
		require.Len(t, strings.TrimSpace(out), 12)
		fake.AssertCalled(t, "ContainerCreate")
		fake.AssertNotCalled(t, "CopyToContainer")
	})
}
