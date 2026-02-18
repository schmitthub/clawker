package config

// Provider is the single config gateway contract consumed by Factory.
//
// It exposes project- and settings-centric access, along with narrow write
// helpers and reload support.
type Provider interface {
	ProjectCfg() *Project
	UserSettings() *Settings
	ProjectKey() string
	ProjectFound() bool
	WorkDir() string
	ProjectRegistry() (Registry, error)
	SettingsLoader() SettingsLoader
	ProjectLoader() *ProjectLoader
	Reload() error
	SetSettingsLoader(SettingsLoader)
}

var _ Provider = (*Config)(nil)
