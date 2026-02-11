package version

import (
	"testing"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		buildDate string
		want      string
	}{
		{
			name:    "version only",
			version: "1.2.3",
			want:    "clawker version 1.2.3\n",
		},
		{
			name:      "version with date",
			version:   "1.2.3",
			buildDate: "2026-02-11",
			want:      "clawker version 1.2.3 (2026-02-11)\n",
		},
		{
			name:    "dev version",
			version: "DEV",
			want:    "clawker version DEV\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.version, tt.buildDate)
			if got != tt.want {
				t.Errorf("Format(%q, %q) = %q, want %q", tt.version, tt.buildDate, got, tt.want)
			}
		})
	}
}
