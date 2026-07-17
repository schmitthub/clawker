package bundler

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// HarnessManifestFile is the manifest filename inside a harness bundle
// directory.
const HarnessManifestFile = "harness.yaml"

// HarnessTemplateFile is the Dockerfile fragment filename inside a harness
// bundle directory. Its {{define}} bodies override the master template's block
// slots.
const HarnessTemplateFile = "Dockerfile.harness.tmpl"

// AssetsDir is the bundle subdirectory holding every file the bundle
// contributes to the docker build context. The whole tree is staged
// verbatim under the same assets/ prefix; the template's COPY instructions
// and seeds[].file entries reference assets/-relative paths.
const AssetsDir = "assets"

// File modes for staged build-context files.
const (
	plainFileMode  = fs.FileMode(0o644)
	scriptFileMode = fs.FileMode(0o755)
)

// FileMode returns the on-disk mode for a bundle file written outside the
// bundle (build-context staging dirs): scripts stay executable.
func FileMode(name string) fs.FileMode {
	if filepath.Ext(name) == ".sh" {
		return scriptFileMode
	}
	return plainFileMode
}

// Bundle is a loaded harness bundle: manifest, template fragment, and a
// handle to the bundle directory for reading asset files.
type Bundle struct {
	// Name is the registry slug (also the image tag and label value).
	Name string
	// Manifest is the parsed harness.yaml.
	Manifest config.Manifest
	// Template is the raw Dockerfile.harness.tmpl content.
	Template string

	fsys fs.FS
}

// LoadBundle reads a bundle from fsys, whose root must be the bundle directory
// (containing harness.yaml and Dockerfile.harness.tmpl). Use [os.DirFS] for
// on-disk (project-registered) bundles and a sub-FS of the embedded assets
// for shipped bundles.
func LoadBundle(name string, fsys fs.FS) (*Bundle, error) {
	rawManifest, err := fs.ReadFile(fsys, HarnessManifestFile)
	if err != nil {
		return nil, fmt.Errorf("harness %q: read %s: %w", name, HarnessManifestFile, err)
	}

	var m config.Manifest
	if unmarshalErr := yaml.Unmarshal(rawManifest, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("harness %q: parse %s: %w", name, HarnessManifestFile, unmarshalErr)
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
	if tcErr := validateStackDecls(name, m.Stacks); tcErr != nil {
		return nil, tcErr
	}
	if egressErr := validateEgressFloor(name, m.Egress); egressErr != nil {
		return nil, egressErr
	}
	if mpErr := validateManagedPrompt(name, m.Volumes, m.ManagedPrompt); mpErr != nil {
		return nil, mpErr
	}

	rawTmpl, readErr := fs.ReadFile(fsys, HarnessTemplateFile)
	if readErr != nil {
		return nil, fmt.Errorf("harness %q: read %s: %w", name, HarnessTemplateFile, readErr)
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
func validateStaging(name string, volumes []config.VolumeSpec, st config.Staging) error {
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

// validateStackDecls checks a harness manifest's stacks: dependency list at the
// load front door: every entry is a valid possibly-qualified stack address and
// no address repeats. Whether each address resolves to a definition is a
// generation-time concern handled by the single resolution algorithm
// (bundle.Resolver) — a bare name hits the loose/floor tiers, a qualified
// namespace.bundle.component address hits the installed bundle set (a bundled
// harness references its shipped sibling stack by its qualified self-address).
func validateStackDecls(name string, decls []string) error {
	seen := map[string]bool{}
	for _, dep := range decls {
		if err := ValidateStackName(dep); err != nil {
			return fmt.Errorf("harness %q: %w", name, err)
		}
		if seen[dep] {
			return fmt.Errorf("harness %q: duplicate stack declaration %q", name, dep)
		}
		seen[dep] = true
	}
	return nil
}

// validateEgressFloor rejects a harness floor rule that tries to weaken upstream
// TLS verification. A harness bundle is third-party-authored content and shares
// the project egress rule schema, so a manifest can now spell the
// insecure_skip_tls_verify field — but that knob is reserved for the machine
// owner's own project security.firewall.rules. A floor may widen which
// destinations are reachable; it may never lower the TLS trust bar on reaching
// them. Setting it is a hard load error at the register and build front doors
// (both load through here).
func validateEgressFloor(name string, rules []config.EgressRule) error {
	for _, r := range rules {
		if r.InsecureSkipTLSVerify {
			return fmt.Errorf(
				"harness %q: egress floor rule %q must not set insecure_skip_tls_verify — "+
					"that knob is reserved for a project's own security.firewall.rules; "+
					"a bundle floor may not lower the TLS trust bar",
				name, r.Dst,
			)
		}
	}
	return nil
}

// validateManagedPrompt checks the managed_prompt block at the load front
// door. Absent is valid — it means the harness has no managed-context
// location. When present, dest must be an absolute container path outside
// every declared volume (the copy is baked at build time; a volume mount
// would shadow it), and owner/mode must come from their closed vocabularies.
func validateManagedPrompt(name string, volumes []config.VolumeSpec, mp *config.ManagedPromptSpec) error {
	if mp == nil {
		return nil
	}
	if mp.Dest == "" {
		return fmt.Errorf("harness %q: managed_prompt.dest is required", name)
	}
	if !path.IsAbs(mp.Dest) {
		return fmt.Errorf("harness %q: managed_prompt.dest %q must be an absolute container path", name, mp.Dest)
	}
	for _, v := range volumes {
		volPath := path.Join("/home", DefaultUsername, v.Path)
		if mp.Dest == volPath || strings.HasPrefix(mp.Dest, volPath+"/") {
			return fmt.Errorf(
				"harness %q: managed_prompt.dest %q falls under declared volume %q (%s) — "+
					"the managed prompt is baked at build time and a volume mount would shadow it",
				name, mp.Dest, v.Name, volPath,
			)
		}
	}
	switch mp.Owner {
	case "", config.PromptOwnerRoot, config.PromptOwnerUser:
	default:
		return fmt.Errorf(
			"harness %q: managed_prompt.owner %q must be %q or %q",
			name, mp.Owner, config.PromptOwnerRoot, config.PromptOwnerUser,
		)
	}
	if mp.Mode != "" {
		v, err := strconv.ParseUint(mp.Mode, 8, 32)
		if err != nil || v > 0o7777 {
			return fmt.Errorf(
				"harness %q: managed_prompt.mode %q must be an octal permission value like 0644",
				name,
				mp.Mode,
			)
		}
	}
	return nil
}

// validateVolumes checks the declared persisted-dir list: unified-rule
// unique names (infra suffixes reserved), valid unique home-relative paths.
func validateVolumes(name string, volumes []config.VolumeSpec) error {
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, v := range volumes {
		if err := validateVolumeSpec(name, v); err != nil {
			return err
		}
		p := config.NormalizeContainerPath(v.Path)
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

func validateVolumeSpec(name string, v config.VolumeSpec) error {
	// The unified naming rule (lowercase kebab, dot-free) — not a looser
	// docker-charset grammar — because the name is embedded verbatim in the
	// composed clawker.<project>.<agent>-<harness>.<name> volume identity: a
	// dotted volume name could alias a qualified harness's volume (bare
	// harness "a" + volume "b.c.d" vs qualified harness "a.b.c" + volume
	// "d"). Dot-free names keep that composition injective.
	if err := consts.ValidateName(v.Name); err != nil {
		return fmt.Errorf("harness %q: volume name: %w", name, err)
	}
	switch v.Name {
	case consts.VolumePurposeHistory, consts.VolumePurposeWorkspace, consts.VolumePurposeClawker:
		return fmt.Errorf("harness %q: volume name %q is reserved for clawker infrastructure", name, v.Name)
	}
	p := config.NormalizeContainerPath(v.Path)
	if v.Path == "" || p == "" || p == "." || !fs.ValidPath(p) {
		return fmt.Errorf(
			"harness %q: volume %q: path %q must be a container-home-relative directory",
			name, v.Name, v.Path,
		)
	}
	return nil
}

func validateCopySpec(name string, volumes []config.VolumeSpec, c config.CopySpec) error {
	if c.Src == "" || c.Dest == "" {
		return fmt.Errorf(
			"harness %q: staging copy entries require explicit src and dest (got src=%q dest=%q)",
			name, c.Src, c.Dest,
		)
	}
	if err := validateStagingDest(name, "copy", c.Src, c.Dest, volumes); err != nil {
		return err
	}
	if len(c.JSONKeys) > 0 && config.HasGlobMeta(c.Src) {
		return fmt.Errorf(
			"harness %q: copy %q: json_keys requires a single-file src, not a glob",
			name, c.Src,
		)
	}
	for _, rw := range c.JSONRewrites {
		switch rw.Rewrite {
		case config.RewritePrefixSwap, config.RewriteReplaceWithWorkdir:
		default:
			return fmt.Errorf(
				"harness %q: copy %q: unknown json rewrite %q (want %s or %s)",
				name, c.Src, rw.Rewrite, config.RewritePrefixSwap, config.RewriteReplaceWithWorkdir,
			)
		}
	}
	return nil
}

func validateMountSpec(name string, volumes []config.VolumeSpec, m config.MountSpec) error {
	if m.Src == "" || m.Dest == "" {
		return fmt.Errorf(
			"harness %q: staging mounts require explicit src and dest (got src=%q dest=%q)",
			name, m.Src, m.Dest,
		)
	}
	if config.HasGlobMeta(m.Src) {
		return fmt.Errorf("harness %q: mount src %q must be a literal path, not a glob", name, m.Src)
	}
	return validateStagingDest(name, "mount", m.Src, m.Dest, volumes)
}

// destVolume returns the declared volume whose path covers dest, if any.
func destVolume(dest string, volumes []config.VolumeSpec) (config.VolumeSpec, bool) {
	d := config.NormalizeContainerPath(dest)
	for _, v := range volumes {
		p := config.NormalizeContainerPath(v.Path)
		if d == p || strings.HasPrefix(d, p+"/") {
			return v, true
		}
	}
	return config.VolumeSpec{}, false
}

// validateStagingDest enforces that a directive's container dest falls
// under a declared volume — the only persistence targets. Copies land in
// volumes at create time, so a dest outside every volume is a config
// error, caught loud at the load front door.
func validateStagingDest(name, kind, id, dest string, volumes []config.VolumeSpec) error {
	d := config.NormalizeContainerPath(dest)
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
func validateSeeds(name string, fsys fs.FS, volumes []config.VolumeSpec, seeds []config.Seed) error {
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
		case config.SeedApplyCopyIfMissing, config.SeedApplyCopyIfMissingOrEmpty, config.SeedApplyJSONMerge:
		default:
			return fmt.Errorf(
				"harness %q: seed %q: unknown apply strategy %q (want %s, %s, or %s)",
				name, s.File, s.Apply,
				config.SeedApplyCopyIfMissing, config.SeedApplyCopyIfMissingOrEmpty, config.SeedApplyJSONMerge,
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
