package firewall

import "github.com/schmitthub/clawker/internal/cmdutil"

// validatePortFlag validates a user-supplied --port value for firewall
// rule commands. Zero means "protocol default" (resolved server-side by
// NormalizeRule — e.g. 443 for tls) and is accepted. Any non-zero value
// must fall in the TCP port range 1..65535.
//
// Without this guard, negative ints silently wrap when cast to the
// protobuf uint32 (e.g. -1 → 4294967295), producing nonsense rules that
// Envoy rejects at reload time and that Remove cannot match.
func validatePortFlag(port int) error {
	if port == 0 {
		return nil
	}
	if port < 1 || port > 65535 {
		return cmdutil.FlagErrorf("invalid --port %d: must be between 1 and 65535", port)
	}
	return nil
}
