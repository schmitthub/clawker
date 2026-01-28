package opts

import (
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListOpts(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		opts := NewListOpts(nil)

		// Initially empty
		assert.Equal(t, "", opts.String())
		assert.Equal(t, 0, opts.Len())
		assert.Nil(t, opts.GetAll())

		// Add values
		require.NoError(t, opts.Set("value1"))
		require.NoError(t, opts.Set("value2"))

		assert.Equal(t, 2, opts.Len())
		assert.Equal(t, []string{"value1", "value2"}, opts.GetAll())
		assert.Equal(t, "list", opts.Type())
	})

	t.Run("with validator that accepts", func(t *testing.T) {
		validator := func(val string) (string, error) {
			return "validated:" + val, nil
		}
		opts := NewListOpts(validator)

		require.NoError(t, opts.Set("test"))
		assert.Equal(t, []string{"validated:test"}, opts.GetAll())
	})

	t.Run("with validator that rejects", func(t *testing.T) {
		validator := func(val string) (string, error) {
			return "", assert.AnError
		}
		opts := NewListOpts(validator)

		err := opts.Set("test")
		assert.Error(t, err)
		assert.Equal(t, 0, opts.Len())
	})

	t.Run("with ref", func(t *testing.T) {
		values := []string{"initial"}
		opts := NewListOptsRef(&values, nil)

		require.NoError(t, opts.Set("added"))
		assert.Equal(t, []string{"initial", "added"}, opts.GetAll())
		assert.Equal(t, []string{"initial", "added"}, values) // Original slice modified
	})
}

func TestMapOpts(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		opts := NewMapOpts(nil)

		// Initially empty
		assert.Equal(t, "", opts.String())
		assert.Equal(t, 0, opts.Len())

		// Add key=value pair
		require.NoError(t, opts.Set("key1=value1"))
		require.NoError(t, opts.Set("key2=value2"))

		assert.Equal(t, 2, opts.Len())
		assert.Equal(t, "map", opts.Type())

		val, ok := opts.Get("key1")
		assert.True(t, ok)
		assert.Equal(t, "value1", val)

		val, ok = opts.Get("key2")
		assert.True(t, ok)
		assert.Equal(t, "value2", val)

		_, ok = opts.Get("nonexistent")
		assert.False(t, ok)
	})

	t.Run("empty value", func(t *testing.T) {
		opts := NewMapOpts(nil)

		require.NoError(t, opts.Set("key="))
		val, ok := opts.Get("key")
		assert.True(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("no equals sign", func(t *testing.T) {
		opts := NewMapOpts(nil)

		require.NoError(t, opts.Set("key"))
		val, ok := opts.Get("key")
		assert.True(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("value with equals sign", func(t *testing.T) {
		opts := NewMapOpts(nil)

		require.NoError(t, opts.Set("key=value=with=equals"))
		val, ok := opts.Get("key")
		assert.True(t, ok)
		assert.Equal(t, "value=with=equals", val)
	})

	t.Run("overwrites existing key", func(t *testing.T) {
		opts := NewMapOpts(nil)

		require.NoError(t, opts.Set("key=original"))
		require.NoError(t, opts.Set("key=updated"))

		val, ok := opts.Get("key")
		assert.True(t, ok)
		assert.Equal(t, "updated", val)
		assert.Equal(t, 1, opts.Len())
	})

	t.Run("with validator that accepts", func(t *testing.T) {
		validator := func(key, val string) error {
			return nil
		}
		opts := NewMapOpts(validator)

		require.NoError(t, opts.Set("key=value"))
		assert.Equal(t, 1, opts.Len())
	})

	t.Run("with validator that rejects", func(t *testing.T) {
		validator := func(key, val string) error {
			return assert.AnError
		}
		opts := NewMapOpts(validator)

		err := opts.Set("key=value")
		assert.Error(t, err)
		assert.Equal(t, 0, opts.Len())
	})
}

func TestPortOpts(t *testing.T) {
	t.Run("basic port mapping", func(t *testing.T) {
		opts := NewPortOpts()

		// Initially empty
		assert.Equal(t, "", opts.String())
		assert.Equal(t, 0, opts.Len())

		// Add a simple port mapping
		require.NoError(t, opts.Set("8080:80"))

		assert.Equal(t, 1, opts.Len())
		assert.Equal(t, "port", opts.Type())

		exposed := opts.GetExposedPorts()
		bindings := opts.GetPortBindings()

		assert.Equal(t, 1, len(exposed))
		assert.Equal(t, 1, len(bindings))
	})

	t.Run("port with protocol", func(t *testing.T) {
		opts := NewPortOpts()

		require.NoError(t, opts.Set("8080:80/tcp"))
		require.NoError(t, opts.Set("53:53/udp"))

		assert.Equal(t, 2, opts.Len())
	})

	t.Run("port with host IP", func(t *testing.T) {
		opts := NewPortOpts()

		require.NoError(t, opts.Set("127.0.0.1:8080:80"))

		bindings := opts.GetPortBindings()
		assert.Equal(t, 1, len(bindings))

		// Check that host IP was parsed
		for _, binding := range bindings {
			assert.Equal(t, 1, len(binding))
			assert.Equal(t, "127.0.0.1", binding[0].HostIP.String())
		}
	})

	t.Run("container port only", func(t *testing.T) {
		opts := NewPortOpts()

		require.NoError(t, opts.Set("80"))

		assert.Equal(t, 1, opts.Len())
		exposed := opts.GetExposedPorts()
		assert.Equal(t, 1, len(exposed))
	})

	t.Run("multiple ports", func(t *testing.T) {
		opts := NewPortOpts()

		require.NoError(t, opts.Set("80"))
		require.NoError(t, opts.Set("443"))
		require.NoError(t, opts.Set("8080:8000"))

		// 80, 443, and 8000 (from 8080:8000) = 3 unique container ports
		exposed := opts.GetExposedPorts()
		assert.Equal(t, 3, len(exposed))
	})

	t.Run("invalid port mapping", func(t *testing.T) {
		opts := NewPortOpts()

		err := opts.Set("invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port mapping")
	})

	t.Run("invalid host IP", func(t *testing.T) {
		opts := NewPortOpts()

		err := opts.Set("notanip:8080:80")
		assert.Error(t, err)
	})
}

func TestContainerOptions_ResourceLimitFlags(t *testing.T) {
	t.Run("memory flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--memory", "512m"})
		require.NoError(t, err)
		assert.Equal(t, int64(512*1024*1024), opts.Memory.Value())
	})

	t.Run("memory flag with gigabytes", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"-m", "2g"})
		require.NoError(t, err)
		assert.Equal(t, int64(2*1024*1024*1024), opts.Memory.Value())
	})

	t.Run("cpus flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cpus", "1.5"})
		require.NoError(t, err)
		assert.Equal(t, int64(1.5e9), opts.CPUs.Value())
	})

	t.Run("cpu-shares flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cpu-shares", "1024"})
		require.NoError(t, err)
		assert.Equal(t, int64(1024), opts.CPUShares)
	})

	t.Run("cpu-shares shorthand", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"-c", "512"})
		require.NoError(t, err)
		assert.Equal(t, int64(512), opts.CPUShares)
	})

	t.Run("memory-swap flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--memory-swap", "1g"})
		require.NoError(t, err)
		assert.Equal(t, int64(1*1024*1024*1024), opts.MemorySwap.Value())
	})

	t.Run("memory-swap unlimited", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--memory-swap", "-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(-1), opts.MemorySwap.Value())
	})
}

func TestContainerOptions_ValidateFlags(t *testing.T) {
	t.Run("memory-swap without memory fails", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.MemorySwap.Set("1g"))

		err := opts.ValidateFlags()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "memory-swap requires --memory")
	})

	t.Run("memory-swap with memory succeeds", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.Memory.Set("512m"))
		require.NoError(t, opts.MemorySwap.Set("1g"))

		err := opts.ValidateFlags()
		assert.NoError(t, err)
	})

	t.Run("memory-swap unlimited without memory succeeds", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.MemorySwap.Set("-1"))

		err := opts.ValidateFlags()
		assert.NoError(t, err)
	})

	t.Run("memory-swap less than memory fails", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.Memory.Set("1g"))
		require.NoError(t, opts.MemorySwap.Set("512m"))

		err := opts.ValidateFlags()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "memory-swap must be greater than or equal")
	})

	t.Run("memory-swap equal to memory succeeds", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.Memory.Set("1g"))
		require.NoError(t, opts.MemorySwap.Set("1g"))

		err := opts.ValidateFlags()
		assert.NoError(t, err)
	})
}

func TestContainerOptions_BuildConfigs_ResourceLimits(t *testing.T) {
	t.Run("memory limit is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Memory.Set("512m"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(512*1024*1024), hostCfg.Memory)
	})

	t.Run("memory-swap limit is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Memory.Set("512m"))
		require.NoError(t, opts.MemorySwap.Set("1g"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(1*1024*1024*1024), hostCfg.MemorySwap)
	})

	t.Run("cpus limit is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.CPUs.Set("1.5"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(1.5e9), hostCfg.NanoCPUs)
	})

	t.Run("cpu-shares is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CPUShares = 1024

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(1024), hostCfg.CPUShares)
	})

	t.Run("zero values are not set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(0), hostCfg.Memory)
		assert.Equal(t, int64(0), hostCfg.MemorySwap)
		assert.Equal(t, int64(0), hostCfg.NanoCPUs)
		assert.Equal(t, int64(0), hostCfg.CPUShares)
	})
}

// Verify docker types implement pflag.Value interface
func TestDockerTypesImplementPflagValue(t *testing.T) {
	var m docker.MemBytes
	var ms docker.MemSwapBytes
	var c docker.NanoCPUs
	var _ pflag.Value = &m
	var _ pflag.Value = &ms
	var _ pflag.Value = &c
}

func TestContainerOptions_NetworkingFlags(t *testing.T) {
	t.Run("hostname flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--hostname", "myhost"})
		require.NoError(t, err)
		assert.Equal(t, "myhost", opts.Hostname)
	})

	t.Run("dns flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--dns", "8.8.8.8", "--dns", "8.8.4.4"})
		require.NoError(t, err)
		assert.Equal(t, []string{"8.8.8.8", "8.8.4.4"}, opts.DNS)
	})

	t.Run("dns-search flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--dns-search", "example.com", "--dns-search", "test.local"})
		require.NoError(t, err)
		assert.Equal(t, []string{"example.com", "test.local"}, opts.DNSSearch)
	})

	t.Run("add-host flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--add-host", "myservice:192.168.1.100", "--add-host", "db:10.0.0.5"})
		require.NoError(t, err)
		assert.Equal(t, []string{"myservice:192.168.1.100", "db:10.0.0.5"}, opts.ExtraHosts)
	})
}

func TestContainerOptions_BuildConfigs_Networking(t *testing.T) {
	t.Run("hostname is set in container config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Hostname = "myhost"

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "myhost", cfg.Hostname)
	})

	t.Run("dns servers are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.DNS = []string{"8.8.8.8", "8.8.4.4"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.Len(t, hostCfg.DNS, 2)
		assert.Equal(t, "8.8.8.8", hostCfg.DNS[0].String())
		assert.Equal(t, "8.8.4.4", hostCfg.DNS[1].String())
	})

	t.Run("dns-search is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.DNSSearch = []string{"example.com", "test.local"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"example.com", "test.local"}, hostCfg.DNSSearch)
	})

	t.Run("extra hosts are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.ExtraHosts = []string{"myservice:192.168.1.100", "db:10.0.0.5"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"myservice:192.168.1.100", "db:10.0.0.5"}, hostCfg.ExtraHosts)
	})

	t.Run("invalid dns server returns error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.DNS = []string{"not-an-ip"}

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid DNS server address")
	})

	t.Run("empty networking options are not set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		cfg, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "", cfg.Hostname)
		assert.Nil(t, hostCfg.DNS)
		assert.Nil(t, hostCfg.DNSSearch)
		assert.Nil(t, hostCfg.ExtraHosts)
	})
}

func TestContainerOptions_StorageFlags(t *testing.T) {
	t.Run("tmpfs flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--tmpfs", "/tmp", "--tmpfs", "/run:rw,size=64m"})
		require.NoError(t, err)
		assert.Equal(t, []string{"/tmp", "/run:rw,size=64m"}, opts.Tmpfs)
	})

	t.Run("read-only flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--read-only"})
		require.NoError(t, err)
		assert.True(t, opts.ReadOnly)
	})

	t.Run("volumes-from flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--volumes-from", "container1", "--volumes-from", "container2:ro"})
		require.NoError(t, err)
		assert.Equal(t, []string{"container1", "container2:ro"}, opts.VolumesFrom)
	})
}

func TestContainerOptions_BuildConfigs_Storage(t *testing.T) {
	t.Run("read-only is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.ReadOnly = true

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.True(t, hostCfg.ReadonlyRootfs)
	})

	t.Run("volumes-from is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.VolumesFrom = []string{"container1", "container2:ro"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"container1", "container2:ro"}, hostCfg.VolumesFrom)
	})

	t.Run("tmpfs without options is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Tmpfs = []string{"/tmp"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, hostCfg.Tmpfs)
		assert.Equal(t, "", hostCfg.Tmpfs["/tmp"])
	})

	t.Run("tmpfs with options is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Tmpfs = []string{"/tmp:rw,size=64m", "/run:noexec"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, hostCfg.Tmpfs)
		assert.Equal(t, "rw,size=64m", hostCfg.Tmpfs["/tmp"])
		assert.Equal(t, "noexec", hostCfg.Tmpfs["/run"])
	})

	t.Run("empty storage options are not set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.False(t, hostCfg.ReadonlyRootfs)
		assert.Nil(t, hostCfg.VolumesFrom)
		assert.Nil(t, hostCfg.Tmpfs)
	})
}

func TestContainerOptions_SecurityFlags(t *testing.T) {
	t.Run("cap-add flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cap-add", "SYS_PTRACE", "--cap-add", "NET_ADMIN"})
		require.NoError(t, err)
		assert.Equal(t, []string{"SYS_PTRACE", "NET_ADMIN"}, opts.CapAdd)
	})

	t.Run("cap-drop flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cap-drop", "ALL", "--cap-drop", "MKNOD"})
		require.NoError(t, err)
		assert.Equal(t, []string{"ALL", "MKNOD"}, opts.CapDrop)
	})

	t.Run("privileged flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--privileged"})
		require.NoError(t, err)
		assert.True(t, opts.Privileged)
	})

	t.Run("security-opt flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--security-opt", "seccomp=unconfined", "--security-opt", "label=disable"})
		require.NoError(t, err)
		assert.Equal(t, []string{"seccomp=unconfined", "label=disable"}, opts.SecurityOpt)
	})
}

func TestContainerOptions_BuildConfigs_Security(t *testing.T) {
	t.Run("cap-add from CLI is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CapAdd = []string{"SYS_PTRACE", "NET_ADMIN"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"SYS_PTRACE", "NET_ADMIN"}, hostCfg.CapAdd)
	})

	t.Run("cap-add from project config is used when CLI not provided", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		projectCfg := &config.Config{
			Security: config.SecurityConfig{
				CapAdd: []string{"NET_RAW"},
			},
		}

		_, hostCfg, _, err := opts.BuildConfigs(nil, projectCfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"NET_RAW"}, hostCfg.CapAdd)
	})

	t.Run("cap-add from CLI takes precedence over project config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CapAdd = []string{"SYS_PTRACE"}

		projectCfg := &config.Config{
			Security: config.SecurityConfig{
				CapAdd: []string{"NET_RAW"},
			},
		}

		_, hostCfg, _, err := opts.BuildConfigs(nil, projectCfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"SYS_PTRACE"}, hostCfg.CapAdd)
	})

	t.Run("cap-drop is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CapDrop = []string{"ALL", "MKNOD"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"ALL", "MKNOD"}, hostCfg.CapDrop)
	})

	t.Run("privileged is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Privileged = true

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.True(t, hostCfg.Privileged)
	})

	t.Run("security-opt is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.SecurityOpt = []string{"seccomp=unconfined", "label=disable"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"seccomp=unconfined", "label=disable"}, hostCfg.SecurityOpt)
	})

	t.Run("empty security options are not set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Nil(t, hostCfg.CapAdd)
		assert.Nil(t, hostCfg.CapDrop)
		assert.False(t, hostCfg.Privileged)
		assert.Nil(t, hostCfg.SecurityOpt)
	})
}

func TestContainerOptions_HealthCheckFlags(t *testing.T) {
	t.Run("health-cmd flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--health-cmd", "curl -f http://localhost/"})
		require.NoError(t, err)
		assert.Equal(t, "curl -f http://localhost/", opts.HealthCmd)
	})

	t.Run("health-interval flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--health-interval", "30s"})
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, opts.HealthInterval)
	})

	t.Run("health-timeout flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--health-timeout", "10s"})
		require.NoError(t, err)
		assert.Equal(t, 10*time.Second, opts.HealthTimeout)
	})

	t.Run("health-retries flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--health-retries", "3"})
		require.NoError(t, err)
		assert.Equal(t, 3, opts.HealthRetries)
	})

	t.Run("health-start-period flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--health-start-period", "5s"})
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, opts.HealthStartPeriod)
	})

	t.Run("no-healthcheck flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--no-healthcheck"})
		require.NoError(t, err)
		assert.True(t, opts.NoHealthcheck)
	})
}

func TestContainerOptions_BuildConfigs_HealthCheck(t *testing.T) {
	t.Run("health-cmd sets CMD-SHELL healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.HealthCmd = "curl -f http://localhost/"

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, []string{"CMD-SHELL", "curl -f http://localhost/"}, cfg.Healthcheck.Test)
	})

	t.Run("health-interval is set in healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.HealthCmd = "echo ok"
		opts.HealthInterval = 30 * time.Second

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, 30*time.Second, cfg.Healthcheck.Interval)
	})

	t.Run("health-timeout is set in healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.HealthCmd = "echo ok"
		opts.HealthTimeout = 10 * time.Second

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, 10*time.Second, cfg.Healthcheck.Timeout)
	})

	t.Run("health-retries is set in healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.HealthCmd = "echo ok"
		opts.HealthRetries = 3

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, 3, cfg.Healthcheck.Retries)
	})

	t.Run("health-start-period is set in healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.HealthCmd = "echo ok"
		opts.HealthStartPeriod = 5 * time.Second

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, 5*time.Second, cfg.Healthcheck.StartPeriod)
	})

	t.Run("no-healthcheck disables health check", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.NoHealthcheck = true

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, []string{"NONE"}, cfg.Healthcheck.Test)
	})

	t.Run("no-healthcheck conflicts with health options", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.NoHealthcheck = true
		opts.HealthCmd = "echo ok"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--no-healthcheck conflicts")
	})

	t.Run("empty health options do not set healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Nil(t, cfg.Healthcheck)
	})
}

func TestContainerOptions_RuntimeFlags(t *testing.T) {
	t.Run("restart flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--restart", "on-failure:3"})
		require.NoError(t, err)
		assert.Equal(t, "on-failure:3", opts.Restart)
	})

	t.Run("stop-signal flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--stop-signal", "SIGKILL"})
		require.NoError(t, err)
		assert.Equal(t, "SIGKILL", opts.StopSignal)
	})

	t.Run("stop-timeout flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--stop-timeout", "30"})
		require.NoError(t, err)
		assert.Equal(t, 30, opts.StopTimeout)
	})

	t.Run("init flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--init"})
		require.NoError(t, err)
		assert.True(t, opts.Init)
	})
}

func TestContainerOptions_BuildConfigs_Runtime(t *testing.T) {
	t.Run("restart policy is set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Restart = "always"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "always", string(hostCfg.RestartPolicy.Name))
	})

	t.Run("restart policy with max retries", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Restart = "on-failure:5"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "on-failure", string(hostCfg.RestartPolicy.Name))
		assert.Equal(t, 5, hostCfg.RestartPolicy.MaximumRetryCount)
	})

	t.Run("invalid restart policy format", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Restart = "on-failure:invalid"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum retry count must be an integer")
	})

	t.Run("restart policy with empty name before colon", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Restart = ":5"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no policy provided before colon")
	})

	t.Run("stop-signal is set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.StopSignal = "SIGKILL"

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "SIGKILL", cfg.StopSignal)
	})

	t.Run("stop-timeout is set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.StopTimeout = 30

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.StopTimeout)
		assert.Equal(t, 30, *cfg.StopTimeout)
	})

	t.Run("init is set", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Init = true

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, hostCfg.Init)
		assert.True(t, *hostCfg.Init)
	})

	t.Run("restart and rm conflict", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.AutoRemove = true
		opts.Restart = "always"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot specify both --restart and --rm")
	})

	t.Run("restart no and rm do not conflict", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.AutoRemove = true
		opts.Restart = "no"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
	})
}

// TestContainerOptions_ValidationErrors tests that invalid options produce clear error messages.
// This is critical for user experience - errors should guide users toward correct usage.
func TestContainerOptions_ValidationErrors(t *testing.T) {
	t.Run("invalid memory format produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		err := opts.Memory.Set("invalid")
		require.Error(t, err)
		// Error should mention it's invalid - pflag handles this
	})

	t.Run("invalid CPU format produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		err := opts.CPUs.Set("not-a-number")
		require.Error(t, err)
	})

	t.Run("invalid DNS address produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.DNS = []string{"not-an-ip-address"}

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid DNS server address")
	})

	t.Run("invalid restart policy max retries produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Restart = "on-failure:not-a-number"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum retry count must be an integer")
	})

	t.Run("restart policy missing name produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Restart = ":3"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no policy provided before colon")
	})

	t.Run("memory-swap without memory produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.MemorySwap.Set("1g"))

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memory-swap requires --memory")
	})

	t.Run("memory-swap less than memory produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		require.NoError(t, opts.Memory.Set("2g"))
		require.NoError(t, opts.MemorySwap.Set("1g"))

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memory-swap must be greater than or equal")
	})

	t.Run("healthcheck conflict produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.NoHealthcheck = true
		opts.HealthCmd = "echo ok"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--no-healthcheck conflicts")
	})

	t.Run("restart and rm conflict produces clear error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.AutoRemove = true
		opts.Restart = "always"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot specify both --restart and --rm")
	})
}

// --- Phase 2: New Fields Tests ---

func TestContainerOptions_AttachFlag(t *testing.T) {
	t.Run("attach stdin only", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"-a", "stdin"})
		require.NoError(t, err)
		assert.Equal(t, []string{"stdin"}, opts.Attach.GetAll())
	})

	t.Run("attach validates values", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"-a", "invalid"})
		require.Error(t, err)
	})

	t.Run("attach controls what gets attached", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Attach.Set("stdout"))

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.False(t, cfg.AttachStdin)
		assert.True(t, cfg.AttachStdout)
		assert.False(t, cfg.AttachStderr)
	})
}

func TestContainerOptions_NewSimpleFields(t *testing.T) {
	t.Run("env-file flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--env-file", "/path/to/.env"})
		require.NoError(t, err)
		assert.Equal(t, []string{"/path/to/.env"}, opts.EnvFile)
	})

	t.Run("label-file flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--label-file", "/path/to/labels"})
		require.NoError(t, err)
		assert.Equal(t, []string{"/path/to/labels"}, opts.LabelsFile)
	})

	t.Run("domainname flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--domainname", "example.com"})
		require.NoError(t, err)
		assert.Equal(t, "example.com", opts.Domainname)
	})

	t.Run("cidfile flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cidfile", "/tmp/cid"})
		require.NoError(t, err)
		assert.Equal(t, "/tmp/cid", opts.ContainerIDFile)
	})

	t.Run("group-add flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--group-add", "audio", "--group-add", "video"})
		require.NoError(t, err)
		assert.Equal(t, []string{"audio", "video"}, opts.GroupAdd)
	})
}

func TestContainerOptions_BuildConfigs_NewSimpleFields(t *testing.T) {
	t.Run("domainname is set in container config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Domainname = "example.com"

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "example.com", cfg.Domainname)
	})

	t.Run("cidfile is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.ContainerIDFile = "/tmp/cid"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "/tmp/cid", hostCfg.ContainerIDFile)
	})

	t.Run("group-add is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.GroupAdd = []string{"audio", "video"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"audio", "video"}, hostCfg.GroupAdd)
	})
}

func TestContainerOptions_NewNetworkingFlags(t *testing.T) {
	t.Run("dns-option flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--dns-option", "ndots:5"})
		require.NoError(t, err)
		assert.Equal(t, []string{"ndots:5"}, opts.DNSOptions)
	})

	t.Run("expose flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--expose", "80", "--expose", "443"})
		require.NoError(t, err)
		assert.Equal(t, []string{"80", "443"}, opts.Expose)
	})

	t.Run("publish-all flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"-P"})
		require.NoError(t, err)
		assert.True(t, opts.PublishAll)
	})

	t.Run("mac-address flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--mac-address", "92:d0:c6:0a:29:33"})
		require.NoError(t, err)
		assert.Equal(t, "92:d0:c6:0a:29:33", opts.MacAddress)
	})

	t.Run("ip flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--ip", "172.30.100.104"})
		require.NoError(t, err)
		assert.Equal(t, "172.30.100.104", opts.IPv4Address)
	})

	t.Run("ip6 flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--ip6", "2001:db8::33"})
		require.NoError(t, err)
		assert.Equal(t, "2001:db8::33", opts.IPv6Address)
	})

	t.Run("link flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--link", "db:database"})
		require.NoError(t, err)
		assert.Equal(t, []string{"db:database"}, opts.Links)
	})

	t.Run("network-alias flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--network-alias", "myalias"})
		require.NoError(t, err)
		assert.Equal(t, []string{"myalias"}, opts.Aliases)
	})
}

func TestContainerOptions_BuildConfigs_NewNetworking(t *testing.T) {
	t.Run("dns-options are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.DNSOptions = []string{"ndots:5", "timeout:2"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"ndots:5", "timeout:2"}, hostCfg.DNSOptions)
	})

	t.Run("expose ports are set in container config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Expose = []string{"80/tcp"}

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.NotNil(t, cfg.ExposedPorts)
		assert.Equal(t, 1, len(cfg.ExposedPorts))
	})

	t.Run("publish-all is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.PublishAll = true

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.True(t, hostCfg.PublishAllPorts)
	})

	t.Run("network with aliases and IPs", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Network = "mynet"
		opts.Aliases = []string{"web", "frontend"}
		opts.IPv4Address = "172.30.100.104"

		_, _, netCfg, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, netCfg)
		ep := netCfg.EndpointsConfig["mynet"]
		require.NotNil(t, ep)
		assert.Equal(t, []string{"web", "frontend"}, ep.Aliases)
		require.NotNil(t, ep.IPAMConfig)
		assert.Equal(t, "172.30.100.104", ep.IPAMConfig.IPv4Address.String())
	})

	t.Run("network with invalid IPv4 returns error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Network = "mynet"
		opts.IPv4Address = "not-an-ip"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid IPv4 address")
	})

	t.Run("network with MAC address", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Network = "mynet"
		opts.MacAddress = "92:d0:c6:0a:29:33"

		_, _, netCfg, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		ep := netCfg.EndpointsConfig["mynet"]
		assert.Equal(t, "92:d0:c6:0a:29:33", ep.MacAddress.String())
	})

	t.Run("invalid MAC address returns error", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Network = "mynet"
		opts.MacAddress = "invalid-mac"

		_, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid MAC address")
	})

	t.Run("links are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Links = []string{"db:database"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"db:database"}, hostCfg.Links)
	})
}

func TestContainerOptions_NewResourceFlags(t *testing.T) {
	t.Run("memory-reservation flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--memory-reservation", "256m"})
		require.NoError(t, err)
		assert.Equal(t, int64(256*1024*1024), opts.MemoryReservation.Value())
	})

	t.Run("shm-size flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--shm-size", "128m"})
		require.NoError(t, err)
		assert.Equal(t, int64(128*1024*1024), opts.ShmSize.Value())
	})

	t.Run("cpuset-cpus flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cpuset-cpus", "0,1,2"})
		require.NoError(t, err)
		assert.Equal(t, "0,1,2", opts.CPUSetCPUs)
	})

	t.Run("blkio-weight flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--blkio-weight", "500"})
		require.NoError(t, err)
		assert.Equal(t, uint16(500), opts.BlkioWeight)
	})

	t.Run("pids-limit flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--pids-limit", "100"})
		require.NoError(t, err)
		assert.Equal(t, int64(100), opts.PidsLimit)
	})

	t.Run("oom-kill-disable flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--oom-kill-disable"})
		require.NoError(t, err)
		assert.True(t, opts.OOMKillDisable)
	})

	t.Run("oom-score-adj flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--oom-score-adj", "-500"})
		require.NoError(t, err)
		assert.Equal(t, -500, opts.OOMScoreAdj)
	})

	t.Run("memory-swappiness flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--memory-swappiness", "50"})
		require.NoError(t, err)
		assert.Equal(t, int64(50), opts.Swappiness)
	})

	t.Run("ulimit flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--ulimit", "nofile=1024:2048"})
		require.NoError(t, err)
		require.Equal(t, 1, opts.Ulimits.Len())
	})
}

func TestContainerOptions_BuildConfigs_NewResourceLimits(t *testing.T) {
	t.Run("memory-reservation is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.MemoryReservation.Set("256m"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(256*1024*1024), hostCfg.MemoryReservation)
	})

	t.Run("shm-size is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.ShmSize.Set("128m"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(128*1024*1024), hostCfg.ShmSize)
	})

	t.Run("cpuset-cpus is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CPUSetCPUs = "0-3"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "0-3", hostCfg.CpusetCpus)
	})

	t.Run("cpuset-mems is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CPUSetMems = "0,1"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "0,1", hostCfg.CpusetMems)
	})

	t.Run("cpu-period is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CPUPeriod = 100000

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(100000), hostCfg.CPUPeriod)
	})

	t.Run("cpu-quota is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CPUQuota = 50000

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, int64(50000), hostCfg.CPUQuota)
	})

	t.Run("blkio-weight is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.BlkioWeight = 500

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, uint16(500), hostCfg.BlkioWeight)
	})

	t.Run("pids-limit is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.PidsLimit = 100

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, hostCfg.PidsLimit)
		assert.Equal(t, int64(100), *hostCfg.PidsLimit)
	})

	t.Run("oom-kill-disable is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.OOMKillDisable = true

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, hostCfg.OomKillDisable)
		assert.True(t, *hostCfg.OomKillDisable)
	})

	t.Run("oom-score-adj is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.OOMScoreAdj = -500

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, -500, hostCfg.OomScoreAdj)
	})

	t.Run("memory-swappiness is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Swappiness = 50

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, hostCfg.MemorySwappiness)
		assert.Equal(t, int64(50), *hostCfg.MemorySwappiness)
	})

	t.Run("default swappiness -1 does not set host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Nil(t, hostCfg.MemorySwappiness)
	})

	t.Run("ulimits are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Ulimits.Set("nofile=1024:2048"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.Len(t, hostCfg.Ulimits, 1)
		assert.Equal(t, "nofile", hostCfg.Ulimits[0].Name)
	})
}

func TestContainerOptions_NamespaceFlags(t *testing.T) {
	t.Run("pid mode flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--pid", "host"})
		require.NoError(t, err)
		assert.Equal(t, "host", opts.PidMode)
	})

	t.Run("ipc mode flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--ipc", "host"})
		require.NoError(t, err)
		assert.Equal(t, "host", opts.IpcMode)
	})

	t.Run("uts mode flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--uts", "host"})
		require.NoError(t, err)
		assert.Equal(t, "host", opts.UtsMode)
	})

	t.Run("userns mode flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--userns", "host"})
		require.NoError(t, err)
		assert.Equal(t, "host", opts.UsernsMode)
	})

	t.Run("cgroupns mode flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cgroupns", "private"})
		require.NoError(t, err)
		assert.Equal(t, "private", opts.CgroupnsMode)
	})

	t.Run("cgroup-parent flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--cgroup-parent", "/mygroup"})
		require.NoError(t, err)
		assert.Equal(t, "/mygroup", opts.CgroupParent)
	})

	t.Run("runtime flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--runtime", "nvidia"})
		require.NoError(t, err)
		assert.Equal(t, "nvidia", opts.Runtime)
	})

	t.Run("isolation flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--isolation", "hyperv"})
		require.NoError(t, err)
		assert.Equal(t, "hyperv", opts.Isolation)
	})
}

func TestContainerOptions_BuildConfigs_Namespaces(t *testing.T) {
	t.Run("pid mode is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.PidMode = "host"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "host", string(hostCfg.PidMode))
	})

	t.Run("ipc mode is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.IpcMode = "host"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "host", string(hostCfg.IpcMode))
	})

	t.Run("uts mode is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.UtsMode = "host"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "host", string(hostCfg.UTSMode))
	})

	t.Run("userns mode is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.UsernsMode = "host"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "host", string(hostCfg.UsernsMode))
	})

	t.Run("cgroupns mode is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CgroupnsMode = "private"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "private", string(hostCfg.CgroupnsMode))
	})

	t.Run("cgroup-parent is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.CgroupParent = "/mygroup"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "/mygroup", hostCfg.CgroupParent)
	})

	t.Run("runtime is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Runtime = "nvidia"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "nvidia", hostCfg.Runtime)
	})

	t.Run("isolation is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.Isolation = "hyperv"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "hyperv", string(hostCfg.Isolation))
	})
}

func TestContainerOptions_LoggingFlags(t *testing.T) {
	t.Run("log-driver flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--log-driver", "json-file"})
		require.NoError(t, err)
		assert.Equal(t, "json-file", opts.LogDriver)
	})

	t.Run("log-opt flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--log-opt", "max-size=10m", "--log-opt", "max-file=3"})
		require.NoError(t, err)
		assert.Equal(t, []string{"max-size=10m", "max-file=3"}, opts.LogOpts)
	})
}

func TestContainerOptions_BuildConfigs_Logging(t *testing.T) {
	t.Run("log config is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.LogDriver = "json-file"
		opts.LogOpts = []string{"max-size=10m", "max-file=3"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "json-file", hostCfg.LogConfig.Type)
		assert.Equal(t, "10m", hostCfg.LogConfig.Config["max-size"])
		assert.Equal(t, "3", hostCfg.LogConfig.Config["max-file"])
	})

	t.Run("log driver without options", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.LogDriver = "syslog"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "syslog", hostCfg.LogConfig.Type)
		assert.Nil(t, hostCfg.LogConfig.Config)
	})
}

func TestContainerOptions_StorageNewFlags(t *testing.T) {
	t.Run("volume-driver flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--volume-driver", "local"})
		require.NoError(t, err)
		assert.Equal(t, "local", opts.VolumeDriver)
	})

	t.Run("storage-opt flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--storage-opt", "size=120G"})
		require.NoError(t, err)
		assert.Equal(t, []string{"size=120G"}, opts.StorageOpt)
	})

	t.Run("mount flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--mount", "type=bind,source=/src,target=/dst"})
		require.NoError(t, err)
		require.Equal(t, 1, opts.Mounts.Len())
	})
}

func TestContainerOptions_BuildConfigs_NewStorage(t *testing.T) {
	t.Run("volume-driver is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.VolumeDriver = "local"

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "local", hostCfg.VolumeDriver)
	})

	t.Run("storage-opt is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.StorageOpt = []string{"size=120G"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "120G", hostCfg.StorageOpt["size"])
	})

	t.Run("mount is set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Mounts.Set("type=bind,source=/src,target=/dst"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		// Mounts from --mount are appended to the mounts parameter
		require.Len(t, hostCfg.Mounts, 1)
		assert.Equal(t, "/dst", hostCfg.Mounts[0].Target)
	})
}

func TestContainerOptions_DeviceFlags(t *testing.T) {
	t.Run("device flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--device", "/dev/sda:/dev/xvdc:r"})
		require.NoError(t, err)
		require.Equal(t, 1, opts.Devices.Len())
	})

	t.Run("gpus flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--gpus", "all"})
		require.NoError(t, err)
		require.Equal(t, 1, opts.GPUs.Len())
	})

	t.Run("device-cgroup-rule flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--device-cgroup-rule", "c 1:3 rwm"})
		require.NoError(t, err)
		assert.Equal(t, []string{"c 1:3 rwm"}, opts.DeviceCgroupRules)
	})
}

func TestContainerOptions_BuildConfigs_Devices(t *testing.T) {
	t.Run("devices are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Devices.Set("/dev/sda:/dev/xvdc:r"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.Len(t, hostCfg.Devices, 1)
		assert.Equal(t, "/dev/sda", hostCfg.Devices[0].PathOnHost)
	})

	t.Run("gpus are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.GPUs.Set("all"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.Len(t, hostCfg.DeviceRequests, 1)
		assert.Equal(t, -1, hostCfg.DeviceRequests[0].Count)
	})

	t.Run("device-cgroup-rules are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.DeviceCgroupRules = []string{"c 1:3 rwm"}

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, []string{"c 1:3 rwm"}, hostCfg.DeviceCgroupRules)
	})
}

func TestContainerOptions_AnnotationsAndSysctls(t *testing.T) {
	t.Run("annotation flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--annotation", "com.example.key=value"})
		require.NoError(t, err)
		val, ok := opts.Annotations.Get("com.example.key")
		assert.True(t, ok)
		assert.Equal(t, "value", val)
	})

	t.Run("sysctl flag parsing", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--sysctl", "net.ipv4.ip_forward=1"})
		require.NoError(t, err)
		val, ok := opts.Sysctls.Get("net.ipv4.ip_forward")
		assert.True(t, ok)
		assert.Equal(t, "1", val)
	})
}

func TestContainerOptions_BuildConfigs_AnnotationsAndSysctls(t *testing.T) {
	t.Run("annotations are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Annotations.Set("com.example.key=value"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "value", hostCfg.Annotations["com.example.key"])
	})

	t.Run("sysctls are set in host config", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		require.NoError(t, opts.Sysctls.Set("net.ipv4.ip_forward=1"))

		_, hostCfg, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		assert.Equal(t, "1", hostCfg.Sysctls["net.ipv4.ip_forward"])
	})
}

func TestContainerOptions_BuildConfigs_HealthStartInterval(t *testing.T) {
	t.Run("health-start-interval is set in healthcheck", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Image = "alpine"
		opts.HealthCmd = "echo ok"
		opts.HealthStartInterval = 5 * time.Second

		cfg, _, _, err := opts.BuildConfigs(nil, &config.Config{})
		require.NoError(t, err)
		require.NotNil(t, cfg.Healthcheck)
		assert.Equal(t, 5*time.Second, cfg.Healthcheck.StartInterval)
	})
}

func TestContainerOptions_ValidateFlags_NewValidations(t *testing.T) {
	t.Run("swappiness too low", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Swappiness = -2

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memory-swappiness")
	})

	t.Run("swappiness too high", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Swappiness = 101

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memory-swappiness")
	})

	t.Run("swappiness valid range", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Swappiness = 50

		err := opts.ValidateFlags()
		require.NoError(t, err)
	})

	t.Run("swappiness -1 is valid", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.Swappiness = -1

		err := opts.ValidateFlags()
		require.NoError(t, err)
	})

	t.Run("blkio-weight too low", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.BlkioWeight = 5

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "blkio-weight")
	})

	t.Run("blkio-weight too high", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.BlkioWeight = 1001

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "blkio-weight")
	})

	t.Run("blkio-weight 0 is valid (disabled)", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.BlkioWeight = 0

		err := opts.ValidateFlags()
		require.NoError(t, err)
	})

	t.Run("oom-score-adj too low", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.OOMScoreAdj = -1001

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "oom-score-adj")
	})

	t.Run("oom-score-adj too high", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.OOMScoreAdj = 1001

		err := opts.ValidateFlags()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "oom-score-adj")
	})

	t.Run("oom-score-adj valid range", func(t *testing.T) {
		opts := NewContainerOptions()
		opts.OOMScoreAdj = -500

		err := opts.ValidateFlags()
		require.NoError(t, err)
	})
}

func TestContainerOptions_HiddenAliases(t *testing.T) {
	t.Run("dns-opt is hidden alias for dns-option", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--dns-opt", "ndots:5"})
		require.NoError(t, err)
		assert.Equal(t, []string{"ndots:5"}, opts.DNSOptions)
	})

	t.Run("net-alias is hidden alias for network-alias", func(t *testing.T) {
		opts := NewContainerOptions()
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		AddFlags(flags, opts)

		err := flags.Parse([]string{"--net-alias", "myalias"})
		require.NoError(t, err)
		assert.Equal(t, []string{"myalias"}, opts.Aliases)
	})
}
