package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing/format/gitignore"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/pkg/whail"
)

// EnsureVolume creates a volume if it doesn't exist, returns true if created.
func (c *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) (bool, error) {
	exists, err := c.VolumeExists(ctx, name)
	if err != nil {
		return false, err
	}

	if exists {
		c.log.Debug().Str("volume", name).Msg("volume already exists")
		return false, nil
	}

	opts := whail.VolumeCreateOptions{
		Name:   name,
		Labels: labels,
	}
	_, err = c.VolumeCreate(ctx, opts, labels)
	if err != nil {
		return false, err
	}

	c.log.Debug().Str("volume", name).Msg("created volume")
	return true, nil
}

// IsNotFound reports whether err denotes a missing — or unmanaged, hence
// invisible to whail's label-scoped inspects — Docker resource. It is the
// one benign sentinel that identity/ownership resolution collapses into a
// fallback path; every other error must surface.
//
// whail's image path collapses EVERY inspect failure into its not-found
// error shape, nesting the real cause (possibly through further whail
// wrappers) in the chain — so the outer message alone would classify a
// daemon failure as benign. Classification therefore dives to the deepest
// wrapped cause: a genuine daemon not-found or a nil cause (pure
// unmanaged/missing) is benign; any other root cause is a real failure.
func IsNotFound(err error) bool {
	if errors.Is(err, whail.ErrNotManaged) {
		return true
	}
	var dockerErr *whail.DockerError
	if !errors.As(err, &dockerErr) {
		return isNotFoundError(err)
	}
	cause := dockerErr.Unwrap()
	for cause != nil {
		var inner *whail.DockerError
		if errors.As(cause, &inner) && inner.Unwrap() != nil {
			cause = inner.Unwrap()
			continue
		}
		break
	}
	if cause != nil {
		return isNotFoundError(cause)
	}
	// No underlying cause: the whail wrapper itself is the whole story
	// (e.g. "not found" minted for an unmanaged resource).
	return isNotFoundError(err)
}

// HarnessVolumeOwnershipError is the typed refusal EnsureHarnessVolume
// returns when an existing volume at a harness-scoped name is owned — per
// its harness ownership label — by a different harness than the one asking.
// Owner is always non-empty: unlabeled occupants are adopted, not refused
// (see EnsureHarnessVolume).
type HarnessVolumeOwnershipError struct {
	// Volume is the volume name the requesting harness composed.
	Volume string
	// Owner is the occupying volume's ownership-label value.
	Owner string
	// Requested is the harness whose ensure was refused.
	Requested string
}

func (e *HarnessVolumeOwnershipError) Error() string {
	return fmt.Sprintf(
		"volume %s belongs to harness %q, not %q — refusing to reuse another harness's state; "+
			"remove it with 'clawker volume remove %s' if it is stale, or use a different agent name",
		e.Volume, e.Owner, e.Requested, e.Volume)
}

// EnsureHarnessVolume creates a harness-scoped volume if it doesn't exist,
// returning true if created. It is the ownership failsafe behind the
// harness-scoped naming scheme (HarnessVolumeName): when a volume already
// sits at the target name, its harness ownership label is checked — a volume
// labeled for a DIFFERENT harness is refused rather than adopted, so a
// naming bug or a hand-placed volume can never silently hand one harness
// another harness's state (config, plugins, the in-container login).
//
// Legitimate re-use stays silent: a volume labeled for the same harness
// (container recreation, repeated run for the same agent+harness) is
// adopted without error, as is a managed volume with no ownership label
// (hand-placed — e.g. a backup/restore; see the in-body rationale).
func (c *Client) EnsureHarnessVolume(
	ctx context.Context,
	name string,
	labels map[string]string,
	harness string,
) (bool, error) {
	inspect, err := c.VolumeInspect(ctx, name)
	switch {
	case err == nil:
		// Ownership check: an occupant labeled for a DIFFERENT harness is
		// refused. An occupant with NO ownership label is adopted: clawker
		// itself always labels harness-scoped volumes (label and naming
		// shipped together) and pre-existing flat-named volumes are
		// unreachable by HarnessVolumeName composition, so the unlabeled
		// managed population is hand-placed — above all legitimate volume
		// backup/restore (docker volume create + tar restore drops labels).
		// Refusing those would not stop deliberate placement anyway
		// (whoever can create the volume can forge the label) and Docker
		// cannot retro-label a local volume, so an adopted unlabeled volume
		// stays unlabeled and re-adopts on every ensure. The check's value
		// is against ACCIDENTAL cross-harness collisions; deliberate
		// forgery is outside any label check's reach. Volumes without the
		// managed label are invisible to whail's label-scoped inspect and
		// outside this check entirely.
		owner := inspect.Volume.Labels[consts.LabelHarness]
		if owner != "" && owner != harness {
			return false, &HarnessVolumeOwnershipError{Volume: name, Owner: owner, Requested: harness}
		}
		c.log.Debug().Str("volume", name).Str("harness", harness).Msg("volume already exists for this harness")
		return false, nil
	case !IsNotFound(err):
		return false, fmt.Errorf("inspect volume %s: %w", name, err)
	}

	return c.EnsureVolume(ctx, name, labels)
}

// CopyToVolume copies a directory to a Docker volume using a temporary container.
func (c *Client) CopyToVolume(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error {
	c.log.Debug().
		Str("volume", volumeName).
		Str("src", srcDir).
		Str("dest", destPath).
		Msg("copying to volume")

	// Create a tar archive of the source directory
	tarBuffer := new(bytes.Buffer)
	if err := createTarArchive(
		srcDir,
		tarBuffer,
		ignorePatterns,
		c.cfg.ContainerUID(),
		c.cfg.ContainerGID(),
	); err != nil {
		return fmt.Errorf("failed to create tar archive: %w", err)
	}

	// Create a temporary container with the volume mounted.
	// Cmd runs chown after CopyToContainer to fix file ownership:
	// Docker's CopyToContainer extracts tar archives as root regardless of
	// tar header UID/GID (NoLchown=true server-side). We keep correct UID/GID
	// in createTarArchive as defense-in-depth, but the chown is what actually
	// ensures the container user can read the files.
	chownImg := c.chownImage()
	containerConfig := &container.Config{
		Image: chownImg,
		Cmd:   []string{"chown", "-R", fmt.Sprintf("%d:%d", c.cfg.ContainerUID(), c.cfg.ContainerGID()), destPath},
		Labels: map[string]string{
			c.cfg.LabelPurpose(): "copy-to-volume",
		},
	}

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: volumeName,
				Target: destPath,
			},
		},
	}

	// Pull chown image if needed (uses raw check — image may be external/unmanaged)
	exists, err := c.imageExistsRaw(ctx, chownImg)
	if err != nil {
		return fmt.Errorf("checking for chown image %s: %w", chownImg, err)
	}
	if !exists {
		pullResp, err := c.ImagePull(ctx, chownImg, whail.ImagePullOptions{})
		if err != nil {
			return fmt.Errorf("failed to pull chown image %s: %w", chownImg, err)
		}
		if _, err := io.Copy(io.Discard, pullResp); err != nil {
			pullResp.Close()
			return fmt.Errorf("failed to drain image pull response: %w", err)
		}
		pullResp.Close()
	}

	// Create temporary container via whail Engine (inherits managed labels + any
	// configured labels like test labels from TestLabelConfig).
	createOpts := whail.ContainerCreateOptions{
		Config:     containerConfig,
		HostConfig: hostConfig,
		Name:       fmt.Sprintf("clawker-copy-%s", GenerateRandomName()),
	}
	resp, err := c.ContainerCreate(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create temp container: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ContainerRemove(cleanupCtx, resp.ID, true); err != nil {
			c.log.Warn().Err(err).Str("container", resp.ID).Msg("failed to cleanup temp container")
		}
	}()

	// Copy tar archive to container (works on created, not-started containers)
	_, err = c.CopyToContainer(
		ctx,
		resp.ID,
		whail.CopyToContainerOptions{
			DestinationPath: destPath,
			Content:         tarBuffer,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to copy to container: %w", err)
	}

	// Start the container to run chown, fixing file ownership for container user
	if _, err := c.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID}); err != nil {
		return fmt.Errorf("failed to start chown container: %w", err)
	}

	// Wait for chown to complete
	waitResult := c.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case result := <-waitResult.Result:
		if result.StatusCode != 0 {
			// Attempt to fetch logs for diagnostics
			logOutput := ""
			logReader, logErr := c.ContainerLogs(ctx, resp.ID, whail.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
			if logErr == nil {
				defer logReader.Close()
				var stdout, stderr bytes.Buffer
				if _, readErr := stdcopy.StdCopy(&stdout, &stderr, logReader); readErr == nil {
					combined := stdout.String() + stderr.String()
					if combined != "" {
						logOutput = combined
					}
				}
			}
			if logOutput != "" {
				return fmt.Errorf("chown failed for volume %s at %s (exit code %d): %s", volumeName, destPath, result.StatusCode, logOutput)
			}
			return fmt.Errorf("chown failed for volume %s at %s (exit code %d)", volumeName, destPath, result.StatusCode)
		}
	case err := <-waitResult.Error:
		return fmt.Errorf("failed waiting for chown container: %w", err)
	case <-ctx.Done():
		return fmt.Errorf("context cancelled waiting for chown: %w", ctx.Err())
	}

	c.log.Debug().
		Str("volume", volumeName).
		Str("src", srcDir).
		Msg("copied files to volume")

	return nil
}

// createTarArchive creates a tar archive of a directory.
func createTarArchive(srcDir string, buf io.Writer, ignorePatterns []string, uid, gid int) error {
	tw := tar.NewWriter(buf)

	srcDir = filepath.Clean(srcDir)
	ignore := compileIgnorePatterns(ignorePatterns)

	if err := filepath.Walk(srcDir, tarEntryWalker(tw, srcDir, ignore, uid, gid)); err != nil {
		// The walk error is the actionable one; close best-effort without
		// masking it — the caller discards the buffer on error.
		_ = tw.Close()
		return fmt.Errorf("walk %s: %w", srcDir, err)
	}

	// Close flushes the final entry and writes the tar trailer; a short write
	// on the last entry only surfaces here.
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	return nil
}

// tarEntryWalker returns the [filepath.WalkFunc] that writes each visited entry
// into the archive, skipping entries matched by the ignore patterns.
func tarEntryWalker(tw *tar.Writer, srcDir string, ignore gitignore.Matcher, uid, gid int) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Skip root directory
		if relPath == "." {
			return nil
		}

		// Check if should be ignored
		if ignore.Match(splitIgnorePath(relPath), info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		return writeTarEntry(tw, path, relPath, info, uid, gid)
	}
}

// writeTarEntry writes one filesystem entry (dir, file, or symlink) into the
// archive under its workspace-relative name, owned by the container user.
func writeTarEntry(tw *tar.Writer, path, relPath string, info os.FileInfo, uid, gid int) error {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("creating tar header for %s: %w", relPath, err)
	}

	// Use relative path in archive
	header.Name = relPath

	// Ensure container user ownership so files are readable inside container.
	header.Uid = uid
	header.Gid = gid

	// Handle symlinks
	if info.Mode()&os.ModeSymlink != 0 {
		link, linkErr := os.Readlink(path)
		if linkErr != nil {
			return fmt.Errorf("reading symlink %s: %w", relPath, linkErr)
		}
		header.Linkname = link
	}

	if writeErr := tw.WriteHeader(header); writeErr != nil {
		return fmt.Errorf("writing tar header for %s: %w", relPath, writeErr)
	}

	// Write file content if it's a regular file
	if !info.Mode().IsRegular() {
		return nil
	}
	file, openErr := os.Open(path)
	if openErr != nil {
		return fmt.Errorf("opening %s: %w", relPath, openErr)
	}
	defer file.Close()

	if _, copyErr := io.Copy(tw, file); copyErr != nil {
		return fmt.Errorf("copying %s into archive: %w", relPath, copyErr)
	}

	return nil
}

// compileIgnorePatterns parses raw ignore-file lines into a matcher with
// .gitignore semantics: anchoring (a leading or middle "/" pins the pattern to
// the workspace root; without one it matches at any depth), directory-only
// patterns (trailing "/"), negation ("!pattern"), and "**" globs. Blank lines
// and comments are skipped. Like git, a malformed glob never errors — it just
// doesn't match.
//
//nolint:ireturn // gitignore.NewMatcher returns the interface; no concrete type is exported.
func compileIgnorePatterns(patterns []string) gitignore.Matcher {
	ps := make([]gitignore.Pattern, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		ps = append(ps, gitignore.ParsePattern(p, nil))
	}
	return gitignore.NewMatcher(ps)
}

// splitIgnorePath converts a workspace-relative path into the component slice
// the gitignore matcher consumes.
func splitIgnorePath(rel string) []string {
	return strings.Split(filepath.ToSlash(rel), "/")
}

// LoadIgnorePatterns reads patterns from an ignore file. Lines are returned
// verbatim minus blanks and comments; matching happens in
// compileIgnorePatterns with .gitignore semantics, where a malformed glob is
// never an error — it just doesn't match.
func LoadIgnorePatterns(ignoreFile string) ([]string, error) {
	file, err := os.Open(ignoreFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return patterns, nil
}

// BindOverlayDirsFromPatterns derives directory overlay targets from ignore
// patterns for bind mode. It intentionally only returns deterministic directory
// paths and skips file-glob patterns. Candidates are re-checked against the
// full pattern list with gitignore semantics, so a later negation
// (!node_modules/) removes a candidate an earlier pattern produced.
func BindOverlayDirsFromPatterns(patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	var dirs []string

	for _, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" || strings.HasPrefix(pattern, "#") || strings.HasPrefix(pattern, "!") {
			continue
		}

		hadTrailingSlash := strings.HasSuffix(pattern, "/")
		clean := strings.TrimSuffix(pattern, "/")
		clean = strings.TrimPrefix(clean, "./")
		clean = strings.TrimPrefix(clean, "/")
		clean = filepath.ToSlash(clean)

		// "**/" matches at any depth including zero, so the workspace-root
		// instance is a deterministic overlay target: mask it even before it
		// exists on the host, or a container-created dir (e.g. npm install's
		// node_modules) writes straight through the bind mount.
		clean = strings.TrimPrefix(clean, "**/")

		if clean == "" || clean == "." {
			continue
		}

		if clean == ".git" || strings.HasPrefix(clean, ".git/") {
			continue
		}

		if strings.ContainsAny(clean, "*?[") {
			continue
		}

		base := filepath.Base(clean)
		isLikelyDirPattern := hadTrailingSlash || strings.Contains(clean, "/") || !strings.Contains(base, ".")
		if !isLikelyDirPattern {
			continue
		}

		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		dirs = append(dirs, clean)
	}

	ignore := compileIgnorePatterns(patterns)
	kept := dirs[:0]
	for _, d := range dirs {
		if ignore.Match(splitIgnorePath(d), true) {
			kept = append(kept, d)
		}
	}
	return kept
}

// FindIgnoredDirs walks hostPath and returns relative paths of directories
// matching the given ignore patterns. Used by bind mode to generate tmpfs
// overlay mounts that mask ignored directories inside the container.
//
// Matching follows .gitignore semantics (anchoring, negation, ** globs).
// Key differences from the snapshot copy path:
//   - Only returns directories (file patterns like *.log are not actionable)
//   - Force-keeps .git/ even if a pattern would match it (bind mode needs git
//     for live development); the snapshot path honors patterns verbatim,
//     including .git, and copies .git by default when no pattern excludes it
//   - Skips recursion into matched directories — their contents are masked
//     wholesale, so as in gitignore, a path under an ignored directory
//     cannot be re-included by a negation
func FindIgnoredDirs(hostPath string, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	ignore := compileIgnorePatterns(patterns)

	var dirs []string
	if err := filepath.Walk(hostPath, findIgnoredDirsWalkFunc(hostPath, ignore, &dirs)); err != nil {
		return nil, fmt.Errorf("scanning %s for ignored directories: %w", hostPath, err)
	}
	return dirs, nil
}

// findIgnoredDirsWalkFunc is FindIgnoredDirs' walk callback: it appends
// matched directories to dirs and prunes recursion into them.
func findIgnoredDirsWalkFunc(hostPath string, ignore gitignore.Matcher, dirs *[]string) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only interested in directories — files are silently skipped
		// because bind mode can only mask directories via tmpfs overlays.
		if !info.IsDir() {
			return nil
		}

		// Skip root directory itself
		rel, err := filepath.Rel(hostPath, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// Never mask .git — bind mode needs it for live development
		if isGitMetaPath(rel) {
			return filepath.SkipDir
		}

		// Check user patterns (directory-aware matching)
		if ignore.Match(splitIgnorePath(rel), true) {
			*dirs = append(*dirs, filepath.ToSlash(rel))
			return filepath.SkipDir // don't recurse into matched directories
		}

		return nil
	}
}

// isGitMetaPath reports whether rel is .git or lives under it.
func isGitMetaPath(rel string) bool {
	return rel == ".git" ||
		strings.HasPrefix(rel, ".git/") ||
		strings.HasPrefix(rel, ".git"+string(filepath.Separator))
}
