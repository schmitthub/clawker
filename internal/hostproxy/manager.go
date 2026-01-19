package hostproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/schmitthub/clawker/pkg/logger"
)

// Manager manages the lifecycle of the host proxy server.
// It provides lazy initialization and ensures only one server runs at a time.
type Manager struct {
	port   int
	server *Server
	mu     sync.Mutex
}

// NewManager creates a new host proxy manager using the default port.
func NewManager() *Manager {
	return &Manager{port: DefaultPort}
}

// NewManagerWithPort creates a new host proxy manager using a custom port.
// This is primarily useful for testing.
func NewManagerWithPort(port int) *Manager {
	return &Manager{port: port}
}

// EnsureRunning starts the host proxy server if it's not already running.
// It performs a health check to verify the server is responsive.
func (m *Manager) EnsureRunning() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if server already running
	if m.server != nil && m.server.IsRunning() {
		return nil
	}

	// Check if another instance is already running on the port
	if m.isPortInUse() {
		logger.Debug().Int("port", m.port).Msg("host proxy already running on port")
		return nil
	}

	// Start new server
	m.server = NewServer(m.port)
	if err := m.server.Start(); err != nil {
		return fmt.Errorf("failed to start host proxy server: %w", err)
	}

	// Wait briefly for server to be ready
	time.Sleep(50 * time.Millisecond)

	// Verify server is responding
	if err := m.healthCheck(); err != nil {
		// Try to stop the server since it's not healthy
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.server.Stop(ctx)
		return fmt.Errorf("host proxy server not responding: %w", err)
	}

	return nil
}

// Stop gracefully stops the host proxy server.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server == nil {
		return nil
	}

	server := m.server
	m.server = nil // Clear reference before stopping

	return server.Stop(ctx)
}

// IsRunning returns whether the host proxy server is running.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.server != nil && m.server.IsRunning()
}

// Port returns the port the host proxy server is configured to use.
func (m *Manager) Port() int {
	return m.port
}

// ProxyURL returns the URL containers should use to reach the host proxy.
// This uses host.docker.internal which Docker automatically resolves to the host.
func (m *Manager) ProxyURL() string {
	return fmt.Sprintf("http://host.docker.internal:%d", m.port)
}

// isPortInUse checks if the configured port is already in use by a clawker host proxy.
// It verifies both the status code and service identifier to avoid mistaking
// another service for the clawker host proxy.
func (m *Manager) isPortInUse() bool {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", m.port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Verify it's actually a clawker host proxy by checking the service identifier
	var health healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return false
	}

	return health.Service == "clawker-host-proxy"
}

// healthCheck verifies the server is responding to requests.
func (m *Manager) healthCheck() error {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", m.port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
