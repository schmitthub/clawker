package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// progressReporter writes user-facing boot status to a TTY-backed
// writer. clawkerd is PID 1 of the agent container; until
// handleAgentReady transfers the controlling tty's foreground pgroup
// to the spawned user CMD, clawkerd owns the attached TTY and the
// user otherwise sees a blank terminal during CP-driven init.
//
// Lifecycle: Banner once at boot, StartStep / EndStep per init
// ShellCommand, Final at handleAgentReady right before spawn, or Stop
// on any non-happy-path Session end. After Stop or Final the writer
// is muted so we never interleave with the user CMD's output (kernel
// TOSTOP defaults off, so writes from the now-background pgroup would
// otherwise still clobber claude's startup banner).
//
// Output is intentionally plain status lines, no animation: per-step
// shell scripts complete in milliseconds, far below the threshold a
// braille animation needs to show perceivable motion. Two lines per
// step ("starting…" then "✓ done") give the user a clear progress log
// without trying to make microscopic intervals look animated.
//
// All methods are nil-safe so test sessions can leave progress unset.
// Methods are safe to call concurrently: the per-Session sender
// goroutine drives EndStep via session.runSender's settleInitStep call
// (after each terminal stream.Send), while the receive loop goroutine
// drives Banner / StartStep / Final / Stop from dispatch,
// handleAgentReady, and runSession's defer. mu serializes the stopped
// check + write so once Stop or Final returns, no further line can
// land on the TTY (which would otherwise clobber the user CMD's
// startup output once handleAgentReady transfers the foreground pgroup
// during spawn).
type progressReporter struct {
	out     io.Writer
	isTTY   bool
	mu      sync.Mutex
	stopped bool
}

// newProgressReporter returns a reporter that writes to out. TTY
// detection probes TIOCGPGRP — same shape used by spawn_unix.go's
// stdinCttyFd, portable across the unix targets clawkerd builds for
// (TCGETS would be linux-only). progress.go has no //go:build unix
// tag itself, but importing golang.org/x/sys/unix unconditionally
// fences this file to unix in practice, matching clawkerd's
// linux-only deployment. On a non-TTY out, ANSI color codes are
// suppressed and the info icon falls back to "[info]" so log scrapes
// stay clean.
func newProgressReporter(out io.Writer) *progressReporter {
	p := &progressReporter{out: out}
	if f, ok := out.(*os.File); ok {
		if _, err := unix.IoctlGetInt(int(f.Fd()), unix.TIOCGPGRP); err == nil {
			p.isTTY = true
		}
	}
	return p
}

// Banner prints a top-level header once at boot.
func (p *progressReporter) Banner(label string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	fmt.Fprintf(p.out, "%s %s\n", p.info(), label)
}

// StartStep prints the "in progress" line for an init step. Paired
// with a subsequent EndStep that prints the completion line — two
// lines per step. CP issues init steps strictly sequentially, so we
// never have to track which step is "current".
func (p *progressReporter) StartStep(label initStepLabel) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	fmt.Fprintf(p.out, "  %s\n", label.Active)
}

// EndStep prints the completion line for an init step. ok=true → ✓ +
// done form; ok=false → ✗ + active form annotated as failed.
func (p *progressReporter) EndStep(label initStepLabel, ok bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	if ok {
		fmt.Fprintf(p.out, "  %s %s\n", p.green("✓"), label.Done)
		return
	}
	fmt.Fprintf(p.out, "  %s %s (failed)\n", p.red("✗"), label.Active)
}

// finalLabel is the fixed closing-banner text. Hard-coded (not a
// parameter) so a caller can't accidentally burn the once-only Final
// slot with an empty string.
const finalLabel = "Running agent command..."

// Final prints the closing banner then mutes the reporter. Use on the
// happy path (handleAgentReady, immediately before spawning the user
// CMD). After this returns, clawkerd writes are muted; the subsequent
// spawnEntry call is what transfers the controlling-tty foreground
// pgroup to the user CMD via SysProcAttr.Foreground.
func (p *progressReporter) Final() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	fmt.Fprintf(p.out, "%s %s\n", p.info(), finalLabel)
	p.stopped = true
}

// Stop is the quiet cleanup path — disables further writes without
// emitting a banner. Use on Session teardown, init failure, or any
// path where Final wasn't reached. Safe to call multiple times; safe
// to interleave with Final.
func (p *progressReporter) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
}

// info returns the standard info icon — cyan ℹ on a TTY, [info] as
// ASCII fallback. Mirrors iostreams.ColorScheme.InfoIcon so the
// in-container output stays visually consistent with host CLI output.
func (p *progressReporter) info() string {
	if !p.isTTY {
		return "[info]"
	}
	return "\033[36mℹ\033[0m"
}

func (p *progressReporter) green(s string) string {
	if !p.isTTY {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

func (p *progressReporter) red(s string) string {
	if !p.isTTY {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

// initStepLabel pairs the in-progress and completion forms of a step
// name. Active is shown when the step starts (gerund + ellipsis);
// Done is shown after success (past-tense, no trailing punctuation).
type initStepLabel struct {
	Active string
	Done   string
}

// initStepLabels maps the CP-side step name (carried via the init-
// CommandID prefix) to its two-form label. Mirrors the plan in
// internal/controlplane/agent/init.go::plan. Unknown step names fall
// back to the raw step in both forms so a CP that adds a plan entry
// without a label still renders something sensible.
var initStepLabels = map[string]initStepLabel{
	"docker-socket":   {Active: "Configuring Docker socket...", Done: "Docker socket configured"},
	"config":          {Active: "Seeding agent config...", Done: "Agent config seeded"},
	"git":             {Active: "Configuring git...", Done: "Git configured"},
	"git-credentials": {Active: "Configuring git credentials...", Done: "Git credentials configured"},
	"ssh":             {Active: "Configuring SSH known_hosts...", Done: "SSH known_hosts configured"},
	"post-init":       {Active: "Running post-init...", Done: "Post-init complete"},
	"agent-ready":     {Active: "Running agent command...", Done: "Agent command running"},
}

// parseInitStep extracts the step label from a CP-issued init
// CommandID. Format: `init-<containerID truncated to 12 chars>-<stepname>-<idx>`
// — see internal/controlplane/agent/init.go::buildCommandID. Returns
// the matched label and ok=true when the ID matches the init shape.
// The strip is fixed-width 13 (12 chars + the trailing hyphen), so a
// container ID shorter than 12 chars would mis-parse. Real Docker
// container IDs are 64 hex chars and always truncate to exactly 12;
// only synthetic test IDs would trip the assumption.
func parseInitStep(commandID string) (initStepLabel, bool) {
	const prefix = "init-"
	if !strings.HasPrefix(commandID, prefix) {
		return initStepLabel{}, false
	}
	rest := commandID[len(prefix):]
	if len(rest) < 14 {
		return initStepLabel{}, false
	}
	rest = rest[13:]
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 {
		return initStepLabel{}, false
	}
	step := rest[:idx]
	if label, ok := initStepLabels[step]; ok {
		return label, true
	}
	return initStepLabel{Active: step + "...", Done: step}, true
}
