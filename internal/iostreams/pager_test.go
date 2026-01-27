package iostreams

import (
	"os"
	"runtime"
	"testing"
)

func TestGetPagerCommand(t *testing.T) {
	// Save original env vars
	origClawkerPager := os.Getenv("CLAWKER_PAGER")
	origPager := os.Getenv("PAGER")
	defer func() {
		os.Setenv("CLAWKER_PAGER", origClawkerPager)
		os.Setenv("PAGER", origPager)
	}()

	tests := []struct {
		name         string
		clawkerPager string
		pager        string
		wantContains string
		wantDefault  bool
	}{
		{
			name:         "CLAWKER_PAGER takes precedence",
			clawkerPager: "custom-pager",
			pager:        "less",
			wantContains: "custom-pager",
		},
		{
			name:         "PAGER when CLAWKER_PAGER empty",
			clawkerPager: "",
			pager:        "more",
			wantContains: "more",
		},
		{
			name:         "platform default when both empty",
			clawkerPager: "",
			pager:        "",
			wantDefault:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("CLAWKER_PAGER", tt.clawkerPager)
			os.Setenv("PAGER", tt.pager)

			got := getPagerCommand()

			if tt.wantDefault {
				if runtime.GOOS == "windows" {
					if got != "more" {
						t.Errorf("getPagerCommand() = %q, want 'more' on Windows", got)
					}
				} else {
					if got != "less -R" {
						t.Errorf("getPagerCommand() = %q, want 'less -R' on Unix", got)
					}
				}
			} else if tt.wantContains != "" {
				if got != tt.wantContains {
					t.Errorf("getPagerCommand() = %q, want %q", got, tt.wantContains)
				}
			}
		})
	}
}

func TestPagerWriter_EmptyCommand(t *testing.T) {
	var buf testBuffer
	pw, err := newPagerWriter("", &buf)
	if err != nil {
		t.Fatalf("newPagerWriter with empty command should not error: %v", err)
	}
	if pw != nil {
		t.Error("newPagerWriter with empty command should return nil")
	}
}
