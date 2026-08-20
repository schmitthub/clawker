package socketbridge_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/socketbridge"
)

func TestBridgedSocketsFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge-sockets.json")
	want := []socketbridge.BridgedSocket{{
		HostPath: "/host/agentd.sock",
		Target:   "/home/clawker/.agentd/control.sock",
		Identity: socketbridge.ListenerIdentity{UID: 501, GID: 20, Owner: "andrew", Group: "staff"},
		Group:    "agentd",
		Mode:     "0660",
	}}

	require.NoError(t, socketbridge.WriteBridgedSocketsFile(path, want))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"host_path":"/host/agentd.sock","target":"/home/clawker/.agentd/control.sock","uid":501,"gid":20,"group":"agentd","mode":"0660"}]`, string(raw))

	got, err := socketbridge.ReadBridgedSocketsFile(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, want[0].HostPath, got[0].HostPath)
	assert.Equal(t, want[0].Target, got[0].Target)
	assert.Equal(t, want[0].Identity.UID, got[0].Identity.UID)
	assert.Equal(t, want[0].Identity.GID, got[0].Identity.GID)
	assert.Equal(t, want[0].Group, got[0].Group)
	assert.Equal(t, want[0].Mode, got[0].Mode)
}

func TestBridgedSocketJSONOmitsDefaultContainerPermissions(t *testing.T) {
	raw, err := json.Marshal(socketbridge.BridgedSocket{
		HostPath: "/host/service.sock",
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: 1000, GID: 1000},
	})
	require.NoError(t, err)

	assert.JSONEq(t, `{"host_path":"/host/service.sock","target":"/run/service.sock","uid":1000,"gid":1000}`, string(raw))
}

func TestBridgedSocketsFileUsesAnEmptyArrayForNoRegistrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge-sockets.json")

	require.NoError(t, socketbridge.WriteBridgedSocketsFile(path, nil))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.JSONEq(t, `[]`, string(raw))
}
