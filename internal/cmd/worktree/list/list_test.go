package list

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testIOStreams creates an IOStreams instance for testing with captured buffers.
func testIOStreams() (*iostreams.IOStreams, *bytes.Buffer, *bytes.Buffer) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	ios := &iostreams.IOStreams{
		In:     &bytes.Buffer{},
		Out:    outBuf,
		ErrOut: errBuf,
	}
	return ios, outBuf, errBuf
}

func TestListRun_NotInProject(t *testing.T) {
	ios, _, _ := testIOStreams()

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("should not be called")
		},
	}

	err := listRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project")
}

func TestListRun_GitManagerError(t *testing.T) {
	ios, _, _ := testIOStreams()

	// Create a project that appears to be found
	proj := &config.Project{
		Project: "test-project",
	}

	opts := &ListOptions{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(proj, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("git init failed")
		},
	}

	err := listRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initializing git")
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1 * time.Minute), "1 minute ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "1 hour ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1 day ago", now.Add(-25 * time.Hour), "1 day ago"},
		{"3 days ago", now.Add(-75 * time.Hour), "3 days ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(tt.time)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatTimeAgo_OldDate(t *testing.T) {
	// Test dates older than a week
	oldTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	result := formatTimeAgo(oldTime)
	assert.Equal(t, "Jan 15, 2024", result)
}

// Verify WorktreeInfo fields are used correctly
func TestWorktreeInfoFields(t *testing.T) {
	info := git.WorktreeInfo{
		Name:       "feature-branch",
		Path:       "/path/to/worktree",
		Head:       plumbing.NewHash("abc123def456789012345678901234567890abcd"),
		Branch:     "feature-branch",
		IsDetached: false,
		Error:      nil,
	}

	assert.Equal(t, "feature-branch", info.Name)
	assert.Equal(t, "/path/to/worktree", info.Path)
	assert.False(t, info.IsDetached)
	assert.Nil(t, info.Error)
	assert.Equal(t, "abc123d", info.Head.String()[:7])
}

func TestNewCmdList(t *testing.T) {
	ios, _, _ := testIOStreams()

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
		GitManager: func() (*git.GitManager, error) {
			return nil, errors.New("not configured")
		},
	}

	cmd := NewCmdList(f, nil)

	assert.Equal(t, "list", cmd.Use)
	assert.Contains(t, cmd.Aliases, "ls")
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Verify flags
	quietFlag := cmd.Flags().Lookup("quiet")
	assert.NotNil(t, quietFlag)
	assert.Equal(t, "q", quietFlag.Shorthand)
}
