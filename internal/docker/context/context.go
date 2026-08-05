// Package context reads the docker CLI's context files.
//
// The docker CLI keeps the daemon addresses it can talk to in its own config
// directory: a pointer to the active context in config.json, and one stored
// entry per context under contexts/meta/<sha256 of the name>/meta.json. A
// rootless install configures the daemon address here and nowhere else, so
// these files are the only record of where that daemon listens.
//
// This is a reader. It copies what the files hold at call time into a Context
// and hands it back; it never writes, creates, or repairs them, and it holds
// no state between calls.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
)

const (
	// configDirName is the config directory under $HOME when DOCKER_CONFIG
	// does not relocate it.
	configDirName = ".docker"

	// configFileName holds currentContext, among unrelated CLI state.
	configFileName = "config.json"

	contextsDir  = "contexts"
	metaDir      = "meta"
	metaFileName = "meta.json"

	// dockerEndpoint keys the daemon endpoint within a stored context. A
	// context may carry others (kubernetes); this reader reads only this one.
	dockerEndpoint = "docker"

	// DefaultName is the docker CLI's built-in context. It is synthesized
	// rather than stored, so it has no file to read.
	DefaultName = "default"
)

// Every path that does not produce an address says exactly which one it hit.
// Nothing is folded into a zero Context.
var (
	// ErrConfigNotFound reports that config.json does not exist. The docker
	// CLI has never run here, or its config directory was removed.
	ErrConfigNotFound = errors.New("docker config not found")

	// ErrNoCurrentContext reports that config.json exists but selects no
	// stored context: it carries no currentContext, or names the built-in
	// default, which is synthesized rather than stored.
	ErrNoCurrentContext = errors.New("no current docker context")

	// ErrContextNotFound reports that a context is named but its file is not
	// in the store. The docker CLI fails outright on this, so a caller that
	// quietly substitutes its own address will reach a daemon the user's own
	// docker commands cannot.
	ErrContextNotFound = errors.New("docker context not found")

	// ErrNoDockerEndpoint reports that the context file exists but carries no
	// docker endpoint — it configures something else, such as kubernetes.
	ErrNoDockerEndpoint = errors.New("docker context has no docker endpoint")
)

// Context is one docker CLI context as stored on disk.
type Context struct {
	// Name is the context's name.
	Name string

	// Description is the human-readable note the docker CLI shows in
	// `docker context ls`.
	Description string

	// Host is the daemon address, verbatim from the file — including its
	// scheme (unix:///run/user/1000/docker.sock, tcp://...). Never empty:
	// a context without one is ErrNoDockerEndpoint.
	Host string

	// SkipTLSVerify reports whether the context disables TLS verification
	// for that address.
	SkipTLSVerify bool
}

// metaFileContent mirrors the parts of meta.json this reader copies out. The
// docker CLI writes more; anything not named here is ignored.
//
//nolint:tagliatelle // these key names are the docker CLI's on-disk format.
type metaFileContent struct {
	Name     string `json:"Name"`
	Metadata struct {
		Description string `json:"Description"`
	} `json:"Metadata"`
	Endpoints map[string]struct {
		Host          string `json:"Host"`
		SkipTLSVerify bool   `json:"SkipTLSVerify"`
	} `json:"Endpoints"`
}

// configFileContent mirrors the one field this reader copies out of the docker
// CLI's config.json. The rest of that file belongs to the docker CLI.
type configFileContent struct {
	CurrentContext string `json:"currentContext"`
}

// Current returns the context the docker CLI would use right now.
//
// It returns ErrConfigNotFound when there is no config.json, ErrNoCurrentContext
// when that file selects nothing, and ErrContextNotFound when it selects a
// context whose file is missing. The first two are ordinary states on a host
// that has never switched contexts; the third is a broken setup.
func Current() (Context, error) {
	dir, err := configDir()
	if err != nil {
		return Context{}, err
	}

	name, err := currentName(dir)
	if err != nil {
		return Context{}, err
	}
	return Read(name)
}

// Read returns the stored context of that name.
//
// The built-in default context resolves to ErrNoCurrentContext: it is
// synthesized by the docker CLI rather than stored, so there is no file to
// read. A name with no file in the store is ErrContextNotFound.
func Read(name string) (Context, error) {
	if name == "" || name == DefaultName {
		return Context{}, ErrNoCurrentContext
	}

	dir, err := configDir()
	if err != nil {
		return Context{}, err
	}

	// The store keys each entry by the digest of the context's name.
	digest := sha256.Sum256([]byte(name))
	path := filepath.Join(dir, contextsDir, metaDir, hex.EncodeToString(digest[:]), metaFileName)

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Context{}, fmt.Errorf("%w: %q", ErrContextNotFound, name)
	}
	if err != nil {
		return Context{}, fmt.Errorf("reading docker context %q: %w", name, err)
	}

	var content metaFileContent
	if err = json.Unmarshal(raw, &content); err != nil {
		return Context{}, fmt.Errorf("parsing docker context %q (%s): %w", name, path, err)
	}

	endpoint, ok := content.Endpoints[dockerEndpoint]
	if !ok || endpoint.Host == "" {
		return Context{}, fmt.Errorf("%w: %q", ErrNoDockerEndpoint, name)
	}

	return Context{
		Name:          content.Name,
		Description:   content.Metadata.Description,
		Host:          endpoint.Host,
		SkipTLSVerify: endpoint.SkipTLSVerify,
	}, nil
}

// configDir returns the docker CLI's config directory.
func configDir() (string, error) {
	if dir := os.Getenv(consts.EnvDockerConfig); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for the docker config: %w", err)
	}
	return filepath.Join(home, configDirName), nil
}

// currentName returns the name of the context config.json selects.
func currentName(dir string) (string, error) {
	if name := os.Getenv(consts.EnvDockerContext); name != "" {
		return name, nil
	}

	path := filepath.Join(dir, configFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", ErrConfigNotFound, path)
	}
	if err != nil {
		return "", fmt.Errorf("reading the docker config: %w", err)
	}

	var content configFileContent
	if err = json.Unmarshal(raw, &content); err != nil {
		return "", fmt.Errorf("parsing the docker config (%s): %w", path, err)
	}
	if content.CurrentContext == "" {
		return "", fmt.Errorf("%w: %s", ErrNoCurrentContext, path)
	}
	return content.CurrentContext, nil
}
