package consts

import "testing"

// TestPersistedValueTripwires deliberately pins const VALUES — the one
// place in the suite allowed to spell them. These values are persisted
// host state on existing installs: after the consts sweep, nothing else
// in the suite would fail if one were renamed, and every unit and e2e
// test would happily pass against freshly-created resources under the
// new name while users' existing resources are silently orphaned.
// Changing one of these requires a migration story, not just a const
// edit.
func TestPersistedValueTripwires(t *testing.T) {
	tripwires := map[string]struct{ got, want string }{
		"Network (existing clawker-net bridges)":     {Network, "clawker-net"},
		"NamePrefix (resource names, XDG dir paths)": {NamePrefix, "clawker"},
		"LabelDomain (labels on existing resources)": {LabelDomain, "dev.clawker"},
	}
	for name, tw := range tripwires {
		if tw.got != tw.want {
			t.Errorf("%s changed: %q → %q — existing installs' resources will be orphaned; this needs a migration, not a rename", name, tw.want, tw.got)
		}
	}
}
