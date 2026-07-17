package remove

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/plugin/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdRemove_DefaultScope(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var captured *RemoveOptions
	cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
		captured = opts
		return nil
	})
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "user", captured.Scope)
	assert.Equal(t, tio, captured.IOStreams)
}

func TestNewCmdRemove_CustomScope(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var captured *RemoveOptions
	cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
		captured = opts
		return nil
	})
	cmd.SetArgs([]string{"--scope", "local"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "local", captured.Scope)
}

func TestNewCmdRemove_Aliases(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdRemove(f, func(_ context.Context, _ *RemoveOptions) error { return nil })
	assert.Contains(t, cmd.Aliases, "uninstall")
	assert.Contains(t, cmd.Aliases, "rm")
}

func TestNewCmdRemove_RejectsArgs(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	cmd := NewCmdRemove(f, func(_ context.Context, _ *RemoveOptions) error { return nil })
	cmd.SetArgs([]string{"extra"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	assert.Error(t, err)
}

func TestRemoveRun_CLINotFound(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams: tio,
		Scope:     "user",
		Harness:   shared.HarnessClaude,
		CheckCLI: func() error {
			return fmt.Errorf("claude CLI not found in PATH")
		},
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, _ ...string) error {
			t.Fatal("RunClaude should not be called when CLI check fails")
			return nil
		},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRemoveRun_RemoveFails(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	opts := &RemoveOptions{
		IOStreams: tio,
		Scope:     "user",
		Harness:   shared.HarnessClaude,
		CheckCLI:  func() error { return nil },
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, _ ...string) error {
			return fmt.Errorf("claude plugin remove exited with status 1")
		},
	}

	err := removeRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing plugin")
}

func TestRemoveRun_Success(t *testing.T) {
	tio, _, _, stderr := iostreams.Test()
	var calls [][]string
	opts := &RemoveOptions{
		IOStreams: tio,
		Scope:     "project",
		Harness:   shared.HarnessClaude,
		CheckCLI:  func() error { return nil },
		RunClaude: func(_ context.Context, _ *iostreams.IOStreams, args ...string) error {
			calls = append(calls, args)
			return nil
		},
	}

	err := removeRun(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"plugin", "remove", "--scope", "project", shared.PluginName}, calls[0])
	assert.Contains(t, stderr.String(), "removed successfully")
}

func TestRemoveRun_CopyLaneRemovesSkills(t *testing.T) {
	tio, _, stdout, _ := iostreams.Test()
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dst, "clawker-support"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "clawker-support", "SKILL.md"), []byte("body"), 0o644))

	opts := &RemoveOptions{
		IOStreams: tio,
		Scope:     "user",
		Harness:   shared.HarnessPi,
		CheckCLI:  func() error { t.Fatal("claude CLI must not be consulted on the copy lane"); return nil },
		RunClaude: nil,
		FetchSkills: func(_ context.Context) (*shared.FetchedSkills, error) {
			return &shared.FetchedSkills{Dir: t.TempDir(), Names: []string{"clawker-support"}, Cleanup: func() {}}, nil
		},
		SkillsDir: func(string) (string, error) { return dst, nil },
	}

	err := removeRun(context.Background(), opts)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(dst, "clawker-support"))
	assert.Contains(t, stdout.String(), "Removed skill clawker-support from pi")
}
