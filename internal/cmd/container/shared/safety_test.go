package shared

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsOutsideHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	home, err = filepath.EvalSymlinks(home)
	require.NoError(t, err)

	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{
			name: "home directory itself",
			dir:  home,
			want: true,
		},
		{
			name: "parent of home",
			dir:  filepath.Dir(home),
			want: true,
		},
		{
			name: "root directory",
			dir:  "/",
			want: true,
		},
		{
			name: "subdirectory of home",
			dir:  filepath.Join(home, "Code"),
			want: false,
		},
		{
			name: "deeply nested inside home",
			dir:  filepath.Join(home, "Code", "project", "src"),
			want: false,
		},
		{
			name: "tmp directory",
			dir:  os.TempDir(),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsOutsideHome(tt.dir))
		})
	}
}
