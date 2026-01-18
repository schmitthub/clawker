package create

import (
	"bytes"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdCreate(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "basic image only",
			input:  "",
			args:   []string{"alpine"},
			output: Options{Image: "alpine"},
		},
		{
			name:   "image with command",
			input:  "",
			args:   []string{"alpine", "echo", "hello"},
			output: Options{Image: "alpine", Command: []string{"echo", "hello"}},
		},
		{
			name:   "with agent flag",
			input:  "--agent myagent",
			args:   []string{"alpine"},
			output: Options{Agent: "myagent", Image: "alpine"},
		},
		{
			name:   "with name flag",
			input:  "--name mycontainer",
			args:   []string{"alpine"},
			output: Options{Name: "mycontainer", Image: "alpine"},
		},
		{
			name:   "with environment variable",
			input:  "-e FOO=bar",
			args:   []string{"alpine"},
			output: Options{Env: []string{"FOO=bar"}, Image: "alpine"},
		},
		{
			name:   "with multiple env vars",
			input:  "-e FOO=bar -e BAZ=qux",
			args:   []string{"alpine"},
			output: Options{Env: []string{"FOO=bar", "BAZ=qux"}, Image: "alpine"},
		},
		{
			name:   "with volume",
			input:  "-v /host:/container",
			args:   []string{"alpine"},
			output: Options{Volumes: []string{"/host:/container"}, Image: "alpine"},
		},
		{
			name:   "with port",
			input:  "-p 8080:80",
			args:   []string{"alpine"},
			output: Options{Publish: []string{"8080:80"}, Image: "alpine"},
		},
		{
			name:   "with workdir",
			input:  "-w /app",
			args:   []string{"alpine"},
			output: Options{Workdir: "/app", Image: "alpine"},
		},
		{
			name:   "with user",
			input:  "-u nobody",
			args:   []string{"alpine"},
			output: Options{User: "nobody", Image: "alpine"},
		},
		{
			name:   "with entrypoint",
			input:  "--entrypoint /bin/sh",
			args:   []string{"alpine"},
			output: Options{Entrypoint: "/bin/sh", Image: "alpine"},
		},
		{
			name:   "with tty",
			input:  "-t",
			args:   []string{"alpine"},
			output: Options{TTY: true, Image: "alpine"},
		},
		{
			name:   "with interactive",
			input:  "-i",
			args:   []string{"alpine"},
			output: Options{Stdin: true, Image: "alpine"},
		},
		{
			name:   "with tty and interactive",
			input:  "-it",
			args:   []string{"alpine"},
			output: Options{TTY: true, Stdin: true, Image: "alpine"},
		},
		{
			name:   "with network",
			input:  "--network mynet",
			args:   []string{"alpine"},
			output: Options{Network: "mynet", Image: "alpine"},
		},
		{
			name:   "with label",
			input:  "-l foo=bar",
			args:   []string{"alpine"},
			output: Options{Labels: []string{"foo=bar"}, Image: "alpine"},
		},
		{
			name:   "with auto-remove",
			input:  "--rm",
			args:   []string{"alpine"},
			output: Options{AutoRemove: true, Image: "alpine"},
		},
		{
			name:   "no image (optional)",
			input:  "",
			args:   []string{},
			output: Options{}, // Image is now optional, will be resolved at runtime
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *Options
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &Options{}
				cmdOpts.Agent, _ = cmd.Flags().GetString("agent")
				cmdOpts.Name, _ = cmd.Flags().GetString("name")
				cmdOpts.Env, _ = cmd.Flags().GetStringArray("env")
				cmdOpts.Volumes, _ = cmd.Flags().GetStringArray("volume")
				cmdOpts.Publish, _ = cmd.Flags().GetStringArray("publish")
				cmdOpts.Workdir, _ = cmd.Flags().GetString("workdir")
				cmdOpts.User, _ = cmd.Flags().GetString("user")
				cmdOpts.Entrypoint, _ = cmd.Flags().GetString("entrypoint")
				cmdOpts.TTY, _ = cmd.Flags().GetBool("tty")
				cmdOpts.Stdin, _ = cmd.Flags().GetBool("interactive")
				cmdOpts.Network, _ = cmd.Flags().GetString("network")
				cmdOpts.Labels, _ = cmd.Flags().GetStringArray("label")
				cmdOpts.AutoRemove, _ = cmd.Flags().GetBool("rm")
				if len(args) > 0 {
					cmdOpts.Image = args[0]
				}
				if len(args) > 1 {
					cmdOpts.Command = args[1:]
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

func TestCmdCreate_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "create [OPTIONS] [IMAGE] [COMMAND] [ARG...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("name"))
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
		opts    *Options
		wantErr bool
	}{
		{
			name: "basic config",
			opts: &Options{
				Image: "alpine",
			},
		},
		{
			name: "with tty and stdin",
			opts: &Options{
				Image: "alpine",
				TTY:   true,
				Stdin: true,
			},
		},
		{
			name: "with command",
			opts: &Options{
				Image:   "alpine",
				Command: []string{"echo", "hello"},
			},
		},
		{
			name: "with env vars",
			opts: &Options{
				Image: "alpine",
				Env:   []string{"FOO=bar", "BAZ=qux"},
			},
		},
		{
			name: "with valid port",
			opts: &Options{
				Image:   "alpine",
				Publish: []string{"8080:80"},
			},
		},
		{
			name:    "with invalid port",
			opts:    &Options{Image: "alpine", Publish: []string{"invalid"}},
			wantErr: true,
		},
		{
			name: "with labels",
			opts: &Options{
				Image:  "alpine",
				Labels: []string{"foo=bar", "baz"},
			},
		},
		{
			name: "with network",
			opts: &Options{
				Image:   "alpine",
				Network: "mynet",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, hostCfg, netCfg, err := buildConfigs(tt.opts)

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
