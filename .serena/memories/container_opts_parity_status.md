# Container Options Parity Implementation Status

## Status: COMPLETE (except CDI)

## What was implemented

### New file: `internal/cmd/container/opts/network.go`
- `NetworkAttachmentOpts` struct with full Docker CLI fields
- `NetworkOpt` type implementing pflag.Value (String, Set, Type, Value, NetworkMode)
- `parseNetworkOpts()` - converts NetworkOpt to endpoint settings
- `applyLegacyNetworkOpts()` - merges legacy --ip, --ip6, --mac-address, --network-alias, --link flags
- `buildEndpointSettings()` - builds network.EndpointSettings from NetworkAttachmentOpts
- `isEndpointSettingsZero()` - backward-compat check for omitting empty endpoint config

### Modified: `internal/cmd/container/opts/opts.go`
- `Network string` → `NetMode NetworkOpt` (advanced multi-network support)
- Added fields: `CPUCount`, `CPUPercent`, `IOMaxBandwidth`, `IOMaxIOps` (Windows)
- `BuildConfigs` signature changed: `(flags *pflag.FlagSet, mounts []mount.Mount, projectCfg *config.Config)`
- Added validations: health check negatives, namespace mode .Valid(), logging "none", storage opt "=", device cgroup rule regex
- Added helpers: `parseSecurityOpts`, `parseSystemPaths`, `parseLoggingOpts`, `parseStorageOpts`, `validateDeviceCgroupRule`, `resolveVolumePath`
- Entrypoint empty handling via `flags.Changed("entrypoint")`
- Port range support via `network.ParsePortRange()`
- StdinOnce handling
- `flags.Changed` for --init and --stop-timeout

### Modified callers
- `run/run.go` and `create/create.go`: Added `flags *pflag.FlagSet` field, pass to BuildConfigs
- All test files updated for new BuildConfigs signature and NetMode field

### New unit tests (opts_test.go)
- TestNetworkOpt_* (6 test functions)
- TestContainerOptions_BuildConfigs_HealthCheckNegatives
- TestContainerOptions_ValidateDeviceCgroupRule
- TestContainerOptions_ValidateNamespaceModes
- TestContainerOptions_BuildConfigs_StdinOnce
- TestContainerOptions_BuildConfigs_PortRange
- TestContainerOptions_BuildConfigs_LoggingNoneValidation
- TestContainerOptions_BuildConfigs_StorageOptValidation
- TestContainerOptions_BuildConfigs_SecurityOpts
- TestContainerOptions_BuildConfigs_AdvancedNetwork
- TestContainerOptions_BuildConfigs_EntrypointEmpty
- TestContainerOptions_BuildConfigs_DeviceCgroupRuleValidation
- TestContainerOptions_NetworkOptFlag

### New/updated acceptance tests
- opts-expose-range.txtar (NEW)
- opts-logging-none-validation.txtar (NEW)
- opts-security-systempaths.txtar (NEW)
- opts-device-cgroup-rule-validation.txtar (NEW)
- opts-namespace-validation.txtar (NEW)
- opts-network-multi.txtar (NEW)
- opts-healthcheck-negative-validation.txtar (UPDATED - now tests rejection)
- opts-entrypoint-clear.txtar (UPDATED - tests [""] behavior)

## Not implemented
- CDI device support (requires external dependency `tags.cncf.io/container-device-interface`)

## Branch: a/container-opts
