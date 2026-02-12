package config

// Domain is the actual domain name for clawker.
// Used in help text, URLs, and user-facing output.
const Domain = "clawker.dev"

// LabelDomain is the reverse-DNS form of Domain, per Docker/OCI label conventions.
// Used as the prefix for Docker labels (dev.clawker.*).
const LabelDomain = "dev.clawker"

// Label key constants for Docker/OCI labels.
// These are the canonical source of truth — internal/docker re-exports them
// so that packages needing labels without docker's heavy deps can import config directly.
const (
	// LabelPrefix is the prefix for all clawker labels (LabelDomain + ".").
	LabelPrefix = LabelDomain + "."

	// LabelManaged marks a resource as managed by clawker.
	LabelManaged = LabelPrefix + "managed"

	// LabelProject identifies the project name.
	LabelProject = LabelPrefix + "project"

	// LabelAgent identifies the agent name within a project.
	LabelAgent = LabelPrefix + "agent"

	// LabelVersion stores the clawker version that created the resource.
	LabelVersion = LabelPrefix + "version"

	// LabelImage stores the source image tag for containers.
	LabelImage = LabelPrefix + "image"

	// LabelCreated stores the creation timestamp.
	LabelCreated = LabelPrefix + "created"

	// LabelWorkdir stores the host working directory.
	LabelWorkdir = LabelPrefix + "workdir"

	// LabelPurpose identifies the purpose of a volume.
	LabelPurpose = LabelPrefix + "purpose"

	// LabelTestName identifies the test function that created a resource.
	LabelTestName = LabelPrefix + "test.name"

	// LabelBaseImage marks a built image as the base image.
	LabelBaseImage = LabelPrefix + "base-image"

	// LabelFlavor stores the Linux flavor used for a base image build.
	LabelFlavor = LabelPrefix + "flavor"

	// LabelTest marks a resource as created by a test.
	LabelTest = LabelPrefix + "test"

	// LabelE2ETest marks a resource as created by an E2E test.
	LabelE2ETest = LabelPrefix + "e2e-test"
)

// ManagedLabelValue is the value for the managed label.
const ManagedLabelValue = "true"

// EngineLabelPrefix is the label prefix for whail.EngineOptions (without trailing dot).
// Use this when configuring the whail Engine; it adds its own dot separator.
const EngineLabelPrefix = LabelDomain

// EngineManagedLabel is the managed label key for whail.EngineOptions.
const EngineManagedLabel = "managed"

// ContainerUID is the default UID for the non-root user inside clawker containers.
// Used by bundler (Dockerfile generation), docker (volume tar headers, chown),
// containerfs (onboarding tar), and test harness (test Dockerfiles).
const ContainerUID = 1001

// ContainerGID is the default GID for the non-root user inside clawker containers.
const ContainerGID = 1001
