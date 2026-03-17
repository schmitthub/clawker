package firewall_test

import (
	"fmt"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic seeds for oracle tests
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCfg creates an isolated test config with XDG dirs.
func testCfg(t *testing.T) config.Config {
	t.Helper()
	env := testenv.New(t, testenv.WithConfig())
	return env.Config()
}

// ruleDsts extracts destination strings from a slice of egress rules.
func ruleDsts(rules []config.EgressRule) []string {
	dsts := make([]string, len(rules))
	for i, r := range rules {
		dsts[i] = r.Dst
	}
	return dsts
}

// rulesFilePath returns the expected path to the egress-rules.yaml file.
func rulesFilePath(t *testing.T, cfg config.Config) string {
	t.Helper()
	dataDir, err := cfg.FirewallDataSubdir()
	require.NoError(t, err)
	return filepath.Join(dataDir, cfg.EgressRulesFileName())
}

func TestUpdateRules_NewRules_Written(t *testing.T) {
	cfg := testCfg(t)

	incoming := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "api.openai.com", Proto: "tls", Port: 443, Action: "allow"},
	}

	written, err := firewall.UpdateRules(cfg, incoming)
	require.NoError(t, err)
	assert.True(t, written, "expected rules to be written")

	// Verify rules persisted on disk.
	filePath := rulesFilePath(t, cfg)
	_, err = os.Stat(filePath)
	require.NoError(t, err, "egress-rules.yaml should exist on disk")

	// Re-read from disk and verify.
	rules, err := firewall.ReadRules(cfg)
	require.NoError(t, err)
	assert.Len(t, rules, 2)
	assert.Equal(t, "github.com", rules[0].Dst)
	assert.Equal(t, "api.openai.com", rules[1].Dst)
}

func TestUpdateRules_SameRules_NoWrite(t *testing.T) {
	cfg := testCfg(t)

	incoming := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
	}

	// First write.
	written, err := firewall.UpdateRules(cfg, incoming)
	require.NoError(t, err)
	require.True(t, written)

	// Record mtime.
	filePath := rulesFilePath(t, cfg)
	info1, err := os.Stat(filePath)
	require.NoError(t, err)
	mtime1 := info1.ModTime()

	// Ensure filesystem clock advances (macOS HFS+ has 1s granularity).
	time.Sleep(50 * time.Millisecond)

	// Second write with same rules.
	written, err = firewall.UpdateRules(cfg, incoming)
	require.NoError(t, err)
	assert.False(t, written, "expected no write for duplicate rules")

	// Verify file was not rewritten (mtime unchanged).
	info2, err := os.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, mtime1, info2.ModTime(), "file mtime should be unchanged")
}

func TestUpdateRules_OverlappingAndNew_OnlyNewAppended(t *testing.T) {
	cfg := testCfg(t)

	// Seed with initial rules.
	initial := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	written, err := firewall.UpdateRules(cfg, initial)
	require.NoError(t, err)
	require.True(t, written)

	// Update with overlapping + new.
	incoming := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},         // duplicate
		{Dst: "registry.npmjs.org", Proto: "tls", Port: 443, Action: "allow"}, // new
	}
	written, err = firewall.UpdateRules(cfg, incoming)
	require.NoError(t, err)
	assert.True(t, written, "expected write for new rules")

	rules, err := firewall.ReadRules(cfg)
	require.NoError(t, err)
	assert.Len(t, rules, 3, "should have 2 initial + 1 new")

	dsts := ruleDsts(rules)
	assert.Contains(t, dsts, "github.com")
	assert.Contains(t, dsts, "api.anthropic.com")
	assert.Contains(t, dsts, "registry.npmjs.org")
}

func TestUpdateRules_DefaultProto(t *testing.T) {
	cfg := testCfg(t)

	// Rule with empty proto should match one with proto="tls" (default).
	initial := []config.EgressRule{
		{Dst: "github.com", Proto: "", Port: 443, Action: "allow"},
	}
	written, err := firewall.UpdateRules(cfg, initial)
	require.NoError(t, err)
	require.True(t, written)

	// Same rule but with explicit proto="tls" — should be a duplicate.
	incoming := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	written, err = firewall.UpdateRules(cfg, incoming)
	require.NoError(t, err)
	assert.False(t, written, "rule with defaulted proto should match explicit tls")
}

func TestUpdateRules_DifferentPortsNotDuplicate(t *testing.T) {
	cfg := testCfg(t)

	initial := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
	}
	written, err := firewall.UpdateRules(cfg, initial)
	require.NoError(t, err)
	require.True(t, written)

	// Same dst+proto but different port.
	incoming := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 8443, Action: "allow"},
	}
	written, err = firewall.UpdateRules(cfg, incoming)
	require.NoError(t, err)
	assert.True(t, written, "different port should not be a duplicate")

	rules, err := firewall.ReadRules(cfg)
	require.NoError(t, err)
	assert.Len(t, rules, 2)
}

func TestRemoveRules_RuleRemoved(t *testing.T) {
	cfg := testCfg(t)

	// Seed rules.
	initial := []config.EgressRule{
		{Dst: "github.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443, Action: "allow"},
		{Dst: "registry.npmjs.org", Proto: "tls", Port: 443, Action: "allow"},
	}
	_, err := firewall.UpdateRules(cfg, initial)
	require.NoError(t, err)

	// Remove one rule.
	err = firewall.RemoveRules(cfg, []config.EgressRule{
		{Dst: "api.anthropic.com", Proto: "tls", Port: 443},
	})
	require.NoError(t, err)

	rules, err := firewall.ReadRules(cfg)
	require.NoError(t, err)
	assert.Len(t, rules, 2)

	dsts := ruleDsts(rules)
	assert.Contains(t, dsts, "github.com")
	assert.Contains(t, dsts, "registry.npmjs.org")
	assert.NotContains(t, dsts, "api.anthropic.com")
}

func TestUpdateRules_Oracle(t *testing.T) {
	// Oracle test: random rules, independent computation verifies result.
	cfg := testCfg(t)
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))

	domains := []string{
		"github.com", "api.openai.com", "registry.npmjs.org",
		"proxy.golang.org", "pypi.org", "rubygems.org",
		"crates.io", "hub.docker.com", "api.anthropic.com",
		"storage.googleapis.com",
	}
	protos := []string{"tls", "tcp", "ssh"}
	ports := []int{0, 443, 886}

	// Generate random rules across multiple rounds.
	numRounds := 3 + rng.IntN(5)
	allAdded := make(map[string]config.EgressRule) // oracle: track what should be in the store

	for round := 0; round < numRounds; round++ {
		numRules := 1 + rng.IntN(6)
		var incoming []config.EgressRule
		for i := 0; i < numRules; i++ {
			r := config.EgressRule{
				Dst:    domains[rng.IntN(len(domains))],
				Proto:  protos[rng.IntN(len(protos))],
				Port:   ports[rng.IntN(len(ports))],
				Action: "allow",
			}
			incoming = append(incoming, r)
		}

		_, err := firewall.UpdateRules(cfg, incoming)
		require.NoError(t, err)

		// Oracle: independently compute expected state.
		// UpdateRules deduplicates incoming against existing AND within incoming.
		for _, r := range incoming {
			key := fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
			if _, exists := allAdded[key]; !exists {
				allAdded[key] = r
			}
		}
	}

	// Verify store matches oracle.
	rules, err := firewall.ReadRules(cfg)
	require.NoError(t, err)
	assert.Len(t, rules, len(allAdded), "store rule count should match oracle")

	storeKeys := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		key := fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
		storeKeys[key] = struct{}{}
	}

	for key := range allAdded {
		assert.Contains(t, storeKeys, key, "oracle key %s missing from store", key)
	}
}

func TestUpdateRules_ConcurrentAppend(t *testing.T) {
	// 10 goroutines each add unique rules concurrently.
	// Assert: all rules present, no duplicates, no lost writes.
	cfg := testCfg(t)

	const numGoroutines = 10

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rules := []config.EgressRule{
				{
					Dst:    fmt.Sprintf("g%d-a.example.com", id),
					Proto:  "tls",
					Port:   443,
					Action: "allow",
				},
				{
					Dst:    fmt.Sprintf("g%d-b.example.com", id),
					Proto:  "tls",
					Port:   443,
					Action: "allow",
				},
			}
			if _, err := firewall.UpdateRules(cfg, rules); err != nil {
				errCh <- fmt.Errorf("goroutine %d: update: %w", id, err)
				return
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// Read final state.
	rules, err := firewall.ReadRules(cfg)
	require.NoError(t, err)

	// Each goroutine adds 2 unique rules = 20 total expected.
	assert.Len(t, rules, numGoroutines*2, "all rules should be present")

	// Verify no duplicates.
	seen := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		key := fmt.Sprintf("%s:%s:%d", r.Dst, r.Proto, r.Port)
		if _, dup := seen[key]; dup {
			t.Errorf("duplicate rule found: %s", key)
		}
		seen[key] = struct{}{}
	}

	// Verify all goroutines' rules are present.
	for g := 0; g < numGoroutines; g++ {
		keyA := fmt.Sprintf("g%d-a.example.com:tls:443", g)
		keyB := fmt.Sprintf("g%d-b.example.com:tls:443", g)
		assert.Contains(t, seen, keyA, "missing rule from goroutine %d", g)
		assert.Contains(t, seen, keyB, "missing rule from goroutine %d", g)
	}
}
