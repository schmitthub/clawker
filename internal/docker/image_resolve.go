package docker

import (
	"context"
	"fmt"
)

// ImageSource indicates where an image reference was resolved from.
type ImageSource string

const (
	ImageSourceProject ImageSource = "project" // Found via project label search
	ImageSourceGlobal  ImageSource = "global"  // Globally built image (built outside any project)
)

// ResolvedImage contains the result of image resolution with source tracking.
type ResolvedImage struct {
	Reference string      // The image reference (name:tag)
	Source    ImageSource // Where the image was resolved from
}

// findProjectImage searches for a clawker-managed image matching the project
// label, preferring the harness-scheme tags: the explicit harness tag when
// wantTags carries one, else :default, else the legacy :latest. Returns the
// image reference (name:tag) if found, or empty string if not found.
// projectName is the resolved project identity (from ProjectManager); empty
// string means no registered project.
func (c *Client) findProjectImage(ctx context.Context, projectName string, wantTags []string) (string, error) {
	if projectName == "" {
		return "", nil
	}

	f := c.ClawkerFilter().
		Add("label", c.cfg.LabelProject()+"="+projectName)

	result, err := c.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	return matchPreferredTag(result.Items, wantTags), nil
}

// matchPreferredTag returns the first repo tag matching wantTags, honoring
// wantTags order as preference ranking.
func matchPreferredTag(items []ImageSummary, wantTags []string) string {
	for _, want := range wantTags {
		for _, img := range items {
			for _, tag := range img.RepoTags {
				if tag == want {
					return tag
				}
			}
		}
	}
	return ""
}

// findGlobalImage searches for the clawker-managed global image — the image
// `clawker build` produces outside any registered project. Global-scope
// images intentionally omit the project label, so the lookup is the managed
// filter plus a reference match on the global repo. Preference order matches
// findProjectImage.
func (c *Client) findGlobalImage(ctx context.Context, wantTags []string) (string, error) {
	f := c.ClawkerFilter().
		Add("reference", NamePrefix)

	result, err := c.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	return matchPreferredTag(result.Items, wantTags), nil
}

// ResolveImageWithSource resolves the image to use for container operations.
// projectName is the resolved project identity (from ProjectManager); empty
// for global scope (no registered project). harnessTag selects a specific
// harness image (the `@:tag` form); empty means the default: the :default
// alias, falling back to the legacy :latest for images built before harness
// tags existed.
//
// Returns nil if no built image exists for the scope — the caller decides what
// to do. There is deliberately no fallback to the configured build.image: that
// is a bare base image (no harness, no clawkerd) and is never runnable as
// an agent. Scopes do not ladder: inside a project, a missing project image
// resolves to nil rather than silently running the global image.
func (c *Client) ResolveImageWithSource(
	ctx context.Context,
	projectName, harnessTag string,
) (*ResolvedImage, error) {
	wantTags := []string{
		DefaultAliasImageTag(projectName),
		ImageTag(projectName), // legacy :latest fallback
	}
	if harnessTag != "" {
		wantTags = []string{HarnessImageTag(projectName, harnessTag)}
	}

	if projectName == "" {
		globalImage, err := c.findGlobalImage(ctx, wantTags)
		if err != nil {
			return nil, fmt.Errorf("auto-detect global image: %w", err)
		}
		if globalImage != "" {
			return &ResolvedImage{Reference: globalImage, Source: ImageSourceGlobal}, nil
		}
		return nil, nil
	}

	projectImage, err := c.findProjectImage(ctx, projectName, wantTags)
	if err != nil {
		return nil, fmt.Errorf("auto-detect project image: %w", err)
	}
	if projectImage != "" {
		return &ResolvedImage{Reference: projectImage, Source: ImageSourceProject}, nil
	}

	return nil, nil
}
