package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
)

// ImageSource indicates where an image reference was resolved from.
type ImageSource string

const (
	ImageSourceExplicit ImageSource = "explicit" // User specified via CLI or args
	ImageSourceProject  ImageSource = "project"  // Found via project label search
	ImageSourceDefault  ImageSource = "default"  // From config/settings default_image
)

// ResolvedImage contains the result of image resolution with source tracking.
type ResolvedImage struct {
	Reference string      // The image reference (name:tag)
	Source    ImageSource // Where the image was resolved from
}

// ResolveDefaultImage returns the default_image from merged config/settings.
// Local project config takes precedence over user settings.
// Returns empty string if not configured.
func ResolveDefaultImage(cfg *config.Project, settings *config.Settings) string {
	// Local project config takes precedence
	if cfg != nil && cfg.DefaultImage != "" {
		return cfg.DefaultImage
	}

	// Fall back to user settings
	if settings != nil && settings.DefaultImage != "" {
		return settings.DefaultImage
	}

	return ""
}

// findProjectImage searches for a clawker-managed image matching the project label
// with the :latest tag. Returns the image reference (name:tag) if found,
// or empty string if not found.
func (c *Client) findProjectImage(ctx context.Context) (string, error) {
	if c.cfg == nil {
		return "", nil
	}

	cfg := c.cfg.Project
	if cfg == nil || cfg.Project == "" {
		return "", nil
	}

	f := Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+cfg.Project)

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
// Returns empty string if no image could be resolved.
func (c *Client) ResolveImage(ctx context.Context) (string, error) {
	result, err := c.ResolveImageWithSource(ctx)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.Reference, nil
}

// ResolveImageWithSource resolves the image to use for container operations.
// Resolution order:
// 1. ProjectCfg image with :latest tag (by label lookup)
// 2. Merged default_image from config/settings
//
// Returns nil if no image could be resolved (caller decides what to do).
func (c *Client) ResolveImageWithSource(ctx context.Context) (*ResolvedImage, error) {
	if c.cfg == nil {
		return nil, nil
	}

	// 1. Try to find a project image with :latest tag
	projectImage, err := c.findProjectImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("auto-detect project image: %w", err)
	}
	if projectImage != "" {
		return &ResolvedImage{Reference: projectImage, Source: ImageSourceProject}, nil
	}

	// 2. Try merged default_image from config/settings
	cfg := c.cfg.Project
	settings := c.cfg.Settings
	if defaultImage := ResolveDefaultImage(cfg, settings); defaultImage != "" {
		return &ResolvedImage{Reference: defaultImage, Source: ImageSourceDefault}, nil
	}

	return nil, nil
}
