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

// envForUser returns env with HOME, USER, and LOGNAME overridden
// to match the resolved user. clawkerd as PID 1 inherits Docker's
// root-shaped env (HOME=/root, USER=root, LOGNAME=root); forwarding
// those verbatim to a privilege-dropped child means tools like
// claude look for config under /root/.claude instead of the user's
// real home and fail with permission errors. Override is the right
// shape (gosu does the same — it Unsetenv("HOME") before SetupUser
// so the SetupUser default applies; we replace in-place because we
// build the env slice ourselves). Every other variable passes
// through verbatim.
//
// USER and LOGNAME are set in addition to HOME because some tools
// (npm, sshd, mail clients) read them as the canonical username
// rather than calling getpwuid; those would otherwise see "root"
// and produce surprising audit trails or pathing.
func envForUser(env []string, user *ExecUser) []string {
	if user == nil {
		return env
	}
	overrides := map[string]string{}
	if user.Home() != "" {
		overrides["HOME"] = user.Home()
	}
	if name := user.Name(); name != "" {
		overrides["USER"] = name
		overrides["LOGNAME"] = name
	}
	if len(overrides) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(overrides))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if !ok {
			out = append(out, e)
			continue
		}
		if _, override := overrides[key]; override {
			continue
		}
		out = append(out, e)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
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
