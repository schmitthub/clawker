package main

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// errAlreadySpawned is returned by clawkerd's spawn entry on a second
// call. handleAgentReady maps this to Done{0} so a Session reconnect
// (CP redispatching the idempotent init plan) does not double-spawn
// the user CMD.
var errAlreadySpawned = errors.New("clawkerd: user CMD already spawned")

// errEmptyArgv is returned when the spawn entry is invoked with no
// argv. The container CMD is required — refuse with a typed error
// rather than triggering a confusing exec.LookPath failure deeper in
// the stack.
var errEmptyArgv = errors.New("clawkerd: empty argv; container CMD is required")

// mapExitCode converts a *os.ProcessState into a bash-convention
// exit code:
//
//   - normal exit                → state.ExitCode()
//   - signaled (WIFSIGNALED)     → 128 + signum
//   - state == nil               → 1 (process never started)
//   - any other unrecognized end → 1
//
// The 128+signum encoding matches what bash propagates from a child
// killed by a signal, which is what the Docker `restart: on-failure`
// machinery is calibrated against.
func mapExitCode(state *os.ProcessState) int {
	if state == nil {
		return 1
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok {
		switch {
		case ws.Signaled():
			return 128 + int(ws.Signal())
		case ws.Exited():
			return ws.ExitStatus()
		}
	}
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

// envWithHome returns env with HOME=user.Home appended unless env
// already contains a HOME entry. Other entries pass through. user==nil
// returns env unchanged. This is the only env shaping clawkerd
// performs — every other variable inherited from clawkerd's own
// environment is forwarded verbatim.
func envWithHome(env []string, user *ExecUser) []string {
	if user == nil || user.Home() == "" {
		return env
	}
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	out = append(out, env...)
	out = append(out, "HOME="+user.Home())
	return out
}

// routeArgs implements the docker-image "--help routing" convention:
// when argv[0] starts with "-" or is not on PATH, prepend "claude"
// so `docker run <image> --help` invokes claude with --help rather
// than failing with `exec: "--help": not found`. The string "claude"
// is the image's default CMD; changing it requires changing the
// Dockerfile CMD too.
//
// resolvedPath is non-empty only on the no-rewrite success path so
// callers skip a redundant lookPath; the err return surfaces the
// PATH-fail rewrite cause so callers can log a broken image rather
// than silently running "claude <broken>".
func routeArgs(argv []string, lookPath func(string) (string, error)) (routed []string, resolvedPath string, err error) {
	if len(argv) == 0 {
		return argv, "", nil
	}
	first := argv[0]
	if strings.HasPrefix(first, "-") {
		out := make([]string, 0, len(argv)+1)
		out = append(out, "claude")
		out = append(out, argv...)
		return out, "", nil
	}
	p, lookErr := lookPath(first)
	if lookErr != nil {
		out := make([]string, 0, len(argv)+1)
		out = append(out, "claude")
		out = append(out, argv...)
		return out, "", lookErr
	}
	return argv, p, nil
}
