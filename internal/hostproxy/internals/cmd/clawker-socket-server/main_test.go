package main

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBridgedSocketListenerAppliesGroupAndMode(t *testing.T) {
	current, err := user.Current()
	require.NoError(t, err)
	group, err := user.LookupGroupId(current.Gid)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "service.sock")
	forwarder := &Forwarder{}

	listener, err := forwarder.createSocketListener(SocketConfig{
		Path:  path,
		Type:  socketTypeBridged,
		Group: group.Name,
		Mode:  "0660",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o660), info.Mode().Perm())
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	wantGID, err := strconv.Atoi(current.Gid)
	require.NoError(t, err)
	assert.Equal(t, uint32(wantGID), stat.Gid)
}

func TestDropAgentPrivilegesRestoresSupplementaryGroups(t *testing.T) {
	var (
		steps     []string
		gotGroups []int
		gotGID    int
		gotUID    int
	)
	ops := privilegeOps{
		effectiveUID: func() int { return 0 },
		lookupUser: func(username string) (*user.User, error) {
			assert.Equal(t, "agent", username)
			return &user.User{Uid: "1001", Gid: "1002"}, nil
		},
		groupIDs: func(*user.User) ([]string, error) {
			return []string{"1002", "2001"}, nil
		},
		setGroups: func(groups []int) error {
			steps = append(steps, "groups")
			gotGroups = groups
			return nil
		},
		setGID: func(gid int) error {
			steps = append(steps, "gid")
			gotGID = gid
			return nil
		},
		setUID: func(uid int) error {
			steps = append(steps, "uid")
			gotUID = uid
			return nil
		},
	}

	require.NoError(t, dropAgentPrivileges("agent", ops))
	assert.Equal(t, []string{"groups", "gid", "uid"}, steps)
	assert.Equal(t, []int{1002, 2001}, gotGroups)
	assert.Equal(t, 1002, gotGID)
	assert.Equal(t, 1001, gotUID)
}

func TestCreateBridgedSocketListenerUsesDefaultPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	forwarder := &Forwarder{}

	listener, err := forwarder.createSocketListener(SocketConfig{Path: path, Type: socketTypeBridged})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestCreateBridgedSocketListenersReportsUnknownGroup(t *testing.T) {
	var output bytes.Buffer
	forwarder := &Forwarder{
		sockets: []SocketConfig{{
			Path:  filepath.Join(t.TempDir(), "service.sock"),
			Type:  socketTypeBridged,
			Group: "clawker-group-that-does-not-exist",
		}},
		streams: make(map[uint32]net.Conn),
		stdout:  bufio.NewWriter(&output),
	}

	listeners, err := forwarder.createSocketListeners()
	require.Error(t, err)
	assert.Empty(t, listeners)
	assert.ErrorContains(t, err, "lookup group")

	message, err := readMessage(bufio.NewReader(&output))
	require.NoError(t, err)
	assert.Equal(t, MsgError, message.Type)
	assert.Contains(t, string(message.Payload), "lookup group")
}

func TestBridgedSocketOpenUsesTargetAsIdentifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	reader, writer := io.Pipe()
	forwarder := &Forwarder{
		sockets: []SocketConfig{{Path: path, Type: socketTypeBridged}},
		streams: make(map[uint32]net.Conn),
		stdout:  bufio.NewWriter(writer),
	}
	listeners, err := forwarder.createSocketListeners()
	require.NoError(t, err)
	listener := listeners[path]
	require.NotNil(t, listener)

	connection, err := net.Dial("unix", path)
	require.NoError(t, err)
	wireReader := bufio.NewReader(reader)
	message, err := readMessage(wireReader)
	require.NoError(t, err)
	assert.Equal(t, MsgOpen, message.Type)
	assert.Equal(t, path, string(message.Payload))

	require.NoError(t, connection.Close())
	closeMessage, err := readMessage(wireReader)
	require.NoError(t, err)
	assert.Equal(t, MsgClose, closeMessage.Type)
	require.NoError(t, listener.Close())
	require.NoError(t, writer.Close())
	require.NoError(t, reader.Close())
}
