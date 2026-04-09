package run

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRun(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		wantErr    bool
		wantErrMsg string
		// Expected values (checked only when wantErr is false)
		wantAgent      string
		wantName       string
		wantDetach     bool
		wantMode       string
		wantImage      string
		wantCommand    []string
		wantEnv        []string
		wantVolumes    []string
		wantPublish    []string
		wantUser       string
		wantEntrypoint string
		wantTTY        bool
		wantStdin      bool
		wantNetwork    string
		wantLabels     []string
		wantAutoRemove bool
	}{
		{
			name:    "no image specified",
			input:   "",
			args:    []string{},
			wantErr: true, // RequiresMinArgs(1) rejects empty args
		},
		{
			name:      "basic image only",
			args:      []string{"alpine"},
			wantImage: "alpine",
		},
		{
			name:      "image with tag",
			args:      []string{"alpine:latest"},
			wantImage: "alpine:latest",
		},
		{
			name:        "image with command",
			args:        []string{"alpine", "echo", "hello"},
			wantImage:   "alpine",
			wantCommand: []string{"echo", "hello"},
		},
		{
			name:      "with agent flag",
			input:     "--agent myagent",
			args:      []string{"alpine"},
			wantAgent: "myagent",
			wantImage: "alpine",
		},
		{
			name:      "with name flag",
			input:     "--name mycontainer",
			args:      []string{"alpine"},
			wantName:  "mycontainer",
			wantImage: "alpine",
		},
		{
			name:       "with detach flag",
			input:      "--detach",
			args:       []string{"alpine"},
			wantDetach: true,
			wantImage:  "alpine",
		},
		{
			name:      "with environment variable",
			input:     "-e FOO=bar",
			args:      []string{"alpine"},
			wantEnv:   []string{"FOO=bar"},
			wantImage: "alpine",
		},
		{
			name:      "with multiple env vars",
			input:     "-e FOO=bar -e BAZ=qux",
			args:      []string{"alpine"},
			wantEnv:   []string{"FOO=bar", "BAZ=qux"},
			wantImage: "alpine",
		},
		{
			name:        "with volume",
			input:       "-v /host:/container",
			args:        []string{"alpine"},
			wantVolumes: []string{"/host:/container"},
			wantImage:   "alpine",
		},
		{
			name:        "with port",
			input:       "-p 8080:80",
			args:        []string{"alpine"},
			wantPublish: []string{"8080:80"},
			wantImage:   "alpine",
		},
		{
			name:      "with user",
			input:     "-u nobody",
			args:      []string{"alpine"},
			wantUser:  "nobody",
			wantImage: "alpine",
		},
		{
			name:           "with entrypoint",
			input:          "--entrypoint /bin/sh",
			args:           []string{"alpine"},
			wantEntrypoint: "/bin/sh",
			wantImage:      "alpine",
		},
		{
			name:      "with tty",
			input:     "-t",
			args:      []string{"alpine"},
			wantTTY:   true,
			wantImage: "alpine",
		},
		{
			name:      "with interactive",
			input:     "-i",
			args:      []string{"alpine"},
			wantStdin: true,
			wantImage: "alpine",
		},
		{
			name:      "with tty and interactive",
			input:     "-it",
			args:      []string{"alpine"},
			wantTTY:   true,
			wantStdin: true,
			wantImage: "alpine",
		},
		{
			name:        "with network",
			input:       "--network mynet",
			args:        []string{"alpine"},
			wantNetwork: "mynet",
			wantImage:   "alpine",
		},
		{
			name:       "with label",
			input:      "-l foo=bar",
			args:       []string{"alpine"},
			wantLabels: []string{"foo=bar"},
			wantImage:  "alpine",
		},
		{
			name:           "with auto-remove",
			input:          "--rm",
			args:           []string{"alpine"},
			wantAutoRemove: true,
			wantImage:      "alpine",
		},
		{
			name:           "interactive detached with rm",
			input:          "-it --detach --rm",
			args:           []string{"alpine", "sh"},
			wantTTY:        true,
			wantStdin:      true,
			wantDetach:     true,
			wantAutoRemove: true,
			wantImage:      "alpine",
			wantCommand:    []string{"sh"},
		},
		{
			name:    "no image requires error",
			args:    []string{},
			wantErr: true, // RequiresMinArgs(1) rejects empty args
		},
		// @ symbol tests - triggers default image resolution at runtime
		{
			name:      "@ symbol as image",
			args:      []string{"@"},
			wantImage: "@",
		},
		{
			name:      "@ symbol with agent flag",
			input:     "--agent dev",
			args:      []string{"@"},
			wantAgent: "dev",
			wantImage: "@",
		},
		{
			name:           "@ symbol with common flags",
			input:          "-it --rm",
			args:           []string{"@"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantImage:      "@",
		},
		{
			name:        "@ symbol with command",
			args:        []string{"@", "echo", "hello"},
			wantImage:   "@",
			wantCommand: []string{"echo", "hello"},
		},
		{
			name:      "@ symbol with mode",
			input:     "--agent dev --mode=snapshot",
			args:      []string{"@"},
			wantAgent: "dev",
			wantMode:  "snapshot",
			wantImage: "@",
		},
		{
			name:      "with mode bind",
			input:     "--agent dev --mode=bind",
			args:      []string{"alpine"},
			wantAgent: "dev",
			wantMode:  "bind",
			wantImage: "alpine",
		},
		{
			name:      "with mode snapshot",
			input:     "--agent dev --mode=snapshot",
			args:      []string{"alpine"},
			wantAgent: "dev",
			wantMode:  "snapshot",
			wantImage: "alpine",
		},
		{
			name:           "with mode and other flags",
			input:          "-it --agent sandbox --mode=snapshot --rm",
			args:           []string{"alpine", "sh"},
			wantTTY:        true,
			wantStdin:      true,
			wantAgent:      "sandbox",
			wantMode:       "snapshot",
			wantAutoRemove: true,
			wantImage:      "alpine",
			wantCommand:    []string{"sh"},
		},
		{
			name:           "flags after image passed as command",
			input:          "-it --rm",
			args:           []string{"alpine", "--version"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantImage:      "alpine",
			wantCommand:    []string{"--version"},
		},
		{
			name:           "mixed clawker and container flags",
			input:          "-it --rm -e FOO=bar",
			args:           []string{"alpine", "-p", "prompt"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantEnv:        []string{"FOO=bar"},
			wantImage:      "alpine",
			wantCommand:    []string{"-p", "prompt"},
		},
		{
			name:           "claude flags passthrough",
			input:          "-it --rm",
			args:           []string{"clawker-image:latest", "--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantImage:      "clawker-image:latest",
			wantCommand:    []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
		},
		{
			// After --, all remaining args are positional. The real RunE always
			// treats args[0] as Image, so a flag-like arg becomes the image.
			name:           "flags only as command with -- separator",
			input:          "-it --rm --agent dev --",
			args:           []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantAgent:      "dev",
			wantImage:      "--allow-dangerously-skip-permissions",
			wantCommand:    []string{"-p", "Fix bugs"},
		},
		{
			// After --, args[0] is always Image regardless of leading dash.
			name:           "arg starting with dash treated as image after -- separator",
			input:          "-it --rm --",
			args:           []string{"-unusual-image:v1"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantImage:      "-unusual-image:v1",
		},
		{
			name:           "multiple flag-value pairs after image",
			input:          "-it --rm",
			args:           []string{"alpine", "--flag1", "value1", "--flag2", "value2"},
			wantTTY:        true,
			wantStdin:      true,
			wantAutoRemove: true,
			wantImage:      "alpine",
			wantCommand:    []string{"--flag1", "value1", "--flag2", "value2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *RunOptions
			cmd := NewCmdRun(f, func(_ context.Context, opts *RunOptions) error {
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
				if tt.wantErrMsg != "" {
					require.Contains(t, err.Error(), tt.wantErrMsg)
				} else if len(tt.args) == 0 {
					// For empty args, verify error matches RequiresMinArgs
					expectedErr := cmdutil.RequiresMinArgs(1)(cmd, tt.args)
					require.Equal(t, expectedErr.Error(), err.Error())
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)

			require.Equal(t, tt.wantAgent, gotOpts.ContainerCreateOptions.Agent)
			require.Equal(t, tt.wantName, gotOpts.ContainerCreateOptions.Name)
			require.Equal(t, tt.wantDetach, gotOpts.Detach)
			require.Equal(t, tt.wantMode, gotOpts.ContainerCreateOptions.Mode)
			require.Equal(t, tt.wantImage, gotOpts.ContainerCreateOptions.Image)
			require.Equal(t, tt.wantCommand, gotOpts.ContainerCreateOptions.Command)
			requireSliceEqual(t, tt.wantEnv, gotOpts.ContainerCreateOptions.Env)
			requireSliceEqual(t, tt.wantVolumes, gotOpts.ContainerCreateOptions.Volumes)
			requireSliceEqual(t, tt.wantPublish, gotOpts.ContainerCreateOptions.Publish.GetAsStrings())
			require.Equal(t, tt.wantUser, gotOpts.ContainerCreateOptions.User)
			require.Equal(t, tt.wantEntrypoint, gotOpts.ContainerCreateOptions.Entrypoint)
			require.Equal(t, tt.wantTTY, gotOpts.ContainerCreateOptions.TTY)
			require.Equal(t, tt.wantStdin, gotOpts.ContainerCreateOptions.Stdin)
			require.Equal(t, tt.wantNetwork, gotOpts.ContainerCreateOptions.NetMode.NetworkMode())
			requireSliceEqual(t, tt.wantLabels, gotOpts.ContainerCreateOptions.Labels)
			require.Equal(t, tt.wantAutoRemove, gotOpts.ContainerCreateOptions.AutoRemove)
		})
	}
}

// TestCmdRun_NoDetachShorthand verifies --detach does NOT have -d shorthand (conflicts with --debug)
func TestCmdRun_NoDetachShorthand(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRun(f, nil)

	// Verify --detach does NOT have -d shorthand (conflicts with --debug)
	require.Nil(t, cmd.Flags().ShorthandLookup("d"))
}

// TestBuildConfigs tests the shared BuildConfigs function from shared package
func TestBuildConfigs(t *testing.T) {
	tests := []struct {
		name    string
		opts    *shared.ContainerCreateOptions
		wantErr bool
	}{
		{
			name: "basic config",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with tty and stdin",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				TTY:     true,
				Stdin:   true,
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with command",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				Command: []string{"echo", "hello"},
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with env vars",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				Env:     []string{"FOO=bar", "BAZ=qux"},
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with valid port",
			opts: func() *shared.ContainerCreateOptions {
				o := shared.NewContainerOptions()
				o.Image = "alpine"
				o.Publish.Set("8080:80")
				return o
			}(),
		},
		// Note: Invalid port validation happens in PortOpts.Set(), not in BuildConfigs.
		// See TestPortOpts in internal/cmd/container/shared/container_test.go for port validation tests.
		{
			name: "with labels",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				Labels:  []string{"foo=bar", "baz"},
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with network",
			opts: func() *shared.ContainerCreateOptions {
				o := shared.NewContainerOptions()
				o.Image = "alpine"
				o.NetMode.Set("mynet")
				return o
			}(),
		},
		{
			name: "with auto-remove",
			opts: &shared.ContainerCreateOptions{
				Image:      "alpine",
				AutoRemove: true,
				Publish:    shared.NewPortOpts(),
			},
		},
		{
			name: "with entrypoint",
			opts: &shared.ContainerCreateOptions{
				Image:      "alpine",
				Entrypoint: "/custom/entrypoint",
				Publish:    shared.NewPortOpts(),
			},
		},
		{
			name: "with volumes/binds",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				Volumes: []string{"/host/path:/container/path", "/another:/mount"},
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with user",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				User:    "nobody",
				Publish: shared.NewPortOpts(),
			},
		},
		{
			name: "with workdir",
			opts: &shared.ContainerCreateOptions{
				Image:   "alpine",
				Workdir: "/custom/workdir",
				Publish: shared.NewPortOpts(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, hostCfg, _, err := tt.opts.BuildConfigs(nil, nil, &config.Project{})

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

			// Verify auto-remove
			require.Equal(t, tt.opts.AutoRemove, hostCfg.AutoRemove)

			// Verify entrypoint
			if tt.opts.Entrypoint != "" {
				require.Equal(t, []string{tt.opts.Entrypoint}, []string(cfg.Entrypoint))
			}

			// Verify volumes/binds
			if len(tt.opts.Volumes) > 0 {
				require.Equal(t, tt.opts.Volumes, hostCfg.Binds)
			}

			// Verify user
			if tt.opts.User != "" {
				require.Equal(t, tt.opts.User, cfg.User)
			}

			// Verify workdir
			if tt.opts.Workdir != "" {
				require.Equal(t, tt.opts.Workdir, cfg.WorkingDir)
			}

			// Verify labels
			if len(tt.opts.Labels) > 0 {
				require.NotNil(t, cfg.Labels)
			}

			// Verify network mode is set in host config
			if tt.opts.NetMode.NetworkMode() != "" {
				require.Equal(t, container.NetworkMode(tt.opts.NetMode.NetworkMode()), hostCfg.NetworkMode)
			}
		})
	}
}

func TestBuildConfigs_CapAdd(t *testing.T) {
	opts := &shared.ContainerCreateOptions{
		Image:   "alpine",
		Publish: shared.NewPortOpts(),
	}
	projectCfg := &config.Project{
		Security: config.SecurityConfig{
			CapAdd: []string{"NET_ADMIN", "SYS_PTRACE"},
		},
	}

	_, hostCfg, _, err := opts.BuildConfigs(nil, nil, projectCfg)
	require.NoError(t, err)
	require.Len(t, hostCfg.CapAdd, 2)
	require.Contains(t, hostCfg.CapAdd, "NET_ADMIN")
	require.Contains(t, hostCfg.CapAdd, "SYS_PTRACE")
}

// requireSliceEqual compares two slices, treating nil and empty slice as equal.
func requireSliceEqual(t *testing.T, expected, actual []string) {
	t.Helper()
	if len(expected) == 0 && len(actual) == 0 {
		return // Both are empty (nil or [])
	}
	require.Equal(t, expected, actual)
}

// TestImageArg tests image argument handling for the run command.
// Tests @ symbol resolution (using mock Docker client) and explicit image pass-through.
func TestImageArg(t *testing.T) {
	// Tests for @ symbol resolution (uses dockertest.FakeClient)
	// ResolveImageWithSource resolves project images only (no default image fallback).
	// Returns nil when no project image with :latest tag is found.
	t.Run("@ symbol resolution", func(t *testing.T) {
		tests := []struct {
			name          string
			projectName   string
			fakeImages    []string // Images to return from fake ImageList
			wantReference string
			wantSource    docker.ImageSource
			wantNil       bool // Expect nil result (no resolution)
		}{
			{
				name:          "@ resolves to project image when exists",
				projectName:   "myproject",
				fakeImages:    []string{"clawker-myproject:latest"},
				wantReference: "clawker-myproject:latest",
				wantSource:    docker.ImageSourceProject,
			},
			{
				name:        "@ returns nil when no project image",
				projectName: "myproject",
				fakeImages:  []string{}, // No project images
				wantNil:     true,
			},
			{
				name:        "@ returns nil for empty project",
				projectName: "",
				fakeImages:  []string{},
				wantNil:     true,
			},
			{
				name:          "@ prefers latest-tagged project image",
				projectName:   "myproject",
				fakeImages:    []string{"clawker-myproject:latest", "other:tag"},
				wantReference: "clawker-myproject:latest",
				wantSource:    docker.ImageSourceProject,
			},
			{
				name:        "@ ignores non-latest project images",
				projectName: "myproject",
				fakeImages:  []string{"clawker-myproject:v1.0"}, // No :latest tag
				wantNil:     true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()

				testCfg := configmocks.NewBlankConfig()

				// Create fake Docker client with the config
				fake := mock.NewFakeClient(testCfg)

				// Build image summaries and configure fake
				var summaries []whail.ImageSummary
				for _, ref := range tt.fakeImages {
					summaries = append(summaries, whail.ImageSummary{
						RepoTags: []string{ref},
					})
				}
				fake.SetupImageList(summaries...)

				// Call the resolution method on the client with projectName
				result, err := fake.Client.ResolveImageWithSource(ctx, tt.projectName)
				require.NoError(t, err)

				if tt.wantNil {
					require.Nil(t, result, "expected nil result")
					return
				}

				require.NotNil(t, result, "expected non-nil result")
				require.Equal(t, tt.wantReference, result.Reference)
				require.Equal(t, tt.wantSource, result.Source)
			})
		}
	})

	// Tests for explicit image pass-through (no resolution, no mock needed)
	// These test that explicit images are not modified by the command
	t.Run("explicit image pass-through", func(t *testing.T) {
		tests := []struct {
			name      string
			imageArg  string
			wantImage string
		}{
			{
				name:      "simple image name",
				imageArg:  "alpine",
				wantImage: "alpine",
			},
			{
				name:      "image with tag",
				imageArg:  "alpine:latest",
				wantImage: "alpine:latest",
			},
			{
				name:      "image with version tag",
				imageArg:  "node:20-slim",
				wantImage: "node:20-slim",
			},
			{
				name:      "image with registry",
				imageArg:  "docker.io/library/alpine:latest",
				wantImage: "docker.io/library/alpine:latest",
			},
			{
				name:      "private registry image",
				imageArg:  "ghcr.io/myorg/myimage:v1.0.0",
				wantImage: "ghcr.io/myorg/myimage:v1.0.0",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := &cmdutil.Factory{}

				var gotOpts *RunOptions
				cmd := NewCmdRun(f, func(_ context.Context, opts *RunOptions) error {
					gotOpts = opts
					return nil
				})

				// Cobra hack-around for help flag
				cmd.Flags().BoolP("help", "x", false, "")

				cmd.SetArgs([]string{tt.imageArg})
				cmd.SetIn(&bytes.Buffer{})
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})

				_, err := cmd.ExecuteC()
				require.NoError(t, err)
				require.NotNil(t, gotOpts)
				require.Equal(t, tt.wantImage, gotOpts.ContainerCreateOptions.Image, "explicit image should pass through unchanged")
			})
		}
	})

	// Test for empty image (no image argument provided)
	t.Run("empty image shows error", func(t *testing.T) {
		f := &cmdutil.Factory{}
		cmd := NewCmdRun(f, nil)

		// Cobra hack-around for help flag
		cmd.Flags().BoolP("help", "x", false, "")

		cmd.SetArgs([]string{}) // No image provided
		cmd.SetIn(&bytes.Buffer{})
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)

		_, err := cmd.ExecuteC()

		argsErr := cmdutil.RequiresMinArgs(1)(cmd, []string{})

		// Expect an error with helpful message
		require.Error(t, err, "should return error when no image provided")
		require.Equal(t, argsErr.Error(), err.Error(), "error should match RequiresMinArgs output")
		// Verify stderr contains the error message (without "Error: " prefix that cobra adds to err)
		require.Contains(t, stderr.String(), "requires at least 1 argument", "stderr should show argument requirement")
	})
}

// --- Cobra + fake Factory tests (Task 2: Phase 4a proof-of-concept) ---
//
// These tests go through cmd.Execute() with a faked *cmdutil.Factory.
// The real runRun executes (runF is nil), exercising the full command path
// including flag parsing, config loading, workspace setup, and Docker calls.
// Docker operations are faked via dockertest.FakeClient.

// testFactory constructs a minimal *cmdutil.Factory for command-level testing.
// The returned Factory wires fake Docker client, test config, and test IOStreams.
func testFactory(t *testing.T, fake *mock.FakeClient) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	// Ensure CWD is inside $HOME so IsOutsideHome returns false (matters in containers).
	cwd, _ := os.Getwd()
	t.Setenv("HOME", filepath.Dir(cwd))
	tio, in, out, errOut := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		TUI:       tui.NewTUI(tio),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			mock := configmocks.NewFromString(`
version: "1"
workspace: { default_mode: "bind" }
security: { enable_host_proxy: false }
`, `firewall: { enable: false }`)
			mock.GetProjectIgnoreFileFunc = func() (string, error) {
				return filepath.Join(os.TempDir(), mock.ClawkerIgnoreName()), nil
			}
			mock.GetProjectRootFunc = func() (string, error) {
				return os.TempDir(), nil
			}
			return mock, nil
		},
		HostProxy: func() hostproxy.HostProxyService {
			return hostproxytest.NewMockManager()
		},
		Prompter: func() *prompter.Prompter { return prompter.NewPrompter(tio) },
	}, in, out, errOut
}

func TestRunRun(t *testing.T) {
	t.Run("detached mode prints container ID", func(t *testing.T) {
		fake := mock.NewFakeClient(configmocks.NewBlankConfig())
		fake.SetupContainerCreate()
		fake.SetupCopyToContainer()
		fake.SetupContainerStart()

		f, in, out, errOut := testFactory(t, fake)
		cmd := NewCmdRun(f, nil)

		cmd.SetArgs([]string{"--detach", "alpine"})
		cmd.SetIn(in)
		cmd.SetOut(out)
		cmd.SetErr(errOut)

		err := cmd.Execute()
		require.NoError(t, err)

		// Detached mode prints 12-char container ID to stdout
		outStr := out.String()
		require.Contains(t, outStr, "sha256:fakec")
		require.Len(t, strings.TrimSpace(outStr), 12)

		fake.AssertCalled(t, "ContainerCreate")
		fake.AssertCalled(t, "ContainerStart")
	})

	t.Run("container create failure returns error", func(t *testing.T) {
		fake := mock.NewFakeClient(configmocks.NewBlankConfig())
		fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
			return moby.ContainerCreateResult{}, fmt.Errorf("disk full")
		}
		fake.SetupContainerStart()

		f, in, out, errOut := testFactory(t, fake)
		cmd := NewCmdRun(f, nil)

		cmd.SetArgs([]string{"--detach", "alpine"})
		cmd.SetIn(in)
		cmd.SetOut(out)
		cmd.SetErr(errOut)

		err := cmd.Execute()
		require.Error(t, err)
		fake.AssertNotCalled(t, "ContainerStart")
	})

	t.Run("container start failure returns error", func(t *testing.T) {
		fake := mock.NewFakeClient(configmocks.NewBlankConfig())
		fake.SetupContainerCreate()
		fake.SetupCopyToContainer()
		fake.FakeAPI.ContainerStartFn = func(_ context.Context, _ string, _ moby.ContainerStartOptions) (moby.ContainerStartResult, error) {
			return moby.ContainerStartResult{}, fmt.Errorf("port already in use")
		}

		f, in, out, errOut := testFactory(t, fake)
		cmd := NewCmdRun(f, nil)

		cmd.SetArgs([]string{"--detach", "alpine"})
		cmd.SetIn(in)
		cmd.SetOut(out)
		cmd.SetErr(errOut)

		err := cmd.Execute()
		require.Error(t, err)
		fake.AssertCalled(t, "ContainerCreate")
	})

	t.Run("non-interactive @ with no project image returns error", func(t *testing.T) {
		// With no project image and no default image fallback, @ should fail
		// with a "no image found" message guiding the user.
		testCfg := configmocks.NewFromString(`
workspace: { default_mode: "bind" }
security: { enable_host_proxy: false }
`, "")
		testCfg.GetProjectIgnoreFileFunc = func() (string, error) {
			return filepath.Join(os.TempDir(), testCfg.ClawkerIgnoreName()), nil
		}
		testCfg.GetProjectRootFunc = func() (string, error) {
			return os.TempDir(), nil
		}
		fake := mock.NewFakeClient(testCfg)
		fake.SetupImageList() // empty — no project image found

		tio, in, out, errOut := iostreams.Test() // non-interactive
		f := &cmdutil.Factory{
			IOStreams: tio,
			Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
			TUI:       tui.NewTUI(tio),
			Client: func(_ context.Context) (*docker.Client, error) {
				return fake.Client, nil
			},
			Config: func() (config.Config, error) {
				return testCfg, nil
			},
			HostProxy: func() hostproxy.HostProxyService {
				return hostproxytest.NewMockManager()
			},
			Prompter: func() *prompter.Prompter { return nil },
		}

		cmd := NewCmdRun(f, nil)
		cmd.SetArgs([]string{"--detach", "@"})
		cmd.SetIn(in)
		cmd.SetOut(out)
		cmd.SetErr(errOut)

		err := cmd.Execute()
		require.ErrorIs(t, err, cmdutil.SilentError)

		errOutput := errOut.String()
		require.Contains(t, errOutput, "No image specified")
		require.Contains(t, errOutput, "no project image found")

		fake.AssertNotCalled(t, "ContainerCreate")
	})
}
