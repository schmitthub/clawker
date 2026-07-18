// Package docker re-exports types from whail for use by commands.
// This allows commands to import internal/docker as their single Docker interface.
package docker

import (
	"github.com/schmitthub/clawker/pkg/whail"
)

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

	// Image result types.
	ImageSummary    = whail.ImageSummary
	ImageListResult = whail.ImageListResult

	// Volume operation options.
	VolumeCreateOptions = whail.VolumeCreateOptions

	// Network operation options and results.
	NetworkCreateOptions  = whail.NetworkCreateOptions
	NetworkCreateResult   = whail.NetworkCreateResult
	NetworkInspectOptions = whail.NetworkInspectOptions
	NetworkInspectResult  = whail.NetworkInspectResult

	// Connection types.
	HijackedResponse = whail.HijackedResponse

	// Error types.
	DockerError = whail.DockerError
)

// Sentinel errors re-exported from whail for errors.Is matching.
var (
	// ErrNotManaged matches errors from the managed-label jail: the resource
	// either lacks the managed label or no longer exists (a NotFound during
	// the managed check collapses to this).
	ErrNotManaged = whail.ErrNotManaged
)

// Container configuration types.
type (
	Resources             = whail.Resources
	RestartPolicy         = whail.RestartPolicy
	UpdateConfig          = whail.UpdateConfig
	ContainerUpdateResult = whail.ContainerUpdateResult
)

// BuildProgressFunc is a callback for reporting build progress events.
type BuildProgressFunc = whail.BuildProgressFunc

const (
	// WaitConditionNotRunning is used to wait until a container is not running.
	WaitConditionNotRunning = whail.WaitConditionNotRunning
	WaitConditionNextExit   = whail.WaitConditionNextExit
	WaitConditionRemoved    = whail.WaitConditionRemoved
)
