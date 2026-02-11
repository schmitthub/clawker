package clawker

import (
  "errors"
  "fmt"
  "io"

  "github.com/spf13/cobra"

  "github.com/schmitthub/clawker/internal/cmd/factory"
  "github.com/schmitthub/clawker/internal/cmd/root"
  "github.com/schmitthub/clawker/internal/cmdutil"
  "github.com/schmitthub/clawker/internal/iostreams"
  "github.com/schmitthub/clawker/internal/logger"
)

// Build-time variables injected via ldflags
var (
  Version = "dev"
  Commit  = "none"
)

// Main is the entry point for the clawker CLI.
// It initializes the Factory, creates the root command, and executes it.
// Error rendering is centralized here — commands return typed errors
// rather than printing them directly.
func Main() int {
  // Ensure logs are flushed on exit
  defer logger.CloseFileWriter()

  // Create factory with version info
  f := factory.New(Version, Commit)

  // Create root command
  rootCmd := root.NewCmdRoot(f)

  // Silence Cobra's built-in error printing — we handle it in printError.
  rootCmd.SilenceErrors = true

  cmd, err := rootCmd.ExecuteC()
  if err != nil {
    if !errors.Is(err, cmdutil.SilentError) {
      printError(f.IOStreams.ErrOut, f.IOStreams.ColorScheme(), err, cmd)
    }

    var exitErr *cmdutil.ExitError
    if errors.As(err, &exitErr) {
      return exitErr.Code
    }
    return 1
  }

  return 0
}

// userFormattedError is a duck-typed interface for errors that provide
// rich user-facing output (e.g., Docker errors with context and suggestions).
type userFormattedError interface {
  FormatUserError() string
}

// printError renders an error to the given writer. It dispatches based on
// error type:
//   - FlagError: prints the error followed by usage
//   - userFormattedError: uses rich formatting (e.g., Docker error context)
//   - default: prints failure icon + error message
//
// A contextual help hint is always appended.
func printError(out io.Writer, cs *iostreams.ColorScheme, err error, cmd *cobra.Command) {
  var flagErr *cmdutil.FlagError
  var ufErr userFormattedError

  switch {
  case errors.As(err, &flagErr):
    fmt.Fprintln(out, err)
    fmt.Fprintln(out)
    fmt.Fprintln(out, cmd.UsageString())
  case errors.As(err, &ufErr):
    fmt.Fprint(out, ufErr.FormatUserError())
  default:
    fmt.Fprintf(out, "%s %s\n", cs.FailureIcon(), err)
  }

  fmt.Fprintf(out, "\nRun '%s --help' for more information.\n", cmd.CommandPath())
}
