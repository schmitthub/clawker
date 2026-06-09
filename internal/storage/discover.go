package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// discoveredFile represents a file found during discovery.
type discoveredFile struct {
	path     string // absolute path to the file
	filename string // which filename matched (e.g., "clawker.yaml")
}

// discover finds all files matching the options.
// Walk-up files come first (highest priority = closest to CWD), followed
// by dir-probe files (WithDirs), then explicit path files (lowest priority).
// Duplicate paths from overlapping discovery are removed (first wins).
func discover(opts *options) ([]discoveredFile, error) {
	var files []discoveredFile

	if opts.walkUpAnchor != "" {
		walkUpFiles, err := walkUp(opts.filenames, opts.walkUpAnchor)
		if err != nil {
			// Walk-up failures are non-fatal — fall through to explicit paths.
			_ = err
		} else {
			files = append(files, walkUpFiles...)
		}
	}

	for _, dir := range opts.dirs {
		files = append(files, probeDir(dir, opts.filenames)...)
	}

	files = append(files, probeExplicitDirs(opts.paths, opts.filenames)...)

	return dedup(files), nil
}

// walkUp walks from CWD up to anchor (inclusive), probing for matching files at
// each level. Returns files in CWD-first order (highest priority first). anchor
// is a plain directory supplied by the caller — storage holds no knowledge of
// how it was chosen.
//
// anchor must be CWD or an ancestor of it. If it is not (a garbage path, or a
// real directory elsewhere on the filesystem), an upward walk would never reach
// it and would escape to the filesystem root, pulling in stray files above the
// intended bound. In that case walk-up is skipped (no files) rather than
// allowed to escape.
func walkUp(filenames []string, anchor string) ([]discoveredFile, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("storage: getting CWD: %w", err)
	}
	cwd = filepath.Clean(cwd)
	anchor = filepath.Clean(anchor)

	rel, relErr := filepath.Rel(anchor, cwd)
	// An anchor that can't be related to CWD (e.g. a different volume) or that
	// sits below/beside it is not an ancestor — skip walk-up rather than escape
	// upward. A nil error here is intentional: this is "no walk-up", not a failure.
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil //nolint:nilerr // un-relatable anchor → skip walk-up, not an error
	}

	var files []discoveredFile
	dir := cwd

	for {
		files = append(files, probeDir(dir, filenames)...)

		if dir == anchor {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // filesystem root reached
		}
		dir = parent
	}

	return files, nil
}

// probeDir checks a single directory for matching files using dual placement.
// If .clawker/ directory exists, checks .clawker/{filename} (dir form).
// Otherwise, checks .{filename} (flat dotfile form).
// Both .yaml and .yml extensions are accepted.
func probeDir(dir string, filenames []string) []discoveredFile {
	var files []discoveredFile

	clawkerDir := filepath.Join(dir, ".clawker")
	hasDirForm := isDir(clawkerDir)

	for _, fname := range filenames {
		if hasDirForm {
			// Dir form: .clawker/{filename}
			for _, ext := range yamlExtensions(fname) {
				path := filepath.Join(clawkerDir, ext)
				if isFile(path) {
					files = append(files, discoveredFile{path: path, filename: fname})
					break // first extension match wins
				}
			}
		} else {
			// Flat form: .{filename}
			for _, ext := range yamlExtensions("." + fname) {
				path := filepath.Join(dir, ext)
				if isFile(path) {
					files = append(files, discoveredFile{path: path, filename: fname})
					break
				}
			}
		}
	}

	return files
}

// probeExplicitDirs checks explicit directories for files.
// No dual placement — just {dir}/{filename} for each requested filename.
func probeExplicitDirs(dirs []string, filenames []string) []discoveredFile {
	var files []discoveredFile
	for _, dir := range dirs {
		for _, fname := range filenames {
			for _, ext := range yamlExtensions(fname) {
				path := filepath.Join(dir, ext)
				if isFile(path) {
					files = append(files, discoveredFile{path: path, filename: fname})
					break // first extension match wins
				}
			}
		}
	}
	return files
}

// yamlExtensions returns the .yaml and .yml variants of a filename.
// If the filename already ends in .yaml or .yml, returns the original
// and the alternate extension. Otherwise appends both extensions.
func yamlExtensions(name string) []string {
	switch {
	case strings.HasSuffix(name, ".yaml"):
		base := strings.TrimSuffix(name, ".yaml")
		return []string{name, base + ".yml"}
	case strings.HasSuffix(name, ".yml"):
		base := strings.TrimSuffix(name, ".yml")
		return []string{name, base + ".yaml"}
	default:
		return []string{name + ".yaml", name + ".yml"}
	}
}

// dedup removes entries with duplicate paths, preserving order (first wins).
// Handles overlap between walk-up and explicit path discovery resolving
// to the same file.
func dedup(files []discoveredFile) []discoveredFile {
	seen := make(map[string]bool, len(files))
	result := make([]discoveredFile, 0, len(files))
	for _, f := range files {
		if !seen[f.path] {
			seen[f.path] = true
			result = append(result, f)
		}
	}
	return result
}

// isDir returns true if path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isFile returns true if path exists and is a regular file.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
