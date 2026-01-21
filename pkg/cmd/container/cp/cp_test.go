package cp

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "copy from container",
			input:    "mycontainer:/app/file.txt ./file.txt",
			wantOpts: Options{},
		},
		{
			name:     "copy to container",
			input:    "./file.txt mycontainer:/app/file.txt",
			wantOpts: Options{},
		},
		{
			name:     "archive flag",
			input:    "-a mycontainer:/app ./app",
			wantOpts: Options{Archive: true},
		},
		{
			name:     "follow-link flag",
			input:    "-L mycontainer:/app ./app",
			wantOpts: Options{FollowLink: true},
		},
		{
			name:     "copy-uidgid flag",
			input:    "--copy-uidgid mycontainer:/app ./app",
			wantOpts: Options{CopyUIDGID: true},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "accepts 2 arg(s), received 0",
		},
		{
			name:       "only one argument",
			input:      "mycontainer:/app",
			wantErr:    true,
			wantErrMsg: "accepts 2 arg(s), received 1",
		},
		{
			name:     "agent flag with colon path",
			input:    "--agent ralph :/app/file.txt ./file.txt",
			wantOpts: Options{Agent: "ralph"},
		},
		{
			name:     "agent flag with named container path",
			input:    "--agent ralph writer:/app/file.txt ./file.txt",
			wantOpts: Options{Agent: "ralph"},
		},
		{
			name:     "agent flag with destination colon path",
			input:    "--agent ralph ./file.txt :/app/file.txt",
			wantOpts: Options{Agent: "ralph"},
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
				cmdOpts.Archive, _ = cmd.Flags().GetBool("archive")
				cmdOpts.FollowLink, _ = cmd.Flags().GetBool("follow-link")
				cmdOpts.CopyUIDGID, _ = cmd.Flags().GetBool("copy-uidgid")
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := testutil.SplitArgs(tt.input)

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantOpts.Agent, cmdOpts.Agent)
			require.Equal(t, tt.wantOpts.Archive, cmdOpts.Archive)
			require.Equal(t, tt.wantOpts.FollowLink, cmdOpts.FollowLink)
			require.Equal(t, tt.wantOpts.CopyUIDGID, cmdOpts.CopyUIDGID)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Contains(t, cmd.Use, "cp")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("archive"))
	require.NotNil(t, cmd.Flags().Lookup("follow-link"))
	require.NotNil(t, cmd.Flags().Lookup("copy-uidgid"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("L"))
}

func TestParseContainerPath(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantContainer   string
		wantPath        string
		wantIsContainer bool
	}{
		{
			name:            "container path",
			input:           "mycontainer:/app/file.txt",
			wantContainer:   "mycontainer",
			wantPath:        "/app/file.txt",
			wantIsContainer: true,
		},
		{
			name:            "full container name",
			input:           "clawker.myapp.ralph:/workspace/config.json",
			wantContainer:   "clawker.myapp.ralph",
			wantPath:        "/workspace/config.json",
			wantIsContainer: true,
		},
		{
			name:            "local path",
			input:           "./file.txt",
			wantContainer:   "",
			wantPath:        "./file.txt",
			wantIsContainer: false,
		},
		{
			name:            "absolute local path",
			input:           "/home/user/file.txt",
			wantContainer:   "",
			wantPath:        "/home/user/file.txt",
			wantIsContainer: false,
		},
		{
			name:            "container with root path",
			input:           "mycontainer:/",
			wantContainer:   "mycontainer",
			wantPath:        "/",
			wantIsContainer: true,
		},
		{
			name:            "stdout special path",
			input:           "-",
			wantContainer:   "",
			wantPath:        "-",
			wantIsContainer: false,
		},
		{
			name:            "colon path syntax for agent flag",
			input:           ":/app/file.txt",
			wantContainer:   "",
			wantPath:        "/app/file.txt",
			wantIsContainer: true,
		},
		{
			name:            "colon path with root",
			input:           ":/",
			wantContainer:   "",
			wantPath:        "/",
			wantIsContainer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container, path, isContainer := parseContainerPath(tt.input)
			require.Equal(t, tt.wantContainer, container)
			require.Equal(t, tt.wantPath, path)
			require.Equal(t, tt.wantIsContainer, isContainer)
		})
	}
}

func TestCmd_ArgsParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantSrc string
		wantDst string
	}{
		{
			name:    "copy from container",
			args:    []string{"mycontainer:/app/file.txt", "./file.txt"},
			wantSrc: "mycontainer:/app/file.txt",
			wantDst: "./file.txt",
		},
		{
			name:    "copy to container",
			args:    []string{"./file.txt", "mycontainer:/app/file.txt"},
			wantSrc: "./file.txt",
			wantDst: "mycontainer:/app/file.txt",
		},
		{
			name:    "stream to stdout",
			args:    []string{"mycontainer:/app", "-"},
			wantSrc: "mycontainer:/app",
			wantDst: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmd(f)

			var capturedSrc, capturedDst string

			// Override RunE to capture args
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if len(args) >= 2 {
					capturedSrc = args[0]
					capturedDst = args[1]
				}
				return nil
			}

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.Equal(t, tt.wantSrc, capturedSrc)
			require.Equal(t, tt.wantDst, capturedDst)
		})
	}
}
