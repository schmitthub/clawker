package socketbridge

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridge_Stop_DoubleCallDoesNotPanic(t *testing.T) {
	b := NewBridge("test-container-id", false)

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
	// Create a bridge with a reader that returns EOF immediately
	b := NewBridge("test-container-id", false)

	// Set up a reader that returns EOF immediately (simulating docker exec dying)
	b.stdout = io.NopCloser(strings.NewReader(""))
	b.stdin = nopWriteCloser{}
	b.errCh = make(chan error, 1)

	// Start the read loop
	b.readWg.Add(1)
	go b.readLoop()

	// Wait for error — should NOT hang
	err := <-b.errCh
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bridge exited before READY")

	b.readWg.Wait()
}

func TestBridge_ReadLoop_ReceivesReady(t *testing.T) {
	b := NewBridge("test-container-id", false)

	// Create a pipe for the protocol message
	var buf bytes.Buffer
	// Write a READY message in protocol format
	writeTestMessage(&buf, Message{Type: MsgReady, StreamID: 0})

	b.stdout = io.NopCloser(&buf)
	b.stdin = nopWriteCloser{}
	b.errCh = make(chan error, 1)

	b.readWg.Add(1)
	go b.readLoop()

	// Should receive nil error (READY)
	err := <-b.errCh
	assert.NoError(t, err)

	b.readWg.Wait()
}

func TestSendMessage_ReducedAllocations(t *testing.T) {
	var buf bytes.Buffer
	b := &Bridge{
		stdin: &nopFlushWriteCloser{w: &buf},
	}

	msg := Message{Type: MsgData, StreamID: 42, Payload: []byte("hello")}
	err := b.sendMessage(msg)
	require.NoError(t, err)

	// Verify the wire format is correct
	reader := bufio.NewReader(&buf)
	got, err := readMessage(reader)
	require.NoError(t, err)
	assert.Equal(t, MsgData, got.Type)
	assert.Equal(t, uint32(42), got.StreamID)
	assert.Equal(t, []byte("hello"), got.Payload)
}

// writeTestMessage writes a protocol message to a buffer.
func writeTestMessage(buf *bytes.Buffer, msg Message) {
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

// nopWriteCloser is a no-op WriteCloser for testing.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                 { return nil }

// nopFlushWriteCloser wraps a writer for sendMessage testing.
type nopFlushWriteCloser struct {
	w io.Writer
}

func (n *nopFlushWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n *nopFlushWriteCloser) Close() error                 { return nil }
