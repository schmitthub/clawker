// Package internals provides embedded container-side scripts and source code
// that run inside clawker containers to communicate with the host proxy
// and socketbridge. These are leaf assets (stdlib + embed only) consumed by
// the bundler package when assembling Docker build contexts.
//
// IMPORTANT: All embedded scripts in this package are automatically included
// in image content hashing via AllScripts(). When adding new scripts, export
// them as package-level vars and add them to AllScripts() to ensure image
// rebuilds when scripts change.
package internals

import _ "embed"

// HostOpenScript is a shell script used as the BROWSER env var inside containers.
// It opens URLs via the host proxy and handles OAuth callback interception.
//
//go:embed host-open.sh
var HostOpenScript string

// GitCredentialScript is a git credential helper that forwards credential
// requests to the host proxy for HTTPS git authentication.
//
//go:embed git-credential-clawker.sh
var GitCredentialScript string

// CallbackForwarderSource is the Go source for the callback-forwarder binary.
// It polls the host proxy for captured OAuth callbacks and forwards them
// to the local HTTP server inside the container.
// Compiled during Docker image build via multi-stage Dockerfile.
//
//go:embed cmd/callback-forwarder/main.go
var CallbackForwarderSource string

// SocketForwarderSource is the Go source for the clawker-socket-server binary.
// It provides unified socket forwarding (GPG, SSH) via muxrpc-style protocol
// over stdin/stdout, replacing the separate agent proxy binaries.
// Compiled during Docker image build via multi-stage Dockerfile.
//
//go:embed cmd/clawker-socket-server/main.go
var SocketForwarderSource string

// AllScripts returns all embedded script contents for content hashing.
// This is used by the bundler package to ensure image rebuilds when any
// container-side script changes.
//
// IMPORTANT: When adding new embedded scripts to this package, add them
// to this list to ensure they are included in image content hashing.
func AllScripts() []string {
	return []string{
		CallbackForwarderSource,
		GitCredentialScript,
		HostOpenScript,
		SocketForwarderSource,
	}
}
