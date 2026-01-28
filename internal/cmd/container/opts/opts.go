// Package opts provides shared options and utilities for container commands.
// This package exists to avoid import cycles - subcommands (run/, create/) need to
// share container option types and functions, but the parent package (container/)
// imports all subcommands. Go doesn't allow A importing B that imports A.
//
// Architectural note: Subpackages under internal/cmd/<noun>/ are typically for
// subcommands only. This package is an exception - it exists solely because of
// Go's import cycle constraint. The types here are CLI flag types, not API types.
package opts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// seccompProfileDefault is the built-in default seccomp profile.
	seccompProfileDefault = "builtin"
	// seccompProfileUnconfined is a special profile name for an "unconfined" seccomp profile.
	seccompProfileUnconfined = "unconfined"
)

// deviceCgroupRuleRegexp validates device cgroup rule format: 'type major:minor mode'
var deviceCgroupRuleRegexp = regexp.MustCompile(`^[acb] ([0-9]+|\*):([0-9]+|\*) [rwm]{1,3}$`)

// ContainerOptions holds common options for container run and create commands.
// Commands can embed this struct and add command-specific options.
type ContainerOptions struct {
	// Naming
	Agent string // Agent name for clawker naming (clawker.<project>.<agent>)
	Name  string // Same as agent, for Docker CLI familiarity

	// Container configuration
	Env             []string // Environment variables
	EnvFile         []string // Read env vars from file(s)
	Volumes         []string // Bind mounts
	Publish         *PortOpts
	Workdir         string     // Working directory
	User            string     // User
	Entrypoint      string     // Override entrypoint
	TTY             bool       // Allocate TTY
	Stdin           bool       // Keep STDIN open
	Attach          *ListOpts  // Attach to STDIN, STDOUT, STDERR
	NetMode         NetworkOpt // Network connection (supports advanced syntax)
	Labels          []string   // Additional labels
	LabelsFile      []string   // Read labels from file(s)
	AutoRemove      bool       // Auto-remove on exit
	Domainname      string     // Container NIS domain name
	ContainerIDFile string     // Write container ID to file
	GroupAdd        []string   // Additional groups

	// Resource limits
	Memory            docker.MemBytes           // Memory limit (e.g., "512m", "2g")
	MemorySwap        docker.MemSwapBytes       // Total memory (memory + swap), -1 for unlimited
	MemoryReservation docker.MemBytes           // Memory soft limit
	ShmSize           docker.MemBytes           // Size of /dev/shm
	CPUs              docker.NanoCPUs           // Number of CPUs (e.g., "1.5", "0.5")
	CPUShares         int64                     // CPU shares (relative weight)
	CPUSetCPUs        string                    // CPUs to allow (0-3, 0,1)
	CPUSetMems        string                    // MEMs to allow (0-3, 0,1)
	CPUPeriod         int64                     // CFS period
	CPUQuota          int64                     // CFS quota
	CPURtPeriod       int64                     // Realtime period
	CPURtRuntime      int64                     // Realtime runtime
	BlkioWeight       uint16                    // Block IO weight (10-1000)
	BlkioWeightDevice *docker.WeightDeviceOpt   // Per-device block IO weight
	DeviceReadBps     *docker.ThrottleDeviceOpt // Read rate limit (bytes/sec)
	DeviceWriteBps    *docker.ThrottleDeviceOpt // Write rate limit (bytes/sec)
	DeviceReadIOps    *docker.ThrottleDeviceOpt // Read rate limit (IO/sec)
	DeviceWriteIOps   *docker.ThrottleDeviceOpt // Write rate limit (IO/sec)
	PidsLimit         int64                     // Process limit (-1 unlimited)
	OOMKillDisable    bool                      // Disable OOM Killer
	OOMScoreAdj       int                       // OOM preferences (-1000 to 1000)
	Swappiness        int64                     // Memory swappiness (0-100, -1 for default)
	CPUCount          int64                     // CPU count (Windows only)
	CPUPercent        int64                     // CPU percent (Windows only)
	IOMaxBandwidth    docker.MemBytes           // Max IO bandwidth (Windows only)
	IOMaxIOps         uint64                    // Max IOps (Windows only)

	// Networking
	Hostname     string   // Container hostname
	DNS          []string // Custom DNS servers
	DNSSearch    []string // Custom DNS search domains
	DNSOptions   []string // DNS options
	ExtraHosts   []string // Extra hosts (host:IP mapping)
	Expose       []string // Expose port(s) without publishing
	PublishAll   bool     // Publish all exposed ports
	MacAddress   string   // Container MAC address
	IPv4Address  string   // IPv4 address
	IPv6Address  string   // IPv6 address
	Links        []string // Add link to another container
	Aliases      []string // Network-scoped aliases
	LinkLocalIPs []string // Link-local addresses

	// Storage
	Tmpfs        []string         // Tmpfs mounts (path or path:options)
	ReadOnly     bool             // Mount root filesystem as read-only
	VolumesFrom  []string         // Mount volumes from another container
	VolumeDriver string           // Volume driver
	StorageOpt   []string         // Storage driver options
	Mounts       *docker.MountOpt // Advanced mount specifications

	// Devices
	Devices           *docker.DeviceOpt // Host devices to add
	GPUs              *docker.GpuOpts   // GPU devices
	DeviceCgroupRules []string          // Device cgroup rules

	// Security
	CapAdd      []string // Add Linux capabilities
	CapDrop     []string // Drop Linux capabilities
	Privileged  bool     // Give extended privileges to container
	SecurityOpt []string // Security options (e.g., seccomp, apparmor, label)

	// Health check
	HealthCmd           string        // Command to run to check health
	HealthInterval      time.Duration // Time between health checks
	HealthTimeout       time.Duration // Maximum time to allow health check to run
	HealthRetries       int           // Consecutive failures needed to report unhealthy
	HealthStartPeriod   time.Duration // Start period for the container to initialize
	HealthStartInterval time.Duration // Check interval during start period
	NoHealthcheck       bool          // Disable any container-specified HEALTHCHECK

	// Process and runtime
	Restart     string // Restart policy (no, always, on-failure[:max-retries], unless-stopped)
	StopSignal  string // Signal to stop the container (e.g., SIGTERM)
	StopTimeout int    // Timeout (in seconds) to stop a container
	Init        bool   // Run init inside the container

	// Namespace/Runtime
	PidMode      string // PID namespace
	IpcMode      string // IPC namespace
	UtsMode      string // UTS namespace
	UsernsMode   string // User namespace
	CgroupnsMode string // Cgroup namespace
	CgroupParent string // Parent cgroup
	Runtime      string // OCI runtime
	Isolation    string // Container isolation

	// Logging
	LogDriver string   // Logging driver
	LogOpts   []string // Log driver options

	// Resource limits (ulimits)
	Ulimits *docker.UlimitOpt // Ulimit options

	// Annotations and kernel parameters
	Annotations *MapOpts // OCI annotations
	Sysctls     *MapOpts // Kernel parameters

	// Workspace mode
	Mode string // "bind" or "snapshot" (empty = use config default)

	// Internal (set after parsing positional args)
	Image   string
	Command []string
}

// NewContainerOptions creates a new ContainerOptions with initialized fields.
func NewContainerOptions() *ContainerOptions {
	return &ContainerOptions{
		Publish:           NewPortOpts(),
		Attach:            NewListOpts(validateAttach),
		Swappiness:        -1, // -1 means use system default
		BlkioWeightDevice: docker.NewWeightDeviceOpt(),
		DeviceReadBps:     docker.NewThrottleDeviceOpt(true),
		DeviceWriteBps:    docker.NewThrottleDeviceOpt(true),
		DeviceReadIOps:    docker.NewThrottleDeviceOpt(false),
		DeviceWriteIOps:   docker.NewThrottleDeviceOpt(false),
		Devices:           docker.NewDeviceOpt(),
		GPUs:              docker.NewGpuOpts(),
		Mounts:            docker.NewMountOpt(),
		Ulimits:           docker.NewUlimitOpt(),
		Annotations:       NewMapOpts(nil),
		Sysctls:           NewMapOpts(nil),
	}
}

// AddFlags adds common container flags to the given flag set.
// This is the single point for flag registration shared between run and create commands.
func AddFlags(flags *pflag.FlagSet, opts *ContainerOptions) {
	// Naming flags
	flags.StringVar(&opts.Agent, "agent", "", "Agent name for container (uses clawker.<project>.<agent> naming)")
	flags.StringVar(&opts.Name, "name", "", "Same as --agent; provided for Docker CLI familiarity (mutually exclusive with --agent)")

	// Container configuration flags
	flags.VarP(opts.Attach, "attach", "a", "Attach to STDIN, STDOUT or STDERR")
	flags.StringArrayVarP(&opts.Env, "env", "e", nil, "Set environment variables")
	flags.StringArrayVar(&opts.EnvFile, "env-file", nil, "Read in a file of environment variables")
	flags.StringArrayVarP(&opts.Volumes, "volume", "v", nil, "Bind mount a volume")
	flags.VarP(opts.Publish, "publish", "p", "Publish container port(s) to host")
	flags.StringVarP(&opts.Workdir, "workdir", "w", "", "Working directory inside the container")
	flags.StringVarP(&opts.User, "user", "u", "", "Username or UID")
	flags.StringVar(&opts.Entrypoint, "entrypoint", "", "Overwrite the default ENTRYPOINT")
	flags.BoolVarP(&opts.TTY, "tty", "t", false, "Allocate a pseudo-TTY")
	flags.BoolVarP(&opts.Stdin, "interactive", "i", false, "Keep STDIN open even if not attached")
	flags.Var(&opts.NetMode, "network", "Connect a container to a network")
	flags.Var(&opts.NetMode, "net", "Connect a container to a network")
	_ = flags.MarkHidden("net")
	flags.StringArrayVarP(&opts.Labels, "label", "l", nil, "Set metadata on container")
	flags.StringArrayVar(&opts.LabelsFile, "label-file", nil, "Read in a file of labels")
	flags.BoolVar(&opts.AutoRemove, "rm", false, "Automatically remove container when it exits")
	flags.StringVar(&opts.Domainname, "domainname", "", "Container NIS domain name")
	flags.StringVar(&opts.ContainerIDFile, "cidfile", "", "Write the container ID to the file")
	flags.StringArrayVar(&opts.GroupAdd, "group-add", nil, "Add additional groups to join")
	flags.StringVar(&opts.Mode, "mode", "", "Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy)")

	// Resource limit flags
	flags.VarP(&opts.Memory, "memory", "m", "Memory limit (e.g., 512m, 2g)")
	flags.Var(&opts.MemorySwap, "memory-swap", "Total memory (memory + swap), -1 for unlimited swap")
	flags.Var(&opts.MemoryReservation, "memory-reservation", "Memory soft limit")
	flags.Int64Var(&opts.Swappiness, "memory-swappiness", -1, "Tune container memory swappiness (0 to 100)")
	flags.Var(&opts.ShmSize, "shm-size", "Size of /dev/shm")
	flags.Var(&opts.CPUs, "cpus", "Number of CPUs (e.g., 1.5)")
	flags.Int64VarP(&opts.CPUShares, "cpu-shares", "c", 0, "CPU shares (relative weight)")
	flags.StringVar(&opts.CPUSetCPUs, "cpuset-cpus", "", "CPUs in which to allow execution (0-3, 0,1)")
	flags.StringVar(&opts.CPUSetMems, "cpuset-mems", "", "MEMs in which to allow execution (0-3, 0,1)")
	flags.Int64Var(&opts.CPUPeriod, "cpu-period", 0, "Limit CPU CFS (Completely Fair Scheduler) period")
	flags.Int64Var(&opts.CPUQuota, "cpu-quota", 0, "Limit CPU CFS (Completely Fair Scheduler) quota")
	flags.Int64Var(&opts.CPURtPeriod, "cpu-rt-period", 0, "Limit CPU real-time period in microseconds")
	flags.Int64Var(&opts.CPURtRuntime, "cpu-rt-runtime", 0, "Limit CPU real-time runtime in microseconds")
	flags.Uint16Var(&opts.BlkioWeight, "blkio-weight", 0, "Block IO (relative weight), between 10 and 1000, or 0 to disable")
	flags.Var(opts.BlkioWeightDevice, "blkio-weight-device", "Block IO weight (relative device weight)")
	flags.Var(opts.DeviceReadBps, "device-read-bps", "Limit read rate (bytes per second) from a device")
	flags.Var(opts.DeviceWriteBps, "device-write-bps", "Limit write rate (bytes per second) to a device")
	flags.Var(opts.DeviceReadIOps, "device-read-iops", "Limit read rate (IO per second) from a device")
	flags.Var(opts.DeviceWriteIOps, "device-write-iops", "Limit write rate (IO per second) to a device")
	flags.Int64Var(&opts.PidsLimit, "pids-limit", 0, "Tune container pids limit (set -1 for unlimited)")
	flags.BoolVar(&opts.OOMKillDisable, "oom-kill-disable", false, "Disable OOM Killer")
	flags.IntVar(&opts.OOMScoreAdj, "oom-score-adj", 0, "Tune host's OOM preferences (-1000 to 1000)")
	flags.Var(opts.Ulimits, "ulimit", "Ulimit options")
	flags.Int64Var(&opts.CPUCount, "cpu-count", 0, "CPU count (Windows only)")
	flags.Int64Var(&opts.CPUPercent, "cpu-percent", 0, "CPU percent (Windows only)")
	flags.Var(&opts.IOMaxBandwidth, "io-maxbandwidth", "Maximum IO bandwidth limit for the system drive (Windows only)")
	flags.Uint64Var(&opts.IOMaxIOps, "io-maxiops", 0, "Maximum IOps limit for the system drive (Windows only)")

	// Networking flags
	// Note: NOT using -h shorthand for --hostname as it conflicts with Cobra's --help flag
	flags.StringVar(&opts.Hostname, "hostname", "", "Container hostname")
	flags.StringArrayVar(&opts.DNS, "dns", nil, "Set custom DNS servers")
	flags.StringArrayVar(&opts.DNSSearch, "dns-search", nil, "Set custom DNS search domains")
	flags.StringArrayVar(&opts.DNSOptions, "dns-option", nil, "Set DNS options")
	flags.StringArrayVar(&opts.ExtraHosts, "add-host", nil, "Add custom host-to-IP mapping (host:ip)")
	flags.StringArrayVar(&opts.Expose, "expose", nil, "Expose a port or a range of ports")
	flags.BoolVarP(&opts.PublishAll, "publish-all", "P", false, "Publish all exposed ports to random ports")
	flags.StringVar(&opts.MacAddress, "mac-address", "", "Container MAC address (e.g., 92:d0:c6:0a:29:33)")
	flags.StringVar(&opts.IPv4Address, "ip", "", "IPv4 address (e.g., 172.30.100.104)")
	flags.StringVar(&opts.IPv6Address, "ip6", "", "IPv6 address (e.g., 2001:db8::33)")
	flags.StringArrayVar(&opts.Links, "link", nil, "Add link to another container")
	flags.StringArrayVar(&opts.Aliases, "network-alias", nil, "Add network-scoped alias for the container")
	flags.StringArrayVar(&opts.LinkLocalIPs, "link-local-ip", nil, "Container IPv4/IPv6 link-local addresses")

	// Storage flags
	flags.StringArrayVar(&opts.Tmpfs, "tmpfs", nil, "Mount a tmpfs directory (e.g., /tmp:rw,size=64m)")
	flags.BoolVar(&opts.ReadOnly, "read-only", false, "Mount the container's root filesystem as read only")
	flags.StringArrayVar(&opts.VolumesFrom, "volumes-from", nil, "Mount volumes from the specified container(s)")
	flags.StringVar(&opts.VolumeDriver, "volume-driver", "", "Optional volume driver for the container")
	flags.StringArrayVar(&opts.StorageOpt, "storage-opt", nil, "Storage driver options for the container")
	flags.Var(opts.Mounts, "mount", "Attach a filesystem mount to the container")

	// Device flags
	flags.Var(opts.Devices, "device", "Add a host device to the container")
	flags.Var(opts.GPUs, "gpus", "GPU devices to add to the container ('all' to pass all GPUs)")
	flags.StringArrayVar(&opts.DeviceCgroupRules, "device-cgroup-rule", nil, "Add a rule to the cgroup allowed devices list")

	// Security flags
	flags.StringArrayVar(&opts.CapAdd, "cap-add", nil, "Add Linux capabilities")
	flags.StringArrayVar(&opts.CapDrop, "cap-drop", nil, "Drop Linux capabilities")
	flags.BoolVar(&opts.Privileged, "privileged", false, "Give extended privileges to this container")
	flags.StringArrayVar(&opts.SecurityOpt, "security-opt", nil, "Security options")

	// Health check flags
	flags.StringVar(&opts.HealthCmd, "health-cmd", "", "Command to run to check health")
	flags.DurationVar(&opts.HealthInterval, "health-interval", 0, "Time between running the check (e.g., 30s, 1m)")
	flags.DurationVar(&opts.HealthTimeout, "health-timeout", 0, "Maximum time to allow one check to run (e.g., 30s)")
	flags.IntVar(&opts.HealthRetries, "health-retries", 0, "Consecutive failures needed to report unhealthy")
	flags.DurationVar(&opts.HealthStartPeriod, "health-start-period", 0, "Start period for the container to initialize (e.g., 5s)")
	flags.DurationVar(&opts.HealthStartInterval, "health-start-interval", 0, "Time between running the check during the start period")
	flags.BoolVar(&opts.NoHealthcheck, "no-healthcheck", false, "Disable any container-specified HEALTHCHECK")

	// Process and runtime flags
	flags.StringVar(&opts.Restart, "restart", "", "Restart policy (no, always, on-failure[:max-retries], unless-stopped)")
	flags.StringVar(&opts.StopSignal, "stop-signal", "", "Signal to stop the container")
	flags.IntVar(&opts.StopTimeout, "stop-timeout", 0, "Timeout (in seconds) to stop a container")
	flags.BoolVar(&opts.Init, "init", false, "Run an init inside the container that forwards signals and reaps processes")

	// Namespace/Runtime flags
	flags.StringVar(&opts.PidMode, "pid", "", "PID namespace to use")
	flags.StringVar(&opts.IpcMode, "ipc", "", "IPC mode to use")
	flags.StringVar(&opts.UtsMode, "uts", "", "UTS namespace to use")
	flags.StringVar(&opts.UsernsMode, "userns", "", "User namespace to use")
	flags.StringVar(&opts.CgroupnsMode, "cgroupns", "", "Cgroup namespace to use (host|private)")
	flags.StringVar(&opts.CgroupParent, "cgroup-parent", "", "Optional parent cgroup for the container")
	flags.StringVar(&opts.Runtime, "runtime", "", "Runtime to use for this container")
	flags.StringVar(&opts.Isolation, "isolation", "", "Container isolation technology")

	// Logging flags
	flags.StringVar(&opts.LogDriver, "log-driver", "", "Logging driver for the container")
	flags.StringArrayVar(&opts.LogOpts, "log-opt", nil, "Log driver options")

	// Annotations and kernel parameters
	flags.Var(opts.Annotations, "annotation", "Add an annotation to the container (passed through to the OCI runtime)")
	flags.Var(opts.Sysctls, "sysctl", "Sysctl options")

	// Hidden aliases for Docker CLI compatibility
	flags.StringArrayVar(&opts.DNSOptions, "dns-opt", nil, "Set DNS options")
	_ = flags.MarkHidden("dns-opt")
	flags.StringArrayVar(&opts.Aliases, "net-alias", nil, "Add network-scoped alias for the container")
	_ = flags.MarkHidden("net-alias")
}

// MarkMutuallyExclusive marks agent and name flags as mutually exclusive on the command.
func MarkMutuallyExclusive(cmd *cobra.Command) {
	cmd.MarkFlagsMutuallyExclusive("agent", "name")
}

// GetAgentName returns the agent name from either --agent or --name flag.
func (opts *ContainerOptions) GetAgentName() string {
	if opts.Agent != "" {
		return opts.Agent
	}
	return opts.Name
}

// BuildConfigs builds Docker container, host, and network configs from the options.
// This consolidates the duplicated buildConfigs logic from run.go and create.go.
// The flags parameter is used to detect whether certain flags were explicitly set
// (e.g., --entrypoint="" to reset entrypoint, --stop-timeout, --init).
func (opts *ContainerOptions) BuildConfigs(flags *pflag.FlagSet, mounts []mount.Mount, projectCfg *config.Config) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	// Determine attach modes
	attachStdin := opts.Stdin
	attachStdout := true
	attachStderr := true
	if opts.Attach != nil && opts.Attach.Len() > 0 {
		attachStdin = false
		attachStdout = false
		attachStderr = false
		for _, a := range opts.Attach.GetAll() {
			switch a {
			case "stdin":
				attachStdin = true
			case "stdout":
				attachStdout = true
			case "stderr":
				attachStderr = true
			}
		}
	}

	// Read env files and prepend to env list (CLI -e values take precedence)
	var envFromFiles []string
	for _, file := range opts.EnvFile {
		fileEnvs, err := readEnvFile(file)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read env file %q: %w", file, err)
		}
		envFromFiles = append(envFromFiles, fileEnvs...)
	}
	allEnv := append(envFromFiles, opts.Env...)

	// Read label files and prepend to labels list (CLI -l values take precedence)
	var labelsFromFiles []string
	for _, file := range opts.LabelsFile {
		fileLabels, err := readLabelFile(file)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read label file %q: %w", file, err)
		}
		labelsFromFiles = append(labelsFromFiles, fileLabels...)
	}
	allLabels := append(labelsFromFiles, opts.Labels...)

	// Container config
	cfg := &container.Config{
		Image:        opts.Image,
		Hostname:     opts.Hostname,
		Domainname:   opts.Domainname,
		Tty:          opts.TTY,
		OpenStdin:    opts.Stdin,
		AttachStdin:  attachStdin,
		AttachStdout: attachStdout,
		AttachStderr: attachStderr,
		Env:          allEnv,
		WorkingDir:   opts.Workdir,
		User:         opts.User,
	}

	// Set command if provided
	if len(opts.Command) > 0 {
		cfg.Cmd = opts.Command
	}

	// Set entrypoint if provided; --entrypoint="" resets entrypoint
	if opts.Entrypoint != "" {
		cfg.Entrypoint = []string{opts.Entrypoint}
	} else if flags != nil && flags.Changed("entrypoint") {
		// --entrypoint="" was explicitly set to reset the entrypoint
		cfg.Entrypoint = []string{""}
	}

	// Parse additional labels
	if len(allLabels) > 0 {
		cfg.Labels = make(map[string]string)
		for _, l := range allLabels {
			parts := strings.SplitN(l, "=", 2)
			if len(parts) == 2 {
				cfg.Labels[parts[0]] = parts[1]
			} else {
				cfg.Labels[parts[0]] = ""
			}
		}
	}

	// Parse exposed ports (--expose), supporting ranges like "3000-3005/tcp"
	if len(opts.Expose) > 0 {
		if cfg.ExposedPorts == nil {
			cfg.ExposedPorts = make(network.PortSet)
		}
		for _, expose := range opts.Expose {
			pr, err := network.ParsePortRange(expose)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("invalid range format for --expose: %w", err)
			}
			for p := range pr.All() {
				cfg.ExposedPorts[p] = struct{}{}
			}
		}
	}

	// Health check configuration
	haveHealthSettings := opts.HealthCmd != "" ||
		opts.HealthInterval != 0 ||
		opts.HealthTimeout != 0 ||
		opts.HealthRetries != 0 ||
		opts.HealthStartPeriod != 0 ||
		opts.HealthStartInterval != 0

	if opts.NoHealthcheck {
		if haveHealthSettings {
			return nil, nil, nil, fmt.Errorf("--no-healthcheck conflicts with --health-* options")
		}
		cfg.Healthcheck = &container.HealthConfig{Test: []string{"NONE"}}
	} else if haveHealthSettings {
		if opts.HealthCmd == "" {
			return nil, nil, nil, fmt.Errorf("--health-cmd is required when using --health-* options")
		}
		probe := []string{"CMD-SHELL", opts.HealthCmd}
		if opts.HealthInterval < 0 {
			return nil, nil, nil, fmt.Errorf("--health-interval cannot be negative")
		}
		if opts.HealthTimeout < 0 {
			return nil, nil, nil, fmt.Errorf("--health-timeout cannot be negative")
		}
		if opts.HealthRetries < 0 {
			return nil, nil, nil, fmt.Errorf("--health-retries cannot be negative")
		}
		if opts.HealthStartPeriod < 0 {
			return nil, nil, nil, fmt.Errorf("--health-start-period cannot be negative")
		}
		if opts.HealthStartInterval < 0 {
			return nil, nil, nil, fmt.Errorf("--health-start-interval cannot be negative")
		}
		cfg.Healthcheck = &container.HealthConfig{
			Test:          probe,
			Interval:      opts.HealthInterval,
			Timeout:       opts.HealthTimeout,
			StartPeriod:   opts.HealthStartPeriod,
			StartInterval: opts.HealthStartInterval,
			Retries:       opts.HealthRetries,
		}
	}

	// Host config
	hostCfg := &container.HostConfig{
		AutoRemove:      opts.AutoRemove,
		Mounts:          mounts,
		DNSSearch:       opts.DNSSearch,
		DNSOptions:      opts.DNSOptions,
		ExtraHosts:      opts.ExtraHosts,
		ContainerIDFile: opts.ContainerIDFile,
		PublishAllPorts: opts.PublishAll,
		VolumeDriver:    opts.VolumeDriver,
		GroupAdd:        opts.GroupAdd,
		Links:           opts.Links,
		OomScoreAdj:     opts.OOMScoreAdj,
	}

	// Security options
	// Merge CLI-provided capabilities with project config capabilities
	// CLI flags take precedence if both are provided
	if len(opts.CapAdd) > 0 {
		hostCfg.CapAdd = opts.CapAdd
	} else if len(projectCfg.Security.CapAdd) > 0 {
		hostCfg.CapAdd = projectCfg.Security.CapAdd
	}
	if len(opts.CapDrop) > 0 {
		hostCfg.CapDrop = opts.CapDrop
	}
	if opts.Privileged {
		hostCfg.Privileged = true
	}
	if len(opts.SecurityOpt) > 0 {
		securityOpts, err := parseSecurityOpts(opts.SecurityOpt)
		if err != nil {
			return nil, nil, nil, err
		}
		securityOpts, maskedPaths, readonlyPaths := parseSystemPaths(securityOpts)
		hostCfg.SecurityOpt = securityOpts
		if maskedPaths != nil {
			hostCfg.MaskedPaths = maskedPaths
		}
		if readonlyPaths != nil {
			hostCfg.ReadonlyPaths = readonlyPaths
		}
	}

	// Namespace modes (with validation)
	pidMode := container.PidMode(opts.PidMode)
	if !pidMode.Valid() {
		return nil, nil, nil, fmt.Errorf("--pid: invalid PID mode")
	}
	hostCfg.PidMode = pidMode

	if opts.IpcMode != "" {
		hostCfg.IpcMode = container.IpcMode(opts.IpcMode)
	}

	utsMode := container.UTSMode(opts.UtsMode)
	if !utsMode.Valid() {
		return nil, nil, nil, fmt.Errorf("--uts: invalid UTS mode")
	}
	hostCfg.UTSMode = utsMode

	usernsMode := container.UsernsMode(opts.UsernsMode)
	if !usernsMode.Valid() {
		return nil, nil, nil, fmt.Errorf("--userns: invalid USER mode")
	}
	hostCfg.UsernsMode = usernsMode

	cgroupnsMode := container.CgroupnsMode(opts.CgroupnsMode)
	if !cgroupnsMode.Valid() {
		return nil, nil, nil, fmt.Errorf("--cgroupns: invalid CGROUP mode")
	}
	hostCfg.CgroupnsMode = cgroupnsMode
	if opts.CgroupParent != "" {
		hostCfg.CgroupParent = opts.CgroupParent
	}
	if opts.Runtime != "" {
		hostCfg.Runtime = opts.Runtime
	}
	if opts.Isolation != "" {
		hostCfg.Isolation = container.Isolation(opts.Isolation)
	}

	// Logging config
	if opts.LogDriver != "" {
		loggingOptsMap, err := parseLoggingOpts(opts.LogDriver, opts.LogOpts)
		if err != nil {
			return nil, nil, nil, err
		}
		hostCfg.LogConfig = container.LogConfig{
			Type:   opts.LogDriver,
			Config: loggingOptsMap,
		}
	}

	// Parse DNS servers from strings to netip.Addr
	if len(opts.DNS) > 0 {
		dnsAddrs := make([]netip.Addr, 0, len(opts.DNS))
		for _, dns := range opts.DNS {
			addr, err := netip.ParseAddr(dns)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("invalid DNS server address %q: %w", dns, err)
			}
			dnsAddrs = append(dnsAddrs, addr)
		}
		hostCfg.DNS = dnsAddrs
	}

	// Resource limits
	if opts.Memory.Value() > 0 {
		hostCfg.Memory = opts.Memory.Value()
	}
	if opts.MemorySwap.Value() != 0 {
		hostCfg.MemorySwap = opts.MemorySwap.Value()
	}
	if opts.MemoryReservation.Value() > 0 {
		hostCfg.MemoryReservation = opts.MemoryReservation.Value()
	}
	if opts.Swappiness != -1 {
		hostCfg.MemorySwappiness = &opts.Swappiness
	}
	if opts.ShmSize.Value() > 0 {
		hostCfg.ShmSize = opts.ShmSize.Value()
	}
	if opts.CPUs.Value() > 0 {
		hostCfg.NanoCPUs = opts.CPUs.Value()
	}
	if opts.CPUShares > 0 {
		hostCfg.CPUShares = opts.CPUShares
	}
	if opts.CPUSetCPUs != "" {
		hostCfg.CpusetCpus = opts.CPUSetCPUs
	}
	if opts.CPUSetMems != "" {
		hostCfg.CpusetMems = opts.CPUSetMems
	}
	if opts.CPUPeriod > 0 {
		hostCfg.CPUPeriod = opts.CPUPeriod
	}
	if opts.CPUQuota > 0 {
		hostCfg.CPUQuota = opts.CPUQuota
	}
	if opts.CPURtPeriod > 0 {
		hostCfg.CPURealtimePeriod = opts.CPURtPeriod
	}
	if opts.CPURtRuntime > 0 {
		hostCfg.CPURealtimeRuntime = opts.CPURtRuntime
	}
	if opts.BlkioWeight > 0 {
		hostCfg.BlkioWeight = opts.BlkioWeight
	}
	if opts.BlkioWeightDevice != nil && opts.BlkioWeightDevice.Len() > 0 {
		hostCfg.BlkioWeightDevice = opts.BlkioWeightDevice.GetAll()
	}
	if opts.DeviceReadBps != nil && opts.DeviceReadBps.Len() > 0 {
		hostCfg.BlkioDeviceReadBps = opts.DeviceReadBps.GetAll()
	}
	if opts.DeviceWriteBps != nil && opts.DeviceWriteBps.Len() > 0 {
		hostCfg.BlkioDeviceWriteBps = opts.DeviceWriteBps.GetAll()
	}
	if opts.DeviceReadIOps != nil && opts.DeviceReadIOps.Len() > 0 {
		hostCfg.BlkioDeviceReadIOps = opts.DeviceReadIOps.GetAll()
	}
	if opts.DeviceWriteIOps != nil && opts.DeviceWriteIOps.Len() > 0 {
		hostCfg.BlkioDeviceWriteIOps = opts.DeviceWriteIOps.GetAll()
	}
	if opts.PidsLimit != 0 {
		hostCfg.PidsLimit = &opts.PidsLimit
	}
	if opts.OOMKillDisable {
		hostCfg.OomKillDisable = &opts.OOMKillDisable
	}

	// Windows-only CPU/IO resources
	if opts.CPUCount > 0 {
		hostCfg.CPUCount = opts.CPUCount
	}
	if opts.CPUPercent > 0 {
		hostCfg.CPUPercent = opts.CPUPercent
	}
	if opts.IOMaxBandwidth.Value() > 0 {
		hostCfg.IOMaximumBandwidth = uint64(opts.IOMaxBandwidth.Value())
	}
	if opts.IOMaxIOps > 0 {
		hostCfg.IOMaximumIOps = opts.IOMaxIOps
	}

	// Ulimits
	if opts.Ulimits != nil && opts.Ulimits.Len() > 0 {
		hostCfg.Ulimits = opts.Ulimits.GetAll()
	}

	// Devices
	if opts.Devices != nil && opts.Devices.Len() > 0 {
		hostCfg.Devices = opts.Devices.GetAll()
	}
	if opts.GPUs != nil && opts.GPUs.Len() > 0 {
		hostCfg.DeviceRequests = opts.GPUs.GetAll()
	}
	if len(opts.DeviceCgroupRules) > 0 {
		for _, rule := range opts.DeviceCgroupRules {
			if err := validateDeviceCgroupRule(rule); err != nil {
				return nil, nil, nil, err
			}
		}
		hostCfg.DeviceCgroupRules = opts.DeviceCgroupRules
	}

	// Annotations
	if opts.Annotations != nil && opts.Annotations.Len() > 0 {
		hostCfg.Annotations = opts.Annotations.GetAll()
	}

	// Sysctls
	if opts.Sysctls != nil && opts.Sysctls.Len() > 0 {
		hostCfg.Sysctls = opts.Sysctls.GetAll()
	}

	// Storage options
	if opts.ReadOnly {
		hostCfg.ReadonlyRootfs = true
	}
	if len(opts.VolumesFrom) > 0 {
		hostCfg.VolumesFrom = opts.VolumesFrom
	}
	if len(opts.StorageOpt) > 0 {
		storageOpts, err := parseStorageOpts(opts.StorageOpt)
		if err != nil {
			return nil, nil, nil, err
		}
		hostCfg.StorageOpt = storageOpts
	}
	if len(opts.Tmpfs) > 0 {
		hostCfg.Tmpfs = make(map[string]string)
		for _, tmpfs := range opts.Tmpfs {
			// Parse "path" or "path:options"
			parts := strings.SplitN(tmpfs, ":", 2)
			path := parts[0]
			options := ""
			if len(parts) == 2 {
				options = parts[1]
			}
			hostCfg.Tmpfs[path] = options
		}
	}

	// Advanced mounts (--mount flag)
	if opts.Mounts != nil && opts.Mounts.Len() > 0 {
		hostCfg.Mounts = append(hostCfg.Mounts, opts.Mounts.GetAll()...)
	}

	// Parse user-provided volumes (via -v flag) as Binds, resolving relative paths
	if len(opts.Volumes) > 0 {
		binds := make([]string, 0, len(opts.Volumes))
		for _, v := range opts.Volumes {
			binds = append(binds, resolveVolumePath(v))
		}
		hostCfg.Binds = binds
	}

	// Process and runtime options
	if opts.Restart != "" {
		restartPolicy, err := parseRestartPolicy(opts.Restart)
		if err != nil {
			return nil, nil, nil, err
		}
		hostCfg.RestartPolicy = restartPolicy
	}
	if opts.StopSignal != "" {
		cfg.StopSignal = opts.StopSignal
	}
	if flags != nil && flags.Changed("stop-timeout") {
		cfg.StopTimeout = &opts.StopTimeout
	} else if opts.StopTimeout > 0 {
		cfg.StopTimeout = &opts.StopTimeout
	}

	// Validate --rm and --restart conflict
	if opts.AutoRemove && opts.Restart != "" && opts.Restart != "no" {
		return nil, nil, nil, fmt.Errorf("conflicting options: cannot specify both --restart and --rm")
	}

	// When allocating stdin in attached mode, close stdin at client disconnect
	if cfg.OpenStdin && cfg.AttachStdin {
		cfg.StdinOnce = true
	}

	// Use flags.Changed for --init and --stop-timeout to distinguish "not set" from "set to zero"
	if flags != nil && flags.Changed("init") {
		hostCfg.Init = &opts.Init
	} else if opts.Init {
		hostCfg.Init = &opts.Init
	}

	// Add port mappings from PortOpts
	if opts.Publish != nil && opts.Publish.Len() > 0 {
		if cfg.ExposedPorts == nil {
			cfg.ExposedPorts = opts.Publish.GetExposedPorts()
		} else {
			for p, v := range opts.Publish.GetExposedPorts() {
				cfg.ExposedPorts[p] = v
			}
		}
		hostCfg.PortBindings = opts.Publish.GetPortBindings()
	}

	// Network config — use NetworkMode from NetMode for HostConfig,
	// and parseNetworkOpts for advanced EndpointsConfig
	networkMode := opts.NetMode.NetworkMode()
	if networkMode != "" {
		hostCfg.NetworkMode = container.NetworkMode(networkMode)
	}

	// Validate MAC address if provided
	if opts.MacAddress != "" {
		if _, err := net.ParseMAC(strings.TrimSpace(opts.MacAddress)); err != nil {
			return nil, nil, nil, fmt.Errorf("%s is not a valid mac address", opts.MacAddress)
		}
	}

	epCfg, err := parseNetworkOpts(opts)
	if err != nil {
		return nil, nil, nil, err
	}

	networkCfg := &network.NetworkingConfig{
		EndpointsConfig: epCfg,
	}

	return cfg, hostCfg, networkCfg, nil
}

// ValidateFlags performs cross-field validation on the options.
func (opts *ContainerOptions) ValidateFlags() error {
	// Validate memory-swap requires memory to be set
	// (unless memory-swap is -1 for unlimited)
	if opts.MemorySwap.Value() > 0 && opts.Memory.Value() == 0 {
		return fmt.Errorf("--memory-swap requires --memory to be set")
	}

	// Validate memory-swap >= memory (unless -1 for unlimited)
	if opts.MemorySwap.Value() > 0 && opts.Memory.Value() > 0 {
		if opts.MemorySwap.Value() < opts.Memory.Value() {
			return fmt.Errorf("--memory-swap must be greater than or equal to --memory")
		}
	}

	// Validate swappiness range (0-100 or -1 for system default)
	if opts.Swappiness < -1 || opts.Swappiness > 100 {
		return fmt.Errorf("--memory-swappiness must be between -1 and 100")
	}

	// Validate blkio-weight range (10-1000 or 0 to disable)
	if opts.BlkioWeight != 0 && (opts.BlkioWeight < 10 || opts.BlkioWeight > 1000) {
		return fmt.Errorf("--blkio-weight must be between 10 and 1000, or 0 to disable")
	}

	// Validate OOM score adjustment range
	if opts.OOMScoreAdj < -1000 || opts.OOMScoreAdj > 1000 {
		return fmt.Errorf("--oom-score-adj must be between -1000 and 1000")
	}

	return nil
}

// parseSecurityOpts reads the content of seccomp profile files, handling special
// profile names (builtin, unconfined) and the no-new-privileges option.
func parseSecurityOpts(securityOpts []string) ([]string, error) {
	for key, opt := range securityOpts {
		k, v, ok := strings.Cut(opt, "=")
		if !ok && k != "no-new-privileges" {
			k, v, ok = strings.Cut(opt, ":")
		}
		if (!ok || v == "") && k != "no-new-privileges" {
			return securityOpts, fmt.Errorf("invalid --security-opt: %q", opt)
		}
		if k == "seccomp" {
			switch v {
			case seccompProfileDefault, seccompProfileUnconfined:
				// known special names for built-in profiles, nothing to do.
			default:
				// value may be a filename, in which case we send the profile's
				// content if it's valid JSON.
				f, err := os.ReadFile(v)
				if err != nil {
					return securityOpts, fmt.Errorf("opening seccomp profile (%s) failed: %w", v, err)
				}
				var b bytes.Buffer
				if err := json.Compact(&b, f); err != nil {
					return securityOpts, fmt.Errorf("compacting json for seccomp profile (%s) failed: %w", v, err)
				}
				securityOpts[key] = "seccomp=" + b.String()
			}
		}
	}
	return securityOpts, nil
}

// parseSystemPaths checks if `systempaths=unconfined` security option is set,
// and returns the `MaskedPaths` and `ReadonlyPaths` accordingly. An updated
// list of security options is returned with this option removed, because the
// `unconfined` option is handled client-side, and should not be sent to the daemon.
func parseSystemPaths(securityOpts []string) (filtered, maskedPaths, readonlyPaths []string) {
	filtered = securityOpts[:0]
	for _, opt := range securityOpts {
		if opt == "systempaths=unconfined" {
			maskedPaths = []string{}
			readonlyPaths = []string{}
		} else {
			filtered = append(filtered, opt)
		}
	}
	return filtered, maskedPaths, readonlyPaths
}

// parseLoggingOpts converts logging opts and validates that no opts are provided
// when the driver is "none".
func parseLoggingOpts(loggingDriver string, loggingOpts []string) (map[string]string, error) {
	if loggingDriver == "none" && len(loggingOpts) > 0 {
		return nil, fmt.Errorf("invalid logging opts for driver %s", loggingDriver)
	}
	if len(loggingOpts) == 0 {
		return nil, nil
	}
	loggingOptsMap := make(map[string]string, len(loggingOpts))
	for _, lo := range loggingOpts {
		k, v, _ := strings.Cut(lo, "=")
		loggingOptsMap[k] = v
	}
	return loggingOptsMap, nil
}

// parseStorageOpts parses storage options per container into a map,
// validating that each option contains a '=' separator.
func parseStorageOpts(storageOpts []string) (map[string]string, error) {
	m := make(map[string]string)
	for _, option := range storageOpts {
		k, v, ok := strings.Cut(option, "=")
		if !ok {
			return nil, fmt.Errorf("invalid storage option %q: missing '=' separator", option)
		}
		m[k] = v
	}
	return m, nil
}

// validateDeviceCgroupRule validates a device cgroup rule string format.
// The format must be: 'type major:minor mode'
func validateDeviceCgroupRule(val string) error {
	if deviceCgroupRuleRegexp.MatchString(val) {
		return nil
	}
	return fmt.Errorf("invalid device cgroup format '%s'", val)
}

// resolveVolumePath converts relative paths (starting with ./) in volume specs
// to absolute paths.
func resolveVolumePath(bind string) string {
	if hostPart, targetPath, ok := strings.Cut(bind, ":"); ok {
		if !filepath.IsAbs(hostPart) && strings.HasPrefix(hostPart, ".") {
			if absHostPart, err := filepath.Abs(hostPart); err == nil {
				hostPart = absHostPart
			}
		}
		return hostPart + ":" + targetPath
	}
	return bind
}

// ResolveAgentName returns the agent name, generating one if not provided.
// This is a helper that commands can use for generating random names.
func ResolveAgentName(agent string, generateRandom func() string) string {
	if agent != "" {
		return agent
	}
	if generateRandom != nil {
		return generateRandom()
	}
	return ""
}

// ParseLabelsToMap converts a slice of "key=value" strings to a map.
// This is useful for merging user labels with clawker labels.
func ParseLabelsToMap(labels []string) map[string]string {
	result := make(map[string]string)
	for _, l := range labels {
		parts := strings.SplitN(l, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		} else {
			result[parts[0]] = ""
		}
	}
	return result
}

// MergeLabels merges user-provided labels with base labels.
// Base labels take precedence (clawker labels should not be overwritten).
func MergeLabels(baseLabels, userLabels map[string]string) map[string]string {
	result := make(map[string]string)
	// First add user labels
	for k, v := range userLabels {
		result[k] = v
	}
	// Then add base labels (overwrites user labels if conflict)
	for k, v := range baseLabels {
		result[k] = v
	}
	return result
}

// FormatContainerName formats a container name using clawker conventions.
func FormatContainerName(project, agent string) string {
	return fmt.Sprintf("clawker.%s.%s", project, agent)
}

// -----------------------------------------------------------------------------
// ListOpts, MapOpts, PortOpts - Custom pflag.Value types for container options
// These are CLI flag types, not API types, so they belong with command options.
// -----------------------------------------------------------------------------

// ListOpts holds a list of values for repeatable string flags (like -e VAR1 -e VAR2).
// Implements pflag.Value interface.
type ListOpts struct {
	values    *[]string
	validator func(string) (string, error)
}

// NewListOpts creates a new ListOpts with optional validator.
// If validator is nil, values are accepted as-is.
func NewListOpts(validator func(string) (string, error)) *ListOpts {
	return &ListOpts{
		values:    new([]string),
		validator: validator,
	}
}

// NewListOptsRef creates a new ListOpts that stores values in the provided slice.
// This is useful when you want to reuse an existing slice.
func NewListOptsRef(values *[]string, validator func(string) (string, error)) *ListOpts {
	return &ListOpts{
		values:    values,
		validator: validator,
	}
}

// String returns a comma-separated string of all values.
func (o *ListOpts) String() string {
	if o.values == nil || len(*o.values) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", *o.values)
}

// Set adds a value to the list after validation.
func (o *ListOpts) Set(value string) error {
	if o.values == nil {
		o.values = new([]string)
	}
	if o.validator != nil {
		validated, err := o.validator(value)
		if err != nil {
			return err
		}
		*o.values = append(*o.values, validated)
	} else {
		*o.values = append(*o.values, value)
	}
	return nil
}

// Type returns the type string for pflag.
func (o *ListOpts) Type() string {
	return "list"
}

// GetAll returns all values in the list.
func (o *ListOpts) GetAll() []string {
	if o.values == nil {
		return nil
	}
	return *o.values
}

// Len returns the number of values in the list.
func (o *ListOpts) Len() int {
	if o.values == nil {
		return 0
	}
	return len(*o.values)
}

// MapOpts holds key=value pairs for flags like --label or --env.
// Implements pflag.Value interface.
type MapOpts struct {
	values    map[string]string
	validator func(key, value string) error
}

// NewMapOpts creates a new MapOpts with optional validator.
func NewMapOpts(validator func(key, value string) error) *MapOpts {
	return &MapOpts{
		values:    make(map[string]string),
		validator: validator,
	}
}

// String returns a string representation of the map.
func (o *MapOpts) String() string {
	if o.values == nil || len(o.values) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", o.values)
}

// Set parses a key=value string and adds it to the map.
func (o *MapOpts) Set(value string) error {
	if o.values == nil {
		o.values = make(map[string]string)
	}

	// Parse key=value or key (empty value)
	var k, v string
	if idx := indexRune(value, '='); idx >= 0 {
		k = value[:idx]
		v = value[idx+1:]
	} else {
		k = value
		v = ""
	}

	if o.validator != nil {
		if err := o.validator(k, v); err != nil {
			return err
		}
	}

	o.values[k] = v
	return nil
}

// Type returns the type string for pflag.
func (o *MapOpts) Type() string {
	return "map"
}

// GetAll returns all key-value pairs as a map.
func (o *MapOpts) GetAll() map[string]string {
	if o.values == nil {
		return nil
	}
	return o.values
}

// Get returns the value for a key.
func (o *MapOpts) Get(key string) (string, bool) {
	if o.values == nil {
		return "", false
	}
	v, ok := o.values[key]
	return v, ok
}

// Len returns the number of key-value pairs.
func (o *MapOpts) Len() int {
	if o.values == nil {
		return 0
	}
	return len(o.values)
}

// indexRune returns the index of the first occurrence of r in s, or -1 if not found.
func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// PortOpts holds port mappings for -p/--publish flags.
// Implements pflag.Value interface.
type PortOpts struct {
	exposedPorts network.PortSet
	portBindings network.PortMap
}

// NewPortOpts creates a new PortOpts.
func NewPortOpts() *PortOpts {
	return &PortOpts{
		exposedPorts: make(network.PortSet),
		portBindings: make(network.PortMap),
	}
}

// String returns a string representation of the port mappings.
func (o *PortOpts) String() string {
	if o.portBindings == nil || len(o.portBindings) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", o.portBindings)
}

// Set parses a port mapping spec (e.g., "8080:80", "127.0.0.1:8080:80/tcp")
// and adds it to the port mappings.
func (o *PortOpts) Set(value string) error {
	if o.exposedPorts == nil {
		o.exposedPorts = make(network.PortSet)
	}
	if o.portBindings == nil {
		o.portBindings = make(network.PortMap)
	}

	portMappings, err := nat.ParsePortSpec(value)
	if err != nil {
		return fmt.Errorf("invalid port mapping %q: %w", value, err)
	}

	for _, pm := range portMappings {
		// Convert nat.Port to network.Port
		// nat.Port is a string like "80/tcp"
		netPort, err := network.ParsePort(string(pm.Port))
		if err != nil {
			return fmt.Errorf("invalid port %q: %w", pm.Port, err)
		}

		o.exposedPorts[netPort] = struct{}{}

		// Convert nat.PortBinding to network.PortBinding
		// HostIP needs to be netip.Addr; HostPort stays as string
		var hostIP netip.Addr
		if pm.Binding.HostIP != "" {
			hostIP, err = netip.ParseAddr(pm.Binding.HostIP)
			if err != nil {
				return fmt.Errorf("invalid host IP %q: %w", pm.Binding.HostIP, err)
			}
		}
		binding := network.PortBinding{
			HostIP:   hostIP,
			HostPort: pm.Binding.HostPort,
		}
		o.portBindings[netPort] = append(o.portBindings[netPort], binding)
	}

	return nil
}

// Type returns the type string for pflag.
func (o *PortOpts) Type() string {
	return "port"
}

// GetExposedPorts returns the exposed ports set for container config.
func (o *PortOpts) GetExposedPorts() network.PortSet {
	if o.exposedPorts == nil {
		return make(network.PortSet)
	}
	return o.exposedPorts
}

// GetPortBindings returns the port bindings for host config.
func (o *PortOpts) GetPortBindings() network.PortMap {
	if o.portBindings == nil {
		return make(network.PortMap)
	}
	return o.portBindings
}

// Len returns the number of port bindings.
func (o *PortOpts) Len() int {
	if o.portBindings == nil {
		return 0
	}
	return len(o.portBindings)
}

// GetAsStrings returns the port mappings as a slice of strings in "hostPort:containerPort" format.
// This is primarily useful for testing and comparison.
func (o *PortOpts) GetAsStrings() []string {
	if o.portBindings == nil || len(o.portBindings) == 0 {
		return nil
	}
	var result []string
	for containerPort, bindings := range o.portBindings {
		for _, binding := range bindings {
			var s string
			if binding.HostIP.IsValid() {
				s = fmt.Sprintf("%s:%s:%d", binding.HostIP, binding.HostPort, containerPort.Num())
			} else if binding.HostPort != "" {
				s = fmt.Sprintf("%s:%d", binding.HostPort, containerPort.Num())
			} else {
				s = fmt.Sprintf("%d", containerPort.Num())
			}
			if containerPort.Proto() != network.TCP {
				s = s + "/" + string(containerPort.Proto())
			}
			result = append(result, s)
		}
	}
	return result
}

// validateAttach validates an attach target (must be stdin, stdout, or stderr).
func validateAttach(val string) (string, error) {
	normalized := strings.ToLower(val)
	switch normalized {
	case "stdin", "stdout", "stderr":
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid attach target %q: must be stdin, stdout, or stderr", val)
	}
}

// parseRestartPolicy parses a restart policy string into a RestartPolicy.
// Valid formats: "no", "always", "unless-stopped", "on-failure", "on-failure:N"
func parseRestartPolicy(policy string) (container.RestartPolicy, error) {
	if policy == "" {
		return container.RestartPolicy{}, nil
	}

	p := container.RestartPolicy{}
	name, maxRetries, hasColon := strings.Cut(policy, ":")

	if hasColon && name == "" {
		return container.RestartPolicy{}, fmt.Errorf("invalid restart policy format: no policy provided before colon")
	}

	if maxRetries != "" {
		count, err := strconv.Atoi(maxRetries)
		if err != nil {
			return container.RestartPolicy{}, fmt.Errorf("invalid restart policy format: maximum retry count must be an integer")
		}
		p.MaximumRetryCount = count
	}

	p.Name = container.RestartPolicyMode(name)
	return p, nil
}

// readEnvFile reads an environment file and returns lines as KEY=VALUE pairs.
// Lines starting with # are comments. Blank lines are skipped.
func readEnvFile(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

// readLabelFile reads a label file and returns lines as KEY=VALUE pairs.
// Lines starting with # are comments. Blank lines are skipped.
func readLabelFile(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}
