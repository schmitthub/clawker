// Package docker re-exports types from whail for use by commands.
// This allows commands to import internal/docker as their single Docker interface.
package docker

import "github.com/schmitthub/clawker/pkg/whail"

// Type aliases re-exported from whail.
// Commands should use these types rather than importing whail directly.
type (
	// Filters for filtering resources.
	Filters = whail.Filters

	// Container operation options.
	// ContainerCreateOptions is whail's custom struct with clawker-specific fields.
	// SDKContainerCreateOptions is the raw Docker SDK type for direct API calls.
	ContainerAttachOptions    = whail.ContainerAttachOptions
	ContainerListOptions      = whail.ContainerListOptions
	ContainerLogsOptions      = whail.ContainerLogsOptions
	ContainerRemoveOptions    = whail.ContainerRemoveOptions
	ContainerCreateOptions    = whail.ContainerCreateOptions
	SDKContainerCreateOptions = whail.SDKContainerCreateOptions

	// Container result types
	ContainerInspectOptions = whail.ContainerInspectOptions
	ContainerInspectResult  = whail.ContainerInspectResult
	ContainerWaitCondition  = whail.WaitCondition

	// Whail-specific container types.
	ContainerStartOptions = whail.ContainerStartOptions
	EnsureNetworkOptions  = whail.EnsureNetworkOptions
	Labels                = whail.Labels

	// Exec operation options and results.
	ExecCreateOptions  = whail.ExecCreateOptions
	ExecStartOptions   = whail.ExecStartOptions
	ExecAttachOptions  = whail.ExecAttachOptions
	ExecResizeOptions  = whail.ExecResizeOptions
	ExecInspectOptions = whail.ExecInspectOptions
	ExecInspectResult  = whail.ExecInspectResult

	// Copy operation options.
	CopyToContainerOptions   = whail.CopyToContainerOptions
	CopyFromContainerOptions = whail.CopyFromContainerOptions

	// Image operation options.
	ImageListOptions   = whail.ImageListOptions
	ImageRemoveOptions = whail.ImageRemoveOptions
	ImageBuildOptions  = whail.ImageBuildOptions
	ImagePullOptions   = whail.ImagePullOptions

	// Volume operation options.
	VolumeCreateOptions = whail.VolumeCreateOptions

	// Network operation options.
	NetworkCreateOptions  = whail.NetworkCreateOptions
	NetworkInspectOptions = whail.NetworkInspectOptions

	// Connection types.
	HijackedResponse = whail.HijackedResponse

	// Error types.
	DockerError = whail.DockerError
)

// Container configuration types.
type (
	Resources       = whail.Resources
	RestartPolicy   = whail.RestartPolicy
	UpdateConfig    = whail.UpdateConfig
	ContainerUpdateResult = whail.ContainerUpdateResult
)

const (
	// WaitConditionNotRunning is used to wait until a container is not running.
	WaitConditionNotRunning = whail.WaitConditionNotRunning
	WaitConditionNextExit   = whail.WaitConditionNextExit
	WaitConditionRemoved    = whail.WaitConditionRemoved
)
