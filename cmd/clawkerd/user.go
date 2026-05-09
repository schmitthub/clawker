package main

import (
	"errors"
	"fmt"
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

// UID returns the resolved primary uid.
func (u *ExecUser) UID() uint32 { return u.uid }

// GID returns the resolved primary gid.
func (u *ExecUser) GID() uint32 { return u.gid }

// Groups returns a copy of the supplementary group set so callers
// cannot mutate the resolved identity material.
func (u *ExecUser) Groups() []uint32 {
	out := make([]uint32, len(u.groups))
	copy(out, u.groups)
	return out
}

// Home returns the resolved $HOME for the user CMD.
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
// File errors are surfaced explicitly. moby/sys/user.GetExecUserPath
// silently ignores open failures and passes a nil reader, which would
// turn "passwd file missing" into "user not found" — a misleading
// diagnostic. Open both files here and forward to GetExecUser so the
// failure mode reaches operators with the path attached.
func resolveUser(spec, passwdPath, groupPath string) (eu *ExecUser, err error) {
	if spec == "" {
		return nil, errEmptyUserSpec
	}

	passwdFile, err := os.Open(passwdPath)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: open passwd %q: %w", passwdPath, err)
	}
	// Surface close failures via the named return so a kernel-rare
	// close error on a read-only /etc/passwd is not silently dropped.
	// Only overrides err on the otherwise-success path.
	defer func() {
		if cerr := passwdFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("clawkerd: close passwd %q: %w", passwdPath, cerr)
		}
	}()

	groupFile, err := os.Open(groupPath)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: open group %q: %w", groupPath, err)
	}
	defer func() {
		if cerr := groupFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("clawkerd: close group %q: %w", groupPath, cerr)
		}
	}()

	resolved, err := mobyuser.GetExecUser(spec, nil, passwdFile, groupFile)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: resolve user %q: %w", spec, err)
	}

	// Resolve the username from /etc/passwd by uid so USER/LOGNAME in
	// the child env match the dropped-privilege identity. GetExecUser
	// returns Uid/Gid/Home but not Name; ParsePasswdFileFilter walks
	// the same passwd file we already validated above.
	name, err := lookupUsernameByUID(passwdPath, resolved.Uid)
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

// lookupUsernameByUID parses passwdPath and returns the Name field
// of the row whose Uid matches the resolved uid. Returns an error if
// no row matches — this should never happen because GetExecUser
// already validated the user exists in the same file, but a race
// between GetExecUser and this call would surface here loudly
// rather than silently producing USER="".
func lookupUsernameByUID(passwdPath string, uid int) (string, error) {
	users, err := mobyuser.ParsePasswdFileFilter(passwdPath, func(u mobyuser.User) bool {
		return u.Uid == uid
	})
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", fmt.Errorf("uid=%d not found in %s", uid, passwdPath)
	}
	return users[0].Name, nil
}

// passwdGroupPaths returns the production passwd/group file paths.
// Wrapping them in a function gives main.go a single seam for path
// injection without touching resolveUser's signature.
func passwdGroupPaths() (passwd, group string) {
	return "/etc/passwd", "/etc/group"
}
