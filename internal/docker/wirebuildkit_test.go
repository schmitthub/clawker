package docker_test

import (
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/stretchr/testify/require"
)

func TestWireBuildKit(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	require.Nil(t, fake.Client.BuildKitImageBuilder)
	docker.WireBuildKit(fake.Client)
	require.NotNil(t, fake.Client.BuildKitImageBuilder)
}
