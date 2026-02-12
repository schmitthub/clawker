package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// EnsureVolume creates a volume if it doesn't exist, returns true if created.
func (c *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) (bool, error) {
	exists, err := c.VolumeExists(ctx, name)
	if err != nil {
		return false, err
	}

	if exists {
		logger.Debug().Str("volume", name).Msg("volume already exists")
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

	logger.Debug().Str("volume", name).Msg("created volume")
	return true, nil
}

// CopyToVolume copies a directory to a Docker volume using a temporary container.
func (c *Client) CopyToVolume(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error {
	logger.Debug().
		Str("volume", volumeName).
		Str("src", srcDir).
		Str("dest", destPath).
		Msg("copying to volume")

	// Create a tar archive of the source directory
	tarBuffer := new(bytes.Buffer)
	if err := createTarArchive(srcDir, tarBuffer, ignorePatterns); err != nil {
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
		Cmd:   []string{"chown", "-R", fmt.Sprintf("%d:%d", config.ContainerUID, config.ContainerGID), destPath},
		Labels: map[string]string{
			LabelPurpose: "copy-to-volume",
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
			logger.Warn().Err(err).Str("container", resp.ID).Msg("failed to cleanup temp container")
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

	logger.Debug().
		Str("volume", volumeName).
		Str("src", srcDir).
		Msg("copied files to volume")

	return nil
}

// createTarArchive creates a tar archive of a directory.
func createTarArchive(srcDir string, buf io.Writer, ignorePatterns []string) error {
	tw := tar.NewWriter(buf)
	defer tw.Close()

	srcDir = filepath.Clean(srcDir)

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
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
		if shouldIgnore(relPath, info.IsDir(), ignorePatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use relative path in archive
		header.Name = relPath

		// Ensure container user ownership so files are readable inside container.
		header.Uid = config.ContainerUID
		header.Gid = config.ContainerGID

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			header.Linkname = link
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content if it's a regular file
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	})
}

// shouldIgnore checks if a path should be ignored based on patterns.
func shouldIgnore(path string, isDir bool, patterns []string) bool {
	// Always ignore .git directory
	if path == ".git" || strings.HasPrefix(path, ".git/") || strings.HasPrefix(path, ".git\\") {
		return true
	}

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)

		// Skip empty lines and comments
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}

		// Handle directory-only patterns (ending with /)
		if strings.HasSuffix(pattern, "/") {
			if isDir {
				pattern = strings.TrimSuffix(pattern, "/")
				if matchPattern(path, pattern) {
					return true
				}
			}
			continue
		}

		// Handle negation patterns
		if strings.HasPrefix(pattern, "!") {
			// Negation patterns are not fully implemented
			continue
		}

		if matchPattern(path, pattern) {
			return true
		}
	}

	return false
}

// matchPattern matches a path against a gitignore-style pattern.
func matchPattern(path, pattern string) bool {
	// Convert path separators
	path = filepath.ToSlash(path)
	pattern = filepath.ToSlash(pattern)

	// Handle ** pattern
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := parts[0]
			suffix := parts[1]

			if !strings.HasPrefix(path, prefix) {
				return false
			}

			// Strip the leading "/" from suffix if present (e.g., "**/*.log" → suffix "/*.log" → "*.log")
			suffixPattern := strings.TrimPrefix(suffix, "/")

			// If the suffix contains wildcards, glob-match against the basename
			if strings.Contains(suffixPattern, "*") || strings.Contains(suffixPattern, "?") {
				matched, err := filepath.Match(suffixPattern, filepath.Base(path))
				if err != nil {
					logger.Warn().Err(err).Str("pattern", suffixPattern).Msg("invalid ignore pattern")
					return false
				}
				return matched
			}

			// Otherwise do a literal suffix check
			return strings.HasSuffix(path, suffix)
		}
	}

	// Handle * pattern
	if strings.Contains(pattern, "*") {
		matched, err := filepath.Match(pattern, filepath.Base(path))
		if err != nil {
			logger.Warn().Err(err).Str("pattern", pattern).Msg("invalid ignore pattern")
			return false
		}
		if matched {
			return true
		}
		// Also try matching the full path
		matched, err = filepath.Match(pattern, path)
		if err != nil {
			logger.Warn().Err(err).Str("pattern", pattern).Msg("invalid ignore pattern")
			return false
		}
		return matched
	}

	// Direct match
	if path == pattern {
		return true
	}

	// Check if path starts with pattern (for directories)
	if strings.HasPrefix(path, pattern+"/") {
		return true
	}

	// Check if the basename matches
	if filepath.Base(path) == pattern {
		return true
	}

	return false
}

// LoadIgnorePatterns reads patterns from an ignore file.
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

	return patterns, scanner.Err()
}
