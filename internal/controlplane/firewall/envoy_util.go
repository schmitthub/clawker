package firewall

import "strings"

// statNameReplacer maps characters that are unsafe in Envoy stat/cluster names
// to underscores.
var statNameReplacer = strings.NewReplacer(".", "_", "-", "_", ":", "_")

// sanitizeName makes a domain or identifier safe to embed in an Envoy
// stat/cluster name.
func sanitizeName(s string) string {
	return statNameReplacer.Replace(s)
}
