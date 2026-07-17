package buildkit

import (
	"fmt"
	"path/filepath"
	"strings"

	bkclient "github.com/moby/buildkit/client"
	"github.com/tonistiigi/fsutil"

	"github.com/schmitthub/clawker/pkg/whail"
)

// toSolveOpt converts ImageBuildKitOptions to a BuildKit SolveOpt.
// Labels are passed as FrontendAttrs with the "label:" prefix.
func toSolveOpt(opts whail.ImageBuildKitOptions) (bkclient.SolveOpt, error) {
	if opts.ContextDir == "" {
		return bkclient.SolveOpt{}, fmt.Errorf("buildkit: context directory is required")
	}

	attrs := make(map[string]string)

	// Dockerfile filename. Relative paths resolve inside the context mount;
	// an absolute path gets its own "dockerfile" local mount rooted at the
	// file's directory (below), so the frontend must see just the base name.
	dockerfile := opts.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if filepath.IsAbs(dockerfile) {
		attrs["filename"] = filepath.Base(dockerfile)
	} else {
		attrs["filename"] = dockerfile
	}

	// Build args
	for k, v := range opts.BuildArgs {
		if v != nil {
			attrs["build-arg:"+k] = *v
		}
	}

	// Labels — already merged by Engine.ImageBuildKit
	for k, v := range opts.Labels {
		attrs["label:"+k] = v
	}

	// No cache
	if opts.NoCache {
		attrs["no-cache"] = ""
	}

	// Target stage
	if opts.Target != "" {
		attrs["target"] = opts.Target
	}

	// Pull policy
	if opts.Pull {
		attrs["image-resolve-mode"] = "pull"
	}

	// Network mode
	if opts.NetworkMode != "" {
		attrs["force-network-mode"] = opts.NetworkMode
	}

	// Local mounts: context and dockerfile directory
	contextDir, err := filepath.Abs(opts.ContextDir)
	if err != nil {
		return bkclient.SolveOpt{}, fmt.Errorf("buildkit: resolve context dir: %w", err)
	}

	contextFS, err := fsutil.NewFS(contextDir)
	if err != nil {
		return bkclient.SolveOpt{}, fmt.Errorf("buildkit: create context fs: %w", err)
	}

	dockerfileDir := contextDir
	if dir := filepath.Dir(dockerfile); dir != "." && filepath.IsAbs(dir) {
		dockerfileDir = dir
	}

	dockerfileFS, err := fsutil.NewFS(dockerfileDir)
	if err != nil {
		return bkclient.SolveOpt{}, fmt.Errorf("buildkit: create dockerfile fs: %w", err)
	}

	// Export entry: load built image into Docker's local image store.
	// Docker's embedded BuildKit (connected via /grpc hijack) registers a "moby"
	// exporter — the standard "image" exporter is only available in standalone
	// buildkitd. See github.com/docker/docker/builder/builder-next/exporter.
	exportAttrs := map[string]string{
		"push": "false",
	}
	if len(opts.Tags) > 0 {
		exportAttrs["name"] = strings.Join(opts.Tags, ",")
	}

	solveOpt := bkclient.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: attrs,
		LocalMounts: map[string]fsutil.FS{
			"context":    contextFS,
			"dockerfile": dockerfileFS,
		},
		Exports: []bkclient.ExportEntry{{
			Type:  "moby",
			Attrs: exportAttrs,
		}},
	}

	// Prevent cache import when NoCache is requested.
	// The "no-cache" frontend attribute only "verifies cache" rather than disabling it
	// (see moby/buildkit#2409). Setting empty CacheImports ensures no cached layers
	// are imported from previous builds.
	if opts.NoCache {
		solveOpt.CacheImports = []bkclient.CacheOptionsEntry{}
	}

	return solveOpt, nil
}
