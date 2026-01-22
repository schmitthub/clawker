package testutil

import (
	"testing"

	"github.com/schmitthub/clawker/internal/docker"
	mock "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/pkg/whail"
	"go.uber.org/mock/gomock"
)

// MockDockerClient wraps a gomock-generated MockAPIClient with the full
// docker.Client chain needed for testing. This allows unit testing code
// that uses docker.Client without requiring a real Docker daemon.
//
// Usage:
//
//	func TestSomething(t *testing.T) {
//	    m := testutil.NewMockDockerClient(t)
//
//	    // Set expectations
//	    m.Mock.EXPECT().
//	        ImageList(gomock.Any(), gomock.Any()).
//	        Return(client.ImageListResult{Items: []image.Summary{...}}, nil)
//
//	    // Use the client
//	    result, err := SomeFunctionThatNeedsDocker(m.Client)
//	}
type MockDockerClient struct {
	// Mock is the underlying gomock mock - use this to set expectations
	Mock *mock.MockAPIClient

	// Client is the docker.Client that wraps the mock - pass this to code under test
	Client *docker.Client

	// Ctrl is the gomock controller - usually you don't need to access this directly
	Ctrl *gomock.Controller
}

// NewMockDockerClient creates a new MockDockerClient for unit testing.
// The gomock controller is automatically cleaned up via t.Cleanup().
//
// The returned MockDockerClient contains:
//   - Mock: for setting expectations with EXPECT()
//   - Client: the docker.Client to pass to code under test
//
// Example:
//
//	m := testutil.NewMockDockerClient(t)
//	m.Mock.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(...)
//	err := functionUnderTest(m.Client)
func NewMockDockerClient(t *testing.T) *MockDockerClient {
	t.Helper()

	ctrl := gomock.NewController(t)

	// Create the mock APIClient
	mockAPI := mock.NewMockAPIClient(ctrl)

	// Wrap in whail.Engine (this is what docker.Client embeds)
	engine := whail.NewFromExisting(mockAPI)

	// Wrap in docker.Client
	client := &docker.Client{Engine: engine}

	return &MockDockerClient{
		Mock:   mockAPI,
		Client: client,
		Ctrl:   ctrl,
	}
}
