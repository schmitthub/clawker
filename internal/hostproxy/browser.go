package hostproxy

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser opens the specified URL in the system's default browser.
// ctx ends a stuck opener (xdg-open can hang on a broken desktop
// session), so the action's deadline and the stream's end reach it.
func openBrowser(ctx context.Context, url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Run()
}
