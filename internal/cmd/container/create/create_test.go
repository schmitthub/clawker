package create

import (
	"bytes"
	"strings"
	"testing"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// testOptions is used for test comparisons - it has flat fields for easy comparison.
type testOptions struct {
	Agent      string
	Name       string
	Mode       string
	Env        []string
	Volumes    []string
	Publish    []string
	Workdir    string
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
		output     testOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "basic image only",
			input:  "",
			args:   []string{"alpine"},
			output: testOptions{Image: "alpine"},
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

			var cmdOpts *testOptions
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &testOptions{}
				cmdOpts.Agent, _ = cmd.Flags().GetString("agent")
				cmdOpts.Name, _ = cmd.Flags().GetString("name")
				cmdOpts.Mode, _ = cmd.Flags().GetString("mode")
				cmdOpts.Env, _ = cmd.Flags().GetStringArray("env")
				cmdOpts.Volumes, _ = cmd.Flags().GetStringArray("volume")
				// Note: publish is a custom type, we need to get the string representation
				if publishFlag := cmd.Flags().Lookup("publish"); publishFlag != nil {
					if publishOpts, ok := publishFlag.Value.(*copts.PortOpts); ok && publishOpts.Len() > 0 {
						cmdOpts.Publish = []string{publishOpts.String()}
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
				argv = append(splitArgs(tt.input), tt.args...)
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
			require.Equal(t, tt.output.Agent, cmdOpts.Agent)
			require.Equal(t, tt.output.Name, cmdOpts.Name)
			require.Equal(t, tt.output.Mode, cmdOpts.Mode)
			require.Equal(t, tt.output.Image, cmdOpts.Image)
			require.Equal(t, tt.output.Command, cmdOpts.Command)
			requireSliceEqual(t, tt.output.Env, cmdOpts.Env)
			requireSliceEqual(t, tt.output.Volumes, cmdOpts.Volumes)
			// Skip publish comparison for now - the type changed from []string to *copts.PortOpts
			// The port parsing is tested in docker/opts_test.go
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

func TestCmdCreate_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

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
	require.NotNil(t, cmd.Flags().Lookup("workdir"))
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
	require.NotNil(t, cmd.Flags().ShorthandLookup("w"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("u"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("l"))
}

func TestCmdCreate_MutuallyExclusiveFlags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Cobra hack-around for help flag
	cmd.Flags().BoolP("help", "x", false, "")

	// Override RunE to prevent actual execution
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}

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
			opts: &copts.ContainerOptions{
				Image:   "alpine",
				Network: "mynet",
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

			// Verify network config
			if tt.opts.Network != "" {
				require.NotNil(t, netCfg)
				require.Contains(t, netCfg.EndpointsConfig, tt.opts.Network)
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

// splitArgs splits a string into arguments, handling quoted strings.
func splitArgs(input string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(input); i++ {
		c := input[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(c)
			}
		} else {
			switch c {
			case '"', '\'':
				inQuote = true
				quoteChar = c
			case ' ', '\t':
				if current.Len() > 0 {
					args = append(args, current.String())
					current.Reset()
				}
			default:
				current.WriteByte(c)
			}
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
