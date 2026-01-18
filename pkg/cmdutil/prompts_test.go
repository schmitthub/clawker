package cmdutil

import (
	"strings"
	"testing"
)

func TestPromptForConfirmation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase y", "y\n", true},
		{"uppercase Y", "Y\n", true},
		{"lowercase n", "n\n", false},
		{"uppercase N", "N\n", false},
		{"yes word", "yes\n", false}, // Only y/Y accepted
		{"no word", "no\n", false},
		{"empty input", "\n", false},
		{"whitespace y", "  y  \n", true},
		{"random text", "maybe\n", false},
		{"EOF (empty reader)", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			got := PromptForConfirmation(reader, "Continue?")
			if got != tt.want {
				t.Errorf("PromptForConfirmation() = %v, want %v", got, tt.want)
			}
		})
	}
}
