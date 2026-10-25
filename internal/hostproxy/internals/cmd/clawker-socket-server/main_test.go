//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
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
