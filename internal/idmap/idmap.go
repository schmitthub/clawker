// Package idmap computes the ID-mapped view that makes host-owned bind
// mounts usable on rootless Docker.
//
// Rootless Docker maps the daemon user to container root, so a bind-mounted
// workspace appears root-owned inside the container and the unprivileged
// clawker user cannot touch it. Docker exposes no per-mount ID mapping
// (moby#52061 died unimplemented), and the kernel reserves idmapped-mount
// creation for init-namespace CAP_SYS_ADMIN — so clawker provisions the view
// itself: a privileged one-shot (cmd/idmap-mount) attaches an idmapped bind
// of the workspace at a clawker-owned path, mapping the owner's uid/gid to
// the kernel ids the container's clawker user occupies. Container specs then
// bind the view instead of the raw path; the host keeps using the raw path
// with its ownership untouched, and files written from either side appear
// natively owned on both.
//
// This package is the unprivileged half: the mapping arithmetic, the view
// path derivation, and the bind-source rewriting. It is a leaf package so
// the privileged helper can link it without dragging in the CLI.
package idmap

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/mount"
)

const (
	// SubUIDFile and SubGIDFile are the shadow-utils subordinate ID
	// registries rootlesskit builds the daemon's user namespace from.
	// Rows are "user:base:count", keyed by name or numeric uid.
	SubUIDFile = "/etc/subuid"
	SubGIDFile = "/etc/subgid"

	// viewHashLen is how many hex digits of the source-path hash go into a
	// view directory name — enough to keep same-basename projects apart,
	// short enough to keep the path readable.
	viewHashLen = 12

	// idPairSep separates the two halves of a FormatIDPair value.
	idPairSep = ":"
)

// Mapping is one uid pair and one gid pair: the on-disk owner IDs of the
// workspace and the kernel IDs the idmapped mount must present them as (the
// IDs the container's clawker user occupies in the daemon's user namespace).
type Mapping struct {
	FromUID uint32
	ToUID   uint32
	FromGID uint32
	ToGID   uint32
}

// MappingInputs carries everything ComputeMapping needs. Subuid and Subgid
// are the raw contents of the subordinate ID files; UserName/UserUID
// identify the daemon user whose rows apply (files may key rows by either
// spelling).
type MappingInputs struct {
	OwnerUID uint32
	OwnerGID uint32
	UserName string
	UserUID  uint32
	Subuid   string
	Subgid   string
}

// ComputeMapping resolves the kernel IDs for the workspace owner using the
// documented rootless formula: container id 0 is the daemon user, container
// id n≥1 lands at offset n-1 within the user's concatenated subordinate
// ranges. The workspace owner's uid IS the container clawker uid (the image
// bakes clawker with the host uid), so the owner's ids feed the formula
// directly.
func ComputeMapping(in MappingInputs) (Mapping, error) {
	if in.OwnerUID == 0 {
		return Mapping{}, errors.New("idmap: workspace is root-owned; an ID-mapped view is for user-owned trees")
	}
	if in.OwnerGID == 0 {
		// Refused for its own reason, not folded into the uid check: id 0 is
		// the daemon user in the rootless formula, so without this guard the
		// n-1 offset underflows and produces range-walk debris instead of an
		// answer that names the problem.
		return Mapping{}, errors.New("idmap: workspace is root-group-owned; an ID-mapped view is for user-owned trees")
	}

	kuid, err := resolveSubordinate(in.Subuid, in.UserName, in.UserUID, in.OwnerUID)
	if err != nil {
		return Mapping{}, fmt.Errorf("idmap: resolving uid %d in %s: %w", in.OwnerUID, SubUIDFile, err)
	}
	kgid, err := resolveSubordinate(in.Subgid, in.UserName, in.UserUID, in.OwnerGID)
	if err != nil {
		return Mapping{}, fmt.Errorf("idmap: resolving gid %d in %s: %w", in.OwnerGID, SubGIDFile, err)
	}

	return Mapping{FromUID: in.OwnerUID, ToUID: kuid, FromGID: in.OwnerGID, ToGID: kgid}, nil
}

// resolveSubordinate walks the user's ranges in file order and returns the
// subordinate id at offset n-1, mirroring how rootlesskit lays container ids
// over the concatenated ranges.
func resolveSubordinate(content, userName string, userUID, n uint32) (uint32, error) {
	ranges := parseSubordinateRanges(content, userName, userUID)
	if len(ranges) == 0 {
		return 0, errors.New("no subordinate ID ranges for the daemon user")
	}

	offset := n - 1
	for _, r := range ranges {
		if offset < r.count {
			return r.base + offset, nil
		}
		offset -= r.count
	}
	return 0, fmt.Errorf("id %d falls outside the user's %d subordinate range(s)", n, len(ranges))
}

type subordinateRange struct {
	base  uint32
	count uint32
}

// subordinateRowFields is user:base:count.
const subordinateRowFields = 3

// parseSubordinateRanges extracts the user's rows from subordinate ID file
// content. Rows keyed by either the user name or the numeric uid count;
// malformed rows are skipped — the file is system-owned and one stray line
// must not break container creation.
func parseSubordinateRanges(content, userName string, userUID uint32) []subordinateRange {
	uidKey := strconv.FormatUint(uint64(userUID), 10)

	var ranges []subordinateRange
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		if r, ok := parseSubordinateRow(scanner.Text(), userName, uidKey); ok {
			ranges = append(ranges, r)
		}
	}
	// A scanner error over an in-memory string cannot occur; the loop above
	// simply ends. Nothing to surface.
	return ranges
}

// parseSubordinateRow reads one "user:base:count" row, reporting false for
// comments, other users' rows, and anything malformed.
func parseSubordinateRow(line, userName, uidKey string) (subordinateRange, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return subordinateRange{}, false
	}
	parts := strings.Split(line, ":")
	if len(parts) != subordinateRowFields {
		return subordinateRange{}, false
	}
	if parts[0] != userName && parts[0] != uidKey {
		return subordinateRange{}, false
	}
	base, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return subordinateRange{}, false
	}
	count, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return subordinateRange{}, false
	}
	return subordinateRange{base: uint32(base), count: uint32(count)}, true
}

// ViewPath derives the deterministic per-source view directory under base:
// the source's basename for readability plus a short content hash of the
// full path so same-named projects in different places get distinct views.
func ViewPath(base, source string) string {
	sum := sha256.Sum256([]byte(source))
	name := fmt.Sprintf("%s-%s", filepath.Base(source), hex.EncodeToString(sum[:])[:viewHashLen])
	return filepath.Join(base, name)
}

// RewriteMounts returns a copy of mounts with every bind source at or under
// root repointed to the corresponding path under view, plus how many entries
// were rewritten. Non-bind mounts and sources outside root pass through
// untouched.
func RewriteMounts(mounts []mount.Mount, root, view string) ([]mount.Mount, int) {
	out := make([]mount.Mount, len(mounts))
	copy(out, mounts)

	rewritten := 0
	for i := range out {
		if out[i].Type != mount.TypeBind {
			continue
		}
		if next, ok := rewritePath(out[i].Source, root, view); ok {
			out[i].Source = next
			rewritten++
		}
	}
	return out, rewritten
}

// RewriteBinds is RewriteMounts for Docker's string-form host binds
// ("src:dst[:opts]"). Entries whose source half is not an absolute path at
// or under root (named volumes, other host paths) pass through untouched.
func RewriteBinds(binds []string, root, view string) ([]string, int) {
	out := make([]string, len(binds))
	copy(out, binds)

	rewritten := 0
	for i, bind := range out {
		src, rest, ok := strings.Cut(bind, ":")
		if !ok {
			continue
		}
		if next, changed := rewritePath(src, root, view); changed {
			out[i] = next + ":" + rest
			rewritten++
		}
	}
	return out, rewritten
}

// rewritePath maps p onto the view when it is root itself or beneath it.
// The separator-suffixed comparison keeps a sibling that merely shares the
// root's string prefix (root "…/proj" vs sibling "…/projects") untouched.
func rewritePath(p, root, view string) (string, bool) {
	if p == root {
		return view, true
	}
	if strings.HasPrefix(p, root+string(filepath.Separator)) {
		return view + p[len(root):], true
	}
	return p, false
}

// FormatIDPair renders one from:to ID pair in the form the privileged
// helper (cmd/idmap-mount) takes on its command line.
func FormatIDPair(from, to uint32) string {
	return strconv.FormatUint(uint64(from), 10) + idPairSep + strconv.FormatUint(uint64(to), 10)
}

// ParseIDPair is the helper-side inverse of FormatIDPair, returning the
// (from, to) halves.
func ParseIDPair(s string) (uint32, uint32, error) {
	left, right, ok := strings.Cut(s, idPairSep)
	if !ok || strings.Contains(right, idPairSep) {
		return 0, 0, fmt.Errorf("idmap: %q is not a from:to ID pair", s)
	}
	f, err := strconv.ParseUint(left, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("idmap: parsing %q: %w", s, err)
	}
	t, err := strconv.ParseUint(right, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("idmap: parsing %q: %w", s, err)
	}
	return uint32(f), uint32(t), nil
}
