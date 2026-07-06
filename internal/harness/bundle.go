package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/consts"
)

// Bundle is a loaded harness bundle: manifest, template fragment, and a
// handle to the bundle directory for reading asset files.
type Bundle struct {
	// Name is the registry slug (also the image tag and label value).
	Name string
	// Manifest is the parsed harness.yaml.
	Manifest Manifest
	// Template is the raw Dockerfile.harness.tmpl content.
	Template string

	fsys fs.FS
}

// Load reads a bundle from fsys, whose root must be the bundle directory
// (containing harness.yaml and Dockerfile.harness.tmpl). Use [os.DirFS] for
// materialized user-owned bundles and a sub-FS of the embedded assets for
// shipped bundles.
func Load(name string, fsys fs.FS) (*Bundle, error) {
	rawManifest, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, fmt.Errorf("harness %q: read %s: %w", name, ManifestFile, err)
	}

	var m Manifest
	if unmarshalErr := yaml.Unmarshal(rawManifest, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("harness %q: parse %s: %w", name, ManifestFile, unmarshalErr)
	}

	if volErr := validateVolumes(name, m.Volumes); volErr != nil {
		return nil, volErr
	}
	if seedErr := validateSeeds(name, fsys, m.Volumes, m.Seeds); seedErr != nil {
		return nil, seedErr
	}
	if stagingErr := validateStaging(name, m.Volumes, m.Staging); stagingErr != nil {
		return nil, stagingErr
	}

	rawTmpl, readErr := fs.ReadFile(fsys, TemplateFile)
	if readErr != nil {
		return nil, fmt.Errorf("harness %q: read %s: %w", name, TemplateFile, readErr)
	}

	return &Bundle{
		Name:     name,
		Manifest: m,
		Template: string(rawTmpl),
		fsys:     fsys,
	}, nil
}

// validateStaging checks the staging vocabulary at the load front door so a
// UGC bundle author gets an immediate, named error instead of a silent
// create-time skip.
func validateStaging(name string, volumes []VolumeSpec, st Staging) error {
	for _, c := range st.Copy {
		if err := validateCopySpec(name, volumes, c); err != nil {
			return err
		}
	}
	for _, m := range st.Mounts {
		if err := validateMountSpec(name, volumes, m); err != nil {
			return err
		}
	}
	return nil
}

// volumeNameRe constrains a volume name to the docker-volume-safe suffix
// grammar (it is embedded verbatim in the composed volume name).
var volumeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,40}$`)

// validateVolumes checks the declared persisted-dir list: docker-safe
// unique names (infra suffixes reserved), valid unique home-relative paths.
func validateVolumes(name string, volumes []VolumeSpec) error {
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, v := range volumes {
		if err := validateVolumeSpec(name, v); err != nil {
			return err
		}
		p := NormalizeContainerPath(v.Path)
		if seenNames[v.Name] {
			return fmt.Errorf("harness %q: duplicate volume name %q", name, v.Name)
		}
		if seenPaths[p] {
			return fmt.Errorf("harness %q: duplicate volume path %q", name, v.Path)
		}
		seenNames[v.Name] = true
		seenPaths[p] = true
	}
	return nil
}

func validateVolumeSpec(name string, v VolumeSpec) error {
	if !volumeNameRe.MatchString(v.Name) {
		return fmt.Errorf("harness %q: volume name %q must match %s", name, v.Name, volumeNameRe)
	}
	if v.Name == consts.VolumePurposeHistory || v.Name == consts.VolumePurposeWorkspace {
		return fmt.Errorf("harness %q: volume name %q is reserved for clawker infrastructure", name, v.Name)
	}
	p := NormalizeContainerPath(v.Path)
	if v.Path == "" || p == "" || p == "." || !fs.ValidPath(p) {
		return fmt.Errorf(
			"harness %q: volume %q: path %q must be a container-home-relative directory",
			name, v.Name, v.Path,
		)
	}
	return nil
}

func validateCopySpec(name string, volumes []VolumeSpec, c CopySpec) error {
	if c.Src == "" || c.Dest == "" {
		return fmt.Errorf(
			"harness %q: staging copy entries require explicit src and dest (got src=%q dest=%q)",
			name, c.Src, c.Dest,
		)
	}
	if err := validateStagingDest(name, "copy", c.Src, c.Dest, volumes); err != nil {
		return err
	}
	if len(c.JSONKeys) > 0 && HasGlobMeta(c.Src) {
		return fmt.Errorf(
			"harness %q: copy %q: json_keys requires a single-file src, not a glob",
			name, c.Src,
		)
	}
	for _, rw := range c.JSONRewrites {
		switch rw.Rewrite {
		case RewritePrefixSwap, RewriteReplaceWithWorkdir:
		default:
			return fmt.Errorf(
				"harness %q: copy %q: unknown json rewrite %q (want %s or %s)",
				name, c.Src, rw.Rewrite, RewritePrefixSwap, RewriteReplaceWithWorkdir,
			)
		}
	}
	return nil
}

func validateMountSpec(name string, volumes []VolumeSpec, m MountSpec) error {
	if m.Src == "" || m.Dest == "" {
		return fmt.Errorf(
			"harness %q: staging mounts require explicit src and dest (got src=%q dest=%q)",
			name, m.Src, m.Dest,
		)
	}
	if HasGlobMeta(m.Src) {
		return fmt.Errorf("harness %q: mount src %q must be a literal path, not a glob", name, m.Src)
	}
	return validateStagingDest(name, "mount", m.Src, m.Dest, volumes)
}

// destVolume returns the declared volume whose path covers dest, if any.
func destVolume(dest string, volumes []VolumeSpec) (VolumeSpec, bool) {
	d := NormalizeContainerPath(dest)
	for _, v := range volumes {
		p := NormalizeContainerPath(v.Path)
		if d == p || strings.HasPrefix(d, p+"/") {
			return v, true
		}
	}
	return VolumeSpec{}, false
}

// validateStagingDest enforces that a directive's container dest falls
// under a declared volume — the only persistence targets. Copies land in
// volumes at create time, so a dest outside every volume is a config
// error, caught loud at the load front door.
func validateStagingDest(name, kind, id, dest string, volumes []VolumeSpec) error {
	d := NormalizeContainerPath(dest)
	if dest == "" || d == "" || d == "." || !fs.ValidPath(d) {
		return fmt.Errorf("harness %q: %s %q: dest %q must be a container-home-relative path", name, kind, id, dest)
	}
	if _, ok := destVolume(d, volumes); !ok {
		return fmt.Errorf(
			"harness %q: %s %q: dest %q is not under any declared volume path — declare the persisted dir in the volumes list",
			name,
			kind,
			id,
			dest,
		)
	}
	return nil
}

// validateSeeds checks each seed entry at the load front door: the source
// must be an existing file under the bundle's assets/ tree (which is what
// gets staged into the build context), the dest a home-relative path under
// a declared volume, and the
// apply strategy a known token.
func validateSeeds(name string, fsys fs.FS, volumes []VolumeSpec, seeds []Seed) error {
	for _, s := range seeds {
		if !fs.ValidPath(s.File) || !strings.HasPrefix(s.File, AssetsDir+"/") {
			return fmt.Errorf(
				"harness %q: seed file %q must be a path under %s/ inside the bundle",
				name,
				s.File,
				AssetsDir,
			)
		}
		if _, statErr := fs.Stat(fsys, s.File); statErr != nil {
			return fmt.Errorf("harness %q: seed file %q: %w", name, s.File, statErr)
		}
		if destErr := validateStagingDest(name, "seed", s.File, s.Dest, volumes); destErr != nil {
			return destErr
		}
		switch s.Apply {
		case SeedApplyCopyIfMissing, SeedApplyCopyIfMissingOrEmpty, SeedApplyJSONMerge:
		default:
			return fmt.Errorf(
				"harness %q: seed %q: unknown apply strategy %q (want %s, %s, or %s)",
				name, s.File, s.Apply,
				SeedApplyCopyIfMissing, SeedApplyCopyIfMissingOrEmpty, SeedApplyJSONMerge,
			)
		}
	}
	return nil
}

// WalkAssets calls fn for every file under the bundle's assets/ tree with
// its bundle-relative slash path (assets/-prefixed, matching what the
// template's COPY instructions reference) and content. A bundle without an
// assets/ directory is valid; WalkAssets is then a no-op.
func (b *Bundle) WalkAssets(fn func(relPath string, content []byte) error) error {
	err := fs.WalkDir(b.fsys, AssetsDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		content, readErr := fs.ReadFile(b.fsys, p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}
		return fn(p, content)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("harness %q: walk %s/: %w", b.Name, AssetsDir, err)
	}
	return nil
}
