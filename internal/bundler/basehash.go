package bundler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// BaseContentHash computes the SHA-256 freshness key for the per-project
// base image: the rendered base Dockerfile bytes; the contents, permission
// bits, and symlink targets of everything referenced by the project's copy
// instructions (their srcs live in the project build context, which is the
// base image's build context); the project's .dockerignore, which gates what
// those COPY steps can see; and the effective values of any user --build-arg
// entries the base build would honor — args the rendered base Dockerfile
// declares via ARG, plus Docker's predefined proxy args, which the builders
// honor with no declaration at all.
//
// Folding the base-relevant build-args in is what keeps clawker honest to
// Docker: a bare `docker build --build-arg TZ=…` — or `--build-arg
// HTTPS_PROXY=…`, declared nowhere — changes what the build produces. The
// base freshness gate skips `docker build` entirely on a hash match, so
// without this the flag would be silently eaten. Args the base neither
// declares nor Docker predefines (harness-only or unknown) stay out so they
// never force a spurious base rebuild.
//
// Deliberately NOT a hash of the whole context directory — that would
// rebuild the base on every source edit. Glob expansion here is Go's
// [filepath.Glob], which is not an exact match for Docker's COPY pattern
// semantics; the imprecision can only cause a spurious base rebuild or a
// stale-skip whose COPY layers Docker itself still cache-validates — never
// a wrong image.
func (g *ProjectGenerator) BaseContentHash(baseDockerfile []byte, buildArgs map[string]*string) (string, error) {
	h := sha256.New()
	h.Write(baseDockerfile)

	if err := g.hashCopySources(h); err != nil {
		return "", err
	}

	hashBaseBuildArgs(h, baseDockerfile, buildArgs)

	return hex.EncodeToString(h.Sum(nil)), nil
}

// isDockerPredefinedArg reports whether name is one of Docker's predefined
// build args — the proxy variables (upper- and lowercase forms) that both
// the classic builder and BuildKit honor WITHOUT an ARG declaration in the
// Dockerfile. They set the network environment of every RUN step, so a
// --build-arg targeting one changes what the base build produces exactly
// like a declared ARG does and must count as base-relevant. The vocabulary
// is Docker's, not clawker's: the set mirrors the predefined-ARG list in the
// Dockerfile reference (moby's builtinAllowedBuildArgs), which is fixed by
// the builders.
func isDockerPredefinedArg(name string) bool {
	switch name {
	case "HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"FTP_PROXY", "ftp_proxy",
		"NO_PROXY", "no_proxy",
		"ALL_PROXY", "all_proxy":
		return true
	default:
		return false
	}
}

// hashBaseBuildArgs folds the effective values of the user --build-arg
// entries the base build honors into h: args the rendered base Dockerfile
// declares (via ARG) plus Docker's predefined proxy args, which need no
// declaration. Every other entry is skipped. When no supplied arg targets an
// honored name, nothing is written and the resulting hash is byte-identical
// to the arg-free hash — an existing base image is never rebuilt merely
// because the caller passed a harness-only arg.
func hashBaseBuildArgs(h hash.Hash, baseDockerfile []byte, buildArgs map[string]*string) {
	if len(buildArgs) == 0 {
		return
	}
	declared := baseDeclaredArgNames(baseDockerfile)

	relevant := make([]string, 0, len(buildArgs))
	for name := range buildArgs {
		_, isDeclared := declared[name]
		if isDeclared || isDockerPredefinedArg(name) {
			relevant = append(relevant, name)
		}
	}
	if len(relevant) == 0 {
		return
	}
	sort.Strings(relevant)

	for _, name := range relevant {
		var effective string
		if value := buildArgs[name]; value != nil {
			effective = *value
		} else {
			// A nil value is `--build-arg NAME` with no `=value`; Docker takes
			// the value from the client environment, so that is the effective
			// value the build would see.
			effective = os.Getenv(name)
		}
		fmt.Fprintf(h, "arg:%s=%s\x00", name, effective)
	}
}

// baseDeclaredArgNames returns the set of ARG names declared anywhere in the
// rendered base Dockerfile. Every stage's ARGs are collected: a --build-arg
// targeting an ARG in any stage can change what the base image builds, so all
// declarations count. ARG names are case-sensitive (BuildKit keys on the
// value under the exact name); the instruction keyword is not, matching
// Dockerfile parsing.
//
// This is a line-oriented parser over clawker's OWN rendered base template (a
// controlled input space), not arbitrary user Dockerfiles. Backslash line
// continuations are joined first so a wrapped `ARG \<newline>NAME` is seen as
// one instruction; heredoc bodies are not special-cased (no rendered template
// puts an ARG-shaped line in one). A misparse is fail-safe by the hash's own
// contract — at worst a spurious rebuild, never a wrong image.
func baseDeclaredArgNames(baseDockerfile []byte) map[string]struct{} {
	joined := strings.ReplaceAll(string(baseDockerfile), "\\\n", " ")
	names := make(map[string]struct{})
	for line := range strings.SplitSeq(joined, "\n") {
		if name, ok := argInstructionName(line); ok {
			names[name] = struct{}{}
		}
	}
	return names
}

// argInstructionName extracts the variable name from a single Dockerfile line
// when it is an ARG instruction, else reports false. Handles `ARG NAME`,
// `ARG NAME=default`, and `ARG NAME="default"` (quoted defaults may contain
// spaces — only the name, which never does, is returned). The keyword match
// is case-insensitive; the returned name is verbatim.
func argInstructionName(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "ARG") {
		return "", false
	}
	name := fields[1]
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// dockerignoreFileName is the ignore file Docker loads from the root of the
// build context; the name is Docker's vocabulary, not clawker's.
const dockerignoreFileName = ".dockerignore"

// hashCopySources feeds the contents of every copy-instruction src (files,
// directories, globs) into h, in a deterministic order, together with the
// context's .dockerignore. With no copy instructions nothing reaches the
// image from the build context, so nothing is hashed — including the ignore
// file — and the hash stays byte-identical to the Dockerfile-only hash.
func (g *ProjectGenerator) hashCopySources(h hash.Hash) error {
	instructions := g.cfg.Project().Build.Instructions
	if instructions == nil || len(instructions.Copy) == 0 {
		return nil
	}

	contextDir := g.GetBuildContext()

	if err := hashDockerignore(h, contextDir); err != nil {
		return err
	}

	srcs := make([]string, 0, len(instructions.Copy))
	for _, c := range instructions.Copy {
		srcs = append(srcs, c.Src)
	}
	sort.Strings(srcs)

	for _, src := range srcs {
		if err := hashCopySrc(h, contextDir, src); err != nil {
			return err
		}
	}

	return nil
}

// hashDockerignore folds the build context's .dockerignore content into h.
// The BuildKit base build hands Docker the project dir as the local context
// and .dockerignore filters what the COPY steps can see, so an ignore edit
// changes the built image without touching a single copy-src byte on disk —
// notably an edit made specifically to purge a file already baked into the
// base. An absent file writes nothing (distinct from an empty one, which
// writes an empty record).
func hashDockerignore(h hash.Hash, contextDir string) error {
	content, err := os.ReadFile(filepath.Join(contextDir, dockerignoreFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("hash %s: %w", dockerignoreFileName, err)
	}
	fmt.Fprintf(h, "dockerignore:%s\x00", content)
	return nil
}

// hashCopySrc expands one copy src (glob or literal path, relative to
// contextDir) and hashes every match. A missing src hashes a stable
// marker so its later appearance flips the hash; the build itself
// surfaces the missing file.
func hashCopySrc(h hash.Hash, contextDir, src string) error {
	resolved := src
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(contextDir, resolved)
	}

	matches, err := filepath.Glob(resolved)
	if err != nil {
		return fmt.Errorf("expand copy src %q: %w", src, err)
	}
	if len(matches) == 0 {
		fmt.Fprintf(h, "missing:%s\x00", src)
		return nil
	}
	sort.Strings(matches)

	for _, match := range matches {
		if hashErr := hashPath(h, contextDir, match); hashErr != nil {
			return hashErr
		}
	}
	return nil
}

// hashPath hashes a single glob match: a file, every regular file under a
// directory, or — when the match itself is a symlink — the link's target
// string plus its dereferenced content. The build reads through a src-root
// symlink, so the target's bytes are what lands in the image; both editing
// the target and repointing the link must flip the hash.
func hashPath(h hash.Hash, contextDir, path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("hash copy src %s: %w", path, err)
	}
	root := path
	if fi.Mode()&fs.ModeSymlink != 0 {
		if linkErr := hashLinkRecord(h, contextRel(contextDir, path), path); linkErr != nil {
			return linkErr
		}
		root = resolveLinkRoot(h, contextDir, path)
		if root == "" {
			return nil
		}
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("hash copy src %s: %w", p, walkErr)
		}

		rel := contextRel(contextDir, p)
		switch hashEntryAction(rel, d) {
		case hashEntrySkip:
			return nil
		case hashEntrySkipDir:
			return filepath.SkipDir
		case hashEntryLink:
			return hashLinkRecord(h, rel, p)
		case hashEntryDir:
			return hashDirRecord(h, rel, p)
		default:
			return hashFileRecord(h, rel, p)
		}
	})
	if err != nil {
		return fmt.Errorf("hash copy sources: %w", err)
	}
	return nil
}

// contextRel returns p relative to contextDir for use as a stable record
// key; when p cannot be relativized it falls back to p itself.
func contextRel(contextDir, p string) string {
	rel, err := filepath.Rel(contextDir, p)
	if err != nil {
		return p
	}
	return rel
}

// resolveLinkRoot dereferences a symlink copy src and returns the walk root.
// An unresolvable link — dangling, looping, or with an unreadable
// intermediate — returns "" after hashing a stable marker: the build cannot
// read through such a link either, so there is no content to hash, and the
// freshness gate must never abort on it (its contract is a spurious rebuild
// at worst, never a blocked build — the Docker build surfaces the real,
// actionable error). The caller's link record still flips the hash on
// repoint, and the link later resolving adds content records.
func resolveLinkRoot(h hash.Hash, contextDir, path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Every resolution failure is an expected input state handled by the
		// marker, not a gate failure; the error itself carries no hashable
		// signal (its text may vary by platform).
		fmt.Fprintf(h, "missing:%s\x00", contextRel(contextDir, path))
		return ""
	}
	return resolved
}

// hashEntryAction verdicts for a walked copy-src entry.
const (
	hashEntryRecord = iota
	hashEntrySkip
	hashEntrySkipDir
	hashEntryLink
	hashEntryDir
)

// hashEntryAction prunes .git wherever it appears in the walked path (a
// dereferenced symlink root can sit outside the context — even in a sibling
// checkout — so a positional prefix test would miss its .git and let every
// commit or fetch there defeat the base cache), records directories for a
// mode record, and marks symlinks for a link record — a symlink inside a
// copied directory transfers as a symlink, so its target string is image
// content.
func hashEntryAction(rel string, d fs.DirEntry) int {
	if hasGitComponent(rel) {
		if d.IsDir() {
			return hashEntrySkipDir
		}
		return hashEntrySkip
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return hashEntryLink
	}
	if d.IsDir() {
		return hashEntryDir
	}
	return hashEntryRecord
}

// hasGitComponent reports whether any path component of rel is .git —
// git state is never a freshness input, at any depth.
func hasGitComponent(rel string) bool {
	return slices.Contains(strings.Split(rel, string(filepath.Separator)), ".git")
}

// hashDirRecord writes one "dir:relpath\x00mode\x00" record into h. A
// directory's permission bits are image content just like a file's: Docker
// COPY preserves the source directory's mode (and creates empty ones), so a
// mode-only chmod of the directory is a different image.
func hashDirRecord(h hash.Hash, rel, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("hash copy src %s: %w", path, err)
	}
	fmt.Fprintf(h, "dir:%s\x00%04o\x00", filepath.ToSlash(rel), fi.Mode().Perm())
	return nil
}

// hashLinkRecord writes one "link:relpath\x00target\x00" record into h so a
// repointed symlink flips the hash even when its old and new targets hold
// identical bytes.
func hashLinkRecord(h hash.Hash, rel, path string) error {
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("hash copy src %s: %w", path, err)
	}
	fmt.Fprintf(h, "link:%s\x00%s\x00", filepath.ToSlash(rel), filepath.ToSlash(target))
	return nil
}

// hashFileRecord writes one "relpath\x00mode\x00content\x00" record into h.
// The permission bits are image content: with no chmod declared on the copy
// instruction Docker COPY preserves the source file's mode, and BuildKit's
// own COPY cache key includes it — a mode-only change is a different image.
func hashFileRecord(h hash.Hash, rel, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("hash copy src %s: %w", path, err)
	}
	fmt.Fprintf(h, "%s\x00%04o\x00", filepath.ToSlash(rel), fi.Mode().Perm())
	// Read-only hashing input; a stat/open race here only skews the
	// freshness hash (spurious rebuild at worst), never what gets built.
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hash copy src %s: %w", path, err)
	}
	defer f.Close()
	if _, copyErr := io.Copy(h, f); copyErr != nil {
		return fmt.Errorf("hash copy src %s: %w", path, copyErr)
	}
	fmt.Fprint(h, "\x00")
	return nil
}
