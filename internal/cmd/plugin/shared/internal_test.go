package shared

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarketplacePluginSource_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		wantRelPath string
		wantErrIs   error  // errors.Is target when non-nil
		wantErrText string // error substring when non-empty
	}{
		{name: "relative path", data: `"./"`, wantRelPath: "./", wantErrIs: nil, wantErrText: ""},
		{
			name: "dot-dot inside a segment is a name", data: `"./my..dir"`,
			wantRelPath: "./my..dir", wantErrIs: nil, wantErrText: "",
		},
		{
			name: "parent traversal", data: `"../outside"`,
			wantRelPath: "", wantErrIs: ErrSourceTraversal, wantErrText: "",
		},
		{
			name: "embedded traversal segment", data: `"plugins/../../outside"`,
			wantRelPath: "", wantErrIs: ErrSourceTraversal, wantErrText: "",
		},
		{
			name: "malformed source", data: `42`,
			wantRelPath: "", wantErrIs: nil, wantErrText: "parsing plugin source object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var src marketplacePluginSource
			err := json.Unmarshal([]byte(tt.data), &src)
			if tt.wantErrIs != nil {
				assert.ErrorIs(t, err, tt.wantErrIs)
				return
			}
			if tt.wantErrText != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRelPath, src.RelPath)
		})
	}
}
