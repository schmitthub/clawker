// Package docker provides clawker-specific Docker middleware.
// It wraps pkg/whail with clawker's label conventions and naming schemes.
package docker

import (
	"time"

	"github.com/schmitthub/clawker/pkg/whail"
)

// ContainerLabels returns labels for a new container.
func (c *Client) ContainerLabels(project, agent, version, image, workdir string) map[string]string {
	labels := map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
		c.cfg.LabelAgent():   agent,
		c.cfg.LabelVersion(): version,
		c.cfg.LabelImage():   image,
		c.cfg.LabelCreated(): time.Now().Format(time.RFC3339),
		c.cfg.LabelWorkdir(): workdir,
	}
	if project != "" {
		labels[c.cfg.LabelProject()] = project
	}
	return labels
}

// VolumeLabels returns labels for a new volume.
func (c *Client) VolumeLabels(project, agent, purpose string) map[string]string {
	labels := map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
		c.cfg.LabelAgent():   agent,
		c.cfg.LabelPurpose(): purpose,
	}
	if project != "" {
		labels[c.cfg.LabelProject()] = project
	}
	return labels
}

// GlobalVolumeLabels returns labels for a global (non-agent-scoped) volume.
// Only includes managed and purpose labels — no project or agent.
func (c *Client) GlobalVolumeLabels(purpose string) map[string]string {
	return map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
		c.cfg.LabelPurpose(): purpose,
	}
}

// ImageLabels returns labels for a built image.
func (c *Client) ImageLabels(project, version string) map[string]string {
	labels := map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
		c.cfg.LabelVersion(): version,
		c.cfg.LabelCreated(): time.Now().Format(time.RFC3339),
	}
	if project != "" {
		labels[c.cfg.LabelProject()] = project
	}
	return labels
}

// NetworkLabels returns labels for a new network.
func (c *Client) NetworkLabels() map[string]string {
	return map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
	}
}

// ClawkerFilter returns Docker filter for listing all clawker resources.
func (c *Client) ClawkerFilter() whail.Filters {
	return whail.Filters{}.Add("label", c.cfg.LabelManaged()+"="+c.cfg.ManagedLabelValue())
}

// ProjectFilter returns Docker filter for a specific project.
func (c *Client) ProjectFilter(project string) whail.Filters {
	return whail.Filters{}.
		Add("label", c.cfg.LabelManaged()+"="+c.cfg.ManagedLabelValue()).
		Add("label", c.cfg.LabelProject()+"="+project)
}

// AgentFilter returns Docker filter for a specific agent within a project.
func (c *Client) AgentFilter(project, agent string) whail.Filters {
	return whail.Filters{}.
		Add("label", c.cfg.LabelManaged()+"="+c.cfg.ManagedLabelValue()).
		Add("label", c.cfg.LabelProject()+"="+project).
		Add("label", c.cfg.LabelAgent()+"="+agent)
}
