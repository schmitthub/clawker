package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePasswdGroup(t *testing.T) (passwdPath, groupPath string) {
	t.Helper()
	dir := t.TempDir()
	passwdPath = filepath.Join(dir, "passwd")
	groupPath = filepath.Join(dir, "group")
	passwdContents := "" +
		"root:x:0:0:root:/root:/bin/bash\n" +
		"claude:x:1000:1000:Claude:/home/claude:/bin/bash\n"
	groupContents := "" +
		"root:x:0:\n" +
		"claude:x:1000:\n" +
		"wheel:x:10:claude\n"
	if err := os.WriteFile(passwdPath, []byte(passwdContents), 0o644); err != nil {
		t.Fatalf("write passwd: %v", err)
	}
	if err := os.WriteFile(groupPath, []byte(groupContents), 0o644); err != nil {
		t.Fatalf("write group: %v", err)
	}
	return passwdPath, groupPath
}

func TestResolveUser_HappyPaths(t *testing.T) {
	passwdPath, groupPath := writePasswdGroup(t)

	cases := []struct {
		name     string
		spec     string
		wantUID  uint32
		wantGID  uint32
		wantHome string
	}{
		{name: "name", spec: "claude", wantUID: 1000, wantGID: 1000, wantHome: "/home/claude"},
		{name: "name:group", spec: "claude:wheel", wantUID: 1000, wantGID: 10, wantHome: "/home/claude"},
		{name: "uid", spec: "1000", wantUID: 1000, wantGID: 1000, wantHome: "/home/claude"},
		{name: "uid:gid", spec: "1000:10", wantUID: 1000, wantGID: 10, wantHome: "/home/claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveUser(tc.spec, passwdPath, groupPath)
			if err != nil {
				t.Fatalf("resolveUser(%q) error: %v", tc.spec, err)
			}
			if got.UID != tc.wantUID {
				t.Errorf("UID = %d, want %d", got.UID, tc.wantUID)
			}
			if got.GID != tc.wantGID {
				t.Errorf("GID = %d, want %d", got.GID, tc.wantGID)
			}
			if got.Home != tc.wantHome {
				t.Errorf("Home = %q, want %q", got.Home, tc.wantHome)
			}
		})
	}
}

func TestResolveUser_EmptySpec(t *testing.T) {
	passwdPath, groupPath := writePasswdGroup(t)
	_, err := resolveUser("", passwdPath, groupPath)
	if !errors.Is(err, errEmptyUserSpec) {
		t.Fatalf("err = %v, want errEmptyUserSpec", err)
	}
}

func TestResolveUser_NotFound(t *testing.T) {
	passwdPath, groupPath := writePasswdGroup(t)
	_, err := resolveUser("nosuchuser", passwdPath, groupPath)
	if err == nil {
		t.Fatalf("resolveUser unexpectedly succeeded for absent user")
	}
	if !strings.Contains(err.Error(), "nosuchuser") {
		t.Errorf("err = %v, want spec %q in message", err, "nosuchuser")
	}
}

func TestResolveUser_PasswdMissing(t *testing.T) {
	_, groupPath := writePasswdGroup(t)
	_, err := resolveUser("claude", filepath.Join(t.TempDir(), "no-such-passwd"), groupPath)
	if err == nil {
		t.Fatalf("resolveUser unexpectedly succeeded with missing passwd")
	}
	if !strings.Contains(err.Error(), "passwd") {
		t.Errorf("err = %v, want path attached", err)
	}
}

func TestResolveUser_GroupMissing(t *testing.T) {
	passwdPath, _ := writePasswdGroup(t)
	_, err := resolveUser("claude", passwdPath, filepath.Join(t.TempDir(), "no-such-group"))
	if err == nil {
		t.Fatalf("resolveUser unexpectedly succeeded with missing group")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Errorf("err = %v, want path attached", err)
	}
}
