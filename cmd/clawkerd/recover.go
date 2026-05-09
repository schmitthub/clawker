package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/schmitthub/clawker/internal/logger"
)

// recoverGoroutine is the resilience-contract recovery wrapper for
// long-lived clawkerd goroutines. clawkerd is PID 1; a panic in any
// of these would kill the supervisor → container exits → user CMD
// dies with no observability surface (root CLAUDE.md "CP crashing
// is a SECURITY incident" describes the same shape on the CP side
// — eBPF state lives there, not here, but the resilience contract
// is identical).
// onPanic, when non-nil, fires after the structured log so a panic
// in a load-bearing goroutine (e.g. the reaper) can release waiters
// via closeDoneCh, or the session sender can cancel its ctx, rather
// than deadlocking peers.
//
// The recovery message is mirrored to os.Stderr so it survives
// lumberjack rotation failure (full disk, broken symlink, etc.) —
// without the stderr mirror, an unwritable rotated log would erase
// the only surface an operator has for triaging a degraded PID 1.
// Stderr lands in `docker logs <agent>` as a fallback.
//
// Lives in its own file (no build tag) so listener.go and session.go
// — both build-tag-free — can share the same wrapper as the unix-
// tagged spawnState goroutines in spawn_unix.go.
func recoverGoroutine(log *logger.Logger, name string, onPanic func()) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	// Defense-in-depth nil-log guard. Production callers always pass
	// the daemon logger; a nil log would otherwise re-panic from inside
	// the recovery wrapper, defeating the resilience contract.
	if log != nil {
		log.Error().
			Interface("panic", r).
			Bytes("stack", stack).
			Str("event", "goroutine_panic").
			Str("goroutine", name).
			Msg("clawkerd: goroutine recovered from panic; supervisor degrading but staying alive")
	}
	fmt.Fprintf(os.Stderr, "clawkerd: goroutine_panic goroutine=%s panic=%v\n%s\n", name, r, stack)
	if onPanic != nil {
		onPanic()
	}
}
