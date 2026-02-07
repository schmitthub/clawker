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

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

func main() {
	defer logger.CloseFileWriter()

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
		cmdutil.PrintHelpHint(f.IOStreams, cmd.CommandPath())
		fmt.Fprintf(os.Stderr, "\n")
		pauseForReview(f, noPause)
		os.Exit(1)
	}

	pauseForReview(f, noPause)
}

// pauseForReview waits for Enter so UAT testers can review output before exit.
func pauseForReview(f *cmdutil.Factory, noPause bool) {
	if noPause {
		return
	}
	cs := f.IOStreams.ColorScheme()
	fmt.Fprintf(os.Stderr, "\n%s\n", cs.Muted("⏸  Paused for review — press Enter to exit"))
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// fawkerLifecycleHook returns a LifecycleHook that pauses for UAT review.
// Reads a single keypress: Enter/Space continues, 'q' quits. Uses raw mode
// so the user doesn't have to press Enter after 'q'.
func fawkerLifecycleHook(ios *iostreams.IOStreams) tui.LifecycleHook {
	return func(component, event string) tui.HookResult {
		cs := ios.ColorScheme()
		fmt.Fprintf(ios.ErrOut, "\n%s",
			cs.Muted(fmt.Sprintf("⏸  [%s] %s — Enter to continue, q to quit", component, event)))

		raw := term.NewRawModeStdin()
		if err := raw.Enable(); err != nil {
			// Fallback: non-TTY, read a line instead.
			fmt.Fprintf(ios.ErrOut, "\n")
			buf := make([]byte, 1)
			os.Stdin.Read(buf) //nolint:errcheck
			if buf[0] == 'q' || buf[0] == 'Q' {
				return tui.HookResult{
					Continue: false,
					Message:  fmt.Sprintf("UAT review terminated at [%s] %s", component, event),
				}
			}
			return tui.HookResult{Continue: true}
		}
		defer raw.Restore() //nolint:errcheck

		buf := make([]byte, 1)
		os.Stdin.Read(buf) //nolint:errcheck
		fmt.Fprintf(ios.ErrOut, "\n") // newline after the keypress
		if buf[0] == 'q' || buf[0] == 'Q' {
			return tui.HookResult{
				Continue: false,
				Message:  fmt.Sprintf("UAT review terminated at [%s] %s", component, event),
			}
		}
		return tui.HookResult{Continue: true}
	}
}
