package engine

import (
	"time"

	"github.com/docker/docker/api/types/filters"
)

// Label keys for clawker-managed resources
// TODO: this shit probably shouldn't be here. engine should be decoupled from config
const (
	LabelManaged = "com.clawker.managed"
	LabelProject = "com.clawker.project"
	LabelAgent   = "com.clawker.agent"
	LabelVersion = "com.clawker.version"
	LabelImage   = "com.clawker.image"
	LabelCreated = "com.clawker.created"
	LabelWorkdir = "com.clawker.workdir"
	LabelPurpose = "com.clawker.purpose"
)

func ImageLabels(project string, version string) map[string]string {
	return map[string]string{
		LabelManaged: "true",
		LabelProject: project,
		LabelVersion: version,
		LabelCreated: time.Now().Format(time.RFC3339),
	}
}

// ContainerLabels returns labels for a new container
func ContainerLabels(project, agent, version, image, workdir string) map[string]string {
	return map[string]string{
		LabelManaged: "true",
		LabelProject: project,
		LabelAgent:   agent,
		LabelVersion: version,
		LabelImage:   image,
		LabelCreated: time.Now().Format(time.RFC3339),
		LabelWorkdir: workdir,
	}
}

// VolumeLabels returns labels for a new volume
func VolumeLabels(project, agent, purpose string) map[string]string {
	return map[string]string{
		LabelManaged: "true",
		LabelProject: project,
		LabelAgent:   agent,
		LabelPurpose: purpose,
	}
}

// ClawkerFilter returns Docker filter args for listing clawker resources
func ClawkerFilter() filters.Args {
	return filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"),
	)
}

// ProjectFilter returns Docker filter args for a specific project
func ProjectFilter(project string) filters.Args {
	return filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"),
		filters.Arg("label", LabelProject+"="+project),
	)
}

// AgentFilter returns Docker filter args for a specific agent within a project
func AgentFilter(project, agent string) filters.Args {
	return filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"),
		filters.Arg("label", LabelProject+"="+project),
		filters.Arg("label", LabelAgent+"="+agent),
	)
}
