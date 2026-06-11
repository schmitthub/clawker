package consts

import "testing"

// TestPersistedValueTripwires deliberately pins const VALUES — the one
// place in the suite allowed to spell them. These values are persisted
// host state on existing installs: nothing else in the suite spells
// these values, so a rename would fail no other test — every unit and e2e
// test would happily pass against freshly-created resources under the
// new name while users' existing resources are silently orphaned.
// Changing one of these requires a migration story, not just a const
// edit.
func TestPersistedValueTripwires(t *testing.T) {
	tripwires := map[string]struct{ got, want string }{
		"Network (existing clawker-net bridges)":                {Network, "clawker-net"},
		"NamePrefix (resource names, XDG dir paths)":            {NamePrefix, "clawker"},
		"LabelDomain (labels on existing resources)":            {LabelDomain, "dev.clawker"},
		"LabelManaged (filter key on existing resources)":       {LabelManaged, "dev.clawker.managed"},
		"LabelProject (filter key on existing resources)":       {LabelProject, "dev.clawker.project"},
		"LabelAgent (filter key on existing resources)":         {LabelAgent, "dev.clawker.agent"},
		"ContainerCP (CN pin baked into existing agent images)": {ContainerCP, "clawker-controlplane"},
		"RegistryFile (existing project registries on disk)":    {RegistryFile, "registry.yaml"},
		"ControlPlaneDBFile (existing agent trust tables)":      {ControlPlaneDBFile, "controlplane.db"},
		"EnvConfigDir (set in user shell profiles)":             {EnvConfigDir, "CLAWKER_CONFIG_DIR"},
		"EnvDataDir (set in user shell profiles)":               {EnvDataDir, "CLAWKER_DATA_DIR"},
		"EnvStateDir (set in user shell profiles)":              {EnvStateDir, "CLAWKER_STATE_DIR"},
		"EnvCacheDir (set in user shell profiles)":              {EnvCacheDir, "CLAWKER_CACHE_DIR"},
	}
	for name, tw := range tripwires {
		if tw.got != tw.want {
			t.Errorf("%s changed: %q → %q — existing installs' resources will be orphaned; this needs a migration, not a rename", name, tw.want, tw.got)
		}
	}
}
