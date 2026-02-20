package mocks

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

// NewTestManager creates a socketbridge.Manager with a mock config fully isolated
// to dir. All env var overrides (config dir, state dir, data dir) are scoped to
// the temp directory so tests never touch a developer's real clawker installation.
// Env var names are sourced from the Config interface to prevent drift.
func NewTestManager(t *testing.T, dir string) *socketbridge.Manager {
	t.Helper()
	cfg := configmocks.NewBlankConfig()
	t.Setenv(cfg.ConfigDirEnvVar(), dir)
	t.Setenv(cfg.StateDirEnvVar(), dir)
	t.Setenv(cfg.DataDirEnvVar(), dir)
	return socketbridge.NewManager(cfg)
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
