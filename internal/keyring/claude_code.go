package keyring

import "github.com/google/uuid"

// claudeCodeCredentialsService is the OS keyring service name Claude
// Code stores its credential blob under.
const claudeCodeCredentialsService = "Claude Code-credentials"

// ClaudeCodeCredentials is the top-level JSON schema stored in the OS keychain
// by Claude Code under the service name "Claude Code-credentials".
type ClaudeCodeCredentials struct {
	ClaudeAiOauth    ClaudeAiOauth `json:"claudeAiOauth"`
	OrganizationUUID uuid.UUID     `json:"organizationUuid"`
}

// ClaudeAiOauth contains the OAuth token fields within ClaudeCodeCredentials.
type ClaudeAiOauth struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"`
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`
}

// claudeCodeService defines the fetch → parse pipeline for Claude Code
// credentials stored in the OS keychain.
//
// No expiry validation: an expired access token is still injected into the
// container because the blob carries the refreshToken, which Claude Code uses
// to refresh in place at runtime. Gating on expiry here would discard a
// perfectly refreshable credential.
var claudeCodeService = ServiceDef[ClaudeCodeCredentials]{
	ServiceName: claudeCodeCredentialsService,
	User:        currentOSUser,
	Parse:       jsonParse[ClaudeCodeCredentials],
}

// GetClaudeCodeCredentials fetches and parses the current user's Claude Code
// credentials from the OS keychain.
//
// The parsed struct is lossy: keys the host omits surface as zero values and
// keys absent from the struct are dropped. Readers that only need typed fields
// can use it; injection paths must use GetClaudeCodeCredentialsRaw to preserve
// the blob verbatim.
func GetClaudeCodeCredentials() (*ClaudeCodeCredentials, error) {
	return getCredential(claudeCodeService)
}

// GetClaudeCodeCredentialsRaw fetches the current user's Claude Code credential
// blob from the OS keychain verbatim, without parsing or re-encoding.
//
// Claude Code stores this as a plain JSON object (not a JWT) and refreshes it in
// place at runtime using the embedded refreshToken. The blob must be injected
// byte-for-byte: round-tripping it through ClaudeCodeCredentials would fabricate
// a zero-value organizationUuid for a host that omits the key (claiming an org
// the user is not a member of, which the refresh endpoint rejects) and would
// drop any field the struct does not model.
func GetClaudeCodeCredentialsRaw() (string, error) {
	return getRawCredential(claudeCodeService)
}
