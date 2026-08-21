// TRIPWIRE: stdlib only — compiled standalone in Docker (//go:embed, no go.mod
// in the build stage). NEVER import clawker-module packages (e.g. internal/consts);
// it breaks the image build. Inline literals here are intentional, exempt from
// the no-hardcoded-strings policy. See internal/hostproxy/internals/CLAUDE.md.
//
// socket-forwarder is a multiplexing socket forwarder that runs inside clawker
// containers. It communicates with the host via stdin/stdout using a simple
// length-prefixed binary protocol, similar to VS Code's muxrpc approach.
//
// This allows socket forwarding without requiring network access from the
// container to the host - all communication happens over the docker exec channel.
//
// Build: go build -o socket-forwarder clawkercp.go
// Usage: Launched via `docker exec -i <container> socket-forwarder`
//
// Environment:
//   - CLAWKER_REMOTE_SOCKETS: JSON array of socket configs, e.g.:
//     [{"path": "/home/<user>/.gnupg/S.gpg-agent", "type": "gpg-agent"}]
//
// Protocol:
//
//	Message format: [4-byte length][1-byte type][4-byte stream][payload]
//	Types: DATA=1, OPEN=2, CLOSE=3, PUBKEY=4, READY=5, ERROR=6
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// ProtocolVersion is the muxrpc wire protocol version.
const ProtocolVersion = 1

// Message types
const (
	MsgData   byte = 1 // Socket data
	MsgOpen   byte = 2 // New connection (payload = socket type)
	MsgClose  byte = 3 // Connection closed
	MsgPubkey byte = 4 // GPG public key data
	MsgReady  byte = 5 // Forwarder ready
	MsgError  byte = 6 // Error message
)

const (
	socketTypeBridged = "bridged"
	envClawkerUser    = "CLAWKER_USER"
	defaultAgentUser  = "clawker"
	defaultSocketMode = 0o600
)

// Buffer and message size limits.
const (
	readBufSize    = 64 * 1024 // Per-stream read buffer
	maxMessageSize = 1 << 20   // 1 MiB maximum message payload
)

// logWriter is the destination for all log output. Defaults to stderr,
// upgraded to MultiWriter(stderr, file) by initLogging().
var logWriter io.Writer = os.Stderr

const (
	logDir     = "/var/log/clawker"
	logFile    = "socket-server.log"
	maxLogSize = 1 << 20 // 1 MiB
)

// initLogging sets up file logging alongside stderr. Returns a cleanup function
// that closes the log file. Errors are non-fatal — logging degrades to stderr only.
func initLogging() func() {
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "[socket-forwarder] warning: cannot create log dir %s: %v\n", logDir, err)
		return func() {}
	}

	logPath := filepath.Join(logDir, logFile)

	// Simple rotation: if existing log > maxLogSize, rename to .1
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogSize {
		_ = os.Rename(logPath, logPath+".1")
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[socket-forwarder] warning: cannot open log file %s: %v\n", logPath, err)
		return func() {}
	}

	logWriter = io.MultiWriter(os.Stderr, f)
	return func() {
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[socket-forwarder] warning: failed to close log file: %v\n", err)
		}
	}
}

// logf writes a formatted log message to logWriter.
func logf(format string, args ...any) {
	fmt.Fprintf(logWriter, format, args...)
}

// logln writes a log line to logWriter.
func logln(msg string) {
	fmt.Fprintln(logWriter, msg)
}

// SocketConfig defines a socket to create and forward.
type SocketConfig struct {
	Path  string `json:"path"`            // Unix socket path
	Type  string `json:"type"`            // "gpg-agent", "ssh-agent", or "bridged"
	Group string `json:"group,omitempty"` // Bridged socket group
	Mode  string `json:"mode,omitempty"`  // Bridged socket mode
}

type bridgedSocketPermissions struct {
	group string
	gid   int
	mode  os.FileMode
}

// Message represents a protocol message.
type Message struct {
	Type     byte
	StreamID uint32
	Payload  []byte
}

// Forwarder manages the socket forwarding.
type Forwarder struct {
	sockets  []SocketConfig
	streams  map[uint32]net.Conn
	streamMu sync.RWMutex
	nextID   uint32
	writeMu  sync.Mutex
	stdout   *bufio.Writer
}

type privilegeOps struct {
	effectiveUID func() int
	lookupUser   func(string) (*user.User, error)
	groupIDs     func(*user.User) ([]string, error)
	setGroups    func([]int) error
	setGID       func(int) error
	setUID       func(int) error
}

func systemPrivilegeOperations() privilegeOps {
	return privilegeOps{
		effectiveUID: os.Geteuid,
		lookupUser:   user.Lookup,
		groupIDs:     func(account *user.User) ([]string, error) { return account.GroupIds() },
		setGroups:    syscall.Setgroups,
		setGID:       syscall.Setgid,
		setUID:       syscall.Setuid,
	}
}

func dropAgentPrivileges(username string, ops privilegeOps) error {
	if ops.effectiveUID() != 0 {
		return nil
	}

	account, uid, gid, lookupErr := lookupPrivilegeIdentity(username, ops)
	if lookupErr != nil {
		return lookupErr
	}
	groups, groupsErr := privilegeGroups(username, account, gid, ops)
	if groupsErr != nil {
		return groupsErr
	}

	if setGroupsErr := ops.setGroups(groups); setGroupsErr != nil {
		return fmt.Errorf("set supplementary groups for agent user %q: %w", username, setGroupsErr)
	}
	if setGIDErr := ops.setGID(gid); setGIDErr != nil {
		return fmt.Errorf("set primary group for agent user %q: %w", username, setGIDErr)
	}
	if setUIDErr := ops.setUID(uid); setUIDErr != nil {
		return fmt.Errorf("set UID for agent user %q: %w", username, setUIDErr)
	}
	return nil
}

func lookupPrivilegeIdentity(username string, ops privilegeOps) (*user.User, int, int, error) {
	account, lookupErr := ops.lookupUser(username)
	if lookupErr != nil {
		return nil, 0, 0, fmt.Errorf("lookup agent user %q: %w", username, lookupErr)
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	if uidErr != nil {
		return nil, 0, 0, fmt.Errorf("parse agent user UID %q: %w", account.Uid, uidErr)
	}
	gid, gidErr := strconv.Atoi(account.Gid)
	if gidErr != nil {
		return nil, 0, 0, fmt.Errorf("parse agent user GID %q: %w", account.Gid, gidErr)
	}
	return account, uid, gid, nil
}

func privilegeGroups(username string, account *user.User, gid int, ops privilegeOps) ([]int, error) {
	rawGroups, groupsErr := ops.groupIDs(account)
	if groupsErr != nil {
		return nil, fmt.Errorf("list groups for agent user %q: %w", username, groupsErr)
	}
	groups := make([]int, 0, len(rawGroups)+1)
	seen := make(map[int]struct{}, len(rawGroups)+1)
	groups = append(groups, gid)
	seen[gid] = struct{}{}
	for _, rawGroup := range rawGroups {
		groupID, parseErr := strconv.Atoi(rawGroup)
		if parseErr != nil {
			return nil, fmt.Errorf("parse group ID %q for agent user %q: %w", rawGroup, username, parseErr)
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		groups = append(groups, groupID)
		seen[groupID] = struct{}{}
	}

	return groups, nil
}

// getTargetUserFromPath extracts the username from a path like /home/<user>/.gnupg
// and returns the UID and GID for that user. Returns -1, -1 if user lookup fails.
func getTargetUserFromPath(path string) (int, int) {
	// Parse username from /home/<username>/... path
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[1] != "home" {
		logf("[socket-forwarder] warning: path %q not under /home/, cannot determine target user\n", path)
		return -1, -1
	}
	username := parts[2]

	// Look up user
	u, err := user.Lookup(username)
	if err != nil {
		logf("[socket-forwarder] warning: user lookup failed for %q: %v\n", username, err)
		return -1, -1
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		logf("[socket-forwarder] warning: failed to parse UID %q for user %s: %v\n", u.Uid, username, err)
		return -1, -1
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		logf("[socket-forwarder] warning: failed to parse GID %q for user %s: %v\n", u.Gid, username, err)
		return -1, -1
	}

	return uid, gid
}

// killExistingGPGAgent kills any running gpg-agent for the given GNUPGHOME directory.
// This is GPG's own sanctioned mechanism — gpgconf --kill targets only the agent for
// the specific homedir, needs no sudo, and doesn't depend on procps/pkill.
// Errors are non-fatal: the agent may not exist (common/expected case).
func killExistingGPGAgent(gnupgDir string) {
	cmd := exec.Command("gpgconf", "--homedir", gnupgDir, "--kill", "gpg-agent")
	if output, err := cmd.CombinedOutput(); err != nil {
		logf(
			"[socket-forwarder] warning: gpgconf --kill gpg-agent failed: %v (output: %s)\n",
			err,
			strings.TrimSpace(string(output)),
		)
	} else {
		logf("[socket-forwarder] killed existing gpg-agent for %s\n", gnupgDir)
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	cleanupLog := initLogging()
	defer cleanupLog()

	// Read socket config from environment
	socketsJSON := os.Getenv("CLAWKER_REMOTE_SOCKETS")
	if socketsJSON == "" {
		logln("[socket-forwarder] error: CLAWKER_REMOTE_SOCKETS not set")
		return 1
	}

	var sockets []SocketConfig
	if err := json.Unmarshal([]byte(socketsJSON), &sockets); err != nil {
		logf("[socket-forwarder] error: failed to parse CLAWKER_REMOTE_SOCKETS: %v\n", err)
		return 1
	}

	if len(sockets) == 0 {
		logln("[socket-forwarder] error: CLAWKER_REMOTE_SOCKETS is empty")
		return 1
	}

	f := &Forwarder{
		sockets: sockets,
		streams: make(map[uint32]net.Conn),
		stdout:  bufio.NewWriter(os.Stdout),
	}

	reader := bufio.NewReader(os.Stdin)
	agentUser := os.Getenv(envClawkerUser)
	if agentUser == "" {
		agentUser = defaultAgentUser
	}
	if err := dropAgentPrivileges(agentUser, systemPrivilegeOperations()); err != nil {
		logf("[socket-forwarder] error: failed to drop privileges: %v\n", err)
		f.sendError(0, "failed to drop privileges: "+err.Error())
		return 1
	}
	groups, err := os.Getgroups()
	if err != nil {
		logf("[socket-forwarder] error: failed to read process groups: %v\n", err)
		f.sendError(0, "failed to read process groups: "+err.Error())
		return 1
	}
	logf(
		"[socket-forwarder] running as user %q uid=%d gid=%d groups=%v\n",
		agentUser,
		os.Geteuid(),
		os.Getegid(),
		groups,
	)

	// Check if GPG forwarding is enabled - if so, wait for PUBKEY message
	hasGPG := false
	for _, s := range sockets {
		if s.Type == "gpg-agent" {
			hasGPG = true
			break
		}
	}

	if hasGPG {
		logln("[socket-forwarder] waiting for PUBKEY message...")
		msg, err := readMessage(reader)
		if err != nil {
			logf("[socket-forwarder] error: failed to read pubkey: %v\n", err)
			f.sendError(0, "failed to read pubkey: "+err.Error())
			return 1
		}
		if msg.Type != MsgPubkey {
			logf("[socket-forwarder] error: expected PUBKEY message, got type %d\n", msg.Type)
			f.sendError(0, "expected PUBKEY message")
			return 1
		}
		if err := f.setupGPGPubkey(msg.Payload); err != nil {
			logf("[socket-forwarder] error: failed to setup GPG pubkey: %v\n", err)
			f.sendError(0, "failed to setup GPG pubkey: "+err.Error())
			return 1
		}

		// Kill any existing gpg-agent that may have been auto-started before
		// our config files were in place. This is GPG's sanctioned mechanism.
		for _, s := range sockets {
			if s.Type == "gpg-agent" {
				killExistingGPGAgent(filepath.Dir(s.Path))
				break
			}
		}
	}

	// Create socket listeners.
	listeners, err := f.createSocketListeners()
	if err != nil {
		return 1
	}

	// Send READY
	logln("[socket-forwarder] ready, listening on sockets")
	if err := f.sendMessage(Message{Type: MsgReady, StreamID: 0}); err != nil {
		logf("[socket-forwarder] error: failed to send READY: %v\n", err)
		return 1
	}

	// Main loop: read messages from host and dispatch to streams
	for {
		msg, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				logln("[socket-forwarder] stdin closed, exiting")
				break
			}
			logf("[socket-forwarder] read error: %v\n", err)
			break
		}

		switch msg.Type {
		case MsgData:
			f.handleData(msg)
		case MsgClose:
			f.handleClose(msg)
		case MsgError:
			f.handleError(msg)
		default:
			// Ignore unknown messages
		}
	}

	// Cleanup
	for _, l := range listeners {
		l.Close()
	}
	return 0
}

func (f *Forwarder) setupGPGPubkey(pubkey []byte) error {
	// Find GPG socket path to determine .gnupg directory
	var gnupgDir string
	for _, s := range f.sockets {
		if s.Type == "gpg-agent" {
			gnupgDir = filepath.Dir(s.Path)
			break
		}
	}
	if gnupgDir == "" {
		return fmt.Errorf("no GPG socket configured")
	}

	// Get target user from socket path (e.g., /home/<user>/.gnupg -> <user>)
	uid, gid := getTargetUserFromPath(gnupgDir)

	// Create .gnupg directory
	if err := os.MkdirAll(gnupgDir, 0o700); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}
	// Chown directory to target user
	if uid >= 0 && gid >= 0 {
		if err := os.Chown(gnupgDir, uid, gid); err != nil {
			logf("[socket-forwarder] warning: failed to chown %s: %v\n", gnupgDir, err)
		}
	}

	// Write pubring.kbx
	pubringPath := filepath.Join(gnupgDir, "pubring.kbx")
	if err := os.WriteFile(pubringPath, pubkey, 0o600); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	// Chown file to target user
	if uid >= 0 && gid >= 0 {
		if err := os.Chown(pubringPath, uid, gid); err != nil {
			logf("[socket-forwarder] warning: failed to chown %s: %v\n", pubringPath, err)
		}
	}

	logf("[socket-forwarder] wrote %d bytes to %s\n", len(pubkey), pubringPath)

	// Write gpg.conf with no-autostart to prevent GPG from spawning gpg-agent.
	// Without this, any GPG operation (e.g., gpg --list-secret-keys) auto-starts
	// gpg-agent --daemon which rebinds S.gpg-agent, stealing the socket from
	// our forwarding server.
	gpgConfPath := filepath.Join(gnupgDir, "gpg.conf")
	gpgConfContent := "# Written by clawker-socket-server to prevent gpg-agent auto-start.\n" +
		"# The clawker socket bridge handles agent forwarding to the host.\n" +
		"no-autostart\n"
	if err := os.WriteFile(gpgConfPath, []byte(gpgConfContent), 0o600); err != nil {
		return fmt.Errorf("write gpg.conf failed: %w", err)
	}
	if uid >= 0 && gid >= 0 {
		if err := os.Chown(gpgConfPath, uid, gid); err != nil {
			logf("[socket-forwarder] warning: failed to chown %s: %v\n", gpgConfPath, err)
		}
	}
	logf("[socket-forwarder] wrote %s (no-autostart)\n", gpgConfPath)

	// Write gpg-agent.conf with sensible container defaults. Note: no-grab and
	// disable-scdaemon are useful container settings but do NOT prevent gpg-agent
	// from binding the standard socket (GnuPG 2.1+ mandates it). The real protection
	// is no-autostart in gpg.conf above, plus killExistingGPGAgent() in main().
	agentConfPath := filepath.Join(gnupgDir, "gpg-agent.conf")
	agentConfContent := "# Written by clawker-socket-server — sensible container defaults.\n" +
		"# These do NOT prevent socket binding; see gpg.conf no-autostart for that.\n" +
		"no-grab\n" +
		"disable-scdaemon\n"
	if err := os.WriteFile(agentConfPath, []byte(agentConfContent), 0o600); err != nil {
		return fmt.Errorf("write gpg-agent.conf failed: %w", err)
	}
	if uid >= 0 && gid >= 0 {
		if err := os.Chown(agentConfPath, uid, gid); err != nil {
			logf("[socket-forwarder] warning: failed to chown %s: %v\n", agentConfPath, err)
		}
	}
	logf("[socket-forwarder] wrote %s (no-grab, disable-scdaemon)\n", agentConfPath)

	return nil
}

func (f *Forwarder) createSocketListener(sock SocketConfig) (net.Listener, error) {
	permissions := bridgedSocketPermissions{group: "", gid: -1, mode: defaultSocketMode}
	if sock.Type == socketTypeBridged {
		var err error
		permissions, err = resolveBridgedSocketPermissions(sock)
		if err != nil {
			return nil, err
		}
	}

	// Create parent directory
	dir := filepath.Dir(sock.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir failed: %w", err)
	}

	uid, gid := -1, -1
	if sock.Type != socketTypeBridged {
		// SSH and GPG paths use the canonical container user.
		uid, gid = getTargetUserFromPath(sock.Path)
		if uid >= 0 && gid >= 0 {
			if err := os.Chown(dir, uid, gid); err != nil {
				logf("[socket-forwarder] warning: failed to chown %s: %v\n", dir, err)
			}
		}
	}

	// Remove existing socket
	if err := os.Remove(sock.Path); err != nil && !os.IsNotExist(err) {
		logf("[socket-forwarder] warning: failed to remove existing socket %s: %v\n", sock.Path, err)
	}

	// Create listener
	var listenConfig net.ListenConfig
	listener, listenErr := listenConfig.Listen(context.Background(), "unix", sock.Path)
	if listenErr != nil {
		return nil, fmt.Errorf("listen on socket %q: %w", sock.Path, listenErr)
	}

	permissionErr := applySocketPermissions(sock, permissions, uid, gid)
	if permissionErr != nil {
		if closeErr := listener.Close(); closeErr != nil {
			return nil, errors.Join(permissionErr, fmt.Errorf("close failed socket listener: %w", closeErr))
		}
		return nil, permissionErr
	}

	logf("[socket-forwarder] listening on %s (%s)\n", sock.Path, sock.Type)
	return listener, nil
}

func applySocketPermissions(sock SocketConfig, permissions bridgedSocketPermissions, uid, gid int) error {
	if sock.Type == socketTypeBridged {
		return applyResolvedBridgedSocketPermissions(sock.Path, permissions)
	}
	// Preserve the existing SSH and GPG permission behavior.
	if chmodErr := os.Chmod(sock.Path, 0o600); chmodErr != nil {
		return fmt.Errorf("set socket mode: %w", chmodErr)
	}
	if uid >= 0 && gid >= 0 {
		if chownErr := os.Chown(sock.Path, uid, gid); chownErr != nil {
			logf("[socket-forwarder] warning: failed to chown %s: %v\n", sock.Path, chownErr)
		}
	}
	return nil
}

func (f *Forwarder) createSocketListeners() (map[string]net.Listener, error) {
	listeners := make(map[string]net.Listener, len(f.sockets))
	for _, socket := range f.sockets {
		listener, err := f.createSocketListener(socket)
		if err != nil {
			for _, active := range listeners {
				if closeErr := active.Close(); closeErr != nil {
					logf("[socket-forwarder] warning: failed to close socket after setup error: %v\n", closeErr)
				}
			}
			wrapped := fmt.Errorf("failed to create socket %s: %w", socket.Path, err)
			logf("[socket-forwarder] error: %v\n", wrapped)
			f.sendError(0, wrapped.Error())
			return nil, wrapped
		}
		identifier := socket.Type
		if socket.Type == socketTypeBridged {
			identifier = socket.Path
		}
		listeners[identifier] = listener
		go f.acceptLoop(listener, identifier)
	}
	return listeners, nil
}

func resolveBridgedSocketPermissions(socket SocketConfig) (bridgedSocketPermissions, error) {
	permissions := bridgedSocketPermissions{group: "", gid: -1, mode: defaultSocketMode}
	if socket.Group != "" {
		group, err := user.LookupGroup(socket.Group)
		if err != nil {
			return permissions, fmt.Errorf("lookup group %q: %w", socket.Group, err)
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			return permissions, fmt.Errorf("parse group ID %q for %s: %w", group.Gid, socket.Group, err)
		}
		permissions.gid = gid
		permissions.group = socket.Group
	}
	if socket.Mode != "" {
		parsed, err := strconv.ParseUint(socket.Mode, 8, 12)
		if err != nil || parsed > 0o777 {
			return permissions, fmt.Errorf("parse socket mode %q", socket.Mode)
		}
		permissions.mode = os.FileMode(parsed)
	}
	return permissions, nil
}

func applyResolvedBridgedSocketPermissions(path string, permissions bridgedSocketPermissions) error {
	if permissions.gid >= 0 {
		if err := os.Chown(path, -1, permissions.gid); err != nil {
			return fmt.Errorf("set socket group %q: %w", permissions.group, err)
		}
	}
	if err := os.Chmod(path, permissions.mode); err != nil {
		return fmt.Errorf("set socket mode %04o: %w", permissions.mode, err)
	}
	return nil
}

func (f *Forwarder) acceptLoop(listener net.Listener, socketType string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logf("[socket-forwarder] accept error: %v\n", err)
			continue
		}

		// Assign stream ID
		streamID := atomic.AddUint32(&f.nextID, 1)

		f.streamMu.Lock()
		f.streams[streamID] = conn
		f.streamMu.Unlock()

		// Send OPEN message to host
		if err := f.sendMessage(Message{
			Type:     MsgOpen,
			StreamID: streamID,
			Payload:  []byte(socketType),
		}); err != nil {
			logf("[socket-forwarder] failed to send OPEN: %v\n", err)
			conn.Close()
			continue
		}

		// Start reading from connection
		go f.readFromConn(streamID, conn)
	}
}

func (f *Forwarder) readFromConn(streamID uint32, conn net.Conn) {
	buf := make([]byte, readBufSize)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			f.closeStream(streamID)
			return
		}

		// Send DATA to host
		if err := f.sendMessage(Message{
			Type:     MsgData,
			StreamID: streamID,
			Payload:  buf[:n],
		}); err != nil {
			f.closeStream(streamID)
			return
		}
	}
}

func (f *Forwarder) handleData(msg Message) {
	f.streamMu.RLock()
	conn, ok := f.streams[msg.StreamID]
	f.streamMu.RUnlock()

	if !ok {
		return
	}

	if _, err := conn.Write(msg.Payload); err != nil {
		f.closeStream(msg.StreamID)
	}
}

func (f *Forwarder) handleClose(msg Message) {
	f.closeStream(msg.StreamID)
}

func (f *Forwarder) handleError(msg Message) {
	logf("[socket-forwarder] host rejected stream %d: %s\n", msg.StreamID, msg.Payload)
	f.closeStream(msg.StreamID)
}

func (f *Forwarder) closeStream(streamID uint32) {
	f.streamMu.Lock()
	conn, ok := f.streams[streamID]
	if ok {
		delete(f.streams, streamID)
	}
	f.streamMu.Unlock()

	if ok {
		conn.Close()
		if err := f.sendMessage(Message{Type: MsgClose, StreamID: streamID}); err != nil {
			logf("[socket-forwarder] failed to send CLOSE for stream %d: %v\n", streamID, err)
		}
	}
}

func (f *Forwarder) sendMessage(msg Message) error {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()

	if err := writeMessage(f.stdout, msg); err != nil {
		return err
	}
	return f.stdout.Flush()
}

func (f *Forwarder) sendError(streamID uint32, errMsg string) {
	if err := f.sendMessage(Message{
		Type:     MsgError,
		StreamID: streamID,
		Payload:  []byte(errMsg),
	}); err != nil {
		logf("[socket-forwarder] failed to send error message: %v\n", err)
	}
}

// readMessage reads a length-prefixed message from the reader.
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
	payloadLen := length - 5 // subtract type + streamID
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

// writeMessage writes a length-prefixed message to the writer.
func writeMessage(w *bufio.Writer, msg Message) error {
	// Calculate length: type (1) + streamID (4) + payload
	length := uint32(1 + 4 + len(msg.Payload))

	// Write length
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, length)
	if _, err := w.Write(lenBuf); err != nil {
		return err
	}

	// Write type
	if err := w.WriteByte(msg.Type); err != nil {
		return err
	}

	// Write stream ID
	streamBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(streamBuf, msg.StreamID)
	if _, err := w.Write(streamBuf); err != nil {
		return err
	}

	// Write payload
	if len(msg.Payload) > 0 {
		if _, err := w.Write(msg.Payload); err != nil {
			return err
		}
	}

	return nil
}
