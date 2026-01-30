package cp

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdCp(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   CpOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "copy from container",
			input:    "mycontainer:/app/file.txt ./file.txt",
			wantOpts: CpOptions{Src: "mycontainer:/app/file.txt", Dst: "./file.txt"},
		},
		{
			name:     "copy to container",
			input:    "./file.txt mycontainer:/app/file.txt",
			wantOpts: CpOptions{Src: "./file.txt", Dst: "mycontainer:/app/file.txt"},
		},
		{
			name:     "archive flag",
			input:    "-a mycontainer:/app ./app",
			wantOpts: CpOptions{Archive: true, Src: "mycontainer:/app", Dst: "./app"},
		},
		{
			name:     "follow-link flag",
			input:    "-L mycontainer:/app ./app",
			wantOpts: CpOptions{FollowLink: true, Src: "mycontainer:/app", Dst: "./app"},
		},
		{
			name:     "copy-uidgid flag",
			input:    "--copy-uidgid mycontainer:/app ./app",
			wantOpts: CpOptions{CopyUIDGID: true, Src: "mycontainer:/app", Dst: "./app"},
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
			name:     "agent flag with container path",
			input:    "--agent ralph:/app/file.txt ./file.txt",
			wantOpts: CpOptions{Agent: true, Src: "ralph:/app/file.txt", Dst: "./file.txt"},
		},
		{
			name:     "agent flag copy to container",
			input:    "--agent ./file.txt ralph:/app/file.txt",
			wantOpts: CpOptions{Agent: true, Src: "./file.txt", Dst: "ralph:/app/file.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *CpOptions
			cmd := NewCmdCp(f, func(_ context.Context, opts *CpOptions) error {
				gotOpts = opts
				return nil
			})

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
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.Agent, gotOpts.Agent)
			require.Equal(t, tt.wantOpts.Archive, gotOpts.Archive)
			require.Equal(t, tt.wantOpts.FollowLink, gotOpts.FollowLink)
			require.Equal(t, tt.wantOpts.CopyUIDGID, gotOpts.CopyUIDGID)
			require.Equal(t, tt.wantOpts.Src, gotOpts.Src)
			require.Equal(t, tt.wantOpts.Dst, gotOpts.Dst)
		})
	}
}

func TestCmdCp_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdCp(f, nil)

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

func TestCmdCp_ArgsParsing(t *testing.T) {
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

			var gotOpts *CpOptions
			cmd := NewCmdCp(f, func(_ context.Context, opts *CpOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantSrc, gotOpts.Src)
			require.Equal(t, tt.wantDst, gotOpts.Dst)
		})
	}
}
