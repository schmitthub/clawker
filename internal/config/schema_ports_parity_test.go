package config

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
)

// TestControlPlanePortDefaultsMatchConsts pins the struct-tag defaults on
// ControlPlaneSettings to the consts.Default* port constants. Struct tags
// cannot reference consts, so the two spellings can silently drift; the
// tags feed the storage defaulting layer, while the consts exist for
// programmatic callers (flag defaults, fixtures, future URL builders) —
// this parity is what makes the consts trustworthy to use. It also
// catches a malformed default tag, which would corrupt
// GenerateDefaultsYAML output.
func TestControlPlanePortDefaultsMatchConsts(t *testing.T) {
	want := map[string]int{
		"AdminPort":         consts.DefaultCPAdminPort,
		"HealthPort":        consts.DefaultCPHealthPort,
		"HydraPublicPort":   consts.DefaultHydraPublicPort,
		"HydraAdminPort":    consts.DefaultHydraAdminPort,
		"OathkeeperPort":    consts.DefaultOathkeeperPort,
		"OathkeeperAPIPort": consts.DefaultOathkeeperAPIPort,
		"KratosPublicPort":  consts.DefaultKratosPublicPort,
		"KratosAdminPort":   consts.DefaultKratosAdminPort,
		"AgentPort":         consts.DefaultCPAgentPort,
	}

	typ := reflect.TypeFor[ControlPlaneSettings]()
	if typ.NumField() != len(want) {
		t.Errorf("ControlPlaneSettings has %d fields, parity table has %d — add the new port to both the consts and this test", typ.NumField(), len(want))
	}
	for name, wantPort := range want {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Errorf("field %s missing from ControlPlaneSettings", name)
			continue
		}
		tag := field.Tag.Get("default")
		got, err := strconv.Atoi(tag)
		if err != nil {
			t.Errorf("%s: default tag %q is not an int: %v", name, tag, err)
			continue
		}
		if got != wantPort {
			t.Errorf("%s: struct-tag default %d != consts default %d", name, got, wantPort)
		}
	}
}
