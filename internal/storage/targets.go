package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
)

// TargetSource classifies where a candidate write location came from.
type TargetSource int

const (
	// TargetWalkUp is the CWD dual-placement candidate of a walk-up store.
	TargetWalkUp TargetSource = iota
	// TargetDir is a dual-placement candidate from a WithDirs directory.
	TargetDir
	// TargetPath is a candidate inside an explicit directory
	// (WithPaths, WithConfigDir, WithDataDir, WithStateDir, WithCacheDir).
	TargetPath
	// TargetLayer is an already-discovered file layer.
	TargetLayer
)

// WriteTarget is a candidate write location derived from the store's own
// configuration. Every target is rediscoverable: a file written to Path is
// picked up by an identically-configured store on reload. UIs offering
// "save to..." destinations must use these rather than inventing locations.
type WriteTarget struct {
	Source TargetSource
	Path   string // absolute file path
}

// WriteTargets enumerates the store's valid write locations: the CWD
// dual-placement candidate when walk-up is enabled, one candidate per
// configured directory, and every discovered file layer. Locations the store
// cannot rediscover (e.g. CWD for a store without walk-up) are never offered.
// Duplicates collapse into the first occurrence; order is walk-up, dirs,
// explicit paths, then remaining layers.
func (s *Store[T]) WriteTargets() ([]WriteTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	targets, err := s.locationCandidates()
	if err != nil {
		return nil, err
	}
	for _, l := range s.layers {
		if l.path != "" {
			targets = append(targets, WriteTarget{Source: TargetLayer, Path: l.path})
		}
	}
	return dedupTargets(targets), nil
}

// locationCandidates derives the new-file write candidates from the store's
// options: walk-up CWD dual placement, WithDirs dual placement, explicit
// paths. Caller must hold s.mu.
func (s *Store[T]) locationCandidates() ([]WriteTarget, error) {
	fname := s.opts.writeFilename()
	if fname == "" {
		return nil, nil
	}
	var out []WriteTarget
	if s.opts.WalkUpAnchor != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("storage: resolving CWD for write targets: %w", err)
		}
		out = append(out, WriteTarget{Source: TargetWalkUp, Path: dualPlacementPath(cwd, fname)})
	}
	for _, dir := range s.opts.Dirs {
		out = append(out, WriteTarget{Source: TargetDir, Path: dualPlacementPath(dir, fname)})
	}
	for _, dir := range s.opts.Paths {
		out = append(out, WriteTarget{Source: TargetPath, Path: filepath.Join(dir, fname)})
	}
	return out, nil
}

// dedupTargets removes empty and duplicate paths, preserving order
// (first occurrence wins).
func dedupTargets(targets []WriteTarget) []WriteTarget {
	seen := make(map[string]bool, len(targets))
	out := make([]WriteTarget, 0, len(targets))
	for _, t := range targets {
		if t.Path == "" || seen[t.Path] {
			continue
		}
		seen[t.Path] = true
		out = append(out, t)
	}
	return out
}

// dualPlacementPath returns the write path for a dual-placement directory:
// the dir form ({dir}/.clawker/{filename}) when the .clawker/ directory
// exists, otherwise the flat dotfile form ({dir}/.{filename}). Mirrors the
// probe order in probeDir so a written candidate is always rediscovered.
func dualPlacementPath(dir, filename string) string {
	clawkerDir := filepath.Join(dir, consts.DotClawkerDir)
	if isDir(clawkerDir) {
		return filepath.Join(clawkerDir, filename)
	}
	return filepath.Join(dir, "."+filename)
}
