package firewall

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// These prove validateBootstrap actually rejects malformed config — otherwise
// the generator's fail-closed self-check would be a silent no-op blessing
// anything.

func TestValidateBootstrap_AcceptsMinimalValid(t *testing.T) {
	ok := []byte("admin:\n  address:\n    socket_address:\n      address: 127.0.0.1\n      port_value: 9901\nstatic_resources:\n  listeners: []\n  clusters: []\n")
	require.NoError(t, validateBootstrap(ok))
}

func TestValidateBootstrap_RejectsUnknownField(t *testing.T) {
	// A typo'd field must error (protojson DiscardUnknown defaults to false).
	bad := []byte("static_resources:\n  listeners:\n    - name: x\n      bogus_field: true\n")
	require.Error(t, validateBootstrap(bad))
}

func TestValidateBootstrap_RejectsWrongType(t *testing.T) {
	// listeners must be a list, not a scalar.
	bad := []byte("static_resources:\n  listeners: not-a-list\n")
	require.Error(t, validateBootstrap(bad))
}
