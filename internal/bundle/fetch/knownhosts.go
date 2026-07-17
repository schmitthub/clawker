package fetch

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/transport"
	gogitssh "github.com/go-git/go-git/v6/plumbing/transport/ssh"
	"github.com/go-git/go-git/v6/plumbing/transport/ssh/knownhosts"
	gossh "golang.org/x/crypto/ssh"
)

const (
	// sshKnownHostsEnv is the override list of known_hosts files consulted by
	// OpenSSH and go-git alike.
	sshKnownHostsEnv = "SSH_KNOWN_HOSTS"
	// systemKnownHosts is the system-wide known_hosts fallback location.
	systemKnownHosts = "/etc/ssh/ssh_known_hosts"
	// protoSSH is the URL scheme selecting the ssh transport.
	protoSSH = "ssh"
)

// sshClientOptions equips an ssh source with agent auth and an OpenSSH-parity
// host key verifier. go-git's default ssh flow hard-fails the whole fetch when
// any known_hosts line is malformed (one truncated entry poisons every host);
// OpenSSH skips damaged lines and verifies against the rest, and this option
// restores that behavior. Non-ssh sources — and any setup failure — return nil
// so the default flow runs and surfaces its own, more specific error.
func sshClientOptions(gitURL string) []client.Option {
	u, err := transport.ParseURL(gitURL)
	if err != nil || u.Scheme != protoSSH {
		return nil
	}
	agentAuth, err := gogitssh.NewSSHAgentAuth(u.User.Username())
	if err != nil {
		// No reachable agent: the default flow fails with its own agent error
		// for the same condition — don't mask it here.
		return nil
	}
	db, err := tolerantHostKeyDB()
	if err != nil {
		// Loader trouble (temp staging failed): the strict default flow
		// surfaces its own error for whatever is wrong with the environment.
		return nil
	}
	agentAuth.HostKeyCallback = db.HostKeyCallback()
	return []client.Option{client.WithSSHAuth(sshAuth{agent: agentAuth, db: db})}
}

// sshAuth wraps agent auth so the tolerant known_hosts database also answers
// the host key ALGORITHM lookup. The transport's connect() consults the
// default (strict) database whenever ClientConfig leaves HostKeyAlgorithms
// empty — even with a custom HostKeyCallback set — which would reintroduce the
// whole-file parse failure this package exists to avoid.
type sshAuth struct {
	agent *gogitssh.PublicKeysCallback
	db    *knownhosts.HostKeyDB
}

func (a sshAuth) ClientConfig(
	ctx context.Context, req *transport.Request,
) (*gossh.ClientConfig, error) {
	cfg, err := a.agent.ClientConfig(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ssh agent client config: %w", err)
	}
	port := req.URL.Port()
	if port == "" {
		port = strconv.Itoa(gogitssh.DefaultPort)
	}
	algos := a.db.HostKeyAlgorithms(net.JoinHostPort(req.URL.Hostname(), port))
	if len(algos) == 0 {
		// Unknown host: offer every supported algorithm so connect() never
		// falls back to the strict database; the callback still refuses the
		// host, with the honest "key is unknown" error.
		algos = gossh.SupportedAlgorithms().HostKeys
	}
	cfg.HostKeyAlgorithms = algos
	return cfg, nil
}

// tolerantHostKeyDB builds a host key database over the surviving lines of the
// known_hosts search list. Dropping a damaged line never weakens verification
// — a host without a surviving valid entry is still refused.
func tolerantHostKeyDB() (*knownhosts.HostKeyDB, error) {
	sane := sanitizeKnownHosts(knownHostsFiles())
	tmp, err := os.CreateTemp("", "clawker-known-hosts-*")
	if err != nil {
		return nil, fmt.Errorf("stage known_hosts: %w", err)
	}
	// NewDB reads the file eagerly, so the staging file can go right away.
	defer os.Remove(tmp.Name()) // best-effort temp cleanup
	if _, werr := tmp.Write(sane); werr != nil {
		_ = tmp.Close() // the write error is the one worth reporting
		return nil, fmt.Errorf("stage known_hosts: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return nil, fmt.Errorf("stage known_hosts: %w", cerr)
	}
	db, err := knownhosts.NewDB(tmp.Name())
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return db, nil
}

// knownHostsFiles resolves the known_hosts search list the way go-git and
// OpenSSH do: SSH_KNOWN_HOSTS wins outright; otherwise the user file then the
// system file.
func knownHostsFiles() []string {
	if env := os.Getenv(sshKnownHostsEnv); env != "" {
		return filepath.SplitList(env)
	}
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".ssh", "known_hosts"))
	}
	return append(files, systemKnownHosts)
}

// sanitizeKnownHosts concatenates the given files, keeping only lines that
// parse as known_hosts entries. Missing files contribute nothing (same as
// OpenSSH); blanks, comments, and damaged lines drop.
func sanitizeKnownHosts(files []string) []byte {
	var kept []byte
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue // unreadable file = no trusted entries from it
		}
		for line := range bytes.SplitSeq(data, []byte("\n")) {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 || trimmed[0] == '#' {
				continue
			}
			if _, _, _, _, _, parseErr := gossh.ParseKnownHosts(trimmed); parseErr != nil {
				continue
			}
			kept = append(kept, trimmed...)
			kept = append(kept, '\n')
		}
	}
	return kept
}
