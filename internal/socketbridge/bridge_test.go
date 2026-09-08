package socketbridge_test

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
	sockebridgemocks "github.com/schmitthub/clawker/internal/socketbridge/mocks"
)

func TestBridge_Stop_DoubleCallDoesNotPanic(t *testing.T) {
	b := socketbridge.NewBridge("test-container-id", false, nil, logger.Nop())

	// First Stop should succeed
	err := b.Stop()
	assert.NoError(t, err)

	// Second Stop must NOT panic (double close of channel)
	assert.NotPanics(t, func() {
		err = b.Stop()
		assert.NoError(t, err)
	})
}

func TestBridge_ReadLoop_EOFSignalsError(t *testing.T) {
	b := socketbridge.NewBridge("test-container-id", false, nil, logger.Nop())

	// Set up a reader that returns EOF immediately (simulating docker exec dying)
	b.SetBridgeIOForTest(io.NopCloser(strings.NewReader("")), sockebridgemocks.NopWriteCloser{})
	errCh := b.InitErrChForTest()

	// Start the read loop
	b.StartReadLoopForTest()

	// Wait for error — should NOT hang
	err := <-errCh
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bridge exited before READY")

	b.WaitReadLoopForTest()
}

func TestBridge_ReadLoop_ReceivesReady(t *testing.T) {
	b := socketbridge.NewBridge("test-container-id", false, nil, logger.Nop())

	// Write a READY message in protocol format
	var buf bytes.Buffer
	sockebridgemocks.WriteTestMessage(&buf, socketbridge.Message{Type: socketbridge.MsgReady, StreamID: 0})

	b.SetBridgeIOForTest(io.NopCloser(&buf), sockebridgemocks.NopWriteCloser{})
	errCh := b.InitErrChForTest()

	b.StartReadLoopForTest()

	// Should receive nil error (READY)
	err := <-errCh
	assert.NoError(t, err)

	b.WaitReadLoopForTest()
}

func TestSendMessage_ReducedAllocations(t *testing.T) {
	var buf bytes.Buffer
	b := socketbridge.NewBridge("test-container-id", false, nil, logger.Nop())
	b.SetBridgeIOForTest(io.NopCloser(strings.NewReader("")), &sockebridgemocks.FlushWriteCloser{W: &buf})

	msg := socketbridge.Message{Type: socketbridge.MsgData, StreamID: 42, Payload: []byte("hello")}
	err := b.SendMessageForTest(msg)
	require.NoError(t, err)

	// Verify the wire format is correct
	reader := bufio.NewReader(&buf)
	got, err := socketbridge.ReadMessageForTest(reader)
	require.NoError(t, err)
	assert.Equal(t, socketbridge.MsgData, got.Type)
	assert.Equal(t, uint32(42), got.StreamID)
	assert.Equal(t, []byte("hello"), got.Payload)
}

func TestBuildRemoteSocketConfigIncludesExistingAndBridgedSockets(t *testing.T) {
	existing := []socketbridge.SocketConfig{
		{Path: "/home/clawker/.ssh/agent.sock", Type: "ssh-agent", Group: "", Mode: ""},
		{Path: "/home/clawker/.gnupg/S.gpg-agent", Type: "gpg-agent", Group: "", Mode: ""},
	}
	bridged := []socketbridge.BridgedSocket{{
		HostPath: "/host/agentd.sock",
		Target:   "/home/clawker/.agentd/control.sock",
		Identity: socketbridge.ListenerIdentity{UID: 501, GID: 20, Owner: "", Group: ""},
		Group:    "agentd",
		Mode:     "0660",
	}}

	raw, err := socketbridge.BuildRemoteSocketConfigForTest(existing, bridged)
	require.NoError(t, err)

	assert.JSONEq(t, `[
		{"path":"/home/clawker/.ssh/agent.sock","type":"ssh-agent"},
		{"path":"/home/clawker/.gnupg/S.gpg-agent","type":"gpg-agent"},
		{"path":"/home/clawker/.agentd/control.sock","type":"bridged","group":"agentd","mode":"0660"}
	]`, string(raw))
}

func TestForwarderCommandArgsCarryStartTimeSocketConfig(t *testing.T) {
	raw := []byte(`[{"path":"/run/service.sock","type":"bridged"}]`)

	args := socketbridge.ForwarderCommandArgsForTest("container-id", raw)

	assert.Equal(t, []string{
		"exec", "-i", "-e", consts.EnvRemoteSockets + "=" + string(raw),
		"container-id", "/usr/local/bin/clawker-socket-server",
	}, args)
}

func TestBridgeRejectsUnknownBridgedTarget(t *testing.T) {
	var output bytes.Buffer
	b := socketbridge.NewBridge("test-container-id", false, nil, logger.Nop())
	b.SetBridgeIOForTest(io.NopCloser(strings.NewReader("")), &sockebridgemocks.FlushWriteCloser{W: &output})

	b.HandleOpenForTest(
		socketbridge.Message{Type: socketbridge.MsgOpen, StreamID: 41, Payload: []byte("/run/unknown.sock")},
	)

	msg, err := socketbridge.ReadMessageForTest(bufio.NewReader(&output))
	require.NoError(t, err)
	assert.Equal(t, socketbridge.MsgError, msg.Type)
	assert.Equal(t, uint32(41), msg.StreamID)
	assert.Contains(t, string(msg.Payload), "unknown socket registration")
}

func TestBridgeOpensApprovedBridgedTargetAndLogsLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	var output bytes.Buffer
	var logs bytes.Buffer
	b := socketbridge.NewBridge("test-container-id", false, []socketbridge.BridgedSocket{{
		HostPath: path,
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: os.Getuid(), GID: os.Getgid(), Owner: "", Group: ""},
		Group:    "",
		Mode:     "",
	}}, logger.NewWriter(&logs))
	b.SetBridgeIOForTest(io.NopCloser(strings.NewReader("")), &sockebridgemocks.FlushWriteCloser{W: &output})

	b.HandleOpenForTest(
		socketbridge.Message{Type: socketbridge.MsgOpen, StreamID: 42, Payload: []byte("/run/service.sock")},
	)
	require.True(t, b.HasStreamForTest(42))
	connection := <-accepted
	b.CloseStreamForTest(42)
	require.NoError(t, connection.Close())

	assert.Contains(t, logs.String(), "bridged_socket_open")
	assert.Contains(t, logs.String(), "bridged_socket_close")
	assert.Contains(t, logs.String(), "/run/service.sock")
}

func TestBridgeRejectsChangedListenerIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	var output bytes.Buffer
	var logs bytes.Buffer
	b := socketbridge.NewBridge("test-container-id", false, []socketbridge.BridgedSocket{{
		HostPath: path,
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: os.Getuid() + 1, GID: os.Getgid(), Owner: "", Group: ""},
		Group:    "",
		Mode:     "",
	}}, logger.NewWriter(&logs))
	b.SetBridgeIOForTest(io.NopCloser(strings.NewReader("")), &sockebridgemocks.FlushWriteCloser{W: &output})

	b.HandleOpenForTest(
		socketbridge.Message{Type: socketbridge.MsgOpen, StreamID: 43, Payload: []byte("/run/service.sock")},
	)
	connection := <-accepted
	require.NoError(t, connection.Close())

	msg, err := socketbridge.ReadMessageForTest(bufio.NewReader(&output))
	require.NoError(t, err)
	assert.Equal(t, socketbridge.MsgError, msg.Type)
	assert.Equal(t, uint32(43), msg.StreamID)
	assert.Contains(t, string(msg.Payload), "listener identity mismatch")
	assert.Contains(t, logs.String(), "listener identity mismatch")
	assert.False(t, b.HasStreamForTest(43))
}
