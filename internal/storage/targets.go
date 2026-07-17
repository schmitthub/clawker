package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Source   TargetSource
	Path     string // absolute file path
	Filename string // which configured filename the target serves (e.g. "clawker.yaml")
}

// WriteTargets enumerates the store's valid write locations: the walk-up
// target when walk-up is enabled (the closest discovered walk-up layer for
// the write filename, or the CWD dual-placement candidate when none is in
// play), one candidate per configured directory, and every discovered file
// layer. Locations the store cannot rediscover (e.g. CWD for a store without
// walk-up) are never offered.
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
			targets = append(targets, WriteTarget{Source: TargetLayer, Path: l.path, Filename: l.filename})
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
		path, err := s.walkUpTargetPath(fname)
		if err != nil {
			return nil, err
		}
		out = append(out, WriteTarget{Source: TargetWalkUp, Path: path, Filename: fname})
	}
	for _, dir := range s.opts.Dirs {
		out = append(out, WriteTarget{Source: TargetDir, Path: dualPlacementPath(dir, fname), Filename: fname})
	}
	for _, dir := range s.opts.Paths {
		out = append(out, WriteTarget{Source: TargetPath, Path: filepath.Join(dir, fname), Filename: fname})
	}
	return out, nil
}

// walkUpTargetPath resolves the walk-up write target for the given filename.
// A discovered walk-up layer for the write filename IS the in-play file a
// save should land in — it is preferred over inventing a new sibling (e.g. a
// .clawker/ candidate beside an existing flat file). Layers are ordered
// closest-first, so the first match wins. Only when nothing is in play does
// the CWD dual-placement candidate (a file to be created) stand in. Caller
// must hold s.mu.
func (s *Store[T]) walkUpTargetPath(fname string) (string, error) {
	for _, l := range s.layers {
		if l.walkUp && l.filename == fname {
			return l.path, nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("storage: resolving CWD for write targets: %w", err)
	}
	return dualPlacementPath(cwd, fname), nil
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

// SiblingTarget returns the write path for filename placed beside an already
// resolved config file at existingPath, matching that file's own placement
// form: inside a .clawker/ directory the sibling is plain
// ({dir}/{filename}); as a flat root dotfile the sibling is dotted
// ({dir}/.{filename}). It derives purely from the resolved path's directory
// and dot-prefix, so the sibling always lands in the exact layer the existing
// file occupies — the same dot-form convention [dualPlacementPath] applies
// when probing a base directory, expressed for a discovered file rather than a
// bare directory. A written sibling is rediscoverable by probeDir, whose
// candidate order accepts both the plain dir form and the flat dotted form.
func SiblingTarget(existingPath, filename string) string {
	dir := filepath.Dir(existingPath)
	if strings.HasPrefix(filepath.Base(existingPath), ".") {
		return filepath.Join(dir, "."+filename)
	}
	return filepath.Join(dir, filename)
}
