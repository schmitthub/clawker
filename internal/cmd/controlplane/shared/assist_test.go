package shared_test

import (
	"errors"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	cpmanager "github.com/schmitthub/clawker/controlplane/manager"
	shared "github.com/schmitthub/clawker/internal/cmd/controlplane/shared"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// TestAssistSOS_Declines pins the arms that must return the SOS unchanged
// instead of prompting or hanging: no streams at all, a non-interactive
// session, and a Kind this CLI predates. Each is the same contract — the SOS
// surfaces as the error and the caller decides.
func TestAssistSOS_Declines(t *testing.T) {
	t.Parallel()

	nonInteractive, _, _, _ := iostreams.Test() //nolint:dogsled // iostreams.Test returns three buffers this test does not assert on

	tests := []struct {
		name string
		sos  *cpmanager.CPSOSError
		ios  *iostreams.IOStreams
	}{
		{
			name: "nil streams",
			sos:  &cpmanager.CPSOSError{Kind: adminv1.SOSKind_SOS_KIND_BPFFS_DELEGATION, Message: "boot blocked"},
			ios:  nil,
		},
		{
			name: "non-interactive session",
			sos:  &cpmanager.CPSOSError{Kind: adminv1.SOSKind_SOS_KIND_BPFFS_DELEGATION, Message: "boot blocked"},
			ios:  nonInteractive,
		},
		{
			name: "unspecified kind",
			sos:  &cpmanager.CPSOSError{Kind: adminv1.SOSKind_SOS_KIND_UNSPECIFIED, Message: "??"},
			ios:  nonInteractive,
		},
		{
			name: "unknown kind from a newer control plane",
			sos:  &cpmanager.CPSOSError{Kind: adminv1.SOSKind(9999), Message: "needs something this CLI predates"},
			ios:  nonInteractive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := shared.AssistSOS(t.Context(), tt.sos, tt.ios)

			var got *cpmanager.CPSOSError
			if !errors.As(err, &got) {
				t.Fatalf("AssistSOS() = %v, want the *CPSOSError surfaced", err)
			}
			if got != tt.sos {
				t.Fatalf("AssistSOS() surfaced a different error: got %v, want %v", got, tt.sos)
			}
		})
	}
}
