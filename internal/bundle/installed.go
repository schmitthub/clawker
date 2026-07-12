package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// InstalledEntry is one cache entry discovered in the host cache. The cache is
// value-keyed under identity levels for browsability —
// <cacheRoot>/<namespace>/<name>/<sourceKey>/ — where sourceKey is the digest
// of the declared source value ([Source.Key]) and the directory is the content
// root. Two declarations differing in any part of their value (url form, ref,
// sha, subdir) occupy sibling entries; duplicated content across keys is
// accepted.
type InstalledEntry struct {
	ID   BundleID
	Key  string // source-digest directory name
	Root string // <cacheRoot>/<namespace>/<name>/<key>
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

// scanInstalled enumerates every cache entry under root:
// <namespace>/<name>/<sourceKey>/ directories. Dot-prefixed entries (staging,
// receipts) are skipped at every level.
func scanInstalled(root string) ([]InstalledEntry, error) {
	nsEntries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read bundle cache %s: %w", root, err)
	}
	var installed []InstalledEntry
	for _, ns := range nsEntries {
		if !ns.IsDir() || strings.HasPrefix(ns.Name(), ".") {
			continue
		}
		nsEntries, nsErr := scanNamespace(root, ns.Name())
		if nsErr != nil {
			return nil, nsErr
		}
		installed = append(installed, nsEntries...)
	}
	sort.Slice(installed, func(i, j int) bool {
		if installed[i].ID.String() != installed[j].ID.String() {
			return installed[i].ID.String() < installed[j].ID.String()
		}
		return installed[i].Key < installed[j].Key
	})
	return installed, nil
}

// scanNamespace enumerates one namespace directory's cache entries.
func scanNamespace(root, namespace string) ([]InstalledEntry, error) {
	nameEntries, err := os.ReadDir(filepath.Join(root, namespace))
	if err != nil {
		return nil, fmt.Errorf("read bundle cache %s/%s: %w", root, namespace, err)
	}
	var entries []InstalledEntry
	for _, name := range nameEntries {
		if !name.IsDir() || strings.HasPrefix(name.Name(), ".") {
			continue
		}
		nameDir, nameErr := scanBundleName(root, namespace, name.Name())
		if nameErr != nil {
			return nil, nameErr
		}
		entries = append(entries, nameDir...)
	}
	return entries, nil
}

// scanBundleName enumerates one identity directory's value-keyed entries.
func scanBundleName(root, namespace, name string) ([]InstalledEntry, error) {
	keyEntries, err := os.ReadDir(filepath.Join(root, namespace, name))
	if err != nil {
		return nil, fmt.Errorf("read bundle cache %s/%s/%s: %w", root, namespace, name, err)
	}
	var entries []InstalledEntry
	for _, key := range keyEntries {
		if !key.IsDir() || strings.HasPrefix(key.Name(), ".") {
			continue
		}
		entries = append(entries, InstalledEntry{
			ID:   BundleID{Namespace: namespace, Name: name},
			Key:  key.Name(),
			Root: filepath.Join(root, namespace, name, key.Name()),
		})
	}
	return entries, nil
}

// cachedKeys scans the cache once and returns the set of present source keys,
// so a batch of declarations can be tested for cache presence by exact value.
func cachedKeys() (map[string]bool, error) {
	root, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	installed, err := scanInstalled(root)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(installed))
	for _, e := range installed {
		keys[e.Key] = true
	}
	return keys, nil
}
