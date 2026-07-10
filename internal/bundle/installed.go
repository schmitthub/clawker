package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// InstalledBundle is a bundle discovered in the host cache. The cache is keyed
// by identity — <cacheRoot>/<namespace>/<name>/ — so a cached bundle's identity
// is its directory position, and multiple versions coexist as sibling content
// roots (<namespace>/<name>/<version>/). Cache-internal metadata files and
// staging directories (dot-prefixed) are not versions.
type InstalledBundle struct {
	ID       BundleID
	Root     string   // <cacheRoot>/<namespace>/<name>
	Versions []string // content-root subdirectory names, sorted
}

// cacheRoot resolves the installed-bundle cache directory (<data>/bundles),
// creating it if absent.
func cacheRoot() (string, error) {
	root, err := consts.BundlesSubdir()
	if err != nil {
		return "", fmt.Errorf("bundle cache dir: %w", err)
	}
	return root, nil
}

// installedBundle reads the cache entry for one identity, if present. It returns
// false (no error) when the identity is not cached — the ordinary
// declared-but-not-yet-installed condition.
func installedBundle(root string, id BundleID) (InstalledBundle, bool, error) {
	bundleDir := filepath.Join(root, id.Namespace, id.Name)
	versions, err := versionDirs(bundleDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return InstalledBundle{}, false, nil
		}
		return InstalledBundle{}, false, err
	}
	if len(versions) == 0 {
		return InstalledBundle{}, false, nil
	}
	return InstalledBundle{ID: id, Root: bundleDir, Versions: versions}, true, nil
}

// scanInstalled enumerates every cached bundle under root: each
// <namespace>/<name>/ directory with at least one version content root.
// Dot-prefixed entries (staging, metadata) are skipped at every level.
func scanInstalled(root string) ([]InstalledBundle, error) {
	nsEntries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read bundle cache %s: %w", root, err)
	}
	var installed []InstalledBundle
	for _, ns := range nsEntries {
		if !ns.IsDir() || strings.HasPrefix(ns.Name(), ".") {
			continue
		}
		nsBundles, nsErr := scanNamespace(root, ns.Name())
		if nsErr != nil {
			return nil, nsErr
		}
		installed = append(installed, nsBundles...)
	}
	sort.Slice(installed, func(i, j int) bool {
		if installed[i].ID.Namespace != installed[j].ID.Namespace {
			return installed[i].ID.Namespace < installed[j].ID.Namespace
		}
		return installed[i].ID.Name < installed[j].ID.Name
	})
	return installed, nil
}

// scanNamespace enumerates one namespace directory's cached bundles.
func scanNamespace(root, namespace string) ([]InstalledBundle, error) {
	nameEntries, err := os.ReadDir(filepath.Join(root, namespace))
	if err != nil {
		return nil, fmt.Errorf("read bundle cache %s/%s: %w", root, namespace, err)
	}
	var bundles []InstalledBundle
	for _, name := range nameEntries {
		if !name.IsDir() || strings.HasPrefix(name.Name(), ".") {
			continue
		}
		ib, ok, ibErr := installedBundle(root, BundleID{Namespace: namespace, Name: name.Name()})
		if ibErr != nil {
			return nil, ibErr
		}
		if ok {
			bundles = append(bundles, ib)
		}
	}
	return bundles, nil
}

// versionDirs lists the content-root subdirectory names of a cached bundle,
// sorted. Dot-prefixed entries (e.g. staging) and cache-metadata files (e.g.
// source.yaml) are skipped — only directories are versions.
func versionDirs(bundleDir string) ([]string, error) {
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", bundleDir, err)
	}
	var versions []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		versions = append(versions, e.Name())
	}
	sort.Strings(versions)
	return versions, nil
}

// versionRoot returns the on-disk content root for a specific version of a
// cached bundle.
func (ib InstalledBundle) versionRoot(version string) string {
	return filepath.Join(ib.Root, version)
}
