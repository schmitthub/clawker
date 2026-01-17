// Package docker provides clawker-specific Docker middleware.
// It wraps pkg/whail with clawker's label conventions and naming schemes.
package docker

import (
	"time"

	"github.com/moby/moby/client"
)

// Clawker label keys for managed resources.
const (
	// LabelPrefix is the prefix for all clawker labels.
	LabelPrefix = "com.clawker."

	// LabelManaged marks a resource as managed by clawker.
	LabelManaged = LabelPrefix + "managed"

	// LabelProject identifies the project name.
	LabelProject = LabelPrefix + "project"

	// LabelAgent identifies the agent name within a project.
	LabelAgent = LabelPrefix + "agent"

	// LabelVersion stores the clawker version that created the resource.
	LabelVersion = LabelPrefix + "version"

	// LabelImage stores the source image tag for containers.
	LabelImage = LabelPrefix + "image"

	// LabelCreated stores the creation timestamp.
	LabelCreated = LabelPrefix + "created"

	// LabelWorkdir stores the host working directory.
	LabelWorkdir = LabelPrefix + "workdir"

	// LabelPurpose identifies the purpose of a volume.
	LabelPurpose = LabelPrefix + "purpose"
)

// ManagedLabelValue is the value for the managed label.
const ManagedLabelValue = "true"

// ContainerLabels returns labels for a new container.
func ContainerLabels(project, agent, version, image, workdir string) map[string]string {
	return map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelProject: project,
		LabelAgent:   agent,
		LabelVersion: version,
		LabelImage:   image,
		LabelCreated: time.Now().Format(time.RFC3339),
		LabelWorkdir: workdir,
	}
}

// VolumeLabels returns labels for a new volume.
func VolumeLabels(project, agent, purpose string) map[string]string {
	return map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelProject: project,
		LabelAgent:   agent,
		LabelPurpose: purpose,
	}
}

// ImageLabels returns labels for a built image.
func ImageLabels(project, version string) map[string]string {
	return map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelProject: project,
		LabelVersion: version,
		LabelCreated: time.Now().Format(time.RFC3339),
	}
}

// NetworkLabels returns labels for a new network.
func NetworkLabels() map[string]string {
	return map[string]string{
		LabelManaged: ManagedLabelValue,
	}
}

// ClawkerFilter returns Docker filter for listing all clawker resources.
func ClawkerFilter() client.Filters {
	return client.Filters{}.Add("label", LabelManaged+"="+ManagedLabelValue)
}

// ProjectFilter returns Docker filter for a specific project.
func ProjectFilter(project string) client.Filters {
	return client.Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+project)
}

// AgentFilter returns Docker filter for a specific agent within a project.
func AgentFilter(project, agent string) client.Filters {
	return client.Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+project).
		Add("label", LabelAgent+"="+agent)
}
