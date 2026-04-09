package mocks

import (
	"net/http"
	"testing"

	moby "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	dockermock "github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/logger"
)

// NewTestManager creates a real firewall.Manager backed by a mock Docker client
// and the given config. All Docker API calls return 503 so rule store and config
// layer operations work without a Docker daemon.
func NewTestManager(t *testing.T, cfg config.Config) *firewall.Manager {
	t.Helper()
	cli, err := moby.New(dockermock.WithMockClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}))
	require.NoError(t, err)
	mgr, err := firewall.NewManager(cli, cfg, logger.Nop())
	require.NoError(t, err)
	return mgr
}
