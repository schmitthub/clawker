package socketbridge_test

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/socketbridge"
	sockebridgemocks "github.com/schmitthub/clawker/internal/socketbridge/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridge_Stop_DoubleCallDoesNotPanic(t *testing.T) {
	b := socketbridge.NewBridge("test-container-id", false)

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
	b := socketbridge.NewBridge("test-container-id", false)

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
	b := socketbridge.NewBridge("test-container-id", false)

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
	b := socketbridge.NewBridge("test-container-id", false)
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
