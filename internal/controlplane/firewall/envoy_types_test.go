package firewall

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Unit tests for the EnvoyConfig accumulator's structural guards — the
// error/merge paths the happy-path config tests can't trigger.

func unmarshalConfig(t *testing.T, c *EnvoyConfig) map[string]any {
	t.Helper()
	out, err := c.Bytes()
	require.NoError(t, err)
	var tree map[string]any
	require.NoError(t, yaml.Unmarshal(out, &tree))
	return tree
}

func listenersOf(t *testing.T, tree map[string]any) []any {
	t.Helper()
	sr, ok := tree["static_resources"].(map[string]any)
	require.True(t, ok)
	ls, _ := sr["listeners"].([]any)
	return ls
}

// hcmChain builds a minimal single-HCM filter chain with one named vhost under
// the given match — the shape addChain merges.
func hcmChain(match map[string]any, vhostName string) map[string]any {
	return map[string]any{
		"filter_chain_match": match,
		"filters": []any{
			map[string]any{
				"name": "envoy.filters.network.http_connection_manager",
				"typed_config": map[string]any{
					"route_config": map[string]any{
						"virtual_hosts": []any{map[string]any{"name": vhostName}},
					},
				},
			},
		},
	}
}

func TestEnvoyConfig_Marshal_Shape(t *testing.T) {
	c := NewEnvoyConfig()
	c.SetAdmin(map[string]any{"address": "x"})
	c.EnsureListener("egress", "0.0.0.0", 10000)

	tree := unmarshalConfig(t, c)
	assert.Contains(t, tree, "admin")
	require.Contains(t, tree, "static_resources")
	ls := listenersOf(t, tree)
	require.Len(t, ls, 1)
	assert.Equal(t, "egress", ls[0].(map[string]any)["name"])
}

func TestEnvoyConfig_EnsureListener_Idempotent(t *testing.T) {
	c := NewEnvoyConfig()
	c.EnsureListener("egress", "0.0.0.0", 10000)
	c.EnsureListener("egress", "127.0.0.1", 9999) // must not replace

	ls := listenersOf(t, unmarshalConfig(t, c))
	require.Len(t, ls, 1)
	addr := ls[0].(map[string]any)["address"].(map[string]any)["socket_address"].(map[string]any)
	assert.EqualValues(t, 10000, addr["port_value"], "first call's address wins")
}

func TestEnvoyConfig_addChain_DistinctMatchesCoexist(t *testing.T) {
	c := NewEnvoyConfig()
	c.EnsureListener("egress", "0.0.0.0", 10000)
	require.NoError(t, c.addChain("egress", hcmChain(map[string]any{"server_names": []any{"a"}}, "a")))
	require.NoError(t, c.addChain("egress", hcmChain(map[string]any{"server_names": []any{"b"}}, "b")))

	chains := listenersOf(t, unmarshalConfig(t, c))[0].(map[string]any)["filter_chains"].([]any)
	assert.Len(t, chains, 2)
}

func TestEnvoyConfig_addChain_MergesSameMatchHCM(t *testing.T) {
	c := NewEnvoyConfig()
	c.EnsureListener("egress", "0.0.0.0", 10000)
	match := func() map[string]any { return map[string]any{"transport_protocol": "raw_buffer"} }
	require.NoError(t, c.addChain("egress", hcmChain(match(), "a")))
	require.NoError(t, c.addChain("egress", hcmChain(match(), "b")), "same-match HCM chains merge, not duplicate")

	chains := listenersOf(t, unmarshalConfig(t, c))[0].(map[string]any)["filter_chains"].([]any)
	require.Len(t, chains, 1, "one shared chain")
	vh := chains[0].(map[string]any)["filters"].([]any)[0].(map[string]any)["typed_config"].(map[string]any)["route_config"].(map[string]any)["virtual_hosts"].([]any)
	names := []any{vh[0].(map[string]any)["name"], vh[1].(map[string]any)["name"]}
	assert.ElementsMatch(t, []any{"a", "b"}, names)
}

func TestEnvoyConfig_addChain_RejectsSameMatchNonHCM(t *testing.T) {
	c := NewEnvoyConfig()
	c.EnsureListener("egress", "0.0.0.0", 10000)
	tcp := func() map[string]any {
		return map[string]any{
			"filter_chain_match": map[string]any{"destination_port": 22},
			"filters":            []any{map[string]any{"name": "envoy.filters.network.tcp_proxy"}},
		}
	}
	require.NoError(t, c.addChain("egress", tcp()))
	err := c.addChain("egress", tcp())
	require.Error(t, err, "two non-HCM chains with the same match must fail closed")
	assert.Contains(t, err.Error(), "duplicate filter_chain_match")
}

func TestEnvoyConfig_addChain_UnknownListener(t *testing.T) {
	c := NewEnvoyConfig()
	require.Error(t, c.addChain("nope", hcmChain(nil, "x")))
}

func TestEnvoyConfig_SetUnmatchedDeny_Emitted(t *testing.T) {
	c := NewEnvoyConfig()
	c.EnsureListener("egress", "0.0.0.0", 10000)
	require.NoError(t, c.SetUnmatchedDeny("egress", map[string]any{"filters": []any{"deny"}}))
	assert.Contains(t, listenersOf(t, unmarshalConfig(t, c))[0].(map[string]any), "default_filter_chain")
}

func TestEnvoyConfig_AddCluster_DedupsIdentical_RejectsConflict(t *testing.T) {
	c := NewEnvoyConfig()
	require.NoError(t, c.AddCluster(map[string]any{"name": "deny_cluster", "type": "STATIC"}))
	require.NoError(t, c.AddCluster(map[string]any{"name": "deny_cluster", "type": "STATIC"}))
	require.Error(t, c.AddCluster(map[string]any{"name": "deny_cluster", "type": "LOGICAL_DNS"}))

	sr := unmarshalConfig(t, c)["static_resources"].(map[string]any)
	assert.Len(t, sr["clusters"].([]any), 1)
}

func TestEnvoyConfig_AddCluster_RejectsEmptyName(t *testing.T) {
	require.Error(t, NewEnvoyConfig().AddCluster(map[string]any{"type": "STATIC"}))
}

func TestEnvoyConfig_ClaimPermutation_Dedup(t *testing.T) {
	c := NewEnvoyConfig()
	assert.True(t, c.ClaimPermutation("k"))
	assert.False(t, c.ClaimPermutation("k"))
	assert.True(t, c.ClaimPermutation("k2"))
}

func TestEnvoyConfig_Bytes_Deterministic(t *testing.T) {
	build := func() string {
		c := NewEnvoyConfig()
		c.SetAdmin(map[string]any{"z": 1, "a": 2})
		c.EnsureListener("egress", "0.0.0.0", 10000)
		_ = c.addChain("egress", hcmChain(map[string]any{"server_names": []any{"a"}}, "a"))
		_ = c.AddCluster(map[string]any{"name": "c2"})
		_ = c.AddCluster(map[string]any{"name": "c1"})
		out, err := c.Bytes()
		require.NoError(t, err)
		return string(out)
	}
	assert.Equal(t, build(), build())
}
