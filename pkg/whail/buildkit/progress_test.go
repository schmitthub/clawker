package buildkit

import (
	"testing"
	"time"

	bkclient "github.com/moby/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/pkg/whail"
)

func TestDrainProgress_ErrorVertex_CallbackReceivesEvent(t *testing.T) {
	ch := make(chan *bkclient.SolveStatus, 1)
	vertexDigest := digest.FromString("error-vertex")

	started := ptrTime()
	ch <- &bkclient.SolveStatus{
		Vertexes: []*bkclient.Vertex{
			{Digest: vertexDigest, Name: "RUN failing-command", Started: started, Error: "exit code: 1"},
		},
	}
	close(ch)

	var events []whail.BuildProgressEvent
	drainProgress(ch, func(event whail.BuildProgressEvent) {
		events = append(events, event)
	})

	require.Len(t, events, 1, "should receive exactly one error event")
	assert.Equal(t, whail.BuildStepError, events[0].Status)
	assert.Equal(t, "exit code: 1", events[0].Error)
	assert.Equal(t, "RUN failing-command", events[0].StepName)
}

func TestDrainProgress_ErrorVertex_NilCallback(t *testing.T) {
	// When no callback is provided, error vertices should not panic.
	ch := make(chan *bkclient.SolveStatus, 1)
	vertexDigest := digest.FromString("error-vertex")

	ch <- &bkclient.SolveStatus{
		Vertexes: []*bkclient.Vertex{
			{Digest: vertexDigest, Name: "RUN failing", Error: "exit code: 1"},
		},
	}
	close(ch)

	// Should not panic with nil callback.
	drainProgress(ch, nil)
}

func TestDrainProgress_NilCallback_SkipsProcessing(t *testing.T) {
	// With nil callback, non-error vertices and logs are skipped silently.
	ch := make(chan *bkclient.SolveStatus, 2)
	vertexDigest := digest.FromString("vertex")
	started := ptrTime()

	ch <- &bkclient.SolveStatus{
		Vertexes: []*bkclient.Vertex{
			{Digest: vertexDigest, Name: "RUN build", Started: started},
		},
	}
	ch <- &bkclient.SolveStatus{
		Logs: []*bkclient.VertexLog{
			{Vertex: vertexDigest, Data: []byte("output\n")},
		},
	}
	close(ch)

	// Should not panic with nil callback.
	drainProgress(ch, nil)
}

func ptrTime() *time.Time {
	t := time.Now()
	return &t
}

func TestDrainProgress_CarriageReturnStripping(t *testing.T) {
	// BuildKit tools (apt-get, pip, npm) emit CR-based progress bars.
	// Lines containing \r should keep only the content after the last \r,
	// mimicking terminal rendering behavior.
	tests := []struct {
		name     string
		data     string
		expected []string
	}{
		{
			name:     "simple line no CR",
			data:     "Installing packages...\n",
			expected: []string{"Installing packages..."},
		},
		{
			name:     "CR progress bar keeps last segment",
			data:     "Progress: 50%\rProgress: 100%\n",
			expected: []string{"Progress: 100%"},
		},
		{
			name:     "multiple CR overwrites",
			data:     "  0%\r 25%\r 50%\r 75%\r100%\n",
			expected: []string{"100%"},
		},
		{
			name:     "trailing CR only",
			data:     "done\r\n",
			expected: []string{"done"},
		},
		{
			name:     "multiple lines with mixed CR",
			data:     "line1\nProgress: 50%\rProgress: 100%\nline3\n",
			expected: []string{"line1", "Progress: 100%", "line3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan *bkclient.SolveStatus, 2)

			vertexDigest := digest.FromString("test-vertex")

			// Send a vertex first so the log can reference it.
			ch <- &bkclient.SolveStatus{
				Vertexes: []*bkclient.Vertex{
					{Digest: vertexDigest, Name: "RUN apt-get install"},
				},
			}

			// Send log data with CR content.
			ch <- &bkclient.SolveStatus{
				Logs: []*bkclient.VertexLog{
					{Vertex: vertexDigest, Data: []byte(tt.data)},
				},
			}

			close(ch)

			var logLines []string
			drainProgress(ch, func(event whail.BuildProgressEvent) {
				if event.LogLine != "" {
					logLines = append(logLines, event.LogLine)
				}
			})

			require.Len(t, logLines, len(tt.expected), "wrong number of log lines")
			for i, exp := range tt.expected {
				assert.Equal(t, exp, logLines[i])
			}
		})
	}
}
