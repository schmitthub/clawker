package buildkit

import (
	"testing"

	bkclient "github.com/moby/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/pkg/whail"
)

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
			drainProgress(ch, false, func(event whail.BuildProgressEvent) {
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
