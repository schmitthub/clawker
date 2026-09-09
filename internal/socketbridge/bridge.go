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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/schmitthub/clawker/internal/consts"
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

const (
	eventBridgedSocketOpen             = "bridged_socket_open"
	eventBridgedSocketClose            = "bridged_socket_close"
	eventBridgedSocketIdentityError    = "bridged_socket_identity_error"
	eventBridgedSocketIdentityMismatch = "bridged_socket_identity_mismatch"
	dockerInspectEnvironmentFormat     = "{{json .Config.Env}}"
	containerSocketServerPath          = "/usr/local/bin/clawker-socket-server"
)

// SocketConfig defines a socket to forward.
type SocketConfig struct {
	Path  string `json:"path"`            // Unix socket path in container
	Type  string `json:"type"`            // Socket type or registration class
	Group string `json:"group,omitempty"` // Container socket group
	Mode  string `json:"mode,omitempty"`  // Container socket mode
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
	log         *logger.Logger
	sockets     map[string]BridgedSocket
	socketList  []BridgedSocket

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// Warnings receives user-visible warning messages (typically stderr).
	// If nil, warnings are suppressed.
	Warnings io.Writer

	streams  map[uint32]net.Conn
	bridged  map[uint32]BridgedSocket
	streamMu sync.RWMutex
	writeMu  sync.Mutex

	done      chan struct{}
	closeOnce sync.Once // Prevents double-close panic on done channel
	errCh     chan error
	readWg    sync.WaitGroup
}

// NewBridge creates a new socket bridge for the given container.
// gpgEnabled indicates whether GPG agent forwarding is configured.
func NewBridge(containerID string, gpgEnabled bool, sockets []BridgedSocket, log *logger.Logger) *Bridge {
	if log == nil {
		log = logger.Nop()
	}
	registrations := make(map[string]BridgedSocket, len(sockets))
	for _, socket := range sockets {
		registrations[socket.Target] = socket
	}
	return &Bridge{
		containerID: containerID,
		gpgEnabled:  gpgEnabled,
		log:         log,
		sockets:     registrations,
		socketList:  append([]BridgedSocket(nil), sockets...),
		streams:     make(map[uint32]net.Conn),
		bridged:     make(map[uint32]BridgedSocket),
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
	// Resolve the GPG pubkey if GPG forwarding is enabled. A host without
	// usable GPG material (no gpg binary, no keys — the config default
	// enables forwarding without knowing) degrades to SSH-only forwarding:
	// killing the whole bridge, and with it the container start, over an
	// optional lane would fail the deployment for a feature it never had.
	if b.gpgEnabled && len(b.gpgPubkey) == 0 {
		pubkey, err := getHostGPGPubkey()
		if err != nil {
			b.log.Warn().Err(err).
				Msg("GPG forwarding unavailable on this host; continuing with SSH-only forwarding")
			b.gpgEnabled = false
		} else {
			b.gpgPubkey = pubkey
		}
	}
	existingSockets, err := readContainerSocketConfig(ctx, b.containerID)
	if err != nil {
		return fmt.Errorf("read container socket configuration: %w", err)
	}
	if !b.gpgEnabled {
		existingSockets = removeSocketType(existingSockets, consts.SocketTypeGPGAgent)
	}
	socketsJSON, err := buildRemoteSocketConfig(existingSockets, b.socketList)
	if err != nil {
		return fmt.Errorf("build start-time socket configuration: %w", err)
	}

	// Start docker exec
	b.cmd = exec.CommandContext( //nolint:gosec // CommandContext passes each validated Docker argument without a shell.
		ctx,
		"docker",
		forwarderCommandArgs(b.containerID, socketsJSON)...,
	)

	b.stdin, err = b.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	b.stdout, err = b.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Route the container-side socket server's stderr through the
	// structured logger so its lines land in the shared bridge log
	// tagged with this container, instead of as raw untagged output.
	stderr, err := b.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}
	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start socket-forwarder: %w", err)
	}
	b.readWg.Add(1)
	go b.logStderr(stderr)

	// Send PUBKEY if GPG forwarding is enabled
	// The socket-forwarder reads socket config from CLAWKER_REMOTE_SOCKETS env var
	if b.gpgEnabled {
		if err := b.sendMessage(Message{Type: MsgPubkey, StreamID: 0, Payload: b.gpgPubkey}); err != nil {
			// Clean up the subprocess we started. Kill closes the stderr
			// pipe, so logStderr reaches EOF; drain readWg before Wait so
			// no pipe read is outstanding (readLoop is not yet running).
			b.cmd.Process.Kill()
			b.readWg.Wait()
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
	for streamID, conn := range b.streams {
		conn.Close()
		if registration, ok := b.bridged[streamID]; ok {
			b.log.Info().
				Str("event", eventBridgedSocketClose).
				Uint32("stream", streamID).
				Str("target", registration.Target).
				Str("host_path", registration.HostPath).
				Msg("closed bridged host socket")
		}
	}
	b.streams = make(map[uint32]net.Conn)
	b.bridged = make(map[uint32]BridgedSocket)
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

// logStderr forwards the container-side socket server's stderr lines to
// the structured logger. Runs until the pipe closes (process exit); Wait
// blocks on readWg so cmd.Wait is never called with reads outstanding.
func (b *Bridge) logStderr(r io.Reader) {
	defer b.readWg.Done()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		b.log.Debug().Str("stream", "socket-server").Msg(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		b.log.Debug().Err(err).Msg("socket server stderr closed with error")
	}
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
				b.log.Debug().Err(err).Msg("bridge read error")
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
			b.log.Error().Str("error", errMsg).Msg("socket-forwarder error")
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
	socketID := string(msg.Payload)
	streamID := msg.StreamID

	socketPath, registration, err := b.resolveOpenTarget(socketID)
	if err != nil {
		b.log.Error().Err(err).Str("socket_id", socketID).Msg("failed to resolve host socket")
		if b.Warnings != nil {
			if _, warningErr := fmt.Fprintf(b.Warnings, "Warning: %v\n", err); warningErr != nil {
				b.log.Debug().Err(warningErr).Msg("failed to write socket bridge warning")
			}
		}
		if registration == nil {
			b.sendOpenError(streamID, err)
		} else {
			b.sendClose(streamID)
		}
		return
	}

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		b.log.Error().Err(err).Str("socket", socketPath).Msg("failed to connect to host socket")
		if registration != nil {
			b.sendOpenError(streamID, fmt.Errorf("connect to registered socket: %w", err))
		} else {
			b.sendClose(streamID)
		}
		return
	}
	if registration != nil {
		uid, gid, identityErr := readListenerCredentials(conn)
		if identityErr != nil {
			b.closeConnection(streamID, conn)
			err = fmt.Errorf("read registered listener identity: %w", identityErr)
			b.log.Error().Err(err).
				Str("event", eventBridgedSocketIdentityError).
				Str("target", registration.Target).
				Str("host_path", registration.HostPath).
				Msg("failed to verify bridged socket listener")
			b.sendOpenError(streamID, err)
			return
		}
		if uid != registration.Identity.UID || gid != registration.Identity.GID {
			b.closeConnection(streamID, conn)
			err = fmt.Errorf(
				"listener identity mismatch for %s: approved uid=%d gid=%d, observed uid=%d gid=%d",
				registration.HostPath,
				registration.Identity.UID,
				registration.Identity.GID,
				uid,
				gid,
			)
			b.log.Error().Err(err).
				Str("event", eventBridgedSocketIdentityMismatch).
				Str("target", registration.Target).
				Str("host_path", registration.HostPath).
				Msg("listener identity mismatch")
			b.sendOpenError(streamID, err)
			return
		}
	}

	b.streamMu.Lock()
	b.streams[streamID] = conn
	if registration != nil {
		b.bridged[streamID] = *registration
	}
	b.streamMu.Unlock()

	// Start reading from the host socket
	go b.readFromHostSocket(streamID, conn)

	if registration != nil {
		b.log.Info().
			Str("event", eventBridgedSocketOpen).
			Uint32("stream", streamID).
			Str("target", registration.Target).
			Str("host_path", registration.HostPath).
			Msg("opened bridged host socket")
		return
	}
	b.log.Debug().Uint32("stream", streamID).Str("type", socketID).Msg("opened host socket")
}

func (b *Bridge) resolveOpenTarget(socketID string) (string, *BridgedSocket, error) {
	if registration, ok := b.sockets[socketID]; ok {
		return registration.HostPath, &registration, nil
	}
	path, err := resolveHostSocket(socketID)
	if err != nil {
		return "", nil, fmt.Errorf("unknown socket registration %q", socketID)
	}
	return path, nil, nil
}

func (b *Bridge) sendOpenError(streamID uint32, cause error) {
	if err := b.sendMessage(Message{Type: MsgError, StreamID: streamID, Payload: []byte(cause.Error())}); err != nil {
		b.log.Error().Err(err).Uint32("stream", streamID).Msg("failed to send socket open error")
	}
}

func (b *Bridge) sendClose(streamID uint32) {
	if err := b.sendMessage(Message{Type: MsgClose, StreamID: streamID, Payload: nil}); err != nil {
		b.log.Debug().Err(err).Uint32("stream", streamID).Msg("failed to send socket close")
	}
}

func (b *Bridge) closeConnection(streamID uint32, connection net.Conn) {
	if err := connection.Close(); err != nil {
		b.log.Debug().Err(err).Uint32("stream", streamID).Msg("failed to close host socket")
	}
}

// resolveHostSocket returns the host Unix socket path for the given type.
func resolveHostSocket(socketType string) (string, error) {
	switch socketType {
	case consts.SocketTypeGPGAgent:
		return getGPGExtraSocket()
	case consts.SocketTypeSSHAgent:
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
	registration, bridged := b.bridged[streamID]
	if ok {
		delete(b.streams, streamID)
		delete(b.bridged, streamID)
	}
	b.streamMu.Unlock()

	if ok {
		b.closeConnection(streamID, conn)
		b.sendClose(streamID)
		if bridged {
			b.log.Info().
				Str("event", eventBridgedSocketClose).
				Uint32("stream", streamID).
				Str("target", registration.Target).
				Str("host_path", registration.HostPath).
				Msg("closed bridged host socket")
		}
	}
}

func readContainerSocketConfig(ctx context.Context, containerID string) ([]SocketConfig, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", dockerInspectEnvironmentFormat, containerID)
	output, outputErr := cmd.Output()
	if outputErr != nil {
		return nil, fmt.Errorf("inspect container environment: %w", outputErr)
	}
	var environment []string
	unmarshalErr := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &environment)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("parse container environment: %w", unmarshalErr)
	}
	prefix := consts.EnvRemoteSockets + "="
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, prefix); ok {
			var sockets []SocketConfig
			socketsErr := json.Unmarshal([]byte(value), &sockets)
			if socketsErr != nil {
				return nil, fmt.Errorf("parse %s: %w", consts.EnvRemoteSockets, socketsErr)
			}
			return sockets, nil
		}
	}
	return nil, nil
}

func removeSocketType(sockets []SocketConfig, socketType string) []SocketConfig {
	filtered := make([]SocketConfig, 0, len(sockets))
	for _, socket := range sockets {
		if socket.Type != socketType {
			filtered = append(filtered, socket)
		}
	}
	return filtered
}

func buildRemoteSocketConfig(existing []SocketConfig, bridged []BridgedSocket) ([]byte, error) {
	sockets := append([]SocketConfig(nil), existing...)
	for _, registration := range bridged {
		sockets = append(sockets, SocketConfig{
			Path:  registration.Target,
			Type:  consts.SocketTypeBridged,
			Group: registration.Group,
			Mode:  registration.Mode,
		})
	}
	data, err := json.Marshal(sockets)
	if err != nil {
		return nil, fmt.Errorf("marshal remote sockets: %w", err)
	}
	return data, nil
}

func forwarderCommandArgs(containerID string, socketsJSON []byte) []string {
	return []string{
		"exec",
		"-i",
		"-e",
		consts.EnvRemoteSockets + "=" + string(socketsJSON),
		containerID,
		containerSocketServerPath,
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
// Returns an error if the socket doesn't exist — on macOS the agent is
// lazy-started and may not be running after a reboot.
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

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"gpg-agent extra socket not found at %s — is gpg-agent running? try: gpgconf --launch gpg-agent",
				path,
			)
		}
		return "", fmt.Errorf("cannot access gpg-agent extra socket at %s: %w", path, err)
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
