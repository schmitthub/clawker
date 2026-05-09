package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	mobyuser "github.com/moby/sys/user"
)

// ExecUser is the resolved identity material clawkerd hands to the
// spawn path when starting the user CMD. Fields are unexported so
// resolveUser is the sole producer — direct struct literals like
// `&ExecUser{}` (UID=0, silently re-introducing root) are not
// representable. Pure data — no syscalls performed here; privilege
// drop happens in the child via SysProcAttr.Credential between fork
// and execve.
type ExecUser struct {
	name   string // username from /etc/passwd row matching uid
	uid    uint32
	gid    uint32
	groups []uint32 // supplementary groups
	home   string   // used to set HOME in child env
}

// Name returns the resolved username (matching the uid's /etc/passwd
// row). Used to set USER and LOGNAME in the child env so tools like
// npm or sshd see the dropped-privilege identity instead of "root"
// inherited from PID 1.
func (u *ExecUser) Name() string { return u.name }

func (u *ExecUser) UID() uint32 { return u.uid }

func (u *ExecUser) GID() uint32 { return u.gid }

// Groups returns a copy of the supplementary group set so callers
// cannot mutate the resolved identity material.
func (u *ExecUser) Groups() []uint32 {
	out := make([]uint32, len(u.groups))
	copy(out, u.groups)
	return out
}

func (u *ExecUser) Home() string { return u.home }

// errEmptyUserSpec is returned by resolveUser when spec is empty.
// Empty resolution is intentionally rejected: a missing CLAWKER_USER
// would otherwise silently default to the moby library's "current
// process" semantics, leaking clawkerd's root identity into the user
// CMD.
var errEmptyUserSpec = errors.New("clawkerd: empty user spec")

// resolveUser parses spec ("name", "name:group", "uid", "uid:gid")
// against the passwd/group databases at the given paths. Production
// callers pass "/etc/passwd" and "/etc/group" (returned by
// passwdGroupPaths()); tests pass synthetic temp files.
//
// /etc/passwd is read ONCE into a byte slice so GetExecUser and the
// follow-up uid→name lookup operate on the same snapshot. A two-read
// approach risks identity skew if the file is rewritten between reads
// (admin tooling, package install) — uid/gid would come from snapshot
// 1 and Name from snapshot 2. Single-read closes the race.
//
// File errors are surfaced explicitly. moby/sys/user.GetExecUserPath
// silently ignores open failures and passes a nil reader, which would
// turn "passwd file missing" into "user not found" — a misleading
// diagnostic.
func resolveUser(spec, passwdPath, groupPath string) (*ExecUser, error) {
	if spec == "" {
		return nil, errEmptyUserSpec
	}

	passwdData, err := os.ReadFile(passwdPath)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: read passwd %q: %w", passwdPath, err)
	}

	groupFile, err := os.Open(groupPath)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: open group %q: %w", groupPath, err)
	}
	defer func() {
		_ = groupFile.Close()
	}()

	// Default Home="/" mirrors gosu's SetupUser: a numeric UID spec with
	// no usable passwd Home would otherwise leave Home="" and HOME would
	// fall through to PID-1's inherited value (HOME=/root from Docker).
	// moby applies defaults to blank fields, so a passwd row with an
	// empty Home column also gets "/" instead of "".
	defaults := &mobyuser.ExecUser{Home: "/"}
	resolved, err := mobyuser.GetExecUser(spec, defaults, bytes.NewReader(passwdData), groupFile)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: resolve user %q: %w", spec, err)
	}

	name, err := lookupUsernameByUID(bytes.NewReader(passwdData), resolved.Uid)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: lookup username for uid=%d: %w", resolved.Uid, err)
	}

	groups := make([]uint32, 0, len(resolved.Sgids))
	for _, g := range resolved.Sgids {
		groups = append(groups, uint32(g))
	}

	return &ExecUser{
		name:   name,
		uid:    uint32(resolved.Uid),
		gid:    uint32(resolved.Gid),
		groups: groups,
		home:   resolved.Home,
	}, nil
}

// lookupUsernameByUID parses the passwd reader and returns the Name
// field of the row whose Uid matches uid. Caller passes the same
// snapshot used by GetExecUser so a /etc/passwd rewrite cannot
// produce a uid/Name mismatch.
func lookupUsernameByUID(r io.Reader, uid int) (string, error) {
	users, err := mobyuser.ParsePasswdFilter(r, func(u mobyuser.User) bool {
		return u.Uid == uid
	})
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", fmt.Errorf("uid=%d not found in passwd", uid)
	}
	return users[0].Name, nil
}

// passwdGroupPaths returns the production passwd/group file paths.
// Wrapping them in a function gives main.go a single seam for path
// injection without touching resolveUser's signature.
func passwdGroupPaths() (passwd, group string) {
	return "/etc/passwd", "/etc/group"
}
