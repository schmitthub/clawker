package run

import (
	"bytes"
	"context"
	"strings"
	"testing"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// testOptions is a flat struct for test comparisons
// It mirrors the fields from the embedded copts.ContainerOptions plus run-specific fields
type testOptions struct {
	// From copts.ContainerOptions
	Agent      string
	Name       string
	Env        []string
	Volumes    []string
	Publish    []string // String representation for comparison
	Workdir    string
	User       string
	Entrypoint string
	TTY        bool
	Stdin      bool
	Network    string
	Labels     []string
	AutoRemove bool
	Mode       string
	Image      string
	Command    []string

	// Run-specific
	Detach bool
}

func TestNewCmdRun(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     testOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:    "no image specified",
			input:   "",
			args:    []string{},
			output:  testOptions{},
			wantErr: true, // RequiresMinArgs(1) rejects empty args
		},
		{
			name:   "basic image only",
			input:  "",
			args:   []string{"alpine"},
			output: testOptions{Image: "alpine"},
		},
		{
			name:   "image with tag",
			input:  "",
			args:   []string{"alpine:latest"},
			output: testOptions{Image: "alpine:latest"},
		},
		{
			name:   "image with command",
			input:  "",
			args:   []string{"alpine", "echo", "hello"},
			output: testOptions{Image: "alpine", Command: []string{"echo", "hello"}},
		},
		{
			name:   "with agent flag",
			input:  "--agent myagent",
			args:   []string{"alpine"},
			output: testOptions{Agent: "myagent", Image: "alpine"},
		},
		{
			name:   "with name flag",
			input:  "--name mycontainer",
			args:   []string{"alpine"},
			output: testOptions{Name: "mycontainer", Image: "alpine"},
		},
		{
			name:   "with detach flag",
			input:  "--detach",
			args:   []string{"alpine"},
			output: testOptions{Detach: true, Image: "alpine"},
		},
		{
			name:   "with environment variable",
			input:  "-e FOO=bar",
			args:   []string{"alpine"},
			output: testOptions{Env: []string{"FOO=bar"}, Image: "alpine"},
		},
		{
			name:   "with multiple env vars",
			input:  "-e FOO=bar -e BAZ=qux",
			args:   []string{"alpine"},
			output: testOptions{Env: []string{"FOO=bar", "BAZ=qux"}, Image: "alpine"},
		},
		{
			name:   "with volume",
			input:  "-v /host:/container",
			args:   []string{"alpine"},
			output: testOptions{Volumes: []string{"/host:/container"}, Image: "alpine"},
		},
		{
			name:   "with port",
			input:  "-p 8080:80",
			args:   []string{"alpine"},
			output: testOptions{Publish: []string{"8080:80"}, Image: "alpine"},
		},
		{
			name:   "with workdir",
			input:  "-w /app",
			args:   []string{"alpine"},
			output: testOptions{Workdir: "/app", Image: "alpine"},
		},
		{
			name:   "with user",
			input:  "-u nobody",
			args:   []string{"alpine"},
			output: testOptions{User: "nobody", Image: "alpine"},
		},
		{
			name:   "with entrypoint",
			input:  "--entrypoint /bin/sh",
			args:   []string{"alpine"},
			output: testOptions{Entrypoint: "/bin/sh", Image: "alpine"},
		},
		{
			name:   "with tty",
			input:  "-t",
			args:   []string{"alpine"},
			output: testOptions{TTY: true, Image: "alpine"},
		},
		{
			name:   "with interactive",
			input:  "-i",
			args:   []string{"alpine"},
			output: testOptions{Stdin: true, Image: "alpine"},
		},
		{
			name:   "with tty and interactive",
			input:  "-it",
			args:   []string{"alpine"},
			output: testOptions{TTY: true, Stdin: true, Image: "alpine"},
		},
		{
			name:   "with network",
			input:  "--network mynet",
			args:   []string{"alpine"},
			output: testOptions{Network: "mynet", Image: "alpine"},
		},
		{
			name:   "with label",
			input:  "-l foo=bar",
			args:   []string{"alpine"},
			output: testOptions{Labels: []string{"foo=bar"}, Image: "alpine"},
		},
		{
			name:   "with auto-remove",
			input:  "--rm",
			args:   []string{"alpine"},
			output: testOptions{AutoRemove: true, Image: "alpine"},
		},
		{
			name:   "interactive detached with rm",
			input:  "-it --detach --rm",
			args:   []string{"alpine", "sh"},
			output: testOptions{TTY: true, Stdin: true, Detach: true, AutoRemove: true, Image: "alpine", Command: []string{"sh"}},
		},
		{
			name:    "no image requires error",
			input:   "",
			args:    []string{},
			output:  testOptions{},
			wantErr: true, // RequiresMinArgs(1) rejects empty args
		},
		// @ symbol tests - triggers default image resolution at runtime
		{
			name:   "@ symbol as image",
			input:  "",
			args:   []string{"@"},
			output: testOptions{Image: "@"},
		},
		{
			name:   "@ symbol with agent flag",
			input:  "--agent ralph",
			args:   []string{"@"},
			output: testOptions{Agent: "ralph", Image: "@"},
		},
		{
			name:   "@ symbol with common flags",
			input:  "-it --rm",
			args:   []string{"@"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Image: "@"},
		},
		{
			name:   "@ symbol with command",
			input:  "",
			args:   []string{"@", "echo", "hello"},
			output: testOptions{Image: "@", Command: []string{"echo", "hello"}},
		},
		{
			name:   "@ symbol with mode",
			input:  "--agent dev --mode=snapshot",
			args:   []string{"@"},
			output: testOptions{Agent: "dev", Mode: "snapshot", Image: "@"},
		},
		{
			name:   "with mode bind",
			input:  "--agent dev --mode=bind",
			args:   []string{"alpine"},
			output: testOptions{Agent: "dev", Mode: "bind", Image: "alpine"},
		},
		{
			name:   "with mode snapshot",
			input:  "--agent dev --mode=snapshot",
			args:   []string{"alpine"},
			output: testOptions{Agent: "dev", Mode: "snapshot", Image: "alpine"},
		},
		{
			name:   "with mode and other flags",
			input:  "-it --agent sandbox --mode=snapshot --rm",
			args:   []string{"alpine", "sh"},
			output: testOptions{TTY: true, Stdin: true, Agent: "sandbox", Mode: "snapshot", AutoRemove: true, Image: "alpine", Command: []string{"sh"}},
		},
		{
			name:   "flags after image passed as command",
			input:  "-it --rm",
			args:   []string{"alpine", "--version"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Image: "alpine", Command: []string{"--version"}},
		},
		{
			name:   "mixed clawker and container flags",
			input:  "-it --rm -e FOO=bar",
			args:   []string{"alpine", "-p", "prompt"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Env: []string{"FOO=bar"}, Image: "alpine", Command: []string{"-p", "prompt"}},
		},
		{
			name:   "claude flags passthrough",
			input:  "-it --rm",
			args:   []string{"clawker-image:latest", "--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Image: "clawker-image:latest", Command: []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"}},
		},
		{
			name:   "flags only as command with -- separator",
			input:  "-it --rm --agent ralph --",
			args:   []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Agent: "ralph", Command: []string{"--allow-dangerously-skip-permissions", "-p", "Fix bugs"}},
		},
		{
			name:   "arg starting with dash treated as command after -- separator",
			input:  "-it --rm --",
			args:   []string{"-unusual-image:v1"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Command: []string{"-unusual-image:v1"}},
		},
		{
			name:   "multiple flag-value pairs after image",
			input:  "-it --rm",
			args:   []string{"alpine", "--flag1", "value1", "--flag2", "value2"},
			output: testOptions{TTY: true, Stdin: true, AutoRemove: true, Image: "alpine", Command: []string{"--flag1", "value1", "--flag2", "value2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts testOptions
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts.Agent, _ = cmd.Flags().GetString("agent")
				cmdOpts.Name, _ = cmd.Flags().GetString("name")
				cmdOpts.Detach, _ = cmd.Flags().GetBool("detach")
				cmdOpts.Mode, _ = cmd.Flags().GetString("mode")
				cmdOpts.Env, _ = cmd.Flags().GetStringArray("env")
				cmdOpts.Volumes, _ = cmd.Flags().GetStringArray("volume")
				// Note: Publish is now a PortOpts, but flag value is still accessible
				if publishFlag := cmd.Flags().Lookup("publish"); publishFlag != nil {
					if portOpts, ok := publishFlag.Value.(*copts.PortOpts); ok {
						cmdOpts.Publish = portOpts.GetAsStrings()
					}
				}
				cmdOpts.Workdir, _ = cmd.Flags().GetString("workdir")
				cmdOpts.User, _ = cmd.Flags().GetString("user")
				cmdOpts.Entrypoint, _ = cmd.Flags().GetString("entrypoint")
				cmdOpts.TTY, _ = cmd.Flags().GetBool("tty")
				cmdOpts.Stdin, _ = cmd.Flags().GetBool("interactive")
				cmdOpts.Network, _ = cmd.Flags().GetString("network")
				cmdOpts.Labels, _ = cmd.Flags().GetStringArray("label")
				cmdOpts.AutoRemove, _ = cmd.Flags().GetBool("rm")
				if len(args) > 0 {
					// Match the actual command logic: if first arg starts with "-",
					// it's a container command, not an image name
					if strings.HasPrefix(args[0], "-") {
						cmdOpts.Command = args
					} else {
						cmdOpts.Image = args[0]
						if len(args) > 1 {
							cmdOpts.Command = args[1:]
						}
					}
				}
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := tt.args
			if tt.input != "" {
				argv = append(testutil.SplitArgs(tt.input), tt.args...)
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
			require.Equal(t, tt.output.Agent, cmdOpts.Agent)
			require.Equal(t, tt.output.Name, cmdOpts.Name)
			require.Equal(t, tt.output.Detach, cmdOpts.Detach)
			require.Equal(t, tt.output.Mode, cmdOpts.Mode)
			require.Equal(t, tt.output.Image, cmdOpts.Image)
			require.Equal(t, tt.output.Command, cmdOpts.Command)
			requireSliceEqual(t, tt.output.Env, cmdOpts.Env)
			requireSliceEqual(t, tt.output.Volumes, cmdOpts.Volumes)
			requireSliceEqual(t, tt.output.Publish, cmdOpts.Publish)
			require.Equal(t, tt.output.Workdir, cmdOpts.Workdir)
			require.Equal(t, tt.output.User, cmdOpts.User)
			require.Equal(t, tt.output.Entrypoint, cmdOpts.Entrypoint)
			require.Equal(t, tt.output.TTY, cmdOpts.TTY)
			require.Equal(t, tt.output.Stdin, cmdOpts.Stdin)
			require.Equal(t, tt.output.Network, cmdOpts.Network)
			requireSliceEqual(t, tt.output.Labels, cmdOpts.Labels)
			require.Equal(t, tt.output.AutoRemove, cmdOpts.AutoRemove)
		})
	}
}

// TestCmdRun_NoDetachShorthand verifies --detach does NOT have -d shorthand (conflicts with --debug)
func TestCmdRun_NoDetachShorthand(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Verify --detach does NOT have -d shorthand (conflicts with --debug)
	require.Nil(t, cmd.Flags().ShorthandLookup("d"))
}

// TestBuildConfigs tests the shared BuildConfigs function from copts package
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
			name: "with valid port",
			opts: func() *copts.ContainerOptions {
				o := copts.NewContainerOptions()
				o.Image = "alpine"
				o.Publish.Set("8080:80")
				return o
			}(),
		},
		// Note: Invalid port validation happens in PortOpts.Set(), not in BuildConfigs.
		// See TestPortOpts in internal/docker/opts_test.go for port validation tests.
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
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Network: "mynet",
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with auto-remove",
			opts: &copts.ContainerOptions{
				Image:      "alpine",
				AutoRemove: true,
				Publish:    copts.NewPortOpts(),
			},
		},
		{
			name: "with entrypoint",
			opts: &copts.ContainerOptions{
				Image:      "alpine",
				Entrypoint: "/custom/entrypoint",
				Publish:    copts.NewPortOpts(),
			},
		},
		{
			name: "with volumes/binds",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Volumes: []string{"/host/path:/container/path", "/another:/mount"},
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with workdir",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Workdir: "/app",
				Publish: copts.NewPortOpts(),
			},
		},
		{
			name: "with user",
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				User:    "nobody",
				Publish: copts.NewPortOpts(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, hostCfg, netCfg, err := tt.opts.BuildConfigs(nil, config.DefaultConfig())

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

			// Verify workdir
			if tt.opts.Workdir != "" {
				require.Equal(t, tt.opts.Workdir, cfg.WorkingDir)
			}

			// Verify user
			if tt.opts.User != "" {
				require.Equal(t, tt.opts.User, cfg.User)
			}

			// Verify labels
			if len(tt.opts.Labels) > 0 {
				require.NotNil(t, cfg.Labels)
			}

			// Verify network config
			if tt.opts.Network != "" {
				require.NotNil(t, netCfg)
				require.Contains(t, netCfg.EndpointsConfig, tt.opts.Network)
			}
		})
	}
}

func TestBuildConfigs_CapAdd(t *testing.T) {
	opts := &copts.ContainerOptions{
		Image:   "alpine",
		Publish: copts.NewPortOpts(),
	}
	projectCfg := &config.Config{
		Project: "test",
		Security: config.SecurityConfig{
			CapAdd: []string{"NET_ADMIN", "SYS_PTRACE"},
		},
	}

	_, hostCfg, _, err := opts.BuildConfigs(nil, projectCfg)
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
	// Tests for @ symbol resolution (requires mock Docker client)
	t.Run("@ symbol resolution", func(t *testing.T) {
		tests := []struct {
			name          string
			projectName   string
			defaultImage  string
			mockImages    []string // Images to return from mock ImageList
			wantReference string
			wantSource    cmdutil.ImageSource
			wantNil       bool // Expect nil result (no resolution)
		}{
			{
				name:          "@ resolves to project image when exists",
				projectName:   "myproject",
				defaultImage:  "alpine:latest",
				mockImages:    []string{"clawker-myproject:latest"},
				wantReference: "clawker-myproject:latest",
				wantSource:    cmdutil.ImageSourceProject,
			},
			{
				name:          "@ resolves to default image when no project image",
				projectName:   "myproject",
				defaultImage:  "node:20-slim",
				mockImages:    []string{}, // No project images
				wantReference: "node:20-slim",
				wantSource:    cmdutil.ImageSourceDefault,
			},
			{
				name:         "@ returns nil when no default available",
				projectName:  "myproject",
				defaultImage: "", // No default configured
				mockImages:   []string{},
				wantNil:      true,
			},
			{
				name:          "@ prefers project image over default",
				projectName:   "myproject",
				defaultImage:  "alpine:latest",
				mockImages:    []string{"clawker-myproject:latest", "other:tag"},
				wantReference: "clawker-myproject:latest",
				wantSource:    cmdutil.ImageSourceProject,
			},
			{
				name:          "@ ignores non-latest project images",
				projectName:   "myproject",
				defaultImage:  "alpine:latest",
				mockImages:    []string{"clawker-myproject:v1.0"}, // No :latest tag
				wantReference: "alpine:latest",
				wantSource:    cmdutil.ImageSourceDefault,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()

				// Create mock Docker client
				m := testutil.NewMockDockerClient(t)

				// Build mock image summaries
				var imageSummaries []whail.ImageSummary
				for _, ref := range tt.mockImages {
					imageSummaries = append(imageSummaries, whail.ImageSummary{
						RepoTags: []string{ref},
					})
				}

				// Set expectation: ImageList will be called to find project images
				m.Mock.EXPECT().
					ImageList(gomock.Any(), gomock.Any()).
					Return(whail.ImageListResult{Items: imageSummaries}, nil).
					AnyTimes()

				// Build config and settings
				cfg := &config.Config{
					Project: tt.projectName,
				}
				var settings *config.Settings
				if tt.defaultImage != "" {
					settings = &config.Settings{
						Project: config.ProjectDefaults{
							DefaultImage: tt.defaultImage,
						},
					}
				}

				// Call the resolution function
				result, err := cmdutil.ResolveImageWithSource(ctx, m.Client, cfg, settings)
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

				var capturedImage string
				cmd := NewCmd(f)

				// Override RunE to capture the image instead of executing
				cmd.RunE = func(cmd *cobra.Command, args []string) error {
					if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
						capturedImage = args[0]
					}
					return nil
				}

				// Cobra hack-around for help flag
				cmd.Flags().BoolP("help", "x", false, "")

				cmd.SetArgs([]string{tt.imageArg})
				cmd.SetIn(&bytes.Buffer{})
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})

				_, err := cmd.ExecuteC()
				require.NoError(t, err)
				require.Equal(t, tt.wantImage, capturedImage, "explicit image should pass through unchanged")
			})
		}
	})

	// Test for empty image (no image argument provided)
	// NOTE: This test documents a known bug in run.go where args[0] causes panic when args is empty.
	// The test is written to expect proper error handling, but will fail/panic until the bug is fixed.
	// DO NOT FIX THE BUG IN THIS PR - it should be addressed separately.
	t.Run("empty image shows error", func(t *testing.T) {
		f := &cmdutil.Factory{}
		cmd := NewCmd(f)

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
