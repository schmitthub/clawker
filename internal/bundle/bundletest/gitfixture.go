// Package bundletest provides an in-process git fixture server for the bundle
// fetch/install integration tests. It serves real repositories over both http
// and ssh from an isolated temp directory, publishes the ssh host key via
// SSH_KNOWN_HOSTS and a live test ssh-agent via SSH_AUTH_SOCK so the production
// fetcher's env-driven ssh auth works with no seams, and authors repositories
// with real go-git so ref/sha/tag resolution and drift are exercised end to end.
//
// The point is fidelity: a test declares a bundle source whose URL is one of
// this server's [Server.HTTPURL]/[Server.SSHURL] values and drives the real
// production clone/fetch path against it — no mocked transport, no in-memory fs
// theater.
package bundletest

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	glssh "github.com/gliderlabs/ssh"
	"github.com/go-git/go-billy/v6/osfs"
	gogit "github.com/go-git/go-git/v6"
	gogitcfg "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/transport"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	gitbackend "github.com/go-git/go-git/v6/backend"
)

// minSSHCommandParts is the minimum token count of a git ssh command
// (service + repository path).
const minSSHCommandParts = 2

// Server is a running http+ssh git fixture server.
type Server struct {
	reposDir string
	backend  *gitbackend.Backend
	httpSrv  *httptest.Server
	sshHost  string
	sshPort  string
}

// New starts an http+ssh git fixture server serving repositories from an
// isolated temp directory. It sets SSH_KNOWN_HOSTS (the fixture host key) and
// SSH_AUTH_SOCK (a keyring-backed agent) for the test process so the production
// fetcher authenticates ssh through the environment with no seams. Everything is
// torn down via t.Cleanup.
func New(t *testing.T) *Server {
	t.Helper()

	reposDir := t.TempDir()
	backend := gitbackend.New(transport.NewFilesystemLoader(osfs.New(reposDir), false))

	srv := &Server{
		reposDir: reposDir,
		backend:  backend,
		httpSrv:  httptest.NewServer(backend),
		sshHost:  "",
		sshPort:  "",
	}
	t.Cleanup(srv.httpSrv.Close)

	// Generate the ed25519 host key inline: ssh.NewSignerFromKey returns the
	// x/crypto Signer interface, so a helper returning it would fight ireturn.
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	hostPub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())))
	srv.startSSH(t, hostSigner)
	isolateSSHEnv(t, srv.sshHost, srv.sshPort, hostPub)

	return srv
}

// HTTPURL returns the http clone URL for repository name.
func (s *Server) HTTPURL(name string) string {
	return s.httpSrv.URL + "/" + name + ".git"
}

// SSHURL returns the ssh clone URL for repository name.
func (s *Server) SSHURL(name string) string {
	return fmt.Sprintf("ssh://git@%s/%s.git", net.JoinHostPort(s.sshHost, s.sshPort), name)
}

// startSSH binds an ssh listener on localhost and serves the git pack protocol
// over each session's stream via the backend.
func (s *Server) startSSH(t *testing.T, hostSigner ssh.Signer) {
	t.Helper()
	//nolint:noctx // fixture bind; listener lifecycle is bound to t.Cleanup
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ssh: %v", err)
	}
	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split ssh addr: %v", err)
	}
	s.sshHost, s.sshPort = host, port

	server := &glssh.Server{ //nolint:exhaustruct // gliderlabs Server is configured by option funcs below; other fields default intentionally
		Handler:          s.handleSSH,
		PublicKeyHandler: func(glssh.Context, glssh.PublicKey) bool { return true },
	}
	server.AddHostKey(hostSigner)

	go func() {
		if serveErr := server.Serve(ln); serveErr != nil {
			return // listener closed on cleanup; nothing to do in a fixture goroutine
		}
	}()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			_ = closeErr
		}
	})
}

// handleSSH serves one git ssh session: it maps the requested git service and
// repository path onto a backend request and runs the pack exchange over the
// session's read/write streams.
func (s *Server) handleSSH(sess glssh.Session) {
	cmd := sess.Command()
	if len(cmd) < minSSHCommandParts {
		exitSession(sess, 1)
		return
	}
	repoPath := strings.Trim(cmd[1], "'\"")
	req := &gitbackend.Request{
		URL:           &url.URL{Path: repoPath},
		Service:       cmd[0],
		GitProtocol:   envValue(sess.Environ(), "GIT_PROTOCOL"),
		AdvertiseRefs: false,
		StatelessRPC:  false,
	}
	if err := s.backend.Serve(sess.Context(), io.NopCloser(sess), sshWriteCloser{sess}, req); err != nil {
		exitSession(sess, 1)
		return
	}
	exitSession(sess, 0)
}

// exitSession sends an ssh exit status, ignoring the unactionable teardown error
// a closing fixture session may return.
func exitSession(sess glssh.Session, code int) {
	if err := sess.Exit(code); err != nil {
		_ = err
	}
}

// Repo is an authoring handle for one fixture repository.
type Repo struct {
	repo *gogit.Repository
	dir  string
}

// InitRepo creates a new repository name served by this fixture. The returned
// handle authors commits and tags with real go-git; the loader serves the
// repository's own object store over http and ssh.
func (s *Server) InitRepo(t *testing.T, name string) *Repo {
	t.Helper()
	dir := filepath.Join(s.reposDir, name+".git")
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init repo %s: %v", name, err)
	}
	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("repo config %s: %v", name, err)
	}
	cfg.Commit.GpgSign = gogitcfg.OptBoolFalse
	cfg.Tag.GpgSign = gogitcfg.OptBoolFalse
	if setErr := repo.SetConfig(cfg); setErr != nil {
		t.Fatalf("set repo config %s: %v", name, setErr)
	}
	return &Repo{repo: repo, dir: dir}
}

// Remove deletes the repository from the fixture server, so subsequent
// fetches and ref resolves against its URL fail — the upstream-vanished
// scenario for cache-keeps-serving tests.
func (r *Repo) Remove(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(r.dir); err != nil {
		t.Fatalf("remove repo %s: %v", r.dir, err)
	}
}

// Commit writes files (relative path → content) into the worktree, stages them,
// and records one commit with the given message on the current branch. It
// returns the new commit SHA.
func (r *Repo) Commit(t *testing.T, message string, files map[string]string) string {
	t.Helper()
	wt, err := r.repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	for rel, content := range files {
		abs := filepath.Join(r.dir, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(abs), 0o750); mkErr != nil {
			t.Fatalf("mkdir for %s: %v", rel, mkErr)
		}
		if writeErr := os.WriteFile(abs, []byte(content), 0o600); writeErr != nil {
			t.Fatalf("write %s: %v", rel, writeErr)
		}
		if _, addErr := wt.Add(filepath.FromSlash(rel)); addErr != nil {
			t.Fatalf("add %s: %v", rel, addErr)
		}
	}
	hash, err := wt.Commit(message, &gogit.CommitOptions{
		All:               false,
		AllowEmptyCommits: false,
		Author:            fixtureSignature(),
		Committer:         fixtureSignature(),
		Parents:           nil,
		Signer:            nil,
		Amend:             false,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash.String()
}

// Symlink stages symlinks (relative path → link target, recorded verbatim as
// authored) and records one commit on the current branch, returning the new
// commit SHA. Targets are slash-separated and stored as git stores them — a
// link target is repository content, so a bundle may legitimately share one
// file between two components this way, and a target climbing out of the
// bundle root is the hostile shape.
func (r *Repo) Symlink(t *testing.T, message string, links map[string]string) string {
	t.Helper()
	wt, err := r.repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	for rel, target := range links {
		abs := filepath.Join(r.dir, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(abs), 0o750); mkErr != nil {
			t.Fatalf("mkdir for %s: %v", rel, mkErr)
		}
		if linkErr := os.Symlink(filepath.FromSlash(target), abs); linkErr != nil {
			t.Fatalf("symlink %s -> %s: %v", rel, target, linkErr)
		}
		if _, addErr := wt.Add(filepath.FromSlash(rel)); addErr != nil {
			t.Fatalf("add %s: %v", rel, addErr)
		}
	}
	hash, err := wt.Commit(message, &gogit.CommitOptions{
		All:               false,
		AllowEmptyCommits: false,
		Author:            fixtureSignature(),
		Committer:         fixtureSignature(),
		Parents:           nil,
		Signer:            nil,
		Amend:             false,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash.String()
}

// Tag creates an annotated tag pointing at the current branch tip and returns
// the tagged commit SHA.
func (r *Repo) Tag(t *testing.T, name string) string {
	t.Helper()
	head, err := r.repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if _, tagErr := r.repo.CreateTag(name, head.Hash(), &gogit.CreateTagOptions{
		Tagger:  fixtureSignature(),
		Message: name,
		Signer:  nil,
	}); tagErr != nil {
		t.Fatalf("tag %s: %v", name, tagErr)
	}
	return head.Hash().String()
}

// fixtureSignature is the fixed author/committer identity for fixture commits.
func fixtureSignature() *object.Signature {
	return &object.Signature{
		Name:  "clawker-fixture",
		Email: "fixture@clawker.test",
		When:  time.Unix(0, 0).UTC(),
	}
}

// isolateSSHEnv publishes a known_hosts entry for the fixture ssh host via
// SSH_KNOWN_HOSTS and starts a keyring-backed ssh-agent published via
// SSH_AUTH_SOCK — so the fetcher's default env-driven ssh auth trusts and
// authenticates against this server with no production seam. SSH_KNOWN_HOSTS is
// used (rather than HOME/.ssh/known_hosts) so the trust survives an isolated
// test env that later relocates HOME.
func isolateSSHEnv(t *testing.T, host, port, hostAuthorizedKey string) {
	t.Helper()
	sshDir := t.TempDir()
	knownHosts := filepath.Join(sshDir, "known_hosts")
	knownLine := fmt.Sprintf("[%s]:%s %s\n", host, port, hostAuthorizedKey)
	if err := os.WriteFile(knownHosts, []byte(knownLine), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)

	startAgent(t)
}

// startAgent generates a client key, serves an in-process ssh-agent over a unix
// socket, and exports SSH_AUTH_SOCK for the test process.
func startAgent(t *testing.T) {
	t.Helper()
	// agent.NewKeyring returns the x/crypto Agent interface; keeping it a
	// local (not a helper return) sidesteps ireturn.
	keyring := agent.NewKeyring()
	addFixtureKey(t, keyring)
	sock := agentSocketPath(t)

	//nolint:noctx // fixture agent-socket bind; lifecycle bound to t.Cleanup
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen agent: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ln.Close(); closeErr != nil {
			_ = closeErr
		}
	})
	t.Setenv("SSH_AUTH_SOCK", sock)

	go serveAgentLoop(ln, keyring)
}

// addFixtureKey generates a fresh client key and adds it to the keyring.
func addFixtureKey(t *testing.T, keyring agent.Agent) {
	t.Helper()
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	if addErr := keyring.Add(agent.AddedKey{
		PrivateKey:           clientPriv,
		Certificate:          nil,
		Comment:              "clawker-fixture",
		LifetimeSecs:         0,
		ConfirmBeforeUse:     false,
		ConstraintExtensions: nil,
	}); addErr != nil {
		t.Fatalf("agent add key: %v", addErr)
	}
}

// agentSocketPath returns a short unix-socket path (the system temp dir keeps
// within the socket path length limit; t.TempDir paths can be too long).
func agentSocketPath(t *testing.T) string {
	t.Helper()
	//nolint:usetesting // t.TempDir paths are too long for the unix-socket path limit; a short /tmp dir is required
	sockDir, err := os.MkdirTemp("", "clawker-agent")
	if err != nil {
		t.Fatalf("agent sock dir: %v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(sockDir); rmErr != nil {
			_ = rmErr
		}
	})
	return filepath.Join(sockDir, "agent.sock")
}

// serveAgentLoop accepts and serves ssh-agent connections until the listener
// closes on test cleanup.
func serveAgentLoop(ln net.Listener, keyring agent.Agent) {
	for {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		go func() {
			if serveErr := agent.ServeAgent(keyring, conn); serveErr != nil {
				_ = serveErr
			}
		}()
	}
}

// sshWriteCloser adapts a gliderlabs session's write side to an [io.WriteCloser],
// signaling EOF to the client on Close.
type sshWriteCloser struct{ sess glssh.Session }

func (w sshWriteCloser) Write(p []byte) (int, error) {
	n, err := w.sess.Write(p)
	if err != nil {
		return n, fmt.Errorf("ssh session write: %w", err)
	}
	return n, nil
}

func (w sshWriteCloser) Close() error {
	if err := w.sess.CloseWrite(); err != nil {
		return fmt.Errorf("ssh session close-write: %w", err)
	}
	return nil
}

// envValue extracts a KEY=value entry from an environment slice.
func envValue(environ []string, key string) string {
	prefix := key + "="
	for _, kv := range environ {
		if value, ok := strings.CutPrefix(kv, prefix); ok {
			return value
		}
	}
	return ""
}
