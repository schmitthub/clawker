package socketbridge

import (
	"bufio"
	"io"
	"time"
)

// --- Bridge accessors ---

// SetBridgeIOForTest replaces a Bridge's stdin/stdout for protocol-level tests.
func (b *Bridge) SetBridgeIOForTest(stdout io.ReadCloser, stdin io.WriteCloser) {
	b.stdout = stdout
	b.stdin = stdin
}

// InitErrChForTest replaces the Bridge's errCh with a new buffered channel and returns it.
func (b *Bridge) InitErrChForTest() chan error {
	b.errCh = make(chan error, 1)
	return b.errCh
}

// StartReadLoopForTest starts the read loop goroutine for testing.
// Call WaitReadLoopForTest after to wait for completion.
func (b *Bridge) StartReadLoopForTest() {
	b.readWg.Add(1)
	go b.readLoop()
}

// WaitReadLoopForTest waits for the read loop goroutine to finish.
func (b *Bridge) WaitReadLoopForTest() {
	b.readWg.Wait()
}

// SendMessageForTest calls the private sendMessage method.
func (b *Bridge) SendMessageForTest(msg Message) error {
	return b.sendMessage(msg)
}

// ReadMessageForTest exposes the package-level readMessage function.
var ReadMessageForTest = func(r *bufio.Reader) (Message, error) {
	return readMessage(r)
}

// --- Manager accessors ---

// SetBridgeForTest injects a bridge tracking entry into the Manager for testing.
func (m *Manager) SetBridgeForTest(id string, pid int, pidFile string) {
	m.bridges[id] = &bridgeProcess{pid: pid, pidFile: pidFile}
}

// HasBridgeForTest returns true if the Manager is tracking a bridge for the given container.
func (m *Manager) HasBridgeForTest(id string) bool {
	_, ok := m.bridges[id]
	return ok
}

// BridgePIDForTest returns the tracked PID for a container, if present.
func (m *Manager) BridgePIDForTest(id string) (int, bool) {
	bp, ok := m.bridges[id]
	if !ok {
		return 0, false
	}
	return bp.pid, true
}

// BridgeCountForTest returns the number of tracked bridges.
func (m *Manager) BridgeCountForTest() int {
	return len(m.bridges)
}

// --- Package-level function accessors ---

// ReadPIDFileForTest exposes the private readPIDFile function.
var ReadPIDFileForTest = readPIDFile

// IsProcessAliveForTest exposes the private isProcessAlive function.
var IsProcessAliveForTest = isProcessAlive

// WaitForPIDFileForTest exposes the private waitForPIDFile function.
var WaitForPIDFileForTest = func(path string, timeout time.Duration) error {
	return waitForPIDFile(path, timeout)
}

// ShortIDForTest exposes the private shortID function.
var ShortIDForTest = shortID
