package clawkerd

import (
	"io/fs"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
)

func TestMapExitCode_NilState(t *testing.T) {
	if got := mapExitCode(nil); got != 1 {
		t.Errorf("mapExitCode(nil) = %d, want 1", got)
	}
}

func TestMapExitCode_NormalExit(t *testing.T) {
	c := exec.Command("/bin/sh", "-c", "exit 7")
	if err := c.Run(); err == nil {
		t.Fatal("expected non-nil error from exit 7")
	}
	if got := mapExitCode(c.ProcessState); got != 7 {
		t.Errorf("mapExitCode(exit 7) = %d, want 7", got)
	}
}

func TestMapExitCode_Signaled(t *testing.T) {
	c := exec.Command("/bin/sleep", "30")
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := c.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	_ = c.Wait()
	if got := mapExitCode(c.ProcessState); got != 128+int(syscall.SIGTERM) {
		t.Errorf("mapExitCode(SIGTERM) = %d, want %d", got, 128+int(syscall.SIGTERM))
	}
}

func TestEnvForUser(t *testing.T) {
	user := &ExecUser{name: "claude", uid: 1000, gid: 1000, home: "/home/claude"}

	contains := func(t *testing.T, env []string, want string) {
		t.Helper()
		for _, e := range env {
			if e == want {
				return
			}
		}
		t.Errorf("env missing %q; got %v", want, env)
	}
	notContains := func(t *testing.T, env []string, unwanted string) {
		t.Helper()
		for _, e := range env {
			if e == unwanted {
				t.Errorf("env unexpectedly contains %q; got %v", unwanted, env)
				return
			}
		}
	}

	t.Run("nil user passes through", func(t *testing.T) {
		got := envForUser([]string{"PATH=/x"}, nil)
		if !reflect.DeepEqual(got, []string{"PATH=/x"}) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("overrides root-shaped HOME/USER/LOGNAME from PID 1", func(t *testing.T) {
		// Docker's default PID-1 env shape: HOME=/root, USER=root,
		// LOGNAME=root inherited from the image's USER root preamble.
		// Forwarding those verbatim to claude (running as uid 1000)
		// would point its config dir at /root which it cannot access.
		got := envForUser([]string{"PATH=/x", "HOME=/root", "USER=root", "LOGNAME=root"}, user)
		contains(t, got, "PATH=/x")
		contains(t, got, "HOME=/home/claude")
		contains(t, got, "USER=claude")
		contains(t, got, "LOGNAME=claude")
		notContains(t, got, "HOME=/root")
		notContains(t, got, "USER=root")
		notContains(t, got, "LOGNAME=root")
	})

	t.Run("appends when missing", func(t *testing.T) {
		got := envForUser([]string{"PATH=/x"}, user)
		contains(t, got, "PATH=/x")
		contains(t, got, "HOME=/home/claude")
		contains(t, got, "USER=claude")
		contains(t, got, "LOGNAME=claude")
	})

	t.Run("user with empty home and name is a no-op", func(t *testing.T) {
		emptyUser := &ExecUser{uid: 1000, gid: 1000}
		env := []string{"PATH=/x"}
		got := envForUser(env, emptyUser)
		if !reflect.DeepEqual(got, env) {
			t.Errorf("got %v, want unchanged", got)
		}
	})
}

func TestRouteArgs(t *testing.T) {
	resolves := func(s string) (string, error) { return "/usr/bin/" + s, nil }
	notFound := func(string) (string, error) { return "", &exec.Error{Name: "x", Err: fs.ErrNotExist} }

	cases := []struct {
		name         string
		argv         []string
		lookPath     func(string) (string, error)
		want         []string
		wantResolved string // non-empty only on the no-rewrite success path
		wantErr      bool   // routeArgs surfaces lookPath errs only on PATH-fail rewrite
	}{
		{name: "empty", argv: nil, lookPath: resolves, want: nil},
		{name: "leading dash", argv: []string{"--help"}, lookPath: resolves, want: []string{"claude", "--help"}},
		{name: "resolvable command", argv: []string{"bash", "-l"}, lookPath: resolves, want: []string{"bash", "-l"}, wantResolved: "/usr/bin/bash"},
		{name: "unresolvable command", argv: []string{"unknown"}, lookPath: notFound, want: []string{"claude", "unknown"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, resolved, err := routeArgs(tc.argv, tc.lookPath)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("routeArgs(%v) = %v, want %v", tc.argv, got, tc.want)
			}
			if resolved != tc.wantResolved {
				t.Errorf("routeArgs(%v) resolved = %q, want %q", tc.argv, resolved, tc.wantResolved)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("routeArgs(%v) err = %v, wantErr=%v", tc.argv, err, tc.wantErr)
			}
		})
	}
}
