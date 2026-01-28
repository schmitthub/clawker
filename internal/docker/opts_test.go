package docker

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemBytes(t *testing.T) {
	t.Run("parse human readable sizes", func(t *testing.T) {
		tests := []struct {
			input    string
			expected int64
		}{
			{"512m", 512 * 1024 * 1024},
			{"1g", 1024 * 1024 * 1024},
			{"2G", 2 * 1024 * 1024 * 1024},
			{"256M", 256 * 1024 * 1024},
			{"1024", 1024},
		}

		for _, tt := range tests {
			t.Run(tt.input, func(t *testing.T) {
				var m MemBytes
				require.NoError(t, m.Set(tt.input))
				assert.Equal(t, tt.expected, m.Value())
			})
		}
	})

	t.Run("string representation", func(t *testing.T) {
		var m MemBytes
		assert.Equal(t, "0", m.String())

		require.NoError(t, m.Set("512m"))
		assert.NotEqual(t, "0", m.String())
	})

	t.Run("type", func(t *testing.T) {
		var m MemBytes
		assert.Equal(t, "bytes", m.Type())
	})
}

func TestNanoCPUs(t *testing.T) {
	t.Run("parse fractional CPUs", func(t *testing.T) {
		tests := []struct {
			input    string
			expected int64
		}{
			{"1", 1e9},
			{"1.5", 1.5e9},
			{"0.5", 0.5e9},
			{"2", 2e9},
		}

		for _, tt := range tests {
			t.Run(tt.input, func(t *testing.T) {
				var c NanoCPUs
				require.NoError(t, c.Set(tt.input))
				assert.Equal(t, tt.expected, c.Value())
			})
		}
	})

	t.Run("string representation", func(t *testing.T) {
		var c NanoCPUs
		assert.Equal(t, "", c.String())

		require.NoError(t, c.Set("1.5"))
		assert.Equal(t, "1.500", c.String())
	})

	t.Run("type", func(t *testing.T) {
		var c NanoCPUs
		assert.Equal(t, "decimal", c.Type())
	})

	t.Run("invalid input", func(t *testing.T) {
		var c NanoCPUs
		err := c.Set("not-a-number")
		assert.Error(t, err)
	})
}

func TestUlimitOpt(t *testing.T) {
	t.Run("parse soft:hard format", func(t *testing.T) {
		o := NewUlimitOpt()
		require.NoError(t, o.Set("nofile=1024:2048"))
		require.Equal(t, 1, o.Len())
		assert.Equal(t, "nofile", o.GetAll()[0].Name)
		assert.Equal(t, int64(1024), o.GetAll()[0].Soft)
		assert.Equal(t, int64(2048), o.GetAll()[0].Hard)
	})

	t.Run("parse single value", func(t *testing.T) {
		o := NewUlimitOpt()
		require.NoError(t, o.Set("nofile=1024"))
		assert.Equal(t, int64(1024), o.GetAll()[0].Soft)
		assert.Equal(t, int64(1024), o.GetAll()[0].Hard)
	})

	t.Run("invalid format", func(t *testing.T) {
		o := NewUlimitOpt()
		err := o.Set("invalid")
		assert.Error(t, err)
	})

	t.Run("type", func(t *testing.T) {
		o := NewUlimitOpt()
		assert.Equal(t, "ulimit", o.Type())
	})

	t.Run("string representation", func(t *testing.T) {
		o := NewUlimitOpt()
		assert.Equal(t, "", o.String())
		require.NoError(t, o.Set("nofile=1024:2048"))
		assert.NotEmpty(t, o.String())
	})

	t.Run("implements pflag.Value", func(t *testing.T) {
		var _ pflag.Value = NewUlimitOpt()
	})
}

func TestWeightDeviceOpt(t *testing.T) {
	t.Run("valid device:weight", func(t *testing.T) {
		o := NewWeightDeviceOpt()
		require.NoError(t, o.Set("/dev/sda:500"))
		require.Equal(t, 1, o.Len())
		assert.Equal(t, "/dev/sda", o.GetAll()[0].Path)
		assert.Equal(t, uint16(500), o.GetAll()[0].Weight)
	})

	t.Run("weight too low", func(t *testing.T) {
		o := NewWeightDeviceOpt()
		err := o.Set("/dev/sda:5")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "between 10 and 1000")
	})

	t.Run("weight too high", func(t *testing.T) {
		o := NewWeightDeviceOpt()
		err := o.Set("/dev/sda:2000")
		assert.Error(t, err)
	})

	t.Run("missing colon", func(t *testing.T) {
		o := NewWeightDeviceOpt()
		err := o.Set("/dev/sda")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected format")
	})

	t.Run("empty device path", func(t *testing.T) {
		o := NewWeightDeviceOpt()
		err := o.Set(":500")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "device path cannot be empty")
	})

	t.Run("type", func(t *testing.T) {
		o := NewWeightDeviceOpt()
		assert.Equal(t, "weight-device", o.Type())
	})

	t.Run("implements pflag.Value", func(t *testing.T) {
		var _ pflag.Value = NewWeightDeviceOpt()
	})
}

func TestThrottleDeviceOpt(t *testing.T) {
	t.Run("byte rate with human readable", func(t *testing.T) {
		o := NewThrottleDeviceOpt(true)
		require.NoError(t, o.Set("/dev/sda:1mb"))
		require.Equal(t, 1, o.Len())
		assert.Equal(t, "/dev/sda", o.GetAll()[0].Path)
		assert.Equal(t, uint64(1024*1024), o.GetAll()[0].Rate)
	})

	t.Run("IO rate numeric", func(t *testing.T) {
		o := NewThrottleDeviceOpt(false)
		require.NoError(t, o.Set("/dev/sda:1000"))
		assert.Equal(t, uint64(1000), o.GetAll()[0].Rate)
	})

	t.Run("IO rate rejects human readable", func(t *testing.T) {
		o := NewThrottleDeviceOpt(false)
		err := o.Set("/dev/sda:1mb")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid IO rate")
	})

	t.Run("missing colon", func(t *testing.T) {
		o := NewThrottleDeviceOpt(true)
		err := o.Set("/dev/sda")
		assert.Error(t, err)
	})

	t.Run("type", func(t *testing.T) {
		o := NewThrottleDeviceOpt(true)
		assert.Equal(t, "throttle-device", o.Type())
	})

	t.Run("implements pflag.Value", func(t *testing.T) {
		var _ pflag.Value = NewThrottleDeviceOpt(true)
	})
}

func TestGpuOpts(t *testing.T) {
	t.Run("all GPUs", func(t *testing.T) {
		o := NewGpuOpts()
		require.NoError(t, o.Set("all"))
		require.Equal(t, 1, o.Len())
		assert.Equal(t, -1, o.GetAll()[0].Count)
		assert.Equal(t, [][]string{{"gpu"}}, o.GetAll()[0].Capabilities)
	})

	t.Run("numeric count", func(t *testing.T) {
		o := NewGpuOpts()
		require.NoError(t, o.Set("2"))
		assert.Equal(t, 2, o.GetAll()[0].Count)
	})

	t.Run("device specification", func(t *testing.T) {
		o := NewGpuOpts()
		require.NoError(t, o.Set("driver=nvidia,count=all,capabilities=gpu"))
		require.Equal(t, 1, o.Len())
		assert.Equal(t, "nvidia", o.GetAll()[0].Driver)
		assert.Equal(t, -1, o.GetAll()[0].Count)
	})

	t.Run("negative count rejected", func(t *testing.T) {
		o := NewGpuOpts()
		err := o.Set("-1")
		assert.Error(t, err)
	})

	t.Run("type", func(t *testing.T) {
		o := NewGpuOpts()
		assert.Equal(t, "gpu-request", o.Type())
	})

	t.Run("implements pflag.Value", func(t *testing.T) {
		var _ pflag.Value = NewGpuOpts()
	})
}

func TestMountOpt(t *testing.T) {
	t.Run("basic bind mount", func(t *testing.T) {
		o := NewMountOpt()
		require.NoError(t, o.Set("type=bind,source=/src,target=/dst"))
		require.Equal(t, 1, o.Len())
		m := o.GetAll()[0]
		assert.Equal(t, "bind", string(m.Type))
		assert.Equal(t, "/src", m.Source)
		assert.Equal(t, "/dst", m.Target)
	})

	t.Run("volume mount defaults type to volume", func(t *testing.T) {
		o := NewMountOpt()
		require.NoError(t, o.Set("source=myvolume,target=/data"))
		assert.Equal(t, "volume", string(o.GetAll()[0].Type))
	})

	t.Run("readonly mount", func(t *testing.T) {
		o := NewMountOpt()
		require.NoError(t, o.Set("type=bind,source=/src,target=/dst,readonly"))
		assert.True(t, o.GetAll()[0].ReadOnly)
	})

	t.Run("tmpfs with size", func(t *testing.T) {
		o := NewMountOpt()
		require.NoError(t, o.Set("type=tmpfs,target=/tmp,tmpfs-size=64m"))
		m := o.GetAll()[0]
		assert.Equal(t, "tmpfs", string(m.Type))
		assert.NotNil(t, m.TmpfsOptions)
		assert.Equal(t, int64(64*1024*1024), m.TmpfsOptions.SizeBytes)
	})

	t.Run("missing target", func(t *testing.T) {
		o := NewMountOpt()
		err := o.Set("type=bind,source=/src")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "target is required")
	})

	t.Run("invalid option", func(t *testing.T) {
		o := NewMountOpt()
		err := o.Set("type=bind,target=/dst,notanoption=foo")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid mount option")
	})

	t.Run("type", func(t *testing.T) {
		o := NewMountOpt()
		assert.Equal(t, "mount", o.Type())
	})

	t.Run("implements pflag.Value", func(t *testing.T) {
		var _ pflag.Value = NewMountOpt()
	})
}

func TestDeviceOpt(t *testing.T) {
	t.Run("host path only", func(t *testing.T) {
		o := NewDeviceOpt()
		require.NoError(t, o.Set("/dev/sda"))
		require.Equal(t, 1, o.Len())
		d := o.GetAll()[0]
		assert.Equal(t, "/dev/sda", d.PathOnHost)
		assert.Equal(t, "/dev/sda", d.PathInContainer)
		assert.Equal(t, "rwm", d.CgroupPermissions)
	})

	t.Run("host:container", func(t *testing.T) {
		o := NewDeviceOpt()
		require.NoError(t, o.Set("/dev/sda:/dev/xvdc"))
		d := o.GetAll()[0]
		assert.Equal(t, "/dev/sda", d.PathOnHost)
		assert.Equal(t, "/dev/xvdc", d.PathInContainer)
		assert.Equal(t, "rwm", d.CgroupPermissions)
	})

	t.Run("host:container:perms", func(t *testing.T) {
		o := NewDeviceOpt()
		require.NoError(t, o.Set("/dev/sda:/dev/xvdc:r"))
		d := o.GetAll()[0]
		assert.Equal(t, "/dev/sda", d.PathOnHost)
		assert.Equal(t, "/dev/xvdc", d.PathInContainer)
		assert.Equal(t, "r", d.CgroupPermissions)
	})

	t.Run("host:perms", func(t *testing.T) {
		o := NewDeviceOpt()
		require.NoError(t, o.Set("/dev/sda:rw"))
		d := o.GetAll()[0]
		assert.Equal(t, "/dev/sda", d.PathOnHost)
		assert.Equal(t, "/dev/sda", d.PathInContainer)
		assert.Equal(t, "rw", d.CgroupPermissions)
	})

	t.Run("too many colons", func(t *testing.T) {
		o := NewDeviceOpt()
		err := o.Set("/dev/a:/dev/b:rw:extra")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many colons")
	})

	t.Run("invalid permissions", func(t *testing.T) {
		o := NewDeviceOpt()
		err := o.Set("/dev/sda:/dev/sda:xyz")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid device mode")
	})

	t.Run("empty device path", func(t *testing.T) {
		o := NewDeviceOpt()
		err := o.Set(":/dev/sda")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "device path cannot be empty")
	})

	t.Run("type", func(t *testing.T) {
		o := NewDeviceOpt()
		assert.Equal(t, "device", o.Type())
	})

	t.Run("implements pflag.Value", func(t *testing.T) {
		var _ pflag.Value = NewDeviceOpt()
	})
}

func TestIsDeviceMode(t *testing.T) {
	tests := []struct {
		mode  string
		valid bool
	}{
		{"r", true},
		{"w", true},
		{"m", true},
		{"rw", true},
		{"rwm", true},
		{"rm", true},
		{"wm", true},
		{"", false},
		{"rwmx", false},
		{"x", false},
		{"rr", false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			assert.Equal(t, tt.valid, isDeviceMode(tt.mode))
		})
	}
}
