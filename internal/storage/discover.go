package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// registryFilename is the well-known filename for the project registry.
// Used by walk-up discovery to resolve project roots.
const registryFilename = "registry.yaml"

// discoveredFile represents a config file found during discovery.
type discoveredFile struct {
	path     string // absolute path to the file
	filename string // which filename matched (e.g., "clawker.yaml")
}

// discover finds all config files matching the options.
// Walk-up files come first (highest priority = closest to CWD),
// followed by explicit path files (lowest priority = home-level).
// Duplicate paths from overlapping discovery are removed (first wins).
func discover(opts *options) ([]discoveredFile, error) {
	var files []discoveredFile

	if opts.walkUp {
		walkUpFiles, err := walkUp(opts.filenames)
		if err != nil {
			// Walk-up failures are non-fatal — fall through to explicit paths.
			// ErrNotInProject and ErrRegistryNotFound are expected conditions.
			_ = err
		} else {
			files = append(files, walkUpFiles...)
		}
	}

	files = append(files, probeExplicitDirs(opts.paths, opts.filenames)...)

	return dedup(files), nil
}

// walkUp resolves CWD and project root from the registry, then walks from
// CWD to the project root (inclusive) probing for config files at each level.
// Returns files in CWD-first order (highest priority first).
func walkUp(filenames []string) ([]discoveredFile, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("storage: getting CWD: %w", err)
	}
	cwd = filepath.Clean(cwd)

	root, err := resolveProjectRoot(cwd)
	if err != nil {
		return nil, err
	}

	var files []discoveredFile
	dir := cwd

	for {
		files = append(files, probeDir(dir, filenames)...)

		if dir == root {
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

// ResolveProjectRoot resolves the project root for the current working directory
// by reading the project registry and finding the deepest registered root that
// contains CWD. Returns ErrRegistryNotFound if the registry does not exist,
// and ErrNotInProject if CWD is not within any registered project.
func ResolveProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("storage: getting CWD: %w", err)
	}
	cwd = filepath.Clean(cwd)
	return resolveProjectRoot(cwd)
}

// resolveProjectRoot reads the registry file and finds the registered project
// root that contains cwd. Returns ErrRegistryNotFound if the registry file
// does not exist, and ErrNotInProject if cwd is not within any registered project.
func resolveProjectRoot(cwd string) (string, error) {
	registryPath := filepath.Join(dataDir(), registryFilename)

	data, err := os.ReadFile(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrRegistryNotFound
		}
		return "", fmt.Errorf("storage: reading registry %s: %w", registryPath, err)
	}

	var registry struct {
		Projects []struct {
			Root string `yaml:"root"`
		} `yaml:"projects"`
	}
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return "", fmt.Errorf("storage: parsing registry %s: %w", registryPath, err)
	}

	// Find the deepest project root that is an ancestor of cwd.
	var bestMatch string
	for _, p := range registry.Projects {
		root := filepath.Clean(p.Root)
		if root == "" {
			continue
		}
		rel, relErr := filepath.Rel(root, cwd)
		if relErr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if len(root) > len(bestMatch) {
				bestMatch = root
			}
		}
	}

	if bestMatch == "" {
		return "", ErrNotInProject
	}
	return bestMatch, nil
}

// probeDir checks a single directory for config files using dual placement.
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
// No dual placement — just {dir}/{filename} for each configured filename.
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
