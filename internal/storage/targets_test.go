package storage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

type targetsSchema struct {
	Name string `yaml:"name"`
}

//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (c targetsSchema) Fields() storage.FieldSet { return storage.NormalizeFields(c) }

// A store without walk-up must not offer a CWD write location: a file written
// there would never be discovered on reload, silently losing the value.
func TestWriteTargets_ExplicitDirOnly_NoCWDCandidate(t *testing.T) {
	cfgDir := t.TempDir()
	t.Chdir(t.TempDir())

	store, err := storage.New[targetsSchema]("", storage.WithFilenames("settings.yaml"), storage.WithPaths(cfgDir))
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, storage.TargetPath, targets[0].Source)
	assert.Equal(t, filepath.Join(cfgDir, "settings.yaml"), targets[0].Path)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, tgt := range targets {
		assert.False(t, strings.HasPrefix(tgt.Path, cwd+string(os.PathSeparator)),
			"target %q must not live under CWD for a store without walk-up", tgt.Path)
	}
}

func TestWriteTargets_WalkUp_CWDDualPlacementFirst(t *testing.T) {
	projectDir := t.TempDir()
	cfgDir := t.TempDir()
	t.Chdir(projectDir)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	store, err := storage.New[targetsSchema]("",
		storage.WithFilenames("config.yaml"),
		storage.WithWalkUp(cwd),
		storage.WithPaths(cfgDir),
	)
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 2)
	assert.Equal(t, storage.TargetWalkUp, targets[0].Source)
	assert.Equal(t, filepath.Join(cwd, ".config.yaml"), targets[0].Path,
		"walk-up candidate uses the flat dotfile form when no %s dir exists", consts.DotClawkerDir)
	assert.Equal(t, storage.TargetPath, targets[1].Source)
	assert.Equal(t, filepath.Join(cfgDir, "config.yaml"), targets[1].Path)
}

func TestWriteTargets_WalkUp_DotClawkerDirForm(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(cwd, consts.DotClawkerDir), 0o755))

	store, err := storage.New[targetsSchema]("", storage.WithFilenames("config.yaml"), storage.WithWalkUp(cwd))
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, storage.TargetWalkUp, targets[0].Source)
	assert.Equal(t, filepath.Join(cwd, consts.DotClawkerDir, "config.yaml"), targets[0].Path)
}

// A discovered walk-up layer for the write filename IS the walk-up target —
// a .clawker/ directory appearing beside an existing flat file must not
// repoint the target to a phantom .clawker/ candidate the save would split
// config into.
func TestWriteTargets_WalkUpPrefersInPlayLayer(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	mainPath := filepath.Join(cwd, ".config.yaml")
	localPath := filepath.Join(cwd, consts.DotClawkerDir, ".config.local.yaml")
	require.NoError(t, os.WriteFile(mainPath, []byte("name: main\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0o755))
	require.NoError(t, os.WriteFile(localPath, []byte("name: local\n"), 0o644))

	store, err := storage.New[targetsSchema]("",
		storage.WithFilenames("config.local.yaml", "config.yaml"),
		storage.WithDefaultFilename("config.yaml"),
		storage.WithWalkUp(cwd),
	)
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 2)

	assert.Equal(t, storage.TargetWalkUp, targets[0].Source)
	assert.Equal(t, mainPath, targets[0].Path,
		"walk-up target must be the in-play flat file, not a %s candidate", consts.DotClawkerDir)
	assert.Equal(t, "config.yaml", targets[0].Filename)

	assert.Equal(t, storage.TargetLayer, targets[1].Source)
	assert.Equal(t, localPath, targets[1].Path)
	assert.Equal(t, "config.local.yaml", targets[1].Filename)
}

// WithDefaultFilename controls which filename new-location candidates use,
// matching defaultWritePath semantics (local override variant must not win).
func TestWriteTargets_DefaultFilenameWins(t *testing.T) {
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	store, err := storage.New[targetsSchema]("",
		storage.WithFilenames("config.local.yaml", "config.yaml"),
		storage.WithDefaultFilename("config.yaml"),
		storage.WithWalkUp(cwd),
	)
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, filepath.Join(cwd, ".config.yaml"), targets[0].Path)
}

func TestWriteTargets_DirsDualPlacement(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())

	store, err := storage.New[targetsSchema]("", storage.WithFilenames("config.yaml"), storage.WithDirs(dir))
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, storage.TargetDir, targets[0].Source)
	assert.Equal(t, filepath.Join(dir, ".config.yaml"), targets[0].Path)
}

// A discovered layer whose path matches a location candidate collapses into
// one entry (candidate source wins); distinct layer files are appended.
func TestWriteTargets_LayerDedupAndAppend(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	t.Chdir(sub)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	parent := filepath.Dir(cwd)

	// Layer at CWD (matches the walk-up candidate) and at the parent level.
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".config.yaml"), []byte("name: sub\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(parent, ".config.yaml"), []byte("name: root\n"), 0o600))

	store, err := storage.New[targetsSchema]("", storage.WithFilenames("config.yaml"), storage.WithWalkUp(parent))
	require.NoError(t, err)

	targets, err := store.WriteTargets()
	require.NoError(t, err)
	require.Len(t, targets, 2)
	assert.Equal(t, storage.TargetWalkUp, targets[0].Source)
	assert.Equal(t, filepath.Join(cwd, ".config.yaml"), targets[0].Path)
	assert.Equal(t, storage.TargetLayer, targets[1].Source)
	assert.Equal(t, filepath.Join(parent, ".config.yaml"), targets[1].Path)
}

// SiblingTarget mirrors the resolved file's placement form: plain inside a
// .clawker/ directory, dotted as a flat root file. It derives from the path
// alone, so a discovered dir-form main file yields a plain-form sibling in the
// same directory and a flat dotfile yields a dotted sibling.
func TestSiblingTarget(t *testing.T) {
	dirForm := filepath.Join("/proj", consts.DotClawkerDir, "clawker.yaml")
	assert.Equal(t,
		filepath.Join("/proj", consts.DotClawkerDir, "clawker.local.yaml"),
		storage.SiblingTarget(dirForm, "clawker.local.yaml"))

	dottedDirForm := filepath.Join("/proj", consts.DotClawkerDir, ".clawker.yaml")
	assert.Equal(t,
		filepath.Join("/proj", consts.DotClawkerDir, ".clawker.local.yaml"),
		storage.SiblingTarget(dottedDirForm, "clawker.local.yaml"))

	flatDotfile := filepath.Join("/proj", ".clawker.yaml")
	assert.Equal(t,
		filepath.Join("/proj", ".clawker.local.yaml"),
		storage.SiblingTarget(flatDotfile, "clawker.local.yaml"))
}

// A SiblingTarget path is rediscoverable: writing the derived sibling beside a
// discovered main file and reloading an identically-configured store finds it.
func TestSiblingTarget_Rediscoverable(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(cwd, consts.DotClawkerDir), 0o755))

	mainPath := filepath.Join(cwd, consts.DotClawkerDir, "config.yaml")
	require.NoError(t, os.WriteFile(mainPath, []byte("name: main\n"), 0o600))

	siblingPath := storage.SiblingTarget(mainPath, "config.local.yaml")
	require.NoError(t, os.WriteFile(siblingPath, []byte("name: local\n"), 0o600))

	store, err := storage.New[targetsSchema]("",
		storage.WithFilenames("config.local.yaml", "config.yaml"),
		storage.WithWalkUp(cwd),
	)
	require.NoError(t, err)

	found := false
	for _, l := range store.Layers() {
		if l.Path == siblingPath {
			found = true
			break
		}
	}
	assert.True(t, found, "sibling target %q was not rediscovered", siblingPath)
}

// The invariant behind the whole API: every candidate location, once written,
// must be rediscovered by an identically-configured store on reload. The
// swept store covers all source shapes: walk-up in .clawker/ dir form,
// WithDirs in flat dotfile form, and an explicit path.
func TestWriteTargets_AllTargetsRediscoverable(t *testing.T) {
	projectDir := t.TempDir()
	dualDir := t.TempDir()
	cfgDir := t.TempDir()
	t.Chdir(projectDir)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(cwd, consts.DotClawkerDir), 0o755))

	newStore := func() *storage.Store[targetsSchema] {
		s, sErr := storage.New[targetsSchema]("",
			storage.WithFilenames("config.yaml"),
			storage.WithWalkUp(cwd),
			storage.WithDirs(dualDir),
			storage.WithPaths(cfgDir),
		)
		require.NoError(t, sErr)
		return s
	}

	initial, err := newStore().WriteTargets()
	require.NoError(t, err)
	for _, tgt := range initial {
		require.NoError(t, os.MkdirAll(filepath.Dir(tgt.Path), 0o755))
		require.NoError(t, os.WriteFile(tgt.Path, []byte("name: probe\n"), 0o600))

		found := false
		for _, l := range newStore().Layers() {
			if l.Path == tgt.Path {
				found = true
				break
			}
		}
		assert.True(t, found, "write target %q (source %d) was not rediscovered", tgt.Path, tgt.Source)
		require.NoError(t, os.Remove(tgt.Path))
	}
}
