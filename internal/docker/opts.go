package docker

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/blkiodev"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

// MemBytes is a type for human readable memory bytes (like 128M, 2g, etc)
type MemBytes int64

// String returns the string format of the human readable memory bytes
func (m *MemBytes) String() string {
	// NOTE: In spf13/pflag/flag.go, "0" is considered as "zero value" while "0 B" is not.
	// We return "0" in case value is 0 here so that the default value is hidden.
	// (Sometimes "default 0 B" is actually misleading)
	if m.Value() != 0 {
		return units.BytesSize(float64(m.Value()))
	}
	return "0"
}

// Set sets the value of the MemBytes by passing a string
func (m *MemBytes) Set(value string) error {
	val, err := units.RAMInBytes(value)
	*m = MemBytes(val)
	return err
}

// Type returns the type
func (*MemBytes) Type() string {
	return "bytes"
}

// Value returns the value in int64
func (m *MemBytes) Value() int64 {
	return int64(*m)
}

// UnmarshalJSON is the customized unmarshaler for MemBytes
func (m *MemBytes) UnmarshalJSON(s []byte) error {
	if len(s) <= 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("invalid size: %q", s)
	}
	val, err := units.RAMInBytes(string(s[1 : len(s)-1]))
	*m = MemBytes(val)
	return err
}

// MemSwapBytes is a type for human readable memory bytes (like 128M, 2g, etc).
// It differs from MemBytes in that -1 is valid and the default.
type MemSwapBytes int64

// Set sets the value of the MemSwapBytes by passing a string
func (m *MemSwapBytes) Set(value string) error {
	if value == "-1" {
		*m = MemSwapBytes(-1)
		return nil
	}
	val, err := units.RAMInBytes(value)
	*m = MemSwapBytes(val)
	return err
}

// Type returns the type
func (*MemSwapBytes) Type() string {
	return "bytes"
}

// Value returns the value in int64
func (m *MemSwapBytes) Value() int64 {
	return int64(*m)
}

func (m *MemSwapBytes) String() string {
	b := MemBytes(*m)
	return b.String()
}

// UnmarshalJSON is the customized unmarshaler for MemSwapBytes
func (m *MemSwapBytes) UnmarshalJSON(s []byte) error {
	b := MemBytes(*m)
	return b.UnmarshalJSON(s)
}

// NanoCPUs is a type for fixed point fractional number.
type NanoCPUs int64

// String returns the string format of the number
func (c *NanoCPUs) String() string {
	if *c == 0 {
		return ""
	}
	return big.NewRat(c.Value(), 1e9).FloatString(3)
}

// Set sets the value of the NanoCPU by passing a string
func (c *NanoCPUs) Set(value string) error {
	cpus, err := ParseCPUs(value)
	*c = NanoCPUs(cpus)
	return err
}

// Type returns the type
func (*NanoCPUs) Type() string {
	return "decimal"
}

// Value returns the value in int64
func (c *NanoCPUs) Value() int64 {
	return int64(*c)
}

// ParseCPUs takes a string ratio and returns an integer value of nano cpus
func ParseCPUs(value string) (int64, error) {
	cpu, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, fmt.Errorf("failed to parse %v as a rational number", value)
	}
	nano := cpu.Mul(cpu, big.NewRat(1e9, 1))
	if !nano.IsInt() {
		return 0, errors.New("value is too precise")
	}
	return nano.Num().Int64(), nil
}

// -----------------------------------------------------------------------------
// UlimitOpt - Parse ulimit flags (name=soft:hard)
// -----------------------------------------------------------------------------

// UlimitOpt holds a list of ulimits parsed from --ulimit flags.
// Implements pflag.Value interface.
type UlimitOpt struct {
	values []*container.Ulimit
}

// NewUlimitOpt creates a new UlimitOpt.
func NewUlimitOpt() *UlimitOpt {
	return &UlimitOpt{}
}

// String returns a string representation.
func (o *UlimitOpt) String() string {
	if len(o.values) == 0 {
		return ""
	}
	var parts []string
	for _, u := range o.values {
		parts = append(parts, u.String())
	}
	return strings.Join(parts, ", ")
}

// Set parses a ulimit value in the format "name=soft:hard" or "name=value".
func (o *UlimitOpt) Set(value string) error {
	u, err := units.ParseUlimit(value)
	if err != nil {
		return fmt.Errorf("invalid ulimit %q: %w", value, err)
	}
	o.values = append(o.values, &container.Ulimit{
		Name: u.Name,
		Hard: u.Hard,
		Soft: u.Soft,
	})
	return nil
}

// Type returns the type string for pflag.
func (o *UlimitOpt) Type() string {
	return "ulimit"
}

// GetAll returns all ulimits.
func (o *UlimitOpt) GetAll() []*container.Ulimit {
	return o.values
}

// Len returns the number of ulimits.
func (o *UlimitOpt) Len() int {
	return len(o.values)
}

// -----------------------------------------------------------------------------
// WeightDeviceOpt - Parse blkio weight device flags (device:weight)
// -----------------------------------------------------------------------------

// WeightDeviceOpt holds block IO weight device settings.
// Implements pflag.Value interface.
type WeightDeviceOpt struct {
	values []*blkiodev.WeightDevice
}

// NewWeightDeviceOpt creates a new WeightDeviceOpt.
func NewWeightDeviceOpt() *WeightDeviceOpt {
	return &WeightDeviceOpt{}
}

// String returns a string representation.
func (o *WeightDeviceOpt) String() string {
	if len(o.values) == 0 {
		return ""
	}
	var parts []string
	for _, w := range o.values {
		parts = append(parts, w.String())
	}
	return strings.Join(parts, ", ")
}

// Set parses a weight device value in the format "device:weight".
func (o *WeightDeviceOpt) Set(value string) error {
	k, v, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("invalid weight device %q: expected format device:weight", value)
	}
	if k == "" {
		return fmt.Errorf("invalid weight device %q: device path cannot be empty", value)
	}
	weight, err := strconv.ParseUint(v, 10, 16)
	if err != nil {
		return fmt.Errorf("invalid weight device %q: invalid weight: %w", value, err)
	}
	if weight < 10 || weight > 1000 {
		return fmt.Errorf("invalid weight device %q: weight must be between 10 and 1000", value)
	}
	o.values = append(o.values, &blkiodev.WeightDevice{
		Path:   k,
		Weight: uint16(weight),
	})
	return nil
}

// Type returns the type string for pflag.
func (o *WeightDeviceOpt) Type() string {
	return "weight-device"
}

// GetAll returns all weight devices.
func (o *WeightDeviceOpt) GetAll() []*blkiodev.WeightDevice {
	return o.values
}

// Len returns the number of weight devices.
func (o *WeightDeviceOpt) Len() int {
	return len(o.values)
}

// -----------------------------------------------------------------------------
// ThrottleDeviceOpt - Parse blkio throttle device flags (device:rate)
// -----------------------------------------------------------------------------

// ThrottleDeviceOpt holds block IO throttle device settings.
// Implements pflag.Value interface.
type ThrottleDeviceOpt struct {
	values  []*blkiodev.ThrottleDevice
	isBytes bool // true for bps (bytes/sec), false for iops (IO/sec)
}

// NewThrottleDeviceOpt creates a new ThrottleDeviceOpt.
// Set isBytes to true for byte rate limits, false for IO rate limits.
func NewThrottleDeviceOpt(isBytes bool) *ThrottleDeviceOpt {
	return &ThrottleDeviceOpt{isBytes: isBytes}
}

// String returns a string representation.
func (o *ThrottleDeviceOpt) String() string {
	if len(o.values) == 0 {
		return ""
	}
	var parts []string
	for _, t := range o.values {
		parts = append(parts, t.String())
	}
	return strings.Join(parts, ", ")
}

// Set parses a throttle device value in the format "device:rate".
// For byte rates, human-readable sizes are accepted (e.g., "1mb").
// For IO rates, only numeric values are accepted.
func (o *ThrottleDeviceOpt) Set(value string) error {
	k, v, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("invalid throttle device %q: expected format device:rate", value)
	}
	if k == "" {
		return fmt.Errorf("invalid throttle device %q: device path cannot be empty", value)
	}

	var rate uint64
	if o.isBytes {
		val, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("invalid throttle device %q: invalid byte rate: %w", value, err)
		}
		if val < 0 {
			return fmt.Errorf("invalid throttle device %q: rate must be positive", value)
		}
		rate = uint64(val)
	} else {
		val, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid throttle device %q: invalid IO rate: %w", value, err)
		}
		rate = val
	}

	o.values = append(o.values, &blkiodev.ThrottleDevice{
		Path: k,
		Rate: rate,
	})
	return nil
}

// Type returns the type string for pflag.
func (o *ThrottleDeviceOpt) Type() string {
	return "throttle-device"
}

// GetAll returns all throttle devices.
func (o *ThrottleDeviceOpt) GetAll() []*blkiodev.ThrottleDevice {
	return o.values
}

// Len returns the number of throttle devices.
func (o *ThrottleDeviceOpt) Len() int {
	return len(o.values)
}

// -----------------------------------------------------------------------------
// GpuOpts - Parse GPU device flags
// -----------------------------------------------------------------------------

// GpuOpts holds GPU device request configuration.
// Implements pflag.Value interface.
type GpuOpts struct {
	values []container.DeviceRequest
}

// NewGpuOpts creates a new GpuOpts.
func NewGpuOpts() *GpuOpts {
	return &GpuOpts{}
}

// String returns a string representation.
func (o *GpuOpts) String() string {
	if len(o.values) == 0 {
		return ""
	}
	return fmt.Sprintf("%d GPU request(s)", len(o.values))
}

// Set parses a GPU specification.
// Accepted formats:
//   - "all" - request all GPUs
//   - "N" (integer) - request N GPUs
//   - "device=ID[,ID...]" - request specific device IDs
//   - "driver=NAME,count=N,capabilities=CAP[,CAP...]" - full specification
func (o *GpuOpts) Set(value string) error {
	req := container.DeviceRequest{
		Capabilities: [][]string{{"gpu"}},
		Options:      make(map[string]string),
	}

	if value == "all" {
		req.Count = -1
		o.values = append(o.values, req)
		return nil
	}

	// Try parsing as a simple integer count
	if count, err := strconv.Atoi(value); err == nil {
		if count < 0 {
			return fmt.Errorf("invalid GPU count: must be non-negative")
		}
		req.Count = count
		o.values = append(o.values, req)
		return nil
	}

	// Parse key=value pairs
	for _, kv := range strings.Split(value, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("invalid GPU option %q: expected key=value format", kv)
		}
		switch k {
		case "driver":
			req.Driver = v
		case "count":
			if v == "all" {
				req.Count = -1
			} else {
				count, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("invalid GPU count %q: %w", v, err)
				}
				req.Count = count
			}
		case "device":
			req.DeviceIDs = strings.Split(v, ",")
		case "capabilities":
			req.Capabilities = [][]string{strings.Split(v, ",")}
		default:
			req.Options[k] = v
		}
	}

	o.values = append(o.values, req)
	return nil
}

// Type returns the type string for pflag.
func (o *GpuOpts) Type() string {
	return "gpu-request"
}

// GetAll returns all device requests.
func (o *GpuOpts) GetAll() []container.DeviceRequest {
	return o.values
}

// Len returns the number of GPU requests.
func (o *GpuOpts) Len() int {
	return len(o.values)
}

// -----------------------------------------------------------------------------
// MountOpt - Parse --mount flags
// -----------------------------------------------------------------------------

// MountOpt holds mount specifications.
// Implements pflag.Value interface.
type MountOpt struct {
	values []mount.Mount
}

// NewMountOpt creates a new MountOpt.
func NewMountOpt() *MountOpt {
	return &MountOpt{}
}

// String returns a string representation.
func (o *MountOpt) String() string {
	if len(o.values) == 0 {
		return ""
	}
	var parts []string
	for _, m := range o.values {
		parts = append(parts, fmt.Sprintf("type=%s,source=%s,target=%s", m.Type, m.Source, m.Target))
	}
	return strings.Join(parts, " ")
}

// Set parses a mount specification in the format "type=X,source=Y,target=Z[,option=value...]".
func (o *MountOpt) Set(value string) error {
	m := mount.Mount{}

	for _, kv := range strings.Split(value, ",") {
		// Handle boolean options that may not have =value
		k, v, hasValue := strings.Cut(kv, "=")
		switch strings.ToLower(k) {
		case "type":
			m.Type = mount.Type(v)
		case "source", "src":
			m.Source = v
		case "target", "destination", "dst":
			m.Target = v
		case "readonly", "ro":
			m.ReadOnly = !hasValue || v == "true" || v == "1"
		case "bind-propagation":
			if m.BindOptions == nil {
				m.BindOptions = &mount.BindOptions{}
			}
			m.BindOptions.Propagation = mount.Propagation(v)
		case "bind-nonrecursive":
			if m.BindOptions == nil {
				m.BindOptions = &mount.BindOptions{}
			}
			m.BindOptions.NonRecursive = !hasValue || v == "true" || v == "1"
		case "volume-driver":
			if m.VolumeOptions == nil {
				m.VolumeOptions = &mount.VolumeOptions{DriverConfig: &mount.Driver{}}
			}
			if m.VolumeOptions.DriverConfig == nil {
				m.VolumeOptions.DriverConfig = &mount.Driver{}
			}
			m.VolumeOptions.DriverConfig.Name = v
		case "volume-label":
			if m.VolumeOptions == nil {
				m.VolumeOptions = &mount.VolumeOptions{}
			}
			if m.VolumeOptions.Labels == nil {
				m.VolumeOptions.Labels = make(map[string]string)
			}
			lk, lv, _ := strings.Cut(v, "=")
			m.VolumeOptions.Labels[lk] = lv
		case "volume-nocopy":
			if m.VolumeOptions == nil {
				m.VolumeOptions = &mount.VolumeOptions{}
			}
			m.VolumeOptions.NoCopy = !hasValue || v == "true" || v == "1"
		case "volume-opt":
			if m.VolumeOptions == nil {
				m.VolumeOptions = &mount.VolumeOptions{DriverConfig: &mount.Driver{}}
			}
			if m.VolumeOptions.DriverConfig == nil {
				m.VolumeOptions.DriverConfig = &mount.Driver{}
			}
			if m.VolumeOptions.DriverConfig.Options == nil {
				m.VolumeOptions.DriverConfig.Options = make(map[string]string)
			}
			ok, ov, _ := strings.Cut(v, "=")
			m.VolumeOptions.DriverConfig.Options[ok] = ov
		case "tmpfs-size":
			if m.TmpfsOptions == nil {
				m.TmpfsOptions = &mount.TmpfsOptions{}
			}
			size, err := units.RAMInBytes(v)
			if err != nil {
				return fmt.Errorf("invalid tmpfs-size %q: %w", v, err)
			}
			m.TmpfsOptions.SizeBytes = size
		case "tmpfs-mode":
			if m.TmpfsOptions == nil {
				m.TmpfsOptions = &mount.TmpfsOptions{}
			}
			mode, err := strconv.ParseUint(v, 8, 32)
			if err != nil {
				return fmt.Errorf("invalid tmpfs-mode %q: %w", v, err)
			}
			m.TmpfsOptions.Mode = os.FileMode(mode)
		default:
			return fmt.Errorf("invalid mount option %q", kv)
		}
	}

	if m.Target == "" {
		return fmt.Errorf("mount target is required")
	}
	if m.Type == "" {
		m.Type = mount.TypeVolume
	}

	o.values = append(o.values, m)
	return nil
}

// Type returns the type string for pflag.
func (o *MountOpt) Type() string {
	return "mount"
}

// GetAll returns all mount specifications.
func (o *MountOpt) GetAll() []mount.Mount {
	return o.values
}

// Len returns the number of mount specifications.
func (o *MountOpt) Len() int {
	return len(o.values)
}

// -----------------------------------------------------------------------------
// DeviceOpt - Parse --device flags (host:container:perms)
// -----------------------------------------------------------------------------

// DeviceOpt holds device mapping specifications.
// Implements pflag.Value interface.
type DeviceOpt struct {
	values []container.DeviceMapping
}

// NewDeviceOpt creates a new DeviceOpt.
func NewDeviceOpt() *DeviceOpt {
	return &DeviceOpt{}
}

// String returns a string representation.
func (o *DeviceOpt) String() string {
	if len(o.values) == 0 {
		return ""
	}
	var parts []string
	for _, d := range o.values {
		s := d.PathOnHost
		if d.PathInContainer != "" && d.PathInContainer != d.PathOnHost {
			s += ":" + d.PathInContainer
		}
		if d.CgroupPermissions != "" && d.CgroupPermissions != "rwm" {
			s += ":" + d.CgroupPermissions
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// Set parses a device specification in the format "host[:container[:permissions]]".
func (o *DeviceOpt) Set(value string) error {
	dm := container.DeviceMapping{
		CgroupPermissions: "rwm",
	}

	parts := strings.Split(value, ":")
	switch len(parts) {
	case 1:
		dm.PathOnHost = parts[0]
		dm.PathInContainer = parts[0]
	case 2:
		dm.PathOnHost = parts[0]
		// Could be host:container or host:perms
		if isDeviceMode(parts[1]) {
			dm.PathInContainer = parts[0]
			dm.CgroupPermissions = parts[1]
		} else {
			dm.PathInContainer = parts[1]
		}
	case 3:
		dm.PathOnHost = parts[0]
		dm.PathInContainer = parts[1]
		dm.CgroupPermissions = parts[2]
	default:
		return fmt.Errorf("invalid device %q: too many colons", value)
	}

	if dm.PathOnHost == "" {
		return fmt.Errorf("invalid device %q: device path cannot be empty", value)
	}

	if !isDeviceMode(dm.CgroupPermissions) {
		return fmt.Errorf("invalid device mode %q: must be a combination of r, w, m", dm.CgroupPermissions)
	}

	o.values = append(o.values, dm)
	return nil
}

// Type returns the type string for pflag.
func (o *DeviceOpt) Type() string {
	return "device"
}

// GetAll returns all device mappings.
func (o *DeviceOpt) GetAll() []container.DeviceMapping {
	return o.values
}

// Len returns the number of device mappings.
func (o *DeviceOpt) Len() int {
	return len(o.values)
}

// isDeviceMode checks if a string is a valid device mode (combination of r, w, m).
func isDeviceMode(mode string) bool {
	if mode == "" || len(mode) > 3 {
		return false
	}
	seen := make(map[byte]bool)
	for i := range len(mode) {
		c := mode[i]
		if c != 'r' && c != 'w' && c != 'm' {
			return false
		}
		if seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}
