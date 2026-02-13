package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResultOutput_Success(t *testing.T) {
	result := &loop.Result{
		LoopsCompleted: 5,
		ExitReason:     "agent signaled completion",
		Session: &loop.Session{
			TotalTasksCompleted: 3,
			TotalFilesModified:  7,
		},
		FinalStatus: &loop.Status{
			Status: loop.StatusComplete,
		},
	}

	output := NewResultOutput(result)

	assert.Equal(t, 5, output.LoopsCompleted)
	assert.Equal(t, "agent signaled completion", output.ExitReason)
	assert.True(t, output.Success)
	assert.Empty(t, output.Error)
	assert.Equal(t, 3, output.TotalTasksCompleted)
	assert.Equal(t, 7, output.TotalFilesModified)
	assert.Equal(t, loop.StatusComplete, output.FinalStatus)
	assert.False(t, output.RateLimitHit)
}

func TestNewResultOutput_Error(t *testing.T) {
	result := &loop.Result{
		LoopsCompleted: 2,
		ExitReason:     "stagnation: no progress",
		Error:          fmt.Errorf("circuit breaker tripped"),
		Session: &loop.Session{
			TotalTasksCompleted: 1,
			TotalFilesModified:  2,
		},
	}

	output := NewResultOutput(result)

	assert.Equal(t, 2, output.LoopsCompleted)
	assert.Equal(t, "stagnation: no progress", output.ExitReason)
	assert.False(t, output.Success)
	assert.Equal(t, "circuit breaker tripped", output.Error)
}

func TestNewResultOutput_NilSession(t *testing.T) {
	result := &loop.Result{
		LoopsCompleted: 0,
		ExitReason:     "failed to load session",
		Error:          fmt.Errorf("some error"),
	}

	output := NewResultOutput(result)

	assert.Equal(t, 0, output.LoopsCompleted)
	assert.Equal(t, 0, output.TotalTasksCompleted)
	assert.Equal(t, 0, output.TotalFilesModified)
	assert.False(t, output.Success)
}

func TestWriteResult_JSON(t *testing.T) {
	result := &loop.Result{
		LoopsCompleted: 3,
		ExitReason:     "agent signaled completion",
		Session: &loop.Session{
			TotalTasksCompleted: 2,
			TotalFilesModified:  4,
		},
		FinalStatus: &loop.Status{
			Status: loop.StatusComplete,
		},
	}

	var stdout, stderr bytes.Buffer
	format := &cmdutil.FormatFlags{}
	// Simulate --json: set format to JSON
	format.Format, _ = cmdutil.ParseFormat("json")

	err := WriteResult(&stdout, &stderr, result, format)
	require.NoError(t, err)

	var parsed ResultOutput
	err = json.Unmarshal(stdout.Bytes(), &parsed)
	require.NoError(t, err)
	assert.Equal(t, 3, parsed.LoopsCompleted)
	assert.True(t, parsed.Success)
	assert.Equal(t, "agent signaled completion", parsed.ExitReason)
}

func TestWriteResult_Default(t *testing.T) {
	result := &loop.Result{
		LoopsCompleted: 5,
		ExitReason:     "agent signaled completion",
		Session:        &loop.Session{},
	}

	var stdout, stderr bytes.Buffer
	format := &cmdutil.FormatFlags{} // default mode

	err := WriteResult(&stdout, &stderr, result, format)
	require.NoError(t, err)

	// Default mode returns nil and writes nothing — the Monitor handles the summary.
	assert.Empty(t, stdout.String(), "default mode should write nothing to stdout")
	assert.Empty(t, stderr.String(), "default mode should write nothing to stderr")
}

func TestWriteResult_Quiet(t *testing.T) {
	result := &loop.Result{
		LoopsCompleted: 5,
		ExitReason:     "agent signaled completion",
		Session:        &loop.Session{},
	}

	var stdout, stderr bytes.Buffer
	format := &cmdutil.FormatFlags{Quiet: true}

	err := WriteResult(&stdout, &stderr, result, format)
	require.NoError(t, err)

	assert.Equal(t, "agent signaled completion\n", stdout.String())
}
