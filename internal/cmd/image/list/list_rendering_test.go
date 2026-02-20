package list

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
	}, tio
}

func TestImageList_Rendering(t *testing.T) {
	t.Run("table_output", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(
			dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
			dockertest.ImageSummaryFixture("node:20-slim"),
		)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, "IMAGE")
		assert.Contains(t, output, "ID")
		assert.Contains(t, output, "CREATED")
		assert.Contains(t, output, "SIZE")
		assert.Contains(t, output, "clawker-fawker-demo:latest")
		assert.Contains(t, output, "node:20-slim")
		assert.Contains(t, output, "a1b2c3d4e5f6")
		assert.Contains(t, output, "256.00MB")
		assert.Empty(t, tio.ErrBuf.String())
	})

	t.Run("quiet_mode", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(
			dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
			dockertest.ImageSummaryFixture("node:20-slim"),
		)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"-q"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, "a1b2c3d4e5f6")
		assert.NotContains(t, output, "IMAGE")
		assert.NotContains(t, output, "clawker-fawker-demo")
	})

	t.Run("empty_list", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList()

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		assert.Empty(t, tio.OutBuf.String())
		assert.Contains(t, tio.ErrBuf.String(), "No clawker images found")
	})

	t.Run("untagged_image", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		untagged := docker.ImageSummary{
			ID:       "sha256:deadbeef1234567890deadbeef1234567890deadbeef1234567890deadbeef1234",
			RepoTags: nil,
			Created:  1700000000,
			Size:     128 * 1024 * 1024,
		}
		fake.SetupImageList(untagged)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, "<none>:<none>")
		assert.Contains(t, output, "deadbeef1234")
	})

	t.Run("multi_tag_image", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		multiTag := docker.ImageSummary{
			ID:       "sha256:a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890",
			RepoTags: []string{"myapp:latest", "myapp:v1.0"},
			Created:  1700000000,
			Size:     256 * 1024 * 1024,
		}
		fake.SetupImageList(multiTag)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, "myapp:latest")
		assert.Contains(t, output, "myapp:v1.0")
	})

	t.Run("error_propagation", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.FakeAPI.ImageListFn = func(_ context.Context, _ docker.ImageListOptions) (docker.ImageListResult, error) {
			return docker.ImageListResult{}, fmt.Errorf("connection refused")
		}

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listing images")
	})

	t.Run("json_output", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(
			dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
			dockertest.ImageSummaryFixture("node:20-slim"),
		)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"--json"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, `"image": "clawker-fawker-demo:latest"`)
		assert.Contains(t, output, `"image": "node:20-slim"`)
		assert.Contains(t, output, `"id": "a1b2c3d4e5f6"`)
		assert.Contains(t, output, `"size":`)
		assert.Empty(t, tio.ErrBuf.String())
	})

	t.Run("format_json", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(dockertest.ImageSummaryFixture("myapp:v1"))

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"--format", "json"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, `"image": "myapp:v1"`)
	})

	t.Run("template_output", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(
			dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
			dockertest.ImageSummaryFixture("node:20-slim"),
		)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"--format", "{{.ID}} {{.Image}}"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, "a1b2c3d4e5f6 clawker-fawker-demo:latest")
		assert.Contains(t, output, "a1b2c3d4e5f6 node:20-slim")
		assert.NotContains(t, output, "IMAGE")
	})

	t.Run("filter_reference", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(
			dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
			dockertest.ImageSummaryFixture("node:20-slim"),
		)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"--filter", "reference=clawker*"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		output := tio.OutBuf.String()
		assert.Contains(t, output, "clawker-fawker-demo:latest")
		assert.NotContains(t, output, "node:20-slim")
	})

	t.Run("filter_no_match", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(
			dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
		)

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"--filter", "reference=nonexistent*"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.NoError(t, err)

		assert.Empty(t, tio.OutBuf.String())
		assert.Contains(t, tio.ErrBuf.String(), "No clawker images found")
	})

	t.Run("invalid_filter_key", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(dockertest.ImageSummaryFixture("myapp:v1"))

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"--filter", "badkey=value"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid filter key")
	})

	t.Run("quiet_and_json_exclusive", func(t *testing.T) {
		fake := dockertest.NewFakeClient(config.NewBlankConfig())
		fake.SetupImageList(dockertest.ImageSummaryFixture("myapp:v1"))

		f, tio := testFactory(t, fake)
		cmd := NewCmdList(f, nil)
		cmd.SetArgs([]string{"-q", "--json"})
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetOut(tio.OutBuf)
		cmd.SetErr(tio.ErrBuf)

		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})
}
