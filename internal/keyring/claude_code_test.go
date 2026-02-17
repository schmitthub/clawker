package keyring

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// maskToken returns the first 12 characters of s followed by "***" if s is
// longer than 12 characters, otherwise returns s unchanged.
func maskToken(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "***"
}

// TestGetClaudeCodeCredentials_Integration reads real credentials from the
// developer's OS keychain. Skipped unless RUN_KEYRING_INTEGRATION=1.
//
//	RUN_KEYRING_INTEGRATION=1 go test ./internal/keyring/... -run TestGetClaudeCodeCredentials_Integration -v
func TestGetClaudeCodeCredentials_Integration(t *testing.T) {
	if os.Getenv("RUN_KEYRING_INTEGRATION") != "1" {
		t.Skip("set RUN_KEYRING_INTEGRATION=1 to run (reads real keychain)")
	}

	cred, err := GetClaudeCodeCredentials()
	if err != nil {
		t.Fatalf("GetClaudeCodeCredentials: %v", err)
	}

	oauth := cred.ClaudeAiOauth
	expiry := time.UnixMilli(oauth.ExpiresAt)

	fmt.Fprintf(os.Stdout, "\n")
	fmt.Fprintf(os.Stdout, "ClaudeCodeCredentials {\n")
	fmt.Fprintf(os.Stdout, "  OrganizationUUID: %s\n", cred.OrganizationUUID)
	fmt.Fprintf(os.Stdout, "  ClaudeAiOauth {\n")
	fmt.Fprintf(os.Stdout, "    AccessToken:      %s\n", maskToken(oauth.AccessToken))
	fmt.Fprintf(os.Stdout, "    RefreshToken:     %s\n", maskToken(oauth.RefreshToken))
	fmt.Fprintf(os.Stdout, "    ExpiresAt:        %d (%s)\n", oauth.ExpiresAt, expiry.Format(time.RFC3339))
	fmt.Fprintf(os.Stdout, "    Scopes:           [%s]\n", strings.Join(oauth.Scopes, ", "))
	fmt.Fprintf(os.Stdout, "    SubscriptionType: %s\n", oauth.SubscriptionType)
	fmt.Fprintf(os.Stdout, "    RateLimitTier:    %s\n", oauth.RateLimitTier)
	fmt.Fprintf(os.Stdout, "  }\n")
	fmt.Fprintf(os.Stdout, "}\n")
}

// seedKeyring initialises the mock keyring and stores raw under the Claude Code
// service name for the current OS user. Pass doNotSeed=true to skip seeding
// (simulates "no entry").
func seedKeyring(t *testing.T, raw string, doNotSeed bool) {
	t.Helper()
	MockInit()

	if doNotSeed {
		return
	}

	current, err := user.Current()
	if err != nil {
		t.Fatalf("get current user: %v", err)
	}
	if err := Set("Claude Code-credentials", current.Username, raw); err != nil {
		t.Fatalf("seed keyring: %v", err)
	}
}

func TestGetClaudeCodeCredentials(t *testing.T) {
	const orgUUID = "550e8400-e29b-41d4-a716-446655440000"

	validJSON := `{
		"claudeAiOauth": {
			"accessToken":      "access",
			"refreshToken":     "refresh",
			"expiresAt":        4102444800000,
			"scopes":           ["scope1"],
			"subscriptionType": "pro",
			"rateLimitTier":    "tier1"
		},
		"organizationUuid": "` + orgUUID + `"
	}`

	expiredJSON := `{
		"claudeAiOauth": {
			"accessToken":  "access",
			"refreshToken": "refresh",
			"expiresAt":    1000000000000,
			"scopes":       ["scope1"]
		},
		"organizationUuid": "` + orgUUID + `"
	}`

	invalidUUIDJSON := `{
		"claudeAiOauth": {
			"accessToken":  "access",
			"refreshToken": "refresh",
			"expiresAt":    4102444800000
		},
		"organizationUuid": "not-a-uuid"
	}`

	tests := []struct {
		name      string
		raw       string
		doNotSeed bool
		wantErr   error
		check     func(t *testing.T, c *ClaudeCodeCredentials)
	}{
		{
			name: "happy path",
			raw:  validJSON,
			check: func(t *testing.T, c *ClaudeCodeCredentials) {
				t.Helper()
				if c.ClaudeAiOauth.AccessToken != "access" {
					t.Errorf("access token: got %q, want %q", c.ClaudeAiOauth.AccessToken, "access")
				}
				wantUUID := uuid.MustParse(orgUUID)
				if c.OrganizationUUID != wantUUID {
					t.Errorf("org uuid: got %v, want %v", c.OrganizationUUID, wantUUID)
				}
			},
		},
		{
			name:      "not found",
			doNotSeed: true,
			wantErr:   ErrNotFound,
		},
		{
			name:    "empty credential",
			raw:     "",
			wantErr: ErrEmptyCredential,
		},
		{
			name:    "invalid schema",
			raw:     "{not-json}",
			wantErr: ErrInvalidSchema,
		},
		{
			name:    "invalid UUID",
			raw:     invalidUUIDJSON,
			wantErr: ErrInvalidSchema,
		},
		{
			name:    "expired token",
			raw:     expiredJSON,
			wantErr: ErrTokenExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seedKeyring(t, tt.raw, tt.doNotSeed)

			cred, err := GetClaudeCodeCredentials()

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error wrapping %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error wrapping %v, got: %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cred)
			}
		})
	}
}
