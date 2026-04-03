package firewall_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

func newTestManager(t *testing.T) (*firewall.Manager, config.Config) {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := &whailtest.FakeAPIClient{}
	// Stub container ops so AddRules/RemoveRules error instead of panic.
	fake.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, fmt.Errorf("no docker in rules tests")
	}
	mgr, err := firewall.NewManager(fake, cfg, logger.Nop())
	require.NoError(t, err)
	return mgr, cfg
}

func TestAddRules_NewRulesWritten(t *testing.T) {
	mgr, _ := newTestManager(t)

	incoming := []config.EgressRule{
		{Dst: "example.com", Proto: "tls", Action: "allow"},
		{Dst: "api.example.com", Proto: "tls", Action: "allow"},
	}

	// AddRules writes to the store first, then calls regenerateAndRestart.
	// regenerateAndRestart checks IsRunning → ContainerList returns error →
	// IsRunning returns false → early return nil. Store write succeeds.
	err := mgr.AddRules(t.Context(), incoming)
	require.NoError(t, err)

	rules, listErr := mgr.List(t.Context())
	require.NoError(t, listErr)
	assert.Len(t, rules, 2)
	assert.Equal(t, "example.com", rules[0].Dst)
	assert.Equal(t, "api.example.com", rules[1].Dst)
}

func TestAddRules_Deduplication(t *testing.T) {
	mgr, _ := newTestManager(t)

	rule := config.EgressRule{Dst: "example.com", Proto: "tls", Action: "allow"}

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{rule}))
	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{rule})) // duplicate

	rules, err := mgr.List(t.Context())
	require.NoError(t, err)
	assert.Len(t, rules, 1)
}

func TestAddRules_DefaultProto(t *testing.T) {
	mgr, _ := newTestManager(t)

	// Empty proto defaults to "tls" before storage.
	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "example.com"},
	}))

	rules, err := mgr.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "tls", rules[0].Proto)
	assert.Equal(t, 443, rules[0].Port)
	assert.Equal(t, "allow", rules[0].Action)
}

func TestAddRules_DifferentPortsNotDuplicate(t *testing.T) {
	mgr, _ := newTestManager(t)

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "example.com", Proto: "tcp", Port: 80, Action: "allow"},
		{Dst: "example.com", Proto: "tcp", Port: 443, Action: "allow"},
	}))

	rules, err := mgr.List(t.Context())
	require.NoError(t, err)
	assert.Len(t, rules, 2)
}

func TestRemoveRules(t *testing.T) {
	mgr, _ := newTestManager(t)

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "keep.com", Proto: "tls", Action: "allow"},
		{Dst: "remove.com", Proto: "tls", Action: "allow"},
	}))

	require.NoError(t, mgr.RemoveRules(t.Context(), []config.EgressRule{
		{Dst: "remove.com", Proto: "tls"},
	}))

	rules, err := mgr.List(t.Context())
	require.NoError(t, err)
	assert.Len(t, rules, 1)
	assert.Equal(t, "keep.com", rules[0].Dst)
}

func TestAddRules_MultipleCallsAdditive(t *testing.T) {
	mgr, _ := newTestManager(t)

	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "first.com", Proto: "tls", Action: "allow"},
	}))
	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "second.com", Proto: "tls", Action: "allow"},
	}))

	rules, err := mgr.List(t.Context())
	require.NoError(t, err)
	assert.Len(t, rules, 2)

	dsts := make(map[string]bool)
	for _, r := range rules {
		dsts[r.Dst] = true
	}
	assert.True(t, dsts["first.com"])
	assert.True(t, dsts["second.com"])
}

func TestValidateDst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dst     string
		wantErr bool
	}{
		// Valid domains.
		{name: "simple domain", dst: "example.com"},
		{name: "subdomain", dst: "api.github.com"},
		{name: "deep subdomain", dst: "a.b.c.example.com"},
		{name: "wildcard", dst: ".example.com"},
		{name: "trailing dot", dst: "example.com."},
		{name: "wildcard trailing dot", dst: ".example.com."},
		{name: "with hyphen", dst: "my-api.example.com"},
		{name: "with digits", dst: "api2.example.com"},
		{name: "all digits label", dst: "123.example.com"},
		{name: "single label", dst: "localhost"},
		{name: "underscore", dst: "_dmarc.example.com"},

		// Case — must be lowercase.
		{name: "uppercase", dst: "EXAMPLE.COM", wantErr: true},
		{name: "mixed case", dst: "Api.GitHub.Com", wantErr: true},
		{name: "wildcard uppercase", dst: ".EXAMPLE.COM", wantErr: true},

		// Multi-dot TLD and new gTLD (other TLDs exercise identical code paths).
		{name: "co.uk", dst: "api.example.co.uk"},
		{name: "new gTLD", dst: "my.example.technology"},

		// Domain length boundaries (253 chars max after normalization).
		{name: "total 253 chars valid", dst: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)},
		{name: "total 254 chars invalid", dst: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62), wantErr: true},

		// Valid IPs and CIDRs.
		{name: "IPv4", dst: "192.168.1.1"},
		{name: "IPv6", dst: "::1"},
		{name: "CIDR", dst: "10.0.0.0/8"},

		// Invalid.
		{name: "empty", dst: "", wantErr: true},
		{name: "just dot", dst: ".", wantErr: true},
		{name: "just dots", dst: "..", wantErr: true},
		{name: "spaces", dst: "example .com", wantErr: true},
		{name: "leading hyphen", dst: "-example.com", wantErr: true},
		{name: "trailing hyphen label", dst: "example-.com", wantErr: true},
		{name: "special chars", dst: "example!.com", wantErr: true},
		{name: "at sign", dst: "user@example.com", wantErr: true},
		{name: "path", dst: "example.com/path", wantErr: true},
		{name: "port", dst: "example.com:443", wantErr: true},
		{name: "scheme", dst: "https://example.com", wantErr: true},
		{name: "empty label", dst: "example..com", wantErr: true},
		{name: "label too long", dst: strings.Repeat("a", 64) + ".com", wantErr: true},
		{name: "all numeric", dst: "123.456", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := firewall.ValidateDst(tt.dst)
			if tt.wantErr {
				assert.Error(t, err, "ValidateDst(%q) should fail", tt.dst)
			} else {
				assert.NoError(t, err, "ValidateDst(%q) should succeed", tt.dst)
			}
		})
	}
}

func TestAddRules_RejectsInvalidDomain(t *testing.T) {
	mgr, _ := newTestManager(t)

	err := mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "valid.com"},
		{Dst: "has spaces.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has spaces.com")

	// Valid rule should not have been stored either (atomic).
	rules, listErr := mgr.List(t.Context())
	require.NoError(t, listErr)
	assert.Empty(t, rules)
}

func TestAddRules_NormalizesEmptyFields(t *testing.T) {
	mgr, _ := newTestManager(t)

	// Normalization fills in defaults before storage: proto→tls, action→allow,
	// TLS port→443. Explicit values (ssh, port 22) are never overridden.
	require.NoError(t, mgr.AddRules(t.Context(), []config.EgressRule{
		{Dst: "a.com"},
		{Dst: "b.com", Proto: "ssh", Port: 22},
	}))

	rules, err := mgr.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rules, 2)

	assert.Equal(t, "tls", rules[0].Proto)
	assert.Equal(t, 443, rules[0].Port)
	assert.Equal(t, "allow", rules[0].Action)
	assert.Equal(t, "ssh", rules[1].Proto)
	assert.Equal(t, 22, rules[1].Port)
	assert.Equal(t, "allow", rules[1].Action)
}
