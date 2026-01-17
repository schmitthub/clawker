// Package whail provides a reusable Docker isolation library ("whale jail").
// It wraps the Docker SDK with automatic label-based resource isolation,
// ensuring operations only affect resources managed by a specific application.
package whail

import (
	"maps"

	"github.com/moby/moby/client"
)

// LabelConfig defines labels to apply to different resource types.
// All labels are optional - if a map is nil, no labels are applied for that resource type.
type LabelConfig struct {
	// Default labels applied to all resource types (containers, volumes, networks, images)
	Default map[string]string

	// Container-specific labels (merged with Default)
	Container map[string]string

	// Volume-specific labels (merged with Default)
	Volume map[string]string

	// Network-specific labels (merged with Default)
	Network map[string]string

	// Image-specific labels (merged with Default)
	Image map[string]string
}

// MergeLabels merges multiple label maps, with later maps overriding earlier ones.
// Returns a new map containing all labels.
func MergeLabels(labelMaps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range labelMaps {
		maps.Copy(result, m)
	}
	return result
}

// ContainerLabels returns the merged labels for containers.
func (c *LabelConfig) ContainerLabels(extra ...map[string]string) map[string]string {
	all := append([]map[string]string{c.Default, c.Container}, extra...)
	return MergeLabels(all...)
}

// VolumeLabels returns the merged labels for volumes.
func (c *LabelConfig) VolumeLabels(extra ...map[string]string) map[string]string {
	all := append([]map[string]string{c.Default, c.Volume}, extra...)
	return MergeLabels(all...)
}

// NetworkLabels returns the merged labels for networks.
func (c *LabelConfig) NetworkLabels(extra ...map[string]string) map[string]string {
	all := append([]map[string]string{c.Default, c.Network}, extra...)
	return MergeLabels(all...)
}

// ImageLabels returns the merged labels for images.
func (c *LabelConfig) ImageLabels(extra ...map[string]string) map[string]string {
	all := append([]map[string]string{c.Default, c.Image}, extra...)
	return MergeLabels(all...)
}

// LabelFilter creates a Docker filter for a single label key=value.
// The key should include the prefix (e.g., "com.myapp.managed").
func LabelFilter(key, value string) client.Filters {
	return client.Filters{}.Add("label", key+"="+value)
}

// LabelFilterMultiple creates a Docker filter from multiple label key=value pairs.
// All labels must match (AND logic).
func LabelFilterMultiple(labels map[string]string) client.Filters {
	f := client.Filters{}
	for k, v := range labels {
		f = f.Add("label", k+"="+v)
	}
	return f
}

// AddLabelFilter adds a label filter to an existing client.Filters.
// Returns a new Filters (immutable pattern).
func AddLabelFilter(f client.Filters, key, value string) client.Filters {
	return f.Add("label", key+"="+value)
}

// MergeLabelFilters merges label filters into an existing client.Filters.
// Returns a new Filters (immutable pattern).
func MergeLabelFilters(f client.Filters, labels map[string]string) client.Filters {
	for k, v := range labels {
		f = f.Add("label", k+"="+v)
	}
	return f
}
