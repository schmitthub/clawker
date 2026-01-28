# Container Options Pattern: Key Dependencies Summary

## For Main PRD Inclusion

### Dependency Overview

Docker CLI's container options DRY pattern relies on **7 core external dependencies** that work together to create a reusable, type-safe flag parsing system. The pattern achieves code reuse by separating flag definition (happens once in `addFlags()`) from command implementation (happens in each command).

**Critical dependencies:**

1. **spf13/pflag (v1.0.10)** - Flag parsing library providing the `FlagSet` and `pflag.Value` interface. All custom option types (ListOpts, MemBytes, MountOpt, etc.) implement this interface, allowing pflag to call `Set(string)` during flag parsing for validation and type conversion.

2. **spf13/cobra (v1.10.2)** - Command framework providing hierarchical command structure. Each command (run, create, etc.) registers itself and receives a `pflag.FlagSet` from cobra. Cobra handles help generation from pflag annotations.

3. **moby/moby/api/types (v1.53.0)** - Three packages providing the output data structures:
   - `container` - Config and HostConfig types holding the container specification
   - `network` - PortSet, PortMap, EndpointSettings for network configuration
   - `mount` - Mount type for volume/mount specifications
   These are the official Docker API types matching the daemon's expectations.

4. **docker/go-connections (v0.6.0)** - Network utilities providing `nat.ParsePortSpecs()` to convert port specifications like "8080:80/tcp" into PortSet and PortMap structures used by the network API types.

5. **docker/go-units (v0.5.0)** - Value parsing providing `units.RAMInBytes()` and `units.ParseUlimit()` for converting human-readable sizes ("1GB", "512MB") and ulimit specifications to numeric values. Implements binary sizing (1024-based).

6. **container-device-interface (v1.1.0)** - Provides `cdi.IsQualifiedName()` for validating CDI device names (e.g., "nvidia.com/gpu=0"), enabling modern GPU and accelerator device allocation alongside legacy /dev paths.

7. **internal Docker CLI packages** - `opts/` package (custom Value type implementations), `internal/volumespec` (volume parsing), `internal/lazyregexp` (lazy regex compilation), `pkg/kvfile` (environment file reading).

### Data Flow Pattern

```
CLI Input → pflag parsing → containerOptions struct → parse() function → API types
    ↓            ↓                   ↓                      ↓              ↓
"1GB"     Set() validates      Raw string values    units.RAMInBytes()  int64 bytes
"8080:80"  & converts         Stored temporarily   nat.ParsePortSpecs() PortSet/PortMap
"/host:/c" using custom       in containerOptions   mount parsing        mount.Mount
           Value types        ready for assembly    network validation   container.Config
```

### Why This Pattern Works

1. **Code Reuse**: `addFlags()` and `parse()` are called by multiple commands (run, create, etc.), eliminating duplication of 68+ flag definitions.

2. **Type Safety**: Custom `pflag.Value` implementations provide early validation during flag parsing. Invalid input rejected before parse() is even called.

3. **Separation of Concerns**:
   - Flag registration (addFlags, pflag)
   - Value parsing (Value.Set(), go-units, go-connections)
   - Type conversion (parse function)
   - Command execution (cobra)

4. **Metadata Support**: pflag annotations enable version checking (which API version supports a flag) and OS-specific flag filtering (Windows vs Linux).

### Replaceability and Adaptation

**Not replaceable (core):**
- moby/moby API types - These define the Docker daemon's expectations
- spf13/pflag - Deep integration in flag system (though Go's built-in flag package exists as minimal alternative)

**Replaceable with effort:**
- docker/go-units - Could write custom size parser (100+ LOC)
- docker/go-connections - Could write custom port parser
- spf13/cobra - Could use simpler command framework but lose features

**Optional:**
- container-device-interface - Only used in one validation check; could replace with regex

### Key Implementation Details to Replicate

1. **pflag.Value interface** (3 methods: Set, String, Type)
   - Set() does validation during parsing
   - String() formats current value for display
   - Type() returns type name for help text

2. **Custom Value types** for complex inputs
   - ListOpts for repeatable flags (-e, -v, -p)
   - MapOpts for key-value flags (--sysctl, --log-opt)
   - MemBytes, NanoCPUs for sized values
   - MountOpt, NetworkOpt for CSV-formatted values
   - Each implements pflag.Value

3. **Separate parse() function**
   - Converts containerOptions to API types
   - Additional validation (cross-field checks)
   - Device resolution, network mode validation
   - Assembles final Config, HostConfig, NetworkingConfig

4. **Validators** (optional function per custom type)
   - Runs during Value.Set()
   - Examples: ValidateEnv, ValidateLabel, ValidateIPAddress
   - Enable early rejection of invalid input

### Lessons Learned from Docker CLI

1. **Version Annotations Matter**: Flags annotated with API version enable automatic compatibility checking and help text generation.

2. **Validation Timing**: Validate during Set() (early rejection), not in parse() (too late). Parse() should only do assembly and cross-field validation.

3. **Support Both Formats**: Accept both modern formats (--mount, --network) and legacy formats (--volume, --net) for backward compatibility.

4. **Binary vs Decimal Sizing**: Docker uses binary sizing (1GB = 1024³) not decimal (1GB = 1000³). Document this clearly.

5. **Device Naming Changes**: Support both /dev/xxx paths and modern CDI names (vendor.com/class=device) for future-proofing.

---

## For Architects Building Similar CLIs

### Recommended Tech Stack

**Go** (Docker's choice):
- Flags: pflag + cobra (battle-tested, no alternatives)
- Types: Define your own matching your API
- Units: go-units (Docker-compatible)
- Networks: go-connections or custom

**Python**:
- Flags: click (industry standard)
- Units: humanize + pint
- Commands: typer (Starlette-based, modern)

**Rust**:
- Flags: clap (most powerful)
- Commands: clap (hierarchical)
- Units: bytesize + humanize crates

**TypeScript**:
- Flags: yargs or commander
- Commands: oclif (plugin system like Docker)
- Units: bytes + humanize packages

### Essential Pattern Elements

1. **Separate flag registration from command logic**
2. **Implement type-safe value parsing with early validation**
3. **Use a command framework supporting hierarchical commands**
4. **Define custom types for complex inputs (ports, mounts, networks)**
5. **Centralize parsing logic for reuse across multiple commands**
6. **Support version/OS-specific flags through metadata**

---

