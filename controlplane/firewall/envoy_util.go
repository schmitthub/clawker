package firewall

import "strings"

// statNameReplacer maps characters that are unsafe in Envoy stat/cluster names
// to underscores. "/" is included so a CIDR dst (e.g. 10.0.0.0/24) yields a clean
// cluster name (tcp_origdst_10_0_0_0_24_5432).
var statNameReplacer = strings.NewReplacer(".", "_", "-", "_", ":", "_", "/", "_")

// sanitizeName makes a domain or identifier safe to embed in an Envoy
// stat/cluster name.
func sanitizeName(s string) string {
	return statNameReplacer.Replace(s)
}
