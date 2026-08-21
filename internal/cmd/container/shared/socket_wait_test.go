package shared_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

func TestSocketsWaitScriptNoSocketsIsNoOp(t *testing.T) {
	assert.Empty(t, shared.SocketsWaitScriptForTest(nil, shared.SocketWaitTimeoutSecondsForTest()))
}

func TestSocketsWaitScriptProbesEachTarget(t *testing.T) {
	script := shared.SocketsWaitScriptForTest([]socketbridge.BridgedSocket{
		{
			HostPath: "",
			Target:   "/run/one.sock",
			Identity: socketbridge.ListenerIdentity{UID: 0, GID: 0, Owner: "", Group: ""},
			Group:    "",
			Mode:     "",
		},
		{
			HostPath: "",
			Target:   "/run/two.sock",
			Identity: socketbridge.ListenerIdentity{UID: 0, GID: 0, Owner: "", Group: ""},
			Group:    "",
			Mode:     "",
		},
	}, shared.SocketWaitTimeoutSecondsForTest())

	assert.Equal(t, 2, strings.Count(script, "[ -S "))
	assert.Contains(t, script, "'/run/one.sock'")
	assert.Contains(t, script, "'/run/two.sock'")
	assert.Contains(t, script, "60")
}

func TestSocketsWaitScriptTimeoutReportsMissingTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-service.sock")
	script := shared.SocketsWaitScriptForTest([]socketbridge.BridgedSocket{{
		HostPath: "",
		Target:   missing,
		Identity: socketbridge.ListenerIdentity{UID: 0, GID: 0, Owner: "", Group: ""},
		Group:    "",
		Mode:     "",
	}}, 0)
	var stdout bytes.Buffer
	command := exec.Command("sh", "-c", script)
	command.Stdout = &stdout

	err := command.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, stdout.String(), missing)
	assert.NotContains(t, stdout.String(), "CLAWKER_REMOTE_SOCKETS")
}

func TestShellQuoteSocketTarget(t *testing.T) {
	quoted := shared.ShellQuoteSocketTargetForTest("/run/agent's.sock")
	command := exec.Command("sh", "-c", "test "+quoted+" = \"/run/agent's.sock\"")

	assert.NoError(t, command.Run())
}
