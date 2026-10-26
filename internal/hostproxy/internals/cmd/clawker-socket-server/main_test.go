//go:build linux

package main

import (
	"errors"
	"flag"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newTestHome(t *testing.T) (string, int, int) {
	t.Helper()
	return t.TempDir(), os.Getuid(), os.Getgid()
}

func mustOpenUserDir(t *testing.T, home, name string, uid, gid int) *userDir {
	t.Helper()
	d, err := openUserDir(home, name, uid, gid)
	if err != nil {
		t.Fatalf("openUserDir: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists (err=%v); a root write followed a symlink", path, err)
	}
}

func TestOpenUserDir_CreatesOwnedDir(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".gnupg", uid, gid)
	var st syscall.Stat_t
	if err := syscall.Fstat(d.fd, &st); err != nil {
		t.Fatal(err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR || st.Mode&0o777 != 0o700 || int(st.Uid) != uid {
		t.Fatalf("mode %o uid %d, want dir 0700 uid %d", st.Mode&0o777, st.Uid, uid)
	}
}

func TestOpenUserDir_RefusesSymlinkedDir(t *testing.T) {
	home, uid, gid := newTestHome(t)
	target := filepath.Join(home, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".gnupg")); err != nil {
		t.Fatal(err)
	}
	if _, err := openUserDir(home, ".gnupg", uid, gid); err == nil {
		t.Fatal("symlinked .gnupg accepted")
	}
}

func TestOpenUserDir_RefusesRegularFile(t *testing.T) {
	home, uid, gid := newTestHome(t)
	if err := os.WriteFile(filepath.Join(home, ".ssh"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openUserDir(home, ".ssh", uid, gid); err == nil {
		t.Fatal("regular file at .ssh accepted")
	}
}

func TestOpenUserDir_RefusesSymlinkedHome(t *testing.T) {
	realHome, uid, gid := newTestHome(t)
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(realHome, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openUserDir(link, ".gnupg", uid, gid); err == nil {
		t.Fatal("symlinked home accepted")
	}
	assertAbsent(t, filepath.Join(realHome, ".gnupg"))
}

func TestOpenUserDir_RefusesForeignOwner(t *testing.T) {
	home, uid, gid := newTestHome(t)
	if _, err := openUserDir(home, ".gnupg", uid+1, gid); err == nil {
		t.Fatal("home owned by another uid accepted")
	}
}

func TestOpenUserDir_RefusesWritableDir(t *testing.T) {
	home, uid, gid := newTestHome(t)
	dir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := openUserDir(home, ".ssh", uid, gid); err == nil {
		t.Fatal("group/other writable .ssh accepted")
	}
}

func TestUserDir_WriteFile(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".gnupg", uid, gid)
	path := filepath.Join(home, ".gnupg", "gpg.conf")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.writeFile("gpg.conf", []byte("new")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("content %q err %v", got, err)
	}
	var st syscall.Stat_t
	if err = syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Mode&0o777 != 0o600 || int(st.Uid) != uid {
		t.Fatalf("mode %o uid %d, want regular 0600 uid %d", st.Mode&0o777, st.Uid, uid)
	}
}

func TestUserDir_WriteFile_RefusesSymlink(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".gnupg", uid, gid)
	target := filepath.Join(home, "victim")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".gnupg", "pubring.kbx")); err != nil {
		t.Fatal(err)
	}
	if err := d.writeFile("pubring.kbx", []byte("key")); err == nil {
		t.Fatal("symlinked pubring.kbx accepted")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "secret" {
		t.Fatalf("symlink target changed: %q err %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".gnupg", "pubring.kbx")); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
}

func TestUserDir_WriteFile_RefusesFIFO(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".gnupg", uid, gid)
	path := filepath.Join(home, ".gnupg", "gpg-agent.conf")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.writeFile("gpg-agent.conf", []byte("x")); err == nil {
		t.Fatal("FIFO at gpg-agent.conf accepted")
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		t.Fatalf("FIFO replaced: mode %o err %v", st.Mode, err)
	}
}

func TestUserDir_ListenUnix(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".ssh", uid, gid)
	path := filepath.Join(home, ".ssh", "agent.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ul, ok := stale.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener type %T", stale)
	}
	ul.SetUnlinkOnClose(false)
	_ = stale.Close()

	l, err := d.listenUnix("agent.sock")
	if err != nil {
		t.Fatalf("listenUnix: %v", err)
	}
	defer l.Close()
	var st syscall.Stat_t
	if err = syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFSOCK || st.Mode&0o777 != 0o600 || int(st.Uid) != uid {
		t.Fatalf("mode %o uid %d, want socket 0600 uid %d", st.Mode&0o777, st.Uid, uid)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = c.Close()
}

func TestUserDir_ListenUnix_RefusesSymlink(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".ssh", uid, gid)
	target := filepath.Join(home, "victim.sock")
	victim, err := net.Listen("unix", target)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	if err = os.Symlink(target, filepath.Join(home, ".ssh", "agent.sock")); err != nil {
		t.Fatal(err)
	}
	if _, err = d.listenUnix("agent.sock"); err == nil {
		t.Fatal("symlinked socket path accepted")
	}
	var st syscall.Stat_t
	if err = syscall.Lstat(target, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFSOCK {
		t.Fatalf("symlink target replaced: mode %o err %v", st.Mode, err)
	}
	if _, err = os.Lstat(filepath.Join(home, ".ssh", "agent.sock")); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
}

func TestUserDir_ListenUnix_RefusesNonSocket(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".ssh", uid, gid)
	path := filepath.Join(home, ".ssh", "agent.sock")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.listenUnix("agent.sock"); err == nil {
		t.Fatal("FIFO at socket path accepted")
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		t.Fatalf("FIFO replaced: mode %o err %v", st.Mode, err)
	}
}

func TestResolveSocketTarget(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveSocketTarget("/home/" + u.Username + "/.ssh/agent.sock")
	if err != nil {
		t.Fatalf("resolveSocketTarget: %v", err)
	}
	want := socketTarget{
		home: "/home/" + u.Username, dir: ".ssh", file: "agent.sock", uid: os.Getuid(), gid: os.Getgid(),
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	for _, bad := range []string{"", "/tmp/x.sock", "/home/u/x.sock", "/home/u/.ssh/deep/x.sock", "/home//.ssh/x"} {
		if _, err = resolveSocketTarget(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err = resolveSocketTarget("/home/no-such-user-clawker/.ssh/agent.sock"); err == nil {
		t.Error("unknown user accepted")
	}
}

func TestOpenUserDir_RefusesForeignOwnedChild(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a directory owned by another uid")
	}
	const uid, gid = 1000, 1000
	home := t.TempDir()
	if err := os.Chown(home, uid, gid); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openUserDir(home, ".ssh", uid, gid); err == nil {
		t.Fatal("root-owned .ssh accepted")
	}
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatal(err)
	}
	if st.Uid != 0 {
		t.Fatalf(".ssh chowned to uid %d; a foreign-owned directory must keep its owner", st.Uid)
	}
}

func TestUserDir_RequireEmpty(t *testing.T) {
	home, uid, gid := newTestHome(t)
	d := mustOpenUserDir(t, home, ".gnupg", uid, gid)
	if err := d.requireEmpty(); err != nil {
		t.Fatalf("empty dir refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gnupg", "pubring.kbx"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.requireEmpty(); err == nil {
		t.Fatal("non-empty dir accepted")
	}
}

// agentUID and agentGID are the agent identity for the root tests. They differ
// from the root writer, so a missing chown leaves an entry the agent cannot use.
const (
	agentUID = 4242
	agentGID = 4242
	// agentHomeEnv names the home the helper process checks as the agent.
	agentHomeEnv = "CLAWKER_TEST_AGENT_HOME"
	// agentSocketArg marks a helper argument as a socket to connect to.
	agentSocketArg = "socket:"
)

// agentTempDir returns a temporary directory that another uid can traverse.
func agentTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestUserDir_DistinctAgentCanUse runs the root setup for a distinct agent uid,
// then checks as that uid that the files are readable and the sockets accept a
// connection.
func TestUserDir_DistinctAgentCanUse(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to set up entries for another uid")
	}
	home := agentTempDir(t)
	if err := os.Chown(home, agentUID, agentGID); err != nil {
		t.Fatal(err)
	}

	// The helper arguments: files to read and sockets to connect to,
	// relative to home.
	entries := make([]string, 0, 5)
	gnupg := mustOpenUserDir(t, home, ".gnupg", agentUID, agentGID)
	for _, name := range []string{"pubring.kbx", "gpg.conf", "gpg-agent.conf"} {
		if err := gnupg.writeFile(name, []byte(name)); err != nil {
			t.Fatalf("writeFile %s: %v", name, err)
		}
		entries = append(entries, ".gnupg/"+name)
	}
	ssh := mustOpenUserDir(t, home, ".ssh", agentUID, agentGID)
	for _, sock := range []struct {
		dir  *userDir
		name string
	}{{gnupg, "S.gpg-agent"}, {ssh, "agent.sock"}} {
		ln, err := sock.dir.listenUnix(sock.name)
		if err != nil {
			t.Fatalf("listenUnix %s: %v", sock.name, err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		entries = append(entries, agentSocketArg+filepath.Base(sock.dir.path)+"/"+sock.name)
	}

	// The test binary sits in a directory only its owner can read, so the
	// agent runs a copy.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	agentBin := filepath.Join(agentTempDir(t), "agent.test")
	if err = os.WriteFile(agentBin, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(agentBin, append([]string{"-test.run=^TestHelperAgentAccess$", "-test.v"}, entries...)...)
	cmd.Dir = filepath.Dir(agentBin)
	cmd.Env = append(os.Environ(), agentHomeEnv+"="+home)
	// No supplementary groups: the agent has only agentGID.
	cred := &syscall.Credential{Uid: agentUID, Gid: agentGID, Groups: nil, NoSetGroups: false}
	cmd.SysProcAttr = &syscall.SysProcAttr{ //nolint:exhaustruct_v5 // stdlib SysProcAttr: only Credential applies
		Credential: cred,
	}
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("agent uid %d cannot use the entries: %v\n%s", agentUID, runErr, out)
	}
}

// TestHelperAgentAccess is the agent side of TestUserDir_DistinctAgentCanUse.
// It does nothing unless that test starts it. Each argument is a file to read
// or, with the socket prefix, a socket to connect to, relative to the home.
func TestHelperAgentAccess(t *testing.T) {
	home := os.Getenv(agentHomeEnv)
	if home == "" {
		t.Skip("helper process for TestUserDir_DistinctAgentCanUse")
	}
	if os.Geteuid() != agentUID {
		t.Fatalf("helper runs as uid %d, want %d", os.Geteuid(), agentUID)
	}
	if flag.NArg() == 0 {
		t.Fatal("no entries to check")
	}
	for _, arg := range flag.Args() {
		if sock, ok := strings.CutPrefix(arg, agentSocketArg); ok {
			c, err := net.Dial("unix", filepath.Join(home, sock))
			if err != nil {
				t.Errorf("dial %s: %v", sock, err)
				continue
			}
			_ = c.Close()
			continue
		}
		if _, err := os.ReadFile(filepath.Join(home, arg)); err != nil {
			t.Errorf("read %s: %v", arg, err)
		}
	}
}
