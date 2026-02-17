// Fawker is a standalone demo CLI that mirrors clawker's command tree but runs
// against faked dependencies with recorded build scenarios. Use it for visual
// UAT of build progress display and other TUI components without Docker.
//
// Usage:
//
//	go build -o bin/fawker ./cmd/fawker
//	./bin/fawker image build
//	./bin/fawker image build --scenario multi-stage
//	./bin/fawker image build --scenario error --progress plain
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

func main() {
	f, scenario := fawkerFactory()
	noPause := false
	step := false
	rootCmd := newFawkerRoot(f, scenario, &noPause, &step)

	// Wire lifecycle hook when --step is active. Must happen after flag parsing
	// but before command execution — use PersistentPreRunE. The TUI struct is
	// already constructed (pointer shared with commands), so hooks registered
	// here are visible when RunE fires.
	rootCmd.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if step {
			f.TUI.RegisterHooks(fawkerLifecycleHook(f.IOStreams))
		}
		return nil
	}

	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "\nError: %s\n", err)
		fmt.Fprintf(f.IOStreams.ErrOut, "Run '%s --help' for more information.\n", cmd.CommandPath())
		pauseForReview(f, noPause)
		logger.CloseFileWriter()
		os.Exit(1)
	}

	pauseForReview(f, noPause)
	logger.CloseFileWriter()
}

// pauseForReview is an exit hook — pauses after command completion so UAT
// testers can review output before fawker terminates. Uses raw mode so other
// keypresses are silently ignored (no terminal echo).
func pauseForReview(f *cmdutil.Factory, noPause bool) {
	if noPause {
		return
	}
	cs := f.IOStreams.ColorScheme()
	fmt.Fprintf(f.IOStreams.ErrOut, "\n%s\n", cs.Muted("⏸  Done — press Enter to exit fawker"))

	raw := term.NewRawModeStdin()
	if err := raw.Enable(); err != nil {
		// Fallback: non-TTY, read lines until empty (Enter).
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "" {
				return
			}
		}
		return
	}
	defer raw.Restore() //nolint:errcheck

	// Raw mode: loop until Enter.
	for {
		buf := make([]byte, 1)
		if _, err := os.Stdin.Read(buf); err != nil {
			return // EOF or read error
		}
		if buf[0] == '\r' || buf[0] == '\n' {
			return
		}
	}
}

// fawkerLifecycleHook returns a LifecycleHook that pauses for UAT review.
// Reads keypresses in a loop: only Enter continues, 'q' quits. Uses raw mode
// so the user doesn't have to press Enter after 'q'.
func fawkerLifecycleHook(ios *iostreams.IOStreams) tui.LifecycleHook {
	return func(component, event string) tui.HookResult {
		cs := ios.ColorScheme()
		fmt.Fprintf(ios.ErrOut, "\n%s",
			cs.Muted(fmt.Sprintf("⏸  [%s] %s — Enter to continue, q to throw error", component, event)))

		raw := term.NewRawModeStdin()
		if err := raw.Enable(); err != nil {
			// Fallback: non-TTY, read lines until Enter or q.
			fmt.Fprintf(ios.ErrOut, "\\r\\n")
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					return tui.HookResult{Continue: true}
				}
				if line == "q" || line == "Q" {
					return tui.HookResult{
						Continue: false,
						Message:  fmt.Sprintf("UAT review terminated at [%s] %s", component, event),
					}
				}
			}
			return tui.HookResult{Continue: true}
		}
		defer raw.Restore() //nolint:errcheck

		// Raw mode: loop until Enter or q.
		for {
			buf := make([]byte, 1)
			if _, err := os.Stdin.Read(buf); err != nil {
				return tui.HookResult{Continue: true} // EOF or read error
			}
			switch buf[0] {
			case '\r', '\n':
				fmt.Fprintf(ios.ErrOut, "\r\n")
				return tui.HookResult{Continue: true}
			case 'q', 'Q':
				fmt.Fprintf(ios.ErrOut, "\r\n")
				return tui.HookResult{
					Continue: false,
					Message:  fmt.Sprintf("UAT review terminated at [%s] %s", component, event),
				}
			}
		}
	}
}
