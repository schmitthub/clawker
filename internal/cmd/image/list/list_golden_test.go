package list

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/require"
)

// timeAgoRE matches relative time strings produced by formatCreated.
var timeAgoRE = regexp.MustCompile(`\d+ (minutes?|hours?|days?|weeks?|months?|years?) ago|Less than a minute ago`)

func TestImageList_Golden(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		images []docker.ImageSummary
	}{
		{
			name: "default",
			args: []string{},
			images: []docker.ImageSummary{
				dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
				dockertest.ImageSummaryFixture("node:20-slim"),
			},
		},
		{
			name: "mixed",
			args: []string{},
			images: []docker.ImageSummary{
				dockertest.ImageSummaryFixture("myapp:v1.0"),
				{
					ID:       "sha256:deadbeef1234567890deadbeef1234567890deadbeef1234567890deadbeef1234",
					RepoTags: nil,
					Created:  1700000000,
					Size:     128 * 1024 * 1024,
				},
			},
		},
		{
			name: "quiet",
			args: []string{"-q"},
			images: []docker.ImageSummary{
				dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
				dockertest.ImageSummaryFixture("node:20-slim"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := dockertest.NewFakeClient(config.NewBlankConfig())
			fake.SetupImageList(tt.images...)

			tio := iostreamstest.New()
			f := &cmdutil.Factory{
				IOStreams: tio.IOStreams,
				TUI:       tui.NewTUI(tio.IOStreams),
				Client: func(_ context.Context) (*docker.Client, error) {
					return fake.Client, nil
				},
			}

			cmd := NewCmdList(f, nil)
			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(tio.OutBuf)
			cmd.SetErr(tio.ErrBuf)

			err := cmd.Execute()
			require.NoError(t, err)

			output := scrubTimeAgo(tio.OutBuf.String())
			compareGolden(t, tt.name, output)
		})
	}
}

// scrubTimeAgo replaces non-deterministic relative time strings with a fixed placeholder.
func scrubTimeAgo(s string) string {
	return timeAgoRE.ReplaceAllString(s, "XX ago")
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	testDir := sanitizeGoldenName(t.Name())
	path := filepath.Join("testdata", testDir, name+".golden")
	updateMode := os.Getenv("GOLDEN_UPDATE") == "1"

	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if updateMode {
			writeGoldenFile(t, path, got)
			t.Logf("Created golden file: %s", path)
			return
		}
		t.Fatalf("golden file not found: %s\n\nTo create, run:\n  GOLDEN_UPDATE=1 go test ./internal/cmd/image/list/... -run %s", path, t.Name())
	}
	require.NoError(t, err)

	if got != string(want) {
		if updateMode {
			writeGoldenFile(t, path, got)
			t.Logf("Updated golden file: %s", path)
			return
		}
		t.Errorf("output does not match golden file: %s\n\nTo update:\n  GOLDEN_UPDATE=1 go test ./internal/cmd/image/list/... -run %s\n\nGot:\n%s\nWant:\n%s",
			path, t.Name(), got, string(want))
	}
}

func sanitizeGoldenName(name string) string {
	result := make([]byte, len(name))
	for i := range len(name) {
		c := name[i]
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			result[i] = '_'
		default:
			result[i] = c
		}
	}
	return string(result)
}

func writeGoldenFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}
