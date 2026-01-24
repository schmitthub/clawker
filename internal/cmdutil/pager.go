package cmdutil

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// getPagerCommand returns the pager command to use.
// Order of precedence: CLAWKER_PAGER > PAGER > platform default
func getPagerCommand() string {
	// Check CLAWKER_PAGER first
	if pager := os.Getenv("CLAWKER_PAGER"); pager != "" {
		return pager
	}

	// Check standard PAGER variable
	if pager := os.Getenv("PAGER"); pager != "" {
		return pager
	}

	// Platform-specific defaults
	if runtime.GOOS == "windows" {
		return "more"
	}
	return "less -R"
}

// pagerWriter manages piping output to a pager process.
type pagerWriter struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	origW  io.Writer
	active bool
}

// newPagerWriter creates a pager that pipes output to the given command.
// The command string is split on spaces for arguments.
func newPagerWriter(pagerCmd string, origWriter io.Writer) (*pagerWriter, error) {
	parts := strings.Fields(pagerCmd)
	if len(parts) == 0 {
		return nil, nil
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = origWriter
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}

	return &pagerWriter{
		cmd:    cmd,
		stdin:  stdin,
		origW:  origWriter,
		active: true,
	}, nil
}

// Write implements io.Writer.
func (p *pagerWriter) Write(data []byte) (int, error) {
	if !p.active {
		return p.origW.Write(data)
	}
	return p.stdin.Write(data)
}

// Close closes the pager stdin and waits for the process to finish.
func (p *pagerWriter) Close() error {
	if !p.active {
		return nil
	}
	p.active = false

	if err := p.stdin.Close(); err != nil {
		return err
	}
	return p.cmd.Wait()
}

// process returns the underlying pager process, or nil if not active.
func (p *pagerWriter) process() *os.Process {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Process
}
