package docker

import (
	"context"
	"fmt"
	"strings"
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

// findProjectImage searches for a clawker-managed image matching the project label
// with the :latest tag. Returns the image reference (name:tag) if found,
// or empty string if not found. projectName is the resolved project identity
// (from ProjectManager); empty string means no registered project.
func (c *Client) findProjectImage(ctx context.Context, projectName string) (string, error) {
	if projectName == "" {
		return "", nil
	}

	f := c.ClawkerFilter().
		Add(filterLabel, c.cfg.LabelProject()+"="+projectName)

	result, err := c.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	for _, img := range result.Items {
		for _, tag := range img.RepoTags {
			if strings.HasSuffix(tag, ":"+latestTag) {
				return tag, nil
			}
		}
	}

	return "", nil
}

// findGlobalImage searches for the clawker-managed global image — the image
// `clawker build` produces outside any registered project, tagged ImageTag("").
// Global-scope images intentionally omit the project label, so the lookup is
// the managed filter plus a reference match on the global tag. Returns the
// image reference if found, or empty string if not found.
func (c *Client) findGlobalImage(ctx context.Context) (string, error) {
	globalRef := ImageTag("")
	f := c.ClawkerFilter().
		Add("reference", globalRef)

	result, err := c.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	for _, img := range result.Items {
		for _, tag := range img.RepoTags {
			if tag == globalRef {
				return tag, nil
			}
		}
	}

	return "", nil
}

// ResolveImageWithSource resolves the image to use for container operations.
// projectName is the resolved project identity (from ProjectManager); empty
// for global scope (no registered project).
//
// Resolution is scope-keyed:
//   - project scope: clawker-managed image matching the project label with :latest tag
//   - global scope: the clawker-managed global image (ImageTag(""))
//
// Returns nil if no built image exists for the scope — the caller decides what
// to do. There is deliberately no fallback to the configured build.image: that
// is a bare base image (no Claude Code, no clawkerd) and is never runnable as
// an agent. Scopes do not ladder: inside a project, a missing project image
// resolves to nil rather than silently running the global image.
func (c *Client) ResolveImageWithSource(ctx context.Context, projectName string) (*ResolvedImage, error) {
	if projectName == "" {
		globalImage, err := c.findGlobalImage(ctx)
		if err != nil {
			return nil, fmt.Errorf("auto-detect global image: %w", err)
		}
		if globalImage != "" {
			return &ResolvedImage{Reference: globalImage, Source: ImageSourceGlobal}, nil
		}
		return nil, nil
	}

	projectImage, err := c.findProjectImage(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("auto-detect project image: %w", err)
	}
	if projectImage != "" {
		return &ResolvedImage{Reference: projectImage, Source: ImageSourceProject}, nil
	}

	return nil, nil
}
