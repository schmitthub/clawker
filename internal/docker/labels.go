// Package docker provides clawker-specific Docker middleware.
// It wraps pkg/whail with clawker's label conventions and naming schemes.
package docker

import (
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Clawker label keys for managed resources.
// Re-exported from internal/config/identity.go — canonical source of truth lives there
// so lightweight packages can use labels without importing docker's heavy dependency tree.
const (
	LabelPrefix    = config.LabelPrefix
	LabelManaged   = config.LabelManaged
	LabelProject   = config.LabelProject
	LabelAgent     = config.LabelAgent
	LabelVersion   = config.LabelVersion
	LabelImage     = config.LabelImage
	LabelCreated   = config.LabelCreated
	LabelWorkdir   = config.LabelWorkdir
	LabelPurpose   = config.LabelPurpose
	LabelTestName  = config.LabelTestName
	LabelBaseImage = config.LabelBaseImage
	LabelFlavor    = config.LabelFlavor
	LabelTest      = config.LabelTest
	LabelE2ETest   = config.LabelE2ETest
)

const (
	EngineLabelPrefix  = config.EngineLabelPrefix
	EngineManagedLabel = config.EngineManagedLabel
	ManagedLabelValue  = config.ManagedLabelValue
)

// ContainerLabels returns labels for a new container.
func ContainerLabels(project, agent, version, image, workdir string) map[string]string {
	labels := map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelAgent:   agent,
		LabelVersion: version,
		LabelImage:   image,
		LabelCreated: time.Now().Format(time.RFC3339),
		LabelWorkdir: workdir,
	}
	if project != "" {
		labels[LabelProject] = project
	}
	return labels
}

// VolumeLabels returns labels for a new volume.
func VolumeLabels(project, agent, purpose string) map[string]string {
	labels := map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelAgent:   agent,
		LabelPurpose: purpose,
	}
	if project != "" {
		labels[LabelProject] = project
	}
	return labels
}

// GlobalVolumeLabels returns labels for a global (non-agent-scoped) volume.
// Only includes managed and purpose labels — no project or agent.
func GlobalVolumeLabels(purpose string) map[string]string {
	return map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelPurpose: purpose,
	}
}

// ImageLabels returns labels for a built image.
func ImageLabels(project, version string) map[string]string {
	labels := map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelVersion: version,
		LabelCreated: time.Now().Format(time.RFC3339),
	}
	if project != "" {
		labels[LabelProject] = project
	}
	return labels
}

// NetworkLabels returns labels for a new network.
func NetworkLabels() map[string]string {
	return map[string]string{
		LabelManaged: ManagedLabelValue,
	}
}

// ClawkerFilter returns Docker filter for listing all clawker resources.
func ClawkerFilter() whail.Filters {
	return whail.Filters{}.Add("label", LabelManaged+"="+ManagedLabelValue)
}

// ProjectFilter returns Docker filter for a specific project.
func ProjectFilter(project string) whail.Filters {
	return whail.Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+project)
}

// AgentFilter returns Docker filter for a specific agent within a project.
func AgentFilter(project, agent string) whail.Filters {
	return whail.Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+project).
		Add("label", LabelAgent+"="+agent)
}
