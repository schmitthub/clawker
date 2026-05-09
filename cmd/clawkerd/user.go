package main

import (
	"errors"
	"fmt"
	"os"

	mobyuser "github.com/moby/sys/user"
)

// ExecUser is the resolved identity material clawkerd hands to the
// spawn path when starting the user CMD. Pure data — no syscalls
// performed here; privilege drop happens in the child via
// SysProcAttr.Credential between fork and execve.
type ExecUser struct {
	UID    uint32
	GID    uint32
	Groups []uint32 // supplementary groups
	Home   string   // used to set HOME in child env
}

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
func resolveUser(spec, passwdPath, groupPath string) (*ExecUser, error) {
	if spec == "" {
		return nil, errEmptyUserSpec
	}

	passwdFile, err := os.Open(passwdPath)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: open passwd %q: %w", passwdPath, err)
	}
	defer passwdFile.Close()

	groupFile, err := os.Open(groupPath)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: open group %q: %w", groupPath, err)
	}
	defer groupFile.Close()

	resolved, err := mobyuser.GetExecUser(spec, nil, passwdFile, groupFile)
	if err != nil {
		return nil, fmt.Errorf("clawkerd: resolve user %q: %w", spec, err)
	}

	groups := make([]uint32, 0, len(resolved.Sgids))
	for _, g := range resolved.Sgids {
		groups = append(groups, uint32(g))
	}

	return &ExecUser{
		UID:    uint32(resolved.Uid),
		GID:    uint32(resolved.Gid),
		Groups: groups,
		Home:   resolved.Home,
	}, nil
}

// passwdGroupPaths returns the production passwd/group file paths.
// Wrapping them in a function gives main.go (Task 2 wiring) a single
// seam for path injection without touching resolveUser's signature.
//
//nolint:unused // wired by Task 2 cutover; landed unwired by design
func passwdGroupPaths() (passwd, group string) {
	return "/etc/passwd", "/etc/group"
}
