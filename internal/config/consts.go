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
