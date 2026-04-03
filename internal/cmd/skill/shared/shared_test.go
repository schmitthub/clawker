package shared

import (
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateScope(t *testing.T) {
	tests := []struct {
		name    string
		scope   string
		wantErr bool
	}{
		{name: "user", scope: "user", wantErr: false},
		{name: "project", scope: "project", wantErr: false},
		{name: "local", scope: "local", wantErr: false},
		{name: "invalid_global", scope: "global", wantErr: true},
		{name: "empty", scope: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScope(tt.scope)
			if tt.wantErr {
				require.Error(t, err)
				var flagErr *cmdutil.FlagError
				assert.True(t, errors.As(err, &flagErr), "expected FlagError, got %T", err)
				assert.Contains(t, err.Error(), tt.scope)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
