package iostreams

import (
	"fmt"
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

func TestIOStreams_pager(t *testing.T) {
	t.Skip("TODO: fix this test in race detection mode")
	ios, _, stdout, _ := Test()
	ios.SetStdoutTTY(true)
	ios.SetPager(fmt.Sprintf("%s -test.run=TestHelperProcess --", os.Args[0]))
	t.Setenv("GH_WANT_HELPER_PROCESS", "1")
	if err := ios.StartPager(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(ios.Out, "line1"); err != nil {
		t.Errorf("error writing line 1: %v", err)
	}
	if _, err := fmt.Fprintln(ios.Out, "line2"); err != nil {
		t.Errorf("error writing line 2: %v", err)
	}
	ios.StopPager()
	wants := "pager: line1\npager: line2\n"
	if got := stdout.String(); got != wants {
		t.Errorf("expected %q, got %q", wants, got)
	}
}

func TestStartPager_NoopWhenEmpty(t *testing.T) {
	ios, _, _, _ := Test()
	ios.SetStdoutTTY(true)
	ios.SetPager("")
	err := ios.StartPager()
	if err != nil {
		t.Errorf("StartPager with empty command should return nil, got %v", err)
	}
}

func TestStartPager_NoopWhenCat(t *testing.T) {
	ios, _, _, _ := Test()
	ios.SetStdoutTTY(true)
	ios.SetPager("cat")
	err := ios.StartPager()
	if err != nil {
		t.Errorf("StartPager with 'cat' should return nil, got %v", err)
	}
}

func TestStartPager_NoopWhenNotTTY(t *testing.T) {
	ios, _, _, _ := Test()
	// stdout not TTY (default from Test())
	ios.SetPager("less")
	err := ios.StartPager()
	if err != nil {
		t.Errorf("StartPager with non-TTY should return nil, got %v", err)
	}
}

func TestStopPager_NoopWithoutStart(t *testing.T) {
	ios, _, _, _ := Test()
	// Should not panic
	ios.StopPager()
}

func TestErrClosedPagerPipe(t *testing.T) {
	inner := fmt.Errorf("broken pipe")
	err := &ErrClosedPagerPipe{inner}
	if err.Error() != "broken pipe" {
		t.Errorf("ErrClosedPagerPipe.Error() = %q, want %q", err.Error(), "broken pipe")
	}
}
