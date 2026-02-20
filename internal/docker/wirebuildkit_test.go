package docker_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/stretchr/testify/require"
)

func TestWireBuildKit(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewBlankConfig())
	require.Nil(t, fake.Client.BuildKitImageBuilder)
	docker.WireBuildKit(fake.Client)
	require.NotNil(t, fake.Client.BuildKitImageBuilder)
}
