package docker

import (
	"context"
	"fmt"
	"strings"
)

// ImageSource indicates where an image reference was resolved from.
type ImageSource string

const (
	ImageSourceExplicit ImageSource = "explicit" // User specified via CLI or args
	ImageSourceProject  ImageSource = "project"  // Found via project label search
	ImageSourceConfig   ImageSource = "config"   // From merged config (build.image)
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
		Add("label", c.cfg.LabelProject()+"="+projectName)

	result, err := c.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	for _, img := range result.Items {
		for _, tag := range img.RepoTags {
			if strings.HasSuffix(tag, ":latest") {
				return tag, nil
			}
		}
	}

	return "", nil
}

// ResolveImage resolves the image reference to use.
// projectName is the resolved project identity (from ProjectManager); empty for unregistered projects.
// Returns empty string if no image could be resolved.
func (c *Client) ResolveImage(ctx context.Context, projectName string) (string, error) {
	result, err := c.ResolveImageWithSource(ctx, projectName)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.Reference, nil
}

// ResolveImageWithSource resolves the image to use for container operations.
// projectName is the resolved project identity (from ProjectManager); empty for unregistered projects.
//
// Resolution order:
//  1. Docker label lookup — clawker-managed image matching project label with :latest tag
//  2. Config fallback — merged build.image from all config layers (project, user, defaults)
//
// Returns nil if no image could be resolved (caller decides what to do).
func (c *Client) ResolveImageWithSource(ctx context.Context, projectName string) (*ResolvedImage, error) {
	projectImage, err := c.findProjectImage(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("auto-detect project image: %w", err)
	}
	if projectImage != "" {
		return &ResolvedImage{Reference: projectImage, Source: ImageSourceProject}, nil
	}

	if configImage := c.cfg.Project().Build.Image; configImage != "" {
		return &ResolvedImage{Reference: configImage, Source: ImageSourceConfig}, nil
	}

	return nil, nil
}
