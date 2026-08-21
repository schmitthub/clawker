package shared

import (
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

// AuthorizeSocketBridgesForTest exposes socket authorization to external tests.
func AuthorizeSocketBridgesForTest(
	container string,
	harness RuntimeHarness,
	cmdOpts CommandOpts,
	log *logger.Logger,
) ([]socketbridge.BridgedSocket, error) {
	return authorizeSocketBridges(container, harness, cmdOpts, log)
}

// FormatListenerIdentityForTest exposes listener display formatting.
func FormatListenerIdentityForTest(identity socketbridge.ListenerIdentity) string {
	return formatListenerIdentity(identity)
}

// BannedSocketPathFromForTest exposes banned-path alias matching.
func BannedSocketPathFromForTest(path string, bannedPaths []string) (string, bool) {
	return bannedSocketPathFrom(path, bannedPaths)
}

// SocketsWaitScriptForTest exposes the socket readiness script builder.
func SocketsWaitScriptForTest(sockets []socketbridge.BridgedSocket, timeoutSeconds int) string {
	return socketsWaitScript(sockets, timeoutSeconds)
}

// SocketWaitTimeoutSecondsForTest returns the standard socket readiness timeout.
func SocketWaitTimeoutSecondsForTest() int {
	return socketWaitTimeoutSeconds
}

// ShellQuoteSocketTargetForTest exposes shell quoting for a socket target.
func ShellQuoteSocketTargetForTest(value string) string {
	return shellQuoteSocketTarget(value)
}
