package config

// Used in help text, URLs, and user-facing output.
const domain = "clawker.dev"

// Used as the prefix for Docker labels (dev.clawker.*).
const labelDomain = "dev.clawker"

const (
	clawkerConfigDirEnv = "CLAWKER_CONFIG_DIR"
	// MonitorSubdir is the subdirectory for monitoring stack configuration
	monitorSubdir = "monitor"
	// BuildSubdir is the subdirectory for build artifacts (versions.json, dockerfiles)
	buildSubdir = "build"
	// DockerfilesSubdir is the subdirectory for generated Dockerfiles
	dockerfilesSubdir = "dockerfiles"
	// ClawkerNetwork is the name of the shared Docker network
	clawkerNetwork = "clawker-net"
	// LogsSubdir is the subdirectory for log files
	logsSubdir = "logs"
	// BridgesSubdir is the subdirectory for socket bridge PID files
	bridgesSubdir = "bridges"
	shareSubdir   = ".clawker-share"
)

type Mode string

const (
	// ModeBind represents direct host mount (live sync)
	ModeBind Mode = "bind"
	// ModeSnapshot represents ephemeral volume copy (isolated)
	ModeSnapshot Mode = "snapshot"
)

// label key constants for Docker/OCI labels.
// These are the canonical source of truth — internal/docker re-exports them
// so that packages needing labels without docker's heavy deps can import config directly.
const (
	// labelPrefix is the prefix for all clawker labels (labelDomain + ".").
	labelPrefix = labelDomain + "."

	// labelManaged marks a resource as managed by clawker.
	labelManaged = labelPrefix + "managed"

	// labelMonitoringStack marks monitoring stack resources.
	labelMonitoringStack = labelPrefix + "monitoring"

	// labelProject identifies the project name.
	labelProject = labelPrefix + "project"

	// labelAgent identifies the agent name within a project.
	labelAgent = labelPrefix + "agent"

	// labelVersion stores the clawker version that created the resource.
	labelVersion = labelPrefix + "version"

	// labelImage stores the source image tag for containers.
	labelImage = labelPrefix + "image"

	// labelCreated stores the creation timestamp.
	labelCreated = labelPrefix + "created"

	// labelWorkdir stores the host working directory.
	labelWorkdir = labelPrefix + "workdir"

	// labelPurpose identifies the purpose of a volume.
	labelPurpose = labelPrefix + "purpose"

	// labelTestName identifies the test function that created a resource.
	labelTestName = labelPrefix + "test.name"

	// labelBaseImage marks a built image as the base image.
	labelBaseImage = labelPrefix + "base-image"

	// labelFlavor stores the Linux flavor used for a base image build.
	labelFlavor = labelPrefix + "flavor"

	// labelTest marks a resource as created by a test.
	labelTest = labelPrefix + "test"

	// labelE2ETest marks a resource as created by an E2E test.
	labelE2ETest = labelPrefix + "e2e-test"
)

// managedLabelValue is the value for the managed label.
const managedLabelValue = "true"

// engineLabelPrefix is the label prefix for whail.EngineOptions (without trailing dot).
// Use this when configuring the whail Engine; it adds its own dot separator.
const engineLabelPrefix = labelDomain

// engineManagedLabel is the managed label key for whail.EngineOptions.
const engineManagedLabel = "managed"

// containerUID is the default UID for the non-root user inside clawker containers.
// Used by bundler (Dockerfile generation), docker (volume tar headers, chown),
// containerfs (onboarding tar), and test harness (test Dockerfiles).
const containerUID = 1001

// containerGID is the default GID for the non-root user inside clawker containers.
const containerGID = 1001

func (c *configImpl) Domain() string            { return domain }
func (c *configImpl) LabelDomain() string       { return labelDomain }
func (c *configImpl) ConfigDirEnvVar() string   { return clawkerConfigDirEnv }
func (c *configImpl) MonitorSubdir() string     { return monitorSubdir }
func (c *configImpl) BuildSubdir() string       { return buildSubdir }
func (c *configImpl) DockerfilesSubdir() string { return dockerfilesSubdir }
func (c *configImpl) ClawkerNetwork() string    { return clawkerNetwork }
func (c *configImpl) LogsSubdir() string        { return logsSubdir }
func (c *configImpl) BridgesSubdir() string     { return bridgesSubdir }
func (c *configImpl) ShareSubdir() string       { return shareSubdir }
func (c *configImpl) LabelPrefix() string       { return labelPrefix }
func (c *configImpl) LabelManaged() string      { return labelManaged }
func (c *configImpl) LabelMonitoringStack() string {
	return labelMonitoringStack
}
func (c *configImpl) LabelProject() string   { return labelProject }
func (c *configImpl) LabelAgent() string     { return labelAgent }
func (c *configImpl) LabelVersion() string   { return labelVersion }
func (c *configImpl) LabelImage() string     { return labelImage }
func (c *configImpl) LabelCreated() string   { return labelCreated }
func (c *configImpl) LabelWorkdir() string   { return labelWorkdir }
func (c *configImpl) LabelPurpose() string   { return labelPurpose }
func (c *configImpl) LabelTestName() string  { return labelTestName }
func (c *configImpl) LabelBaseImage() string { return labelBaseImage }
func (c *configImpl) LabelFlavor() string    { return labelFlavor }
func (c *configImpl) LabelTest() string      { return labelTest }
func (c *configImpl) LabelE2ETest() string   { return labelE2ETest }
func (c *configImpl) ManagedLabelValue() string {
	return managedLabelValue
}
func (c *configImpl) EngineLabelPrefix() string { return engineLabelPrefix }
func (c *configImpl) EngineManagedLabel() string {
	return engineManagedLabel
}
func (c *configImpl) ContainerUID() int { return containerUID }
func (c *configImpl) ContainerGID() int { return containerGID }
