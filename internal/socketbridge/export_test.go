package socketbridge

import (
	"bufio"
	"context"
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

// HandleOpenForTest calls the private OPEN handler.
func (b *Bridge) HandleOpenForTest(msg Message) {
	b.handleOpen(msg)
}

// HasStreamForTest reports whether the bridge tracks a stream.
func (b *Bridge) HasStreamForTest(streamID uint32) bool {
	b.streamMu.RLock()
	defer b.streamMu.RUnlock()
	_, ok := b.streams[streamID]
	return ok
}

// CloseStreamForTest closes one tracked stream.
func (b *Bridge) CloseStreamForTest(streamID uint32) {
	b.closeStream(streamID)
}

// BuildRemoteSocketConfigForTest exposes the start-time socket env builder.
func BuildRemoteSocketConfigForTest(existing []SocketConfig, bridged []BridgedSocket) ([]byte, error) {
	return buildRemoteSocketConfig(existing, bridged)
}

// ForwarderCommandArgsForTest exposes docker exec argument construction.
func ForwarderCommandArgsForTest(containerID string, socketsJSON []byte) []string {
	return forwarderCommandArgs(containerID, socketsJSON)
}

// ReadMessageForTest exposes the package-level readMessage function.
func ReadMessageForTest(r *bufio.Reader) (Message, error) {
	return readMessage(r)
}

// --- Manager accessors ---

// SetBridgeForTest injects a bridge tracking entry into the Manager for testing.
func (m *Manager) SetBridgeForTest(id string, pid int, pidFile, socketsFile string) {
	m.bridges[id] = &bridgeProcess{pid: pid, pidFile: pidFile, socketsFile: socketsFile}
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
func ReadPIDFileForTest(path string) int {
	return readPIDFile(path)
}

// IsProcessAliveForTest exposes the private isProcessAlive function.
func IsProcessAliveForTest(pid int) bool {
	return isProcessAlive(pid)
}

// WaitForPIDFileForTest exposes the private waitForPIDFile function.
func WaitForPIDFileForTest(path string, timeout time.Duration) error {
	return waitForPIDFile(path, timeout)
}

// BridgeExecutableForTest exposes daemon executable resolution.
func BridgeExecutableForTest() (string, error) {
	return bridgeExecutable()
}

// CheckHostSSHAgentForTest exposes the private checkHostSSHAgent function.
func CheckHostSSHAgentForTest(ctx context.Context) error {
	return checkHostSSHAgent(ctx)
}
