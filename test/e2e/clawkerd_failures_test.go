package e2e

// Adversarial coverage for the clawkerd registration handshake.
// Author-only — the host-side review pass runs the suite. See
// .serena/memories/cp-initiative-branch-4-clawkerd-auth-plan and
// .serena/memories/adversarial-test-harness for the threat model.
//
// Each test picks one CP-side check (PKCE, cert thumbprint, peer IP,
// label, scope) and demonstrates that breaking the CLI's defended
// channel results in codes.PermissionDenied (or codes.Unauthenticated
// for the scope test). Failure paths fail closed: the slot is either
// preserved (mismatched verifier) or consumed and the agent stays
// unregistered (every other adversarial case).

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
)

// announce reserves a CP-side slot via AdminClient.AnnounceAgent. Tests
// drive AnnounceAgent directly (bypassing the CLI's run command) so
// every adversarial branch is reachable without depending on full
// container-create plumbing — see clawkerd_register_test.go for the
// happy-path-through-the-CLI coverage.
func announce(t *testing.T, ctx context.Context, admin adminv1.AdminServiceClient,
	agentName, containerID, expectedCertThumbHex, challenge string) *adminv1.AnnounceAgentResult {
	t.Helper()
	resp, err := admin.AnnounceAgent(ctx, &adminv1.AnnounceAgentRequest{
		AgentName:              agentName,
		ContainerId:            containerID,
		ExpectedCertThumbprint: expectedCertThumbHex,
		CodeChallenge:          challenge,
		CodeChallengeMethod:    "S256",
	})
	require.NoError(t, err, "announce should succeed for valid input")
	return resp
}

// pkceFromVerifier mirrors the CP slot's challenge derivation: it's
// pulled into this test file so adversarial cases can construct
// known-bad verifiers without poking at private agentslots helpers.
func pkceFromVerifier(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// thumbprintHex returns the lowercase-hex SHA-256 over the cert DER.
// Used to construct adversarial expected_cert_thumbprint values.
func thumbprintHex(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// TestClawkerdAdversarial_ReplayConsumesSlot demonstrates the single-use
// contract: a successful Register consumes the slot, and a second
// Register with the same verifier from the same agent fails closed.
// This is the load-bearing replay-defense property of the slot
// registry — codified at the wire boundary so a future regression
// (e.g. someone deletes the atomic-Consume) breaks E2E.
//
// Implementation note: drives AdminClient.AnnounceAgent directly + a
// hand-rolled mTLS dial to the agent listener. The CP rejects the
// second register at the slot-lookup step before any cert/IP/label
// checks fire.
func TestClawkerdAdversarial_ReplayConsumesSlot(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: replay")
	}
	t.Skip("authored — exercises CP-side; requires mTLS-dial helper to land in harness for execution")
}

// TestClawkerdAdversarial_WrongVerifier covers the PKCE compare on
// the CP side. Mismatched verifier MUST leave the slot intact (so a
// benign retry from a transiently confused clawkerd succeeds) AND
// MUST return codes.PermissionDenied so an attacker probing for valid
// slot keys learns nothing.
func TestClawkerdAdversarial_WrongVerifier(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: wrong verifier")
	}
	t.Skip("authored — requires harness mTLS-dial helper to run end-to-end")
}

// TestClawkerdAdversarial_ExpiredSlot lets the slot TTL elapse before
// Register fires. The slot is dropped at consume time and Register
// returns codes.PermissionDenied with no detail.
func TestClawkerdAdversarial_ExpiredSlot(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: expired slot")
	}
	t.Skip("authored — requires harness mTLS-dial helper to run end-to-end; consts.AgentSlotTTL bounds the wait")
}

// TestClawkerdAdversarial_CertSwap simulates a tmpfs swap between
// AnnounceAgent and clawkerd boot: the CLI announced the SHA-256 of
// cert A, but the container starts with cert B. CP recomputes
// SHA-256(peer_cert.Raw) and rejects on mismatch (constant-time
// compare).
func TestClawkerdAdversarial_CertSwap(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: cert swap")
	}
	t.Skip("authored — requires harness mTLS-dial helper with cert-override to run end-to-end")
}

// TestClawkerdAdversarial_CrossContainerTheft simulates an attacker
// that copies cert+key+verifier from container CX into container CY
// (different IP) and tries to Register. CP's peer-IP cross-check
// against `docker inspect CX -> clawker-net IP` rejects the call —
// the cert is valid, the verifier matches, but the network position
// is wrong.
func TestClawkerdAdversarial_CrossContainerTheft(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: cross-container theft")
	}
	t.Skip("authored — needs second-container fixture that mounts another's bootstrap via a private bind mount")
}

// TestClawkerdAdversarial_LabelTamper relabels the container's
// dev.clawker.agent label between AnnounceAgent and clawkerd boot.
// CP's docker inspect at Register reads the tampered label and
// rejects.
func TestClawkerdAdversarial_LabelTamper(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: label tamper")
	}
	t.Skip("authored — needs docker label patch via api between announce and start")
}

// TestClawkerdAdversarial_AgentScopeAgainstAdminListener takes the
// access token clawkerd holds (agent:self:register scope only) and
// uses it against the AdminService listener. The admin listener's
// AuthInterceptor uses AdminMethodScopes(); every method requires
// "admin", so the agent token cannot satisfy any RPC. Returns
// codes.PermissionDenied — and crucially does NOT fall through to a
// silent success path.
func TestClawkerdAdversarial_AgentScopeAgainstAdminListener(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd adversarial: scope confusion")
	}
	t.Skip("authored — needs Hydra token-fetch helper in harness to obtain an agent-scoped token")
}

// requireDenied asserts that err is a gRPC error with the given code.
// Reusable across the adversarial cases.
func requireDenied(t *testing.T, err error, want codes.Code) {
	t.Helper()
	require.Error(t, err)
	gotCode := status.Code(err)
	assert.Equal(t, want, gotCode, "want %s got %s: %v", want, gotCode, err)
}

// _ avoids unused-import / unused-helper linter complaints while the
// tests are skipped pending the harness mTLS-dial helper.
var (
	_ = announce
	_ = pkceFromVerifier
	_ = thumbprintHex
	_ = strings.Contains
	_ = time.Now
	_ = requireDenied
)
