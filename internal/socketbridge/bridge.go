// Package socketbridge provides host-side socket forwarding via docker exec.
// It connects to a container running socket-forwarder and multiplexes socket
// connections between the container and host agents (GPG, SSH).
//
// This implements a muxrpc-like protocol over stdin/stdout, avoiding the need
// for network access from container to host.
package socketbridge

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/schmitthub/clawker/internal/logger"
)

// ProtocolVersion is the muxrpc wire protocol version.
// Bump when the message format or semantics change incompatibly.
const ProtocolVersion = 1

// Message types (must match socket-forwarder)
const (
	MsgData   byte = 1 // Socket data
	MsgOpen   byte = 2 // New connection (payload = socket type)
	MsgClose  byte = 3 // Connection closed
	MsgPubkey byte = 4 // GPG public key data
	MsgReady  byte = 5 // Forwarder ready
	MsgError  byte = 6 // Error message
)

// Buffer and message size limits.
const (
	readBufSize    = 64 * 1024 // Per-stream read buffer
	maxMessageSize = 1 << 20   // 1 MiB maximum message payload
)

// SocketConfig defines a socket to forward.
type SocketConfig struct {
	Path string `json:"path"` // Unix socket path in container
	Type string `json:"type"` // "gpg-agent" or "ssh-agent"
}

// Message represents a protocol message.
type Message struct {
	Type     byte
	StreamID uint32
	Payload  []byte
}

// Bridge manages socket forwarding to a container.
type Bridge struct {
	containerID string
	gpgEnabled  bool   // Whether GPG forwarding is enabled
	gpgPubkey   []byte // GPG public key to send

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// Warnings receives user-visible warning messages (typically stderr).
	// If nil, warnings are suppressed.
	Warnings io.Writer

	streams  map[uint32]net.Conn
	streamMu sync.RWMutex
	writeMu  sync.Mutex

	done      chan struct{}
	closeOnce sync.Once // Prevents double-close panic on done channel
	errCh     chan error
	readWg    sync.WaitGroup
}

// NewBridge creates a new socket bridge for the given container.
// gpgEnabled indicates whether GPG agent forwarding is configured.
func NewBridge(containerID string, gpgEnabled bool) *Bridge {
	return &Bridge{
		containerID: containerID,
		gpgEnabled:  gpgEnabled,
		streams:     make(map[uint32]net.Conn),
		done:        make(chan struct{}),
		errCh:       make(chan error, 1),
	}
}

// SetGPGPubkey sets the GPG public key to send to the container.
// Must be called before Start if GPG forwarding is enabled.
func (b *Bridge) SetGPGPubkey(pubkey []byte) {
	b.gpgPubkey = pubkey
}

// Start launches the socket-forwarder in the container and begins forwarding.
func (b *Bridge) Start(ctx context.Context) error {
	// Get GPG pubkey if GPG forwarding is enabled
	if b.gpgEnabled && len(b.gpgPubkey) == 0 {
		pubkey, err := getHostGPGPubkey()
		if err != nil {
			return fmt.Errorf("GPG forwarding requires pubkey: %w", err)
		}
		b.gpgPubkey = pubkey
	}

	// Start docker exec
	b.cmd = exec.CommandContext(ctx, "docker", "exec", "-i", b.containerID, "/usr/local/bin/clawker-socket-server")

	var err error
	b.stdin, err = b.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	b.stdout, err = b.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Capture stderr for debugging
	b.cmd.Stderr = os.Stderr

	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start socket-forwarder: %w", err)
	}

	// Send PUBKEY if GPG forwarding is enabled
	// The socket-forwarder reads socket config from CLAWKER_REMOTE_SOCKETS env var
	if b.gpgEnabled {
		if err := b.sendMessage(Message{Type: MsgPubkey, StreamID: 0, Payload: b.gpgPubkey}); err != nil {
			// Clean up the subprocess we started
			b.cmd.Process.Kill()
			b.cmd.Wait() //nolint:errcheck // best-effort cleanup
			return fmt.Errorf("failed to send pubkey: %w", err)
		}
	}

	// Start reading messages
	b.readWg.Add(1)
	go b.readLoop()

	// Wait for READY message
	select {
	case err := <-b.errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop terminates the bridge and cleans up.
// It is safe to call multiple times.
func (b *Bridge) Stop() error {
	b.closeOnce.Do(func() { close(b.done) })

	// Close streams
	b.streamMu.Lock()
	for _, conn := range b.streams {
		conn.Close()
	}
	b.streams = make(map[uint32]net.Conn)
	b.streamMu.Unlock()

	// Close pipes
	if b.stdin != nil {
		b.stdin.Close()
	}

	// Wait for read loop to finish
	b.readWg.Wait()

	// Kill the process if still running
	if b.cmd != nil && b.cmd.Process != nil {
		b.cmd.Process.Kill()
		b.cmd.Wait()
	}

	return nil
}

// Wait blocks until the bridge exits.
func (b *Bridge) Wait() error {
	b.readWg.Wait()
	if b.cmd != nil {
		return b.cmd.Wait()
	}
	return nil
}

func (b *Bridge) readLoop() {
	defer b.readWg.Done()

	reader := bufio.NewReader(b.stdout)
	readyReceived := false

	defer func() {
		if !readyReceived {
			select {
			case b.errCh <- fmt.Errorf("bridge exited before READY"):
			default:
			}
		}
	}()

	for {
		select {
		case <-b.done:
			return
		default:
		}

		msg, err := readMessage(reader)
		if err != nil {
			if err != io.EOF {
				logger.Debug().Err(err).Msg("bridge read error")
			}
			return
		}

		switch msg.Type {
		case MsgReady:
			readyReceived = true
			// Signal that we're ready (non-blocking)
			select {
			case b.errCh <- nil:
			default:
			}

		case MsgError:
			errMsg := string(msg.Payload)
			logger.Error().Str("error", errMsg).Msg("socket-forwarder error")
			if !readyReceived {
				select {
				case b.errCh <- fmt.Errorf("forwarder error: %s", errMsg):
				default:
				}
			}

		case MsgOpen:
			b.handleOpen(msg)

		case MsgData:
			b.handleData(msg)

		case MsgClose:
			b.handleClose(msg)
		}
	}
}

func (b *Bridge) handleOpen(msg Message) {
	socketType := string(msg.Payload)
	streamID := msg.StreamID

	socketPath, err := resolveHostSocket(socketType)
	if err != nil {
		logger.Error().Err(err).Str("type", socketType).Msg("failed to resolve host socket")
		if b.Warnings != nil {
			fmt.Fprintf(b.Warnings, "Warning: %v\n", err)
		}
		b.sendMessage(Message{Type: MsgClose, StreamID: streamID})
		return
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		logger.Error().Err(err).Str("socket", socketPath).Msg("failed to connect to host socket")
		b.sendMessage(Message{Type: MsgClose, StreamID: streamID})
		return
	}

	b.streamMu.Lock()
	b.streams[streamID] = conn
	b.streamMu.Unlock()

	// Start reading from the host socket
	go b.readFromHostSocket(streamID, conn)

	logger.Debug().Uint32("stream", streamID).Str("type", socketType).Msg("opened host socket")
}

// resolveHostSocket returns the host Unix socket path for the given type.
func resolveHostSocket(socketType string) (string, error) {
	switch socketType {
	case "gpg-agent":
		return getGPGExtraSocket()
	case "ssh-agent":
		path := os.Getenv("SSH_AUTH_SOCK")
		if path == "" {
			return "", fmt.Errorf("SSH_AUTH_SOCK not set on host; SSH agent forwarding unavailable")
		}
		return path, nil
	default:
		return "", fmt.Errorf("unknown socket type: %s", socketType)
	}
}

func (b *Bridge) readFromHostSocket(streamID uint32, conn net.Conn) {
	buf := make([]byte, readBufSize)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			b.closeStream(streamID)
			return
		}

		if err := b.sendMessage(Message{
			Type:     MsgData,
			StreamID: streamID,
			Payload:  buf[:n],
		}); err != nil {
			b.closeStream(streamID)
			return
		}
	}
}

func (b *Bridge) handleData(msg Message) {
	b.streamMu.RLock()
	conn, ok := b.streams[msg.StreamID]
	b.streamMu.RUnlock()

	if !ok {
		return
	}

	if _, err := conn.Write(msg.Payload); err != nil {
		b.closeStream(msg.StreamID)
	}
}

func (b *Bridge) handleClose(msg Message) {
	b.closeStream(msg.StreamID)
}

func (b *Bridge) closeStream(streamID uint32) {
	b.streamMu.Lock()
	conn, ok := b.streams[streamID]
	if ok {
		delete(b.streams, streamID)
	}
	b.streamMu.Unlock()

	if ok {
		conn.Close()
		b.sendMessage(Message{Type: MsgClose, StreamID: streamID})
	}
}

func (b *Bridge) sendMessage(msg Message) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	// Single 9-byte header: length(4) + type(1) + streamID(4)
	var header [9]byte
	length := uint32(1 + 4 + len(msg.Payload))
	binary.BigEndian.PutUint32(header[0:4], length)
	header[4] = msg.Type
	binary.BigEndian.PutUint32(header[5:9], msg.StreamID)

	if _, err := b.stdin.Write(header[:]); err != nil {
		return err
	}
	if len(msg.Payload) > 0 {
		if _, err := b.stdin.Write(msg.Payload); err != nil {
			return err
		}
	}
	return nil
}

// readMessage reads a length-prefixed message.
func readMessage(r *bufio.Reader) (Message, error) {
	// Read length (4 bytes)
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return Message{}, err
	}
	length := binary.BigEndian.Uint32(lenBuf)

	if length < 5 {
		return Message{}, fmt.Errorf("message too short: %d", length)
	}
	if length > maxMessageSize {
		return Message{}, fmt.Errorf("message too large: %d", length)
	}

	// Read type (1 byte)
	msgType, err := r.ReadByte()
	if err != nil {
		return Message{}, err
	}

	// Read stream ID (4 bytes)
	streamBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, streamBuf); err != nil {
		return Message{}, err
	}
	streamID := binary.BigEndian.Uint32(streamBuf)

	// Read payload
	payloadLen := length - 5
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Message{}, err
		}
	}

	return Message{
		Type:     msgType,
		StreamID: streamID,
		Payload:  payload,
	}, nil
}

// getGPGExtraSocket returns the path to the GPG agent's extra socket.
func getGPGExtraSocket() (string, error) {
	cmd := exec.Command("gpgconf", "--list-dir", "agent-extra-socket")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gpgconf failed: %w", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("gpgconf returned empty socket path")
	}
	return path, nil
}

// getHostGPGPubkey exports the host's GPG public key.
func getHostGPGPubkey() ([]byte, error) {
	cmd := exec.Command("gpg", "--export")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gpg --export failed: %w", err)
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("no GPG public keys found")
	}
	return output, nil
}
