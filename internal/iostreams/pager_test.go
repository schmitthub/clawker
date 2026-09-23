package iostreams

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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

// TestHelperProcess is the fake pager subprocess used by TestIOStreams_pager.
// The test binary re-invokes itself with -test.run=TestHelperProcess; the env
// guard ensures this function is a no-op during normal test discovery.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GH_WANT_HELPER_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Printf("pager: %s\n", scanner.Text())
	}
	os.Exit(0)
}

func TestIOStreams_pager(t *testing.T) {
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

func TestSystem_PagerFromEnv(t *testing.T) {
	t.Setenv("CLAWKER_PAGER", "custom-pager")
	t.Setenv("PAGER", "more")

	if got := System().pagerCommand; got != "custom-pager" {
		t.Errorf("System().pagerCommand = %q, want %q", got, "custom-pager")
	}
}

func TestStartPager_StartFailureKeepsOut(t *testing.T) {
	// An executable file that is not a valid program: LookPath finds it,
	// Start fails.
	bad := filepath.Join(t.TempDir(), "bad-pager")
	if err := os.WriteFile(bad, []byte("not a program"), 0o700); err != nil {
		t.Fatal(err)
	}

	ios, _, stdout, _ := Test()
	ios.SetStdoutTTY(true)
	ios.SetPager(bad)
	if err := ios.StartPager(); err == nil {
		t.Fatal("StartPager with an invalid program should return an error")
	}

	if _, err := fmt.Fprint(ios.Out, "text"); err != nil {
		t.Fatalf("write after failed StartPager: %v", err)
	}
	if got := stdout.String(); got != "text" {
		t.Errorf("stdout = %q, want %q", got, "text")
	}
}

type errWriteCloser struct{ err error }

func (w errWriteCloser) Write([]byte) (int, error) { return 0, w.err }
func (w errWriteCloser) Close() error              { return nil }

func TestPagerWriter_ClosedPagerDropsOutput(t *testing.T) {
	for _, closedErr := range []error{syscall.EPIPE, io.ErrClosedPipe} {
		w := &pagerWriter{errWriteCloser{closedErr}}
		n, err := w.Write([]byte("text"))
		if err != nil || n != 4 {
			t.Errorf("Write with %v = (%d, %v), want (4, nil)", closedErr, n, err)
		}
	}

	other := errors.New("disk full")
	w := &pagerWriter{errWriteCloser{other}}
	if _, err := w.Write([]byte("text")); !errors.Is(err, other) {
		t.Errorf("Write with other error = %v, want %v", err, other)
	}
}
