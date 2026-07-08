package bundler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BaseContentHash computes the SHA-256 freshness key for the per-project
// base image: the rendered base Dockerfile bytes, the contents of every
// file referenced by the project's copy instructions (their srcs live in
// the project build context, which is the base image's build context), and
// the effective values of any user --build-arg entries that the rendered
// base Dockerfile actually declares.
//
// Folding the base-relevant build-args in is what keeps clawker honest to
// Docker: BuildKit cache-keys an image on its arg values, so a bare
// `docker build --build-arg TZ=…` produces a different image. The base
// freshness gate skips `docker build` entirely on a hash match, so without
// this the flag would be silently eaten. Args the base does not declare
// (harness-only or unknown) stay out so they never force a spurious base
// rebuild.
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

// hashBaseBuildArgs folds the effective values of the user --build-arg
// entries that the rendered base Dockerfile declares (via ARG) into h.
// Entries the base does not declare are skipped. When no supplied arg
// targets a base-declared ARG, nothing is written and the resulting hash is
// byte-identical to the arg-free hash — an existing base image is never
// rebuilt merely because the caller passed a harness-only arg.
func hashBaseBuildArgs(h hash.Hash, baseDockerfile []byte, buildArgs map[string]*string) {
	if len(buildArgs) == 0 {
		return
	}
	declared := baseDeclaredArgNames(baseDockerfile)

	relevant := make([]string, 0, len(buildArgs))
	for name := range buildArgs {
		if _, ok := declared[name]; ok {
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

// hashCopySources feeds the contents of every copy-instruction src (files,
// directories, globs) into h, in a deterministic order.
func (g *ProjectGenerator) hashCopySources(h hash.Hash) error {
	instructions := g.cfg.Project().Build.Instructions
	if instructions == nil || len(instructions.Copy) == 0 {
		return nil
	}

	contextDir := g.GetBuildContext()

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

// hashPath hashes a single file, or every regular file under a directory,
// as "relpath\x00content" records. Symlinks and .git are skipped with the
// same rules as the build-context tar walk.
func hashPath(h hash.Hash, contextDir, path string) error {
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("hash copy src %s: %w", p, walkErr)
		}

		rel, relErr := filepath.Rel(contextDir, p)
		if relErr != nil {
			rel = p
		}
		switch hashEntryAction(rel, d) {
		case hashEntrySkip:
			return nil
		case hashEntrySkipDir:
			return filepath.SkipDir
		default:
			return hashFileRecord(h, rel, p)
		}
	})
	if err != nil {
		return fmt.Errorf("hash copy sources: %w", err)
	}
	return nil
}

// hashEntryAction verdicts for a walked copy-src entry.
const (
	hashEntryRecord = iota
	hashEntrySkip
	hashEntrySkipDir
)

// hashEntryAction prunes .git and skips symlinks/directories — the same
// rules as the build-context tar walk.
func hashEntryAction(rel string, d fs.DirEntry) int {
	if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		if d.IsDir() {
			return hashEntrySkipDir
		}
		return hashEntrySkip
	}
	if d.Type()&fs.ModeSymlink != 0 || d.IsDir() {
		return hashEntrySkip
	}
	return hashEntryRecord
}

// hashFileRecord writes one "relpath\x00content\x00" record into h.
func hashFileRecord(h hash.Hash, rel, path string) error {
	fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))
	// Read-only hashing input under a walk that already skips symlinks; a
	// race here only skews the freshness hash (spurious rebuild at worst),
	// never what gets built.
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
