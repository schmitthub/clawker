package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPort_UnmarshalYAML(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    Port
		wantErr bool
	}{
		{"lower boundary", "1", 1, false},
		{"upper boundary", "65535", 65535, false},
		{"typical", "4319", 4319, false},
		{"zero rejected", "0", 0, true},
		{"negative rejected", "-1", 0, true},
		{"above max rejected", "65536", 0, true},
		{"non-int rejected", "abc", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p Port
			err := yaml.Unmarshal([]byte(tc.input), &p)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, p)
		})
	}
}
