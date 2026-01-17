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

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/logger"
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

	opts := client.VolumeCreateOptions{
		Name:   name,
		Labels: labels,
	}
	_, err = c.VolumeCreate(ctx, opts, labels)
	if err != nil {
		return false, err
	}

	logger.Info().Str("volume", name).Msg("created volume")
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

	// Create a temporary container with the volume mounted
	containerConfig := &container.Config{
		Image: "busybox:latest",
		Cmd:   []string{"true"},
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

	// Pull busybox if needed
	exists, _ := c.ImageExists(ctx, "busybox:latest")
	if !exists {
		pullResp, err := c.APIClient.ImagePull(ctx, "busybox:latest", client.ImagePullOptions{})
		if err != nil {
			return fmt.Errorf("failed to pull busybox: %w", err)
		}
		if _, err := io.Copy(io.Discard, pullResp); err != nil {
			pullResp.Close()
			return fmt.Errorf("failed to drain image pull response: %w", err)
		}
		pullResp.Close()
	}

	// Create temporary container (bypass whail's label checks for temp container)
	createOpts := client.ContainerCreateOptions{
		Config:     containerConfig,
		HostConfig: hostConfig,
	}
	resp, err := c.APIClient.ContainerCreate(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create temp container: %w", err)
	}
	defer func() {
		// Use background context since original may be cancelled
		cleanupCtx := context.Background()
		if _, err := c.APIClient.ContainerRemove(cleanupCtx, resp.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			logger.Warn().Err(err).Str("container", resp.ID).Msg("failed to cleanup temp container")
		}
	}()

	// Copy tar archive to container
	_, err = c.APIClient.CopyToContainer(
		ctx,
		resp.ID,
		client.CopyToContainerOptions{
			DestinationPath: destPath,
			Content:         tarBuffer,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to copy to container: %w", err)
	}

	logger.Info().
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
		// Replace ** with a regex-like match
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			return strings.HasPrefix(path, parts[0]) && strings.HasSuffix(path, parts[1])
		}
	}

	// Handle * pattern
	if strings.Contains(pattern, "*") {
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if matched {
			return true
		}
		// Also try matching the full path
		matched, _ = filepath.Match(pattern, path)
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
