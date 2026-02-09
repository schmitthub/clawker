package configtest

import (
	"sync"

	"github.com/schmitthub/clawker/internal/config"
)

// InMemorySettingsLoader implements config.SettingsLoader with in-memory storage.
// Useful for tests and fawker that don't need filesystem I/O for settings.
type InMemorySettingsLoader struct {
	mu       sync.Mutex
	settings *config.Settings
	created  bool
}

// NewInMemorySettingsLoader creates a new in-memory settings loader.
// If initial settings are provided, the first one is used; otherwise DefaultSettings is used on first Load.
func NewInMemorySettingsLoader(initial ...*config.Settings) *InMemorySettingsLoader {
	l := &InMemorySettingsLoader{}
	if len(initial) > 0 && initial[0] != nil {
		l.settings = initial[0]
	}
	return l
}

// Path returns a sentinel path indicating in-memory storage.
func (l *InMemorySettingsLoader) Path() string {
	return "(in-memory)"
}

// ProjectSettingsPath returns empty string — in-memory loader has no project layer.
func (l *InMemorySettingsLoader) ProjectSettingsPath() string {
	return ""
}

// Exists always returns true for in-memory settings.
func (l *InMemorySettingsLoader) Exists() bool {
	return true
}

// Load returns the stored settings, or DefaultSettings if none have been saved.
func (l *InMemorySettingsLoader) Load() (*config.Settings, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.settings == nil {
		return config.DefaultSettings(), nil
	}
	return l.settings, nil
}

// Save stores the settings in memory.
func (l *InMemorySettingsLoader) Save(s *config.Settings) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.settings = s
	return nil
}

// EnsureExists returns (true, nil) on first call, (false, nil) thereafter.
func (l *InMemorySettingsLoader) EnsureExists() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.created {
		return false, nil
	}
	l.created = true
	return true, nil
}
