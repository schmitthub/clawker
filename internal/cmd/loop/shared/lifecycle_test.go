package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectLoopHooks_DefaultHooks(t *testing.T) {
	var copies []copyCall
	copyFn := func(_ context.Context, containerID, destPath string, content io.Reader) error {
		data, err := io.ReadAll(content)
		require.NoError(t, err)
		copies = append(copies, copyCall{
			containerID: containerID,
			destPath:    destPath,
			data:        data,
		})
		return nil
	}

	err := InjectLoopHooks(context.Background(), "abc123", "", copyFn)
	require.NoError(t, err)

	// Expect 2 copies: settings.json + hook script files
	require.Len(t, copies, 2)

	// First copy: settings.json to ~/.claude/
	assert.Equal(t, "abc123", copies[0].containerID)
	assert.Equal(t, containerHomeDir+"/.claude", copies[0].destPath)

	// Verify settings.json is valid JSON with hooks key
	settingsFile := extractTarFile(t, copies[0].data, "settings.json")
	require.NotNil(t, settingsFile, "settings.json not found in tar")

	var settings map[string]json.RawMessage
	err = json.Unmarshal(settingsFile, &settings)
	require.NoError(t, err)
	assert.Contains(t, string(settings["hooks"]), EventStop)
	assert.Contains(t, string(settings["hooks"]), EventSessionStart)

	// Second copy: hook scripts to /
	assert.Equal(t, "abc123", copies[1].containerID)
	assert.Equal(t, "/", copies[1].destPath)

	// Verify stop-check.js is in the tar (at relative path without leading /)
	scriptPath := StopCheckScriptPath[1:] // trim leading /
	stopScript := extractTarFile(t, copies[1].data, scriptPath)
	require.NotNil(t, stopScript, "stop-check.js not found in tar")
	assert.Contains(t, string(stopScript), "LOOP_STATUS")
}

func TestInjectLoopHooks_CustomHooksFile(t *testing.T) {
	// Create a temporary hooks file
	tmpDir := t.TempDir()
	hooksFile := tmpDir + "/hooks.json"
	hooksContent := `{"Stop":[{"hooks":[{"type":"command","command":"echo custom"}]}]}`
	err := os.WriteFile(hooksFile, []byte(hooksContent), 0o644)
	require.NoError(t, err)

	var copies []copyCall
	copyFn := func(_ context.Context, containerID, destPath string, content io.Reader) error {
		data, err := io.ReadAll(content)
		require.NoError(t, err)
		copies = append(copies, copyCall{
			containerID: containerID,
			destPath:    destPath,
			data:        data,
		})
		return nil
	}

	err = InjectLoopHooks(context.Background(), "xyz789", hooksFile, copyFn)
	require.NoError(t, err)

	// Custom hooks: only 1 copy (settings.json, no hook files)
	require.Len(t, copies, 1)
	assert.Equal(t, containerHomeDir+"/.claude", copies[0].destPath)

	// Verify the custom hooks are in settings.json
	settingsFile := extractTarFile(t, copies[0].data, "settings.json")
	require.NotNil(t, settingsFile)
	assert.Contains(t, string(settingsFile), "echo custom")
}

func TestInjectLoopHooks_InvalidHooksFile(t *testing.T) {
	copyFn := func(_ context.Context, _, _ string, _ io.Reader) error {
		t.Fatal("copy should not be called")
		return nil
	}

	err := InjectLoopHooks(context.Background(), "abc123", "/nonexistent/hooks.json", copyFn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading hooks file")
}

func TestInjectLoopHooks_CopySettingsFails(t *testing.T) {
	callCount := 0
	copyFn := func(_ context.Context, _, _ string, _ io.Reader) error {
		callCount++
		if callCount == 1 {
			return assert.AnError
		}
		return nil
	}

	err := InjectLoopHooks(context.Background(), "abc123", "", copyFn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "injecting settings.json")
}

func TestInjectLoopHooks_CopyScriptsFails(t *testing.T) {
	callCount := 0
	copyFn := func(_ context.Context, _, _ string, _ io.Reader) error {
		callCount++
		if callCount == 2 {
			return assert.AnError
		}
		return nil
	}

	err := InjectLoopHooks(context.Background(), "abc123", "", copyFn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "injecting hook scripts")
}

func TestBuildSettingsTar(t *testing.T) {
	content := []byte(`{"hooks":{"Stop":[]}}`)
	reader, err := buildSettingsTar(content)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	file := extractTarFile(t, data, "settings.json")
	require.NotNil(t, file)
	assert.Equal(t, content, file)
}

func TestBuildSettingsTar_FileOwnership(t *testing.T) {
	content := []byte(`{}`)
	reader, err := buildSettingsTar(content)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	hdr := extractTarEntry(t, data, "settings.json")
	require.NotNil(t, hdr)
	assert.Equal(t, 1001, hdr.Uid, "should be owned by container UID 1001")
	assert.Equal(t, 1001, hdr.Gid, "should be owned by container GID 1001")
	assert.Equal(t, int64(0o644), hdr.Mode)
}

func TestBuildHookFilesTar(t *testing.T) {
	files := map[string][]byte{
		"/tmp/clawker-hooks/stop-check.js": []byte("// stop check script"),
		"/tmp/clawker-hooks/other.sh":      []byte("#!/bin/bash\necho hello"),
	}

	reader, err := buildHookFilesTar(files)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	// Verify both files exist in the tar
	stopCheck := extractTarFile(t, data, "tmp/clawker-hooks/stop-check.js")
	require.NotNil(t, stopCheck, "stop-check.js not found")
	assert.Equal(t, "// stop check script", string(stopCheck))

	other := extractTarFile(t, data, "tmp/clawker-hooks/other.sh")
	require.NotNil(t, other, "other.sh not found")
	assert.Equal(t, "#!/bin/bash\necho hello", string(other))

	// Verify directory entry exists
	dirEntry := extractTarEntry(t, data, "tmp/clawker-hooks/")
	require.NotNil(t, dirEntry, "directory entry not found")
	assert.Equal(t, byte(tar.TypeDir), dirEntry.Typeflag)
}

func TestBuildHookFilesTar_EmptyFiles(t *testing.T) {
	reader, err := buildHookFilesTar(map[string][]byte{})
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	// Empty tar should just be the end-of-archive markers
	assert.NotEmpty(t, data)
}

func TestBuildHookFilesTar_FilePermissions(t *testing.T) {
	files := map[string][]byte{
		"/tmp/clawker-hooks/script.sh": []byte("#!/bin/bash"),
	}

	reader, err := buildHookFilesTar(files)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	hdr := extractTarEntry(t, data, "tmp/clawker-hooks/script.sh")
	require.NotNil(t, hdr)
	assert.Equal(t, int64(0o755), hdr.Mode, "hook scripts should be executable")
}

// --- helpers ---

type copyCall struct {
	containerID string
	destPath    string
	data        []byte
}

func extractTarFile(t *testing.T, tarData []byte, name string) []byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		require.NoError(t, err)
		if hdr.Name == name {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return data
		}
	}
}

func extractTarEntry(t *testing.T, tarData []byte, name string) *tar.Header {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		require.NoError(t, err)
		if hdr.Name == name {
			return hdr
		}
	}
}
