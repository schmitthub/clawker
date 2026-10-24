//go:build linux

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
// Build: go build -o clawker-socket-server main.go
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
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[socket-forwarder] warning: cannot create log dir %s: %v\n", logDir, err)
		return func() {}
	}

	logPath := filepath.Join(logDir, logFile)

	// Simple rotation: if existing log > maxLogSize, rename to .1
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogSize {
		_ = os.Rename(logPath, logPath+".1")
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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
	Path string `json:"path"` // Unix socket path
	Type string `json:"type"` // "gpg-agent" or "ssh-agent"
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

// Linux openat(2) constants that package syscall does not export.
const (
	atFDCWD           = -0x64  // AT_FDCWD
	atSymlinkNoFollow = 0x100  // AT_SYMLINK_NOFOLLOW
	atEmptyPath       = 0x1000 // AT_EMPTY_PATH
)

// Modes for entries this root process creates in the agent's home.
const (
	permBits    = 0o777 // permission bits of a mode
	userDirMode = 0o700 // ~/.gnupg, ~/.ssh
	userFile    = 0o600 // pubring.kbx, gpg.conf, gpg-agent.conf
	socketUmask = 0o177 // yields a 0600 socket node at bind
)

// socketTarget is where a forwarded socket lives: the owning user's home, the
// directory name under it, and the socket file name. Only the shape
// /home/<user>/<dir>/<file> is accepted; every write is then made relative to
// a directory descriptor so a symlink the agent planted in its home cannot
// redirect a write made by this root process.
type socketTarget struct {
	home string
	dir  string
	file string
	uid  int
	gid  int
}

// resolveSocketTarget parses a socket path and resolves its owning user.
func resolveSocketTarget(path string) (socketTarget, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "home" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return socketTarget{}, fmt.Errorf("socket path %q is not /home/<user>/<dir>/<file>", path)
	}
	u, err := user.Lookup(parts[2])
	if err != nil {
		return socketTarget{}, fmt.Errorf("user lookup for %q: %w", parts[2], err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return socketTarget{}, fmt.Errorf("parse uid %q for user %s: %w", u.Uid, parts[2], err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return socketTarget{}, fmt.Errorf("parse gid %q for user %s: %w", u.Gid, parts[2], err)
	}
	return socketTarget{home: "/home/" + parts[2], dir: parts[3], file: parts[4], uid: uid, gid: gid}, nil
}

// userDir is a directory under the target user's home, opened without
// following symlinks. File operations are relative to its descriptor.
type userDir struct {
	fd   int
	path string
	uid  int
	gid  int
}

// checkOwnedDir refuses a directory that the target user does not own or
// that group or other can write.
func checkOwnedDir(fd int, path string, uid int) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if int(st.Uid) != uid {
		return fmt.Errorf("refusing %s: owned by uid %d, want %d", path, st.Uid, uid)
	}
	if st.Mode&0o022 != 0 {
		return fmt.Errorf("refusing %s: group or other writable (mode %o)", path, st.Mode&permBits)
	}
	return nil
}

// openDirNoFollow opens name under dirFd as a directory. A symlink or a
// non-directory at name is refused.
func openDirNoFollow(dirFd int, name, path string) (int, error) {
	fd, err := syscall.Openat(dirFd, name, syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	switch {
	case errors.Is(err, syscall.ELOOP):
		return -1, fmt.Errorf("refusing %s: is a symlink", path)
	case errors.Is(err, syscall.ENOTDIR):
		return -1, fmt.Errorf("refusing %s: not a directory", path)
	case err != nil:
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	return fd, nil
}

// openUserDir opens home/name for the user uid, creating it 0700 when absent.
// Neither home nor name may be a symlink, and both must be owned by uid.
func openUserDir(home, name string, uid, gid int) (*userDir, error) {
	homeFd, err := openDirNoFollow(atFDCWD, home, home)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(homeFd)
	if err = checkOwnedDir(homeFd, home, uid); err != nil {
		return nil, err
	}
	path := home + "/" + name
	fd, err := openDirNoFollow(homeFd, name, path)
	if errors.Is(err, syscall.ENOENT) {
		if err = syscall.Mkdirat(homeFd, name, userDirMode); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", path, err)
		}
		fd, err = openDirNoFollow(homeFd, name, path)
	}
	if err != nil {
		return nil, err
	}
	d := &userDir{fd: fd, path: path, uid: uid, gid: gid}
	// Chown through the descriptor: a chown by name could be redirected to a
	// hard link the agent swapped in after the open.
	if err = syscall.Fchown(fd, uid, gid); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("chown %s: %w", path, err)
	}
	if err = checkOwnedDir(fd, path, uid); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the directory descriptor.
func (d *userDir) Close() error {
	if err := syscall.Close(d.fd); err != nil {
		return fmt.Errorf("close %s: %w", d.path, err)
	}
	return nil
}

// prepareEntry inspects name under the directory without following symlinks.
// An entry of type want is unlinked so the caller can recreate it; any other
// existing entry is refused.
func (d *userDir) prepareEntry(name string, want uint32) error {
	path := d.path + "/" + name
	var st syscall.Stat_t
	err := syscall.Fstatat(d.fd, name, &st, atSymlinkNoFollow)
	switch {
	case errors.Is(err, syscall.ENOENT):
		return nil
	case err != nil:
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Mode&syscall.S_IFMT != want {
		return fmt.Errorf("refusing %s: existing entry has mode %o", path, st.Mode&syscall.S_IFMT)
	}
	if err = syscall.Unlinkat(d.fd, name); err != nil {
		return fmt.Errorf("unlink %s: %w", path, err)
	}
	return nil
}

// writeFile replaces the regular file name with data, mode 0600, owned by the
// target user. A symlink or special file at name is refused.
func (d *userDir) writeFile(name string, data []byte) error {
	if err := d.prepareEntry(name, syscall.S_IFREG); err != nil {
		return err
	}
	path := d.path + "/" + name
	flags := syscall.O_WRONLY | syscall.O_CREAT | syscall.O_EXCL | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	fd, err := syscall.Openat(d.fd, name, flags, userFile)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Chown(d.uid, d.gid); err != nil {
		_ = f.Close()
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// chownSocketNode changes the owner of the socket node name. The listener
// descriptor refers to the sockfs inode, not the filesystem node, so the node
// is opened with O_PATH|O_NOFOLLOW, checked to be a socket, and chowned
// through that descriptor. The worst a swap after the bind can reach is
// another socket node.
func (d *userDir) chownSocketNode(name string) error {
	path := d.path + "/" + name
	fd, err := syscall.Openat(d.fd, name, syscall.O_PATH|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err = syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFSOCK {
		return fmt.Errorf("refusing %s: not a socket after bind (mode %o)", path, st.Mode&syscall.S_IFMT)
	}
	if err = syscall.Fchownat(fd, "", d.uid, d.gid, atEmptyPath); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

// listenUnix binds a Unix socket at name, mode 0600, owned by the target user.
// The bind resolves through the open directory descriptor, and the umask
// sets the mode at creation so no chmod on the path is needed afterwards.
// The dir fd stays open only for this call; the next start unlinks the stale
// socket node through prepareEntry.
func (d *userDir) listenUnix(name string) (net.Listener, error) {
	if err := d.prepareEntry(name, syscall.S_IFSOCK); err != nil {
		return nil, err
	}
	path := d.path + "/" + name
	old := syscall.Umask(socketUmask)
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "unix", fmt.Sprintf("/proc/self/fd/%d/%s", d.fd, name))
	_ = syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err = d.chownSocketNode(name); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// killExistingGPGAgent kills any running gpg-agent for the given GNUPGHOME directory.
// This is GPG's own sanctioned mechanism — gpgconf --kill targets only the agent for
// the specific homedir, needs no sudo, and doesn't depend on procps/pkill.
// Errors are non-fatal: the agent may not exist (common/expected case).
func killExistingGPGAgent(gnupgDir string) {
	cmd := exec.Command("gpgconf", "--homedir", gnupgDir, "--kill", "gpg-agent")
	if output, err := cmd.CombinedOutput(); err != nil {
		logf("[socket-forwarder] warning: gpgconf --kill gpg-agent failed: %v (output: %s)\n", err, strings.TrimSpace(string(output)))
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

	// Create socket listeners
	listeners := make(map[string]net.Listener)
	for _, sock := range sockets {
		listener, err := f.createSocketListener(sock)
		if err != nil {
			logf("[socket-forwarder] error: failed to create socket %s: %v\n", sock.Path, err)
			f.sendError(0, fmt.Sprintf("failed to create socket %s: %v", sock.Path, err))
			return 1
		}
		listeners[sock.Type] = listener

		// Start accept goroutine
		go f.acceptLoop(listener, sock.Type)
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
	var gpgSock string
	for _, s := range f.sockets {
		if s.Type == "gpg-agent" {
			gpgSock = s.Path
			break
		}
	}
	if gpgSock == "" {
		return fmt.Errorf("no GPG socket configured")
	}
	target, err := resolveSocketTarget(gpgSock)
	if err != nil {
		return err
	}
	dir, err := openUserDir(target.home, target.dir, target.uid, target.gid)
	if err != nil {
		return err
	}
	defer dir.Close()

	if err = dir.writeFile("pubring.kbx", pubkey); err != nil {
		return err
	}
	logf("[socket-forwarder] wrote %d bytes to %s/pubring.kbx\n", len(pubkey), dir.path)

	// gpg.conf with no-autostart prevents GPG from spawning gpg-agent.
	// Without it, any GPG operation (e.g., gpg --list-secret-keys) auto-starts
	// gpg-agent --daemon which rebinds S.gpg-agent, stealing the socket from
	// our forwarding server.
	gpgConf := "# Written by clawker-socket-server to prevent gpg-agent auto-start.\n" +
		"# The clawker socket bridge handles agent forwarding to the host.\n" +
		"no-autostart\n"
	if err = dir.writeFile("gpg.conf", []byte(gpgConf)); err != nil {
		return err
	}
	logf("[socket-forwarder] wrote %s/gpg.conf (no-autostart)\n", dir.path)

	// gpg-agent.conf carries sensible container defaults. no-grab and
	// disable-scdaemon do NOT prevent gpg-agent from binding the standard
	// socket (GnuPG 2.1+ mandates it). The real protection is no-autostart in
	// gpg.conf above, plus killExistingGPGAgent() in run().
	agentConf := "# Written by clawker-socket-server — sensible container defaults.\n" +
		"# These do NOT prevent socket binding; see gpg.conf no-autostart for that.\n" +
		"no-grab\n" +
		"disable-scdaemon\n"
	if err = dir.writeFile("gpg-agent.conf", []byte(agentConf)); err != nil {
		return err
	}
	logf("[socket-forwarder] wrote %s/gpg-agent.conf (no-grab, disable-scdaemon)\n", dir.path)
	return nil
}

func (f *Forwarder) createSocketListener(sock SocketConfig) (net.Listener, error) {
	target, err := resolveSocketTarget(sock.Path)
	if err != nil {
		return nil, err
	}
	dir, err := openUserDir(target.home, target.dir, target.uid, target.gid)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	listener, err := dir.listenUnix(target.file)
	if err != nil {
		return nil, err
	}
	logf("[socket-forwarder] listening on %s (%s)\n", sock.Path, sock.Type)
	return listener, nil
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
