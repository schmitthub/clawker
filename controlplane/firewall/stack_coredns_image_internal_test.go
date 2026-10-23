package firewall

import (
	"context"
	"io"
	"net/netip"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

func TestCorednsImageRefFor_ContentAddressed(t *testing.T) {
	ref := corednsImageRefFor("FROM a\n", []byte("coredns-v1"))
	tag := strings.TrimPrefix(ref, corednsImageRepo+":")
	assert.NotEqual(t, ref, tag, "ref carries the repository")
	assert.Len(t, tag, corednsImageTagLen)
	assert.Equal(t, ref, corednsImageRefFor("FROM a\n", []byte("coredns-v1")), "same content, same ref")
	assert.NotEqual(t, ref, corednsImageRefFor("FROM b\n", []byte("coredns-v1")), "Dockerfile change")
	assert.NotEqual(t, ref, corednsImageRefFor("FROM a\n", []byte("coredns-v2")), "binary change")
}

// newCorednsImageFixture builds a Stack on a FakeClient with a known
// embedded CoreDNS binary and records every image build tag.
func newCorednsImageFixture(t *testing.T) (*dockermocks.FakeClient, *Stack, *[]string) {
	t.Helper()
	testenv.New(t)
	orig := CoreDNSClawkerBinary
	CoreDNSClawkerBinary = []byte("coredns-v2")
	t.Cleanup(func() { CoreDNSClawkerBinary = orig })

	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)
	s, err := NewStack(fake.Client, cfg, logger.Nop(), nil, nil, nil, nil, newTestCAStore(t, consts.FirewallCertSubdir))
	require.NoError(t, err)

	built := &[]string{}
	fake.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		*built = append(*built, opts.Tags...)
		return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return fake, s, built
}

// A cached image from an older release must not stop the build of the
// image for the current embedded binary and Dockerfile.
func TestStack_ensureCorednsImage_StaleImageDoesNotShortCircuit(t *testing.T) {
	fake, s, built := newCorednsImageFixture(t)
	fake.SetupImageExists(corednsImageRepo+":latest", true)

	require.NoError(t, s.ensureCorednsImage(context.Background()))

	assert.Equal(t, []string{s.corednsImage}, *built)
	assert.NotEqual(t, corednsImageRepo+":latest", s.corednsImage)
	netInfo := &NetworkInfo{
		NetworkID: "net-test",
		EnvoyIP:   "172.20.0.2",
		CoreDNSIP: "172.20.0.3",
		Gateway:   netip.Addr{},
		Subnet:    netip.Prefix{},
		CIDR:      "",
	}
	assert.Equal(t, s.corednsImage, s.corednsContainerSpec(netInfo).image,
		"container runs the image that was built")
}

func TestStack_ensureCorednsImage_CurrentImageSkipsBuild(t *testing.T) {
	fake, s, built := newCorednsImageFixture(t)
	fake.SetupImageExists(s.corednsImage, true)

	require.NoError(t, s.ensureCorednsImage(context.Background()))

	assert.Empty(t, *built)
}
