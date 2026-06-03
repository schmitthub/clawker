package firewall

import (
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

// TestValidatePortFlag is a delegation smoke-check only: validatePortFlag is a
// thin wrapper over config.ParsePortSpec (exhaustively covered by
// TestParsePortSpec in internal/config) that adds a --port FlagError prefix. We
// assert the happy path passes, an invalid spec fails, and the error is a
// FlagError (so Cobra prints usage) — not the full parse matrix, which belongs
// to ParsePortSpec's own test.
func TestValidatePortFlag(t *testing.T) {
	t.Parallel()

	if err := validatePortFlag("443"); err != nil {
		t.Fatalf("validatePortFlag(%q) = %v; want nil", "443", err)
	}

	err := validatePortFlag("65536")
	if err == nil {
		t.Fatalf("validatePortFlag(%q) = nil; want error", "65536")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Fatalf("validatePortFlag error type = %T; want *cmdutil.FlagError (triggers usage display)", err)
	}
}
