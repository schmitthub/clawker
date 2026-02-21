package mocks

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

// NewMockManager creates a SocketBridgeManagerMock with no-op defaults.
// Unlike bare moq (which panics on nil Func fields), this wires safe defaults
// so tests only need to override the methods they care about.
func NewMockManager() *SocketBridgeManagerMock {
	return &SocketBridgeManagerMock{
		EnsureBridgeFunc: func(containerID string, gpgEnabled bool) error { return nil },
		StopBridgeFunc:   func(containerID string) error { return nil },
		StopAllFunc:      func() error { return nil },
		IsRunningFunc:    func(containerID string) bool { return false },
	}
}

// CalledWith returns true if the given method was called with the given containerID.
// Convenience wrapper over moq's typed call slices.
func CalledWith(mock *SocketBridgeManagerMock, method, containerID string) bool {
	switch method {
	case "EnsureBridge":
		for _, c := range mock.EnsureBridgeCalls() {
			if c.ContainerID == containerID {
				return true
			}
		}
	case "StopBridge":
		for _, c := range mock.StopBridgeCalls() {
			if c.ContainerID == containerID {
				return true
			}
		}
	case "IsRunning":
		for _, c := range mock.IsRunningCalls() {
			if c.ContainerID == containerID {
				return true
			}
		}
	}
	return false
}

// NewTestManager creates a socketbridge.Manager backed by a real, file-backed
// config isolated to a temp directory. Returns the manager and its state dir
// so tests can create PID files where BridgePIDFilePath will find them.
// Uses NewIsolatedTestConfig under the hood so path helpers (BridgePIDFilePath,
// BridgesSubdir, LogsSubdir) work against real directories instead of panicking
// on nil mock funcs.
func NewTestManager(t *testing.T) (*socketbridge.Manager, string) {
	t.Helper()
	cfg, _ := configmocks.NewIsolatedTestConfig(t)
	stateDir, err := cfg.BridgesSubdir()
	if err != nil {
		t.Fatalf("getting bridges subdir: %v", err)
	}
	return socketbridge.NewManager(cfg), stateDir
}

// WriteTestMessage writes a socketbridge protocol message to buf
// in the wire format: [4-byte length][1-byte type][4-byte streamID][payload].
func WriteTestMessage(buf *bytes.Buffer, msg socketbridge.Message) {
	length := uint32(1 + 4 + len(msg.Payload))
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, length)
	buf.Write(lenBuf)
	buf.WriteByte(msg.Type)
	streamBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(streamBuf, msg.StreamID)
	buf.Write(streamBuf)
	if len(msg.Payload) > 0 {
		buf.Write(msg.Payload)
	}
}

// NopWriteCloser is a no-op io.WriteCloser for testing.
// Writes are accepted and discarded; Close is a no-op.
type NopWriteCloser struct{}

func (NopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }

// Close implements io.WriteCloser.
func (NopWriteCloser) Close() error { return nil }

// FlushWriteCloser wraps an io.Writer with a no-op Close method,
// useful for testing sendMessage against a *bytes.Buffer.
type FlushWriteCloser struct {
	W io.Writer
}

func (f *FlushWriteCloser) Write(p []byte) (int, error) { return f.W.Write(p) }

// Close implements io.WriteCloser.
func (f *FlushWriteCloser) Close() error { return nil }
