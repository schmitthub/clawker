// Package whail re-exports Docker SDK types as aliases.
// This allows higher-level packages to use these types without importing moby/client directly.
package whail

import (
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

// Type aliases for Docker SDK types.
// These allow packages to use whail as their single import for Docker interactions.
type (
	// Filters wraps Docker client.Filters for filtering resources.
	Filters = client.Filters

	// Container operation options.
	// Note: ContainerCreateOptions is a custom whail struct defined in container.go.
	// SDKContainerCreateOptions is the raw Docker SDK type for direct API calls.
	ContainerAttachOptions    = client.ContainerAttachOptions
	ContainerListOptions      = client.ContainerListOptions
	ContainerLogsOptions      = client.ContainerLogsOptions
	ContainerRemoveOptions    = client.ContainerRemoveOptions
	SDKContainerCreateOptions = client.ContainerCreateOptions

	// Container result types.
	ContainerInspectOptions = client.ContainerInspectOptions
	ContainerInspectResult  = client.ContainerInspectResult

	// Exec operation options and results.
	ExecCreateOptions  = client.ExecCreateOptions
	ExecStartOptions   = client.ExecStartOptions
	ExecAttachOptions  = client.ExecAttachOptions
	ExecResizeOptions  = client.ExecResizeOptions
	ExecInspectOptions = client.ExecInspectOptions
	ExecInspectResult  = client.ExecInspectResult

	// Copy operation options.
	CopyToContainerOptions   = client.CopyToContainerOptions
	CopyFromContainerOptions = client.CopyFromContainerOptions

	// Image operation options.
	ImageListOptions   = client.ImageListOptions
	ImageRemoveOptions = client.ImageRemoveOptions
	ImageBuildOptions  = client.ImageBuildOptions
	ImagePullOptions   = client.ImagePullOptions

	// Image result types.
	ImageListResult = client.ImageListResult
	ImageSummary    = image.Summary
	ImageTagOptions = client.ImageTagOptions

	// Volume operation options.
	VolumeCreateOptions = client.VolumeCreateOptions

	// Network operation options.
	NetworkCreateOptions  = client.NetworkCreateOptions
	NetworkInspectOptions = client.NetworkInspectOptions

	// Connection types.
	HijackedResponse = client.HijackedResponse

	// Wait Condition
	WaitCondition = container.WaitCondition

	// Container configuration types.
	Resources       = container.Resources
	RestartPolicy   = container.RestartPolicy
	UpdateConfig    = container.UpdateConfig
	ContainerUpdateResult = client.ContainerUpdateResult
)

const (
	// WaitConditionNotRunning is used to wait until a container is not running.
	WaitConditionNotRunning = container.WaitConditionNotRunning
	WaitConditionNextExit   = container.WaitConditionNextExit
	WaitConditionRemoved    = container.WaitConditionRemoved
)
