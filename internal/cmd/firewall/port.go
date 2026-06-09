package firewall

import (
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
)

// validatePortFlag validates a user-supplied --port value for firewall rule
// commands. The value is the dynamic port spec: empty means "protocol default"
// (resolved server-side by NormalizeRule — e.g. 443 for https) and is accepted;
// otherwise it must be a single port ("443") or an inclusive range ("9000-9100",
// lo-hi), with every port in 1..65535 and lo<=hi.
//
// Validating here gives the user an immediate, descriptive error at the CLI
// boundary; the control plane also validates and returns an error at ingestion,
// but a malformed port must never collapse to a default and widen egress.
func validatePortFlag(port string) error {
	if _, _, _, err := config.ParsePortSpec(port); err != nil {
		return cmdutil.FlagErrorf("--port: %v", err)
	}
	return nil
}
