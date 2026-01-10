package engine

import (
	"time"

	"github.com/docker/docker/api/types/filters"
)

// Label keys for claucker-managed resources
const (
	LabelManaged = "com.claucker.managed"
	LabelProject = "com.claucker.project"
	LabelAgent   = "com.claucker.agent"
	LabelVersion = "com.claucker.version"
	LabelImage   = "com.claucker.image"
	LabelCreated = "com.claucker.created"
	LabelWorkdir = "com.claucker.workdir"
	LabelPurpose = "com.claucker.purpose"
)

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

// ClauckerFilter returns Docker filter args for listing claucker resources
func ClauckerFilter() filters.Args {
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
