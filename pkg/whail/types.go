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
	ImageTagResult  = client.ImageTagResult

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
	Resources             = container.Resources
	RestartPolicy         = container.RestartPolicy
	UpdateConfig          = container.UpdateConfig
	ContainerUpdateResult = client.ContainerUpdateResult
)

const (
	// WaitConditionNotRunning is used to wait until a container is not running.
	WaitConditionNotRunning = container.WaitConditionNotRunning
	WaitConditionNextExit   = container.WaitConditionNextExit
	WaitConditionRemoved    = container.WaitConditionRemoved
)

// ImageBuildKitOptions configures a BuildKit-based image build.
// Labels are injected automatically by Engine.ImageBuildKit — callers should
// set application-specific labels only.
type ImageBuildKitOptions struct {
	// Tags are the image tags to apply (e.g., "myimage:latest").
	Tags []string

	// ContextDir is the build context directory (required).
	ContextDir string

	// Dockerfile is the path relative to ContextDir (default: "Dockerfile").
	Dockerfile string

	// BuildArgs are --build-arg key=value pairs.
	BuildArgs map[string]*string

	// NoCache disables build cache.
	NoCache bool

	// Labels are applied to the built image. Managed labels are injected
	// automatically and cannot be overridden.
	Labels map[string]string

	// Target sets the target build stage.
	Target string

	// Pull forces pulling base images.
	Pull bool

	// SuppressOutput suppresses build output logging.
	SuppressOutput bool

	// NetworkMode sets the network mode for RUN instructions.
	NetworkMode string

	// OnProgress receives build progress events when non-nil.
	// Called from the progress-draining goroutine — must be safe for concurrent use.
	OnProgress BuildProgressFunc
}

// BuildProgressFunc is a callback invoked by build pipelines to report step progress.
// Implementations must be safe for concurrent use.
type BuildProgressFunc func(event BuildProgressEvent)

// BuildProgressEvent represents a single progress update from a build pipeline.
type BuildProgressEvent struct {
	// StepID uniquely identifies a build step (digest for BuildKit, "step-N" for legacy).
	StepID string

	// StepName is a human-readable label (e.g., "RUN npm install").
	StepName string

	// StepIndex is the 0-based ordinal position (-1 if unknown).
	StepIndex int

	// TotalSteps is the total number of steps (-1 if unknown).
	TotalSteps int

	// Status indicates the current state of this step.
	Status BuildStepStatus

	// LogLine contains a single output line (empty for status-only events).
	LogLine string

	// Error contains an error message (empty if no error).
	Error string

	// Cached indicates the step result was served from cache.
	Cached bool
}

// BuildStepStatus represents the state of a build step.
type BuildStepStatus int

const (
	// BuildStepPending means the step has not started yet.
	BuildStepPending BuildStepStatus = iota

	// BuildStepRunning means the step is actively executing.
	BuildStepRunning

	// BuildStepComplete means the step finished successfully.
	BuildStepComplete

	// BuildStepCached means the step was served from cache.
	BuildStepCached

	// BuildStepError means the step failed.
	BuildStepError
)
