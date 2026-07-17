package agent

import (
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// Every seeded host key must parse. A truncated key is invisible to OpenSSH
// (it skips damaged lines) but poisons the container's known_hosts for strict
// whole-file parsers — a corrupt seed shipped unnoticed for exactly that
// reason.
func TestDefaultKnownHostsAllParse(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(defaultKnownHosts), "\n")
	if len(lines) == 0 {
		t.Fatal("defaultKnownHosts is empty")
	}
	for _, line := range lines {
		if _, _, _, _, _, err := gossh.ParseKnownHosts([]byte(line)); err != nil {
			t.Errorf("seeded known_hosts line for %q does not parse: %v",
				strings.Fields(line)[0], err)
		}
	}
}
