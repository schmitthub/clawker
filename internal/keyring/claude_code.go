package keyring

import "github.com/google/uuid"

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
	ServiceName: "Claude Code-credentials",
	User:        currentOSUser,
	Parse:       jsonParse[ClaudeCodeCredentials],
}

// GetClaudeCodeCredentials fetches and parses the current user's Claude Code
// credentials from the OS keychain.
func GetClaudeCodeCredentials() (*ClaudeCodeCredentials, error) {
	return getCredential(claudeCodeService)
}
