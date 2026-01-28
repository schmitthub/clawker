# Quick Reference: Docker CLI Container Options Dependencies

## 7 Core Dependencies at a Glance

| Dependency | Version | Key Functions | Used For | Replaceability |
|-----------|---------|---------------|----------|---|
| **spf13/pflag** | 1.0.10 | `FlagSet.Var()`, `FlagSet.StringVar()`, `Value.Set/String/Type()` | Flag registration & parsing | ❌ Hard |
| **spf13/cobra** | 1.10.2 | `Command`, `RunE()`, `AddCommand()` | Command structure, hierarchy | ⚠️ Medium |
| **moby/moby/api/container** | 1.53.0 | `Config`, `HostConfig`, `Resources`, `DeviceMapping` | Container specification | ❌ Very Hard |
| **moby/moby/api/network** | 1.53.0 | `PortSet`, `PortMap`, `EndpointSettings`, `NetworkingConfig` | Network configuration | ❌ Very Hard |
| **moby/moby/api/mount** | 1.53.0 | `Mount`, `BindOptions`, `VolumeOptions` | Volume/mount specs | ❌ Very Hard |
| **docker/go-connections** | 0.6.0 | `nat.ParsePortSpecs()`, `ParsePort()` | Port parsing | ✅ Easy |
| **docker/go-units** | 0.5.0 | `RAMInBytes()`, `BytesSize()`, `ParseUlimit()` | Size/unit parsing | ✅ Easy |
| **container-device-interface** | 1.1.0 | `IsQualifiedName()` | CDI device validation | ✅ Easy |

## File Locations in Repository

```
cli/command/container/opts.go      ← Main pattern implementation (740 lines)
├─ addFlags() function             ← Flag registration (180 lines)
├─ parse() function                ← Type conversion (400 lines)
├─ parseNetworkOpts()              ← Network parsing (80 lines)
└─ parseNetworkAttachmentOpt()     ← Single network attachment (40 lines)

opts/                               ← Custom Value types
├─ opts.go                         ← ListOpts, MapOpts, MemBytes, NanoCPUs (500 lines)
├─ mount.go                        ← MountOpt with CSV parsing (200 lines)
├─ network.go                      ← NetworkOpt parsing (150 lines)
├─ gpus.go                         ← GpuOpts for GPU allocation (80 lines)
├─ ulimit.go                       ← UlimitOpt with units parsing (80 lines)
└─ other type files                ← Validators, utilities

cli/command/container/run.go       ← Example: Command using pattern (120 lines)
├─ Line 79: copts = addFlags(flags)
└─ Line 109: containerCfg, err := parse(flags, copts, serverInfo.OSType)

cli/command/container/create.go    ← Another example command (130 lines)
├─ Line 91: copts = addFlags(flags)
└─ Line 120: containerCfg, err := parse(flags, copts, serverInfo.OSType)
```

## Data Structure Hierarchy

```
containerOptions (intermediate form - holds strings/raw values)
├─ attach         opts.ListOpts         (validated strings)
├─ volumes        opts.ListOpts         (validated strings)
├─ env            opts.ListOpts         (validated ENV=VAL)
├─ mounts         opts.MountOpt         (parsed mount.Mount)
├─ memory         opts.MemBytes         (bytes from "1GB" string)
├─ cpus           opts.NanoCPUs         (nanos from "1.5" string)
├─ publish        opts.ListOpts         (validated port specs)
├─ netMode        opts.NetworkOpt       (parsed network attachments)
└─ ... 50+ more fields

        ↓ parse() converts to:

containerConfig
├─ Config           *container.Config
│  ├─ Image, Entrypoint, Cmd
│  ├─ Env, Labels, ExposedPorts
│  ├─ Hostname, Domainname
│  ├─ Tty, OpenStdin, HealthCheck
│  └─ User, WorkingDir, OnBuild
├─ HostConfig       *container.HostConfig
│  ├─ Binds, Mounts
│  ├─ NetworkMode, IpcMode, PidMode
│  ├─ PortBindings, PublishAllPorts
│  ├─ Resources (memory, CPU, blkio)
│  ├─ Devices, DeviceRequests, Ulimits
│  ├─ CapAdd, CapDrop, SecurityOpt
│  ├─ RestartPolicy, LogConfig
│  └─ ... 50+ more fields
└─ NetworkingConfig *network.NetworkingConfig
   └─ EndpointsConfig map[string]*network.EndpointSettings
      ├─ IPAddress, IPPrefixLen
      ├─ Gateway, MacAddress
      ├─ IPAMConfig, Aliases
      └─ Links
```

## The 3-Method pflag.Value Interface

Every custom type (ListOpts, MemBytes, MountOpt, etc.) implements:

```go
type Value interface {
    String() string        // Format current value for display
    Set(string) error      // Parse CLI input, return error if invalid
    Type() string          // Return type name for help text
}
```

### Example: MemBytes Implementation

```go
type MemBytes int64

func (m *MemBytes) Set(value string) error {
    val, err := units.RAMInBytes(value)  // "1GB" → 1073741824
    *m = MemBytes(val)
    return err
}

func (m *MemBytes) String() string {
    return units.BytesSize(float64(m.Value()))  // 1073741824 → "1GB"
}

func (m *MemBytes) Type() string {
    return "bytes"  // Shown in help: --memory bytes
}
```

## Parsing Process Flow

```
1. Flag Definition (addFlags)
   └─ flags.VarP(&copts.memory, "memory", "m", "Memory limit")

2. User Input
   └─ docker run -m 1GB image

3. pflag Parsing
   └─ Calls copts.memory.Set("1GB")
      └─ MemBytes.Set() calls units.RAMInBytes("1GB")
         └─ Returns 1073741824 (error if invalid format)
         └─ If error: Stop, print error, exit
         └─ If success: Store value, continue

4. Convert to API Types (parse function)
   └─ resources.Memory = copts.memory.Value()
   └─ config.Config.Memory = int64(copts.memory)

5. Send to Daemon
   └─ POST /containers/create with Config, HostConfig, NetworkingConfig
```

## Key Dependencies by Use Case

### Need to Parse...

| Input Type | Dependency | Function | Example |
|-----------|-----------|----------|---------|
| Memory sizes | docker/go-units | `RAMInBytes()` | "1GB" → 1073741824 |
| Port specs | docker/go-connections | `nat.ParsePortSpecs()` | "8080:80" → PortMap |
| Ulimits | docker/go-units | `ParseUlimit()` | "nofile=1024:2048" |
| Device paths | container-device-interface | `IsQualifiedName()` | "nvidia.com/gpu=0" |
| CSV mounts | Built-in encoding/csv | `csv.Reader` | "type=bind,src=..." |
| Environment | Built-in string ops | Split on `=` | "VAR=value" |

### Need to Generate...

| Output Type | Dependency | Type | Example |
|------------|-----------|------|---------|
| Container spec | moby/moby/api | `container.Config` | ImageID, Cmd, Env, etc. |
| Runtime config | moby/moby/api | `container.HostConfig` | Memory, CPUs, Devices, etc. |
| Network config | moby/moby/api | `network.NetworkingConfig` | Ports, endpoints, aliases |
| Help text | spf13/pflag | Annotations | "version": ["1.40"] |
| Completion | spf13/cobra | `SliceValue` interface | Suggest values for repeated flags |

## Validators Provided by opts Package

```go
// String format validators
ValidateEnv(value string) (string, error)           // KEY=VALUE format
ValidateLabel(value string) (string, error)         // KEY=VALUE format
ValidateLink(value string) (string, error)          // container:alias format
ValidateExtraHost(value string) (string, error)     // host:ip format

// Network validators
ValidateIPAddress(value string) (string, error)     // IP address format
ValidateDNSSearch(value string) (string, error)     // DNS domain format

// Device validators
ValidateThrottleBpsDevice(value string) (string, error)    // device:rate format
ValidateThrottleIOpsDevice(value string) (string, error)   // device:iops format
ValidateWeightDevice(value string) (string, error)         // device:weight format

// Used like:
copts.env = opts.NewListOpts(opts.ValidateEnv)
// Now each -e flag value validated before being added to list
```

## Common Flag Registration Patterns

```go
// Simple string flag
flags.StringVar(&copts.hostname, "hostname", "h", "Container host name")

// Boolean flag
flags.BoolVar(&copts.privileged, "privileged", false, "Extended privileges")

// Repeatable flag with validator
flags.Var(&copts.env, "env", "e", "Set environment variables")
// Can use: -e VAR=value -e VAR2=value2

// Custom type with custom validator
copts.memory := opts.MemBytes(0)
flags.VarP(&copts.memory, "memory", "m", "Memory limit")

// With API version annotation
flags.SetAnnotation("gpus", "version", []string{"1.40"})

// With OS type annotation
flags.SetAnnotation("cpu-count", "ostype", []string{"windows"})

// Deprecated flag
flags.MarkDeprecated("kernel-memory", "and no longer supported by the kernel")

// Hidden flag (legacy)
flags.MarkHidden("net")
```

## Transitive Dependency Tree

```
docker-cli
├─ spf13/pflag v1.0.10 (no dependencies)
├─ spf13/cobra v1.10.2
│  └─ spf13/pflag v1.0.10 (already listed)
├─ moby/moby/api v1.53.0
│  └─ moby/moby (and several others)
├─ moby/moby/client v0.2.2
│  └─ moby/moby/api (already listed)
├─ docker/go-connections v0.6.0 (no additional deps)
├─ docker/go-units v0.5.0 (no additional deps)
└─ container-device-interface v1.1.0 (no additional deps)

Key insight: Most dependencies are independent (no circular deps)
```

## Performance Characteristics

### Flag Parsing Speed
- pflag.Parse() is O(n) where n = number of flags provided
- Validation happens inline during Set()
- Early rejection avoids unnecessary work

### Memory Usage
- containerOptions struct: ~2KB (68+ fields, mostly strings/ints)
- ListOpts grows with repeated flags (-e VAR=val repeated 100x → ~4KB)
- Final API types (Config/HostConfig) similar size

### Parsing Order
1. Flag registration: addFlags() - O(1), happens once at startup
2. User input parsing: pflag.Parse() - O(n) where n = number of flags
3. Type conversion: parse() - O(n) where n = number of fields with values
4. API call: Serialize and send to daemon - O(n) network time

## Testing Patterns

The Docker CLI uses a fake client pattern:

```go
// Create fake CLI with mock client
fakeCLI := test.NewFakeCli(&fakeClient{
    createContainerFunc: func(opts client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
        return client.ContainerCreateResult{ID: "test-id"}, nil
    },
})

// Run command with test flags
cmd := newRunCommand(fakeCLI)
cmd.SetArgs([]string{"--memory", "512MB", "--cpus", "1.5", "image"})
assert.NilError(t, cmd.Execute())
```

Key insight: Test by:
1. Setting command flags
2. Calling Execute()
3. Verifying createContainer was called with correct Config/HostConfig

## Common Gotchas

1. **Binary vs Decimal Sizing**: go-units uses 1024, not 1000
   - "1GB" = 1024³ = 1,073,741,824 bytes (not 10^9)
   - Document this clearly!

2. **Port Protocol Defaults**: "8080" defaults to "tcp", not both
   - Explicit "8080/udp" for UDP

3. **Memory Swap**: "-1" means unlimited, not 1 byte
   - Special case in MemSwapBytes.Set()

4. **Network Mode Validation**: Can't use network-scoped options with "host" mode
   - Validation in parseNetworkOpts() catches this

5. **Device Resolution**: /dev paths need special handling on Windows
   - parseDevice() checks serverOS parameter

## Version Compatibility Notes

- **pflag 1.0.x**: Stable, no breaking changes expected
- **cobra 1.x**: Stable, on v1.10.2
- **moby/moby**: CalVer tracking (v1.53.0 = API version 1.53)
- **go-units**: Stable since Docker 1.0
- **go-connections**: Stable, minimal changes
- **CDI**: Still evolving (1.1.0), but isolated usage

## Migration/Replacement Effort Estimate

| Dependency | Effort to Replace | Notes |
|-----------|-------------------|-------|
| pflag | 40-60 hours | Would need flag.FlagSet equivalent, custom Value infrastructure |
| cobra | 20-40 hours | Could use simpler framework, lose help generation |
| moby/moby types | Impossible | These define the daemon API contract |
| go-connections | 10-15 hours | Write custom port parser + test suite |
| go-units | 5-10 hours | Write custom size parser (watch binary vs decimal!) |
| CDI | 0.5-1 hour | Just one function call, easy regex replacement |

## See Also

- Full analysis: `dependency-catalog.md` (6000+ words, all details)
- Architecture documentation: See CLAUDE.md in docker-cli repo
- Source code: `cli/command/container/opts.go` (main implementation)
