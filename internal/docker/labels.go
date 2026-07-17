// Package docker provides clawker-specific Docker middleware.
// It wraps pkg/whail with clawker's label conventions and naming schemes.
package docker

import (
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/pkg/whail"
)

// ContainerLabels returns labels for a new agent container.
func (c *Client) ContainerLabels(project, agent, version, image, workdir string) map[string]string {
	labels := map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
		c.cfg.LabelPurpose(): c.cfg.PurposeAgent(),
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

// AgentVolumeLabels returns labels for an agent-scoped volume (history or
// workspace). All agent volumes carry purpose=PurposeAgent; the per-volume
// role lives in the volume name suffix, not the label.
func (c *Client) AgentVolumeLabels(project, agent string) map[string]string {
	labels := map[string]string{
		c.cfg.LabelManaged(): c.cfg.ManagedLabelValue(),
		c.cfg.LabelAgent():   agent,
		c.cfg.LabelPurpose(): c.cfg.PurposeAgent(),
	}
	if project != "" {
		labels[c.cfg.LabelProject()] = project
	}
	return labels
}

// HarnessVolumeLabels returns labels for a harness-scoped agent volume
// (bundle-declared persisted dirs and the clawker lifecycle volume): the
// agent volume labels plus the harness identity. The harness value is the
// harness's exact selection spelling — a bare floor/loose name, or the
// qualified namespace.bundle.component address for an installed-bundle
// harness — the same vocabulary containers and images already carry under
// consts.LabelHarness, and what EnsureHarnessVolume checks ownership
// against. Label-based cleanup filters on the agent labels, so
// harness-scoped volumes stay in its net.
func (c *Client) HarnessVolumeLabels(project, agent, harness string) map[string]string {
	labels := c.AgentVolumeLabels(project, agent)
	labels[consts.LabelHarness] = harness
	return labels
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
