package main

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

func TestEnvWithHome(t *testing.T) {
	user := &ExecUser{UID: 1000, GID: 1000, Home: "/home/claude"}

	t.Run("nil user passes through", func(t *testing.T) {
		got := envWithHome([]string{"PATH=/x"}, nil)
		want := []string{"PATH=/x"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("appends HOME when missing", func(t *testing.T) {
		got := envWithHome([]string{"PATH=/x"}, user)
		want := []string{"PATH=/x", "HOME=/home/claude"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("preserves existing HOME", func(t *testing.T) {
		env := []string{"PATH=/x", "HOME=/override"}
		got := envWithHome(env, user)
		if !reflect.DeepEqual(got, env) {
			t.Errorf("got %v, want %v", got, env)
		}
	})

	t.Run("empty user.Home no-ops", func(t *testing.T) {
		emptyUser := &ExecUser{UID: 1000, GID: 1000}
		env := []string{"PATH=/x"}
		got := envWithHome(env, emptyUser)
		if !reflect.DeepEqual(got, env) {
			t.Errorf("got %v, want %v", got, env)
		}
	})
}

func TestRouteArgs(t *testing.T) {
	resolves := func(string) (string, error) { return "/usr/bin/found", nil }
	notFound := func(string) (string, error) { return "", &exec.Error{Name: "x", Err: fs.ErrNotExist} }

	cases := []struct {
		name     string
		argv     []string
		lookPath func(string) (string, error)
		want     []string
	}{
		{name: "empty", argv: nil, lookPath: resolves, want: nil},
		{name: "leading dash", argv: []string{"--help"}, lookPath: resolves, want: []string{"claude", "--help"}},
		{name: "leading dash short", argv: []string{"-c", "echo hi"}, lookPath: resolves, want: []string{"claude", "-c", "echo hi"}},
		{name: "resolvable command", argv: []string{"bash", "-l"}, lookPath: resolves, want: []string{"bash", "-l"}},
		{name: "unresolvable command", argv: []string{"unknown"}, lookPath: notFound, want: []string{"claude", "unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := routeArgs(tc.argv, tc.lookPath)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("routeArgs(%v) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}
