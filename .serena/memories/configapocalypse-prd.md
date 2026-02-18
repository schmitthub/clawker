# GitHub CLI Config Package Architecture

CODE MAP: `@~/.serena/memories/config-codemap.md`

The GitHub CLI config package follows a **layered architecture** with clear separation between domain interfaces and implementation, using dependency injection through the factory pattern to support testing and modularity.

## Overall Design Pattern and Structure

The config package is organized into two main layers:

### 1. Domain Layer (`internal/gh`)
The `internal/gh` package defines the domain interfaces that represent the application's configuration contract. [1](#1-0)

The core interface is `Config`, which provides methods for reading configuration values, managing aliases, handling authentication, and applying migrations. [2](#1-1)

Key abstractions include:
- **ConfigEntry**: Wraps configuration values with their source (user-provided or default) [3](#1-2)
- **AuthConfig**: Interface for authentication-specific operations [4](#1-3)
- **AliasConfig**: Interface for managing command aliases [5](#1-4)
- **Migration**: Interface for versioned config schema migrations [6](#1-5)

### 2. Implementation Layer (`internal/config`)
The `internal/config` package provides concrete implementations of these interfaces. The main type is the unexported `cfg` struct that wraps the underlying `ghConfig.Config` from the external `cli/go-gh` library. [7](#1-6)

## Loading and Merging Multiple Configuration Sources

The configuration system uses a **hierarchical merging strategy** with the following precedence:

### Configuration Loading
The `NewConfig()` function initializes configuration by calling `ghConfig.Read()` with a fallback configuration that contains default values. [8](#1-7)

The fallback configuration is defined as a YAML string with sensible defaults for all settings. [9](#1-8)

### Multi-Level Lookup Strategy
Configuration values are resolved through a three-tier lookup implemented in the `GetOrDefault` method:

1. **Host-specific configuration**: First checks for a value under `hosts.<hostname>.<key>`
2. **Global configuration**: Falls back to top-level configuration `<key>`
3. **Application defaults**: Finally uses hardcoded defaults [10](#1-9)

### Environment Variable Precedence
Environment variables take precedence over configuration files. For example, authentication tokens follow this order:

- `GH_TOKEN` or `GITHUB_TOKEN` (for github.com and ghe.com subdomains)
- `GH_ENTERPRISE_TOKEN` or `GITHUB_ENTERPRISE_TOKEN` (for GitHub Enterprise)
- Configuration file values
- Keyring-stored values [11](#1-10)

The `ioStreams` function demonstrates how environment variables override config file settings for features like pager, prompts, and accessibility options. [12](#1-11)

### Authentication Token Storage
Authentication uses a sophisticated multi-source system:

1. **Environment variables**: Checked first via `ghauth.TokenFromEnvOrConfig()`
2. **Keyring storage**: Secure encrypted storage for tokens per user
3. **Plain text config**: Fallback when keyring is unavailable [13](#1-12)

## Dependency Injection and Testing

### Factory Pattern
The config package uses the **Factory pattern** for dependency injection. The `cmdutil.Factory` struct contains a function pointer for lazy initialization of the config. [14](#1-13)

The actual implementation uses a closure that caches the config after first access, ensuring it's only loaded once per command invocation. [15](#1-14)

### Testing Support

The package provides several mechanisms for testing:

#### 1. Mock Generation
Interfaces are annotated with `go:generate` directives to auto-generate mocks using the `moq` tool. [16](#1-15)

The generated `ConfigMock` provides function fields for each interface method with call tracking. [17](#1-16)

#### 2. Stub Configurations
The `stub.go` file provides test helpers:

- **NewBlankConfig()**: Creates a mock config with default values
- **NewFromString()**: Creates a mock config from a YAML string
- **NewIsolatedTestConfig()**: Sets up isolated test environment with mock keyring and temporary config directory [18](#1-17)

#### 3. Override Mechanisms
The `AuthConfig` type provides testing-specific override methods that allow tests to control behavior without touching disk or keyrings:

- `SetActiveToken()`: Override token resolution
- `SetHosts()`: Override host list
- `SetDefaultHost()`: Override default host [19](#1-18)

### Configuration Migration System
The architecture supports versioned schema migrations through the `Migration` interface. Migrations specify pre/post versions and execute transformations on the config. [20](#1-19)

An example is the `MultiAccount` migration that transforms single-user config to support multiple authenticated users per host. [21](#1-20)

## Notes

The architecture follows clean architecture principles by:
- Defining domain contracts in `internal/gh` independent of implementation details
- Encapsulating external library (`go-gh`) dependencies in `internal/config`
- Using function-based dependency injection for testability
- Supporting multiple configuration sources with well-defined precedence
- Providing rich testing utilities without coupling tests to filesystem or keyring operations

The config system integrates with the broader CLI through the factory pattern, where each command receives a factory instance and calls `f.Config()` to lazily load configuration only when needed, improving startup time and error handling.

### Citations

**File:** internal/gh/gh.go (L1-10)
```go
// Package gh provides types that represent the domain of the CLI application.
//
// For example, the CLI expects to be able to get and set user configuration in order to perform its functionality,
// so the Config interface is defined here, though the concrete implementation lives elsewhere. Though the current
// implementation of config writes to certain files on disk, that is an implementation detail compared to the contract
// laid out in the interface here.
//
// Currently this package is in an early state but we could imagine other domain concepts living here for interacting
// with git or GitHub.
package gh
```

**File:** internal/gh/gh.go (L17-27)
```go
type ConfigSource string

const (
	ConfigDefaultProvided ConfigSource = "default"
	ConfigUserProvided    ConfigSource = "user"
)

type ConfigEntry struct {
	Value  string
	Source ConfigSource
}
```

**File:** internal/gh/gh.go (L29-78)
```go
// A Config implements persistent storage and modification of application configuration.
//
//go:generate moq -rm -pkg ghmock -out mock/config.go . Config
type Config interface {
	// GetOrDefault provides primitive access for fetching configuration values, optionally scoped by host.
	GetOrDefault(hostname string, key string) o.Option[ConfigEntry]
	// Set provides primitive access for setting configuration values, optionally scoped by host.
	Set(hostname string, key string, value string)

	// AccessibleColors returns the configured accessible_colors setting, optionally scoped by host.
	AccessibleColors(hostname string) ConfigEntry
	// AccessiblePrompter returns the configured accessible_prompter setting, optionally scoped by host.
	AccessiblePrompter(hostname string) ConfigEntry
	// Browser returns the configured browser, optionally scoped by host.
	Browser(hostname string) ConfigEntry
	// ColorLabels returns the configured color_label setting, optionally scoped by host.
	ColorLabels(hostname string) ConfigEntry
	// Editor returns the configured editor, optionally scoped by host.
	Editor(hostname string) ConfigEntry
	// GitProtocol returns the configured git protocol, optionally scoped by host.
	GitProtocol(hostname string) ConfigEntry
	// HTTPUnixSocket returns the configured HTTP unix socket, optionally scoped by host.
	HTTPUnixSocket(hostname string) ConfigEntry
	// Pager returns the configured Pager, optionally scoped by host.
	Pager(hostname string) ConfigEntry
	// Prompt returns the configured prompt, optionally scoped by host.
	Prompt(hostname string) ConfigEntry
	// PreferEditorPrompt returns the configured editor-based prompt, optionally scoped by host.
	PreferEditorPrompt(hostname string) ConfigEntry
	// Spinner returns the configured spinner setting, optionally scoped by host.
	Spinner(hostname string) ConfigEntry

	// Aliases provides persistent storage and modification of command aliases.
	Aliases() AliasConfig

	// Authentication provides persistent storage and modification of authentication configuration.
	Authentication() AuthConfig

	// CacheDir returns the directory where the cacheable artifacts can be persisted.
	CacheDir() string

	// Migrate applies a migration to the configuration.
	Migrate(Migration) error

	// Version returns the current schema version of the configuration.
	Version() o.Option[string]

	// Write persists modifications to the configuration.
	Write() error
}
```

**File:** internal/gh/gh.go (L80-98)
```go
// Migration is the interface that config migrations must implement.
//
// Migrations will receive a copy of the config, and should modify that copy
// as necessary. After migration has completed, the modified config contents
// will be used.
//
// The calling code is expected to verify that the current version of the config
// matches the PreVersion of the migration before calling Do, and will set the
// config version to the PostVersion after the migration has completed successfully.
//
//go:generate moq -rm  -pkg ghmock -out mock/migration.go . Migration
type Migration interface {
	// PreVersion is the required config version for this to be applied
	PreVersion() string
	// PostVersion is the config version that must be applied after migration
	PostVersion() string
	// Do is expected to apply any necessary changes to the config in place
	Do(*ghConfig.Config) error
}
```

**File:** internal/gh/gh.go (L100-169)
```go
// AuthConfig is used for interacting with some persistent configuration for gh,
// with knowledge on how to access encrypted storage when necessary.
// Behavior is scoped to authentication specific tasks.
type AuthConfig interface {
	// HasActiveToken returns true when a token for the hostname is present.
	HasActiveToken(hostname string) bool

	// ActiveToken will retrieve the active auth token for the given hostname, searching environment variables,
	// general configuration, and finally encrypted storage.
	ActiveToken(hostname string) (token string, source string)

	// HasEnvToken returns true when a token has been specified in an environment variable, else returns false.
	HasEnvToken() bool

	// TokenFromKeyring will retrieve the auth token for the given hostname, only searching in encrypted storage.
	TokenFromKeyring(hostname string) (token string, err error)

	// TokenFromKeyringForUser will retrieve the auth token for the given hostname and username, only searching
	// in encrypted storage.
	//
	// An empty username will return an error because the potential to return the currently active token under
	// surprising cases is just too high to risk compared to the utility of having the function being smart.
	TokenFromKeyringForUser(hostname, username string) (token string, err error)

	// ActiveUser will retrieve the username for the active user at the given hostname.
	//
	// This will not be accurate if the oauth token is set from an environment variable.
	ActiveUser(hostname string) (username string, err error)

	// Hosts retrieves a list of known hosts.
	Hosts() []string

	// DefaultHost retrieves the default host.
	DefaultHost() (host string, source string)

	// Login will set user, git protocol, and auth token for the given hostname.
	//
	// If the encrypt option is specified it will first try to store the auth token
	// in encrypted storage and will fall back to the general insecure configuration.
	Login(hostname, username, token, gitProtocol string, secureStorage bool) (insecureStorageUsed bool, err error)

	// SwitchUser switches the active user for a given hostname.
	SwitchUser(hostname, user string) error

	// Logout will remove user, git protocol, and auth token for the given hostname.
	// It will remove the auth token from the encrypted storage if it exists there.
	Logout(hostname, username string) error

	// UsersForHost retrieves a list of users configured for a specific host.
	UsersForHost(hostname string) []string

	// TokenForUser retrieves the authentication token and its source for a specified user and hostname.
	TokenForUser(hostname, user string) (token string, source string, err error)

	// The following methods are only for testing and that is a design smell we should consider fixing.

	// SetActiveToken will override any token resolution and return the given token and source for all calls to
	// ActiveToken.
	// Use for testing purposes only.
	SetActiveToken(token, source string)

	// SetHosts will override any hosts resolution and return the given hosts for all calls to Hosts.
	// Use for testing purposes only.
	SetHosts(hosts []string)

	// SetDefaultHost will override any host resolution and return the given host and source for all calls to
	// DefaultHost.
	// Use for testing purposes only.
	SetDefaultHost(host, source string)
}
```

**File:** internal/gh/gh.go (L171-184)
```go
// AliasConfig defines an interface for managing command aliases.
type AliasConfig interface {
	// Get retrieves the expansion for a specified alias.
	Get(alias string) (expansion string, err error)

	// Add adds a new alias with the specified expansion.
	Add(alias, expansion string)

	// Delete removes an alias.
	Delete(alias string) error

	// All returns a map of all aliases to their corresponding expansions.
	All() map[string]string
}
```

**File:** internal/config/config.go (L39-45)
```go
func NewConfig() (gh.Config, error) {
	c, err := ghConfig.Read(fallbackConfig())
	if err != nil {
		return nil, err
	}
	return &cfg{c}, nil
}
```

**File:** internal/config/config.go (L47-50)
```go
// Implements Config interface
type cfg struct {
	cfg *ghConfig.Config
}
```

**File:** internal/config/config.go (L52-80)
```go
func (c *cfg) get(hostname, key string) o.Option[string] {
	if hostname != "" {
		val, err := c.cfg.Get([]string{hostsKey, hostname, key})
		if err == nil {
			return o.Some(val)
		}
	}

	val, err := c.cfg.Get([]string{key})
	if err == nil {
		return o.Some(val)
	}

	return o.None[string]()
}

func (c *cfg) GetOrDefault(hostname, key string) o.Option[gh.ConfigEntry] {
	if val := c.get(hostname, key); val.IsSome() {
		// Map the Option[string] to Option[gh.ConfigEntry] with a source of ConfigUserProvided
		return o.Map(val, toConfigEntry(gh.ConfigUserProvided))
	}

	if defaultVal := defaultFor(key); defaultVal.IsSome() {
		// Map the Option[string] to Option[gh.ConfigEntry] with a source of ConfigDefaultProvided
		return o.Map(defaultVal, toConfigEntry(gh.ConfigDefaultProvided))
	}

	return o.None[gh.ConfigEntry]()
}
```

**File:** internal/config/config.go (L176-203)
```go
func (c *cfg) Migrate(m gh.Migration) error {
	// If there is no version entry we must never have applied a migration, and the following conditional logic
	// handles the version as an empty string correctly.
	version := c.Version().UnwrapOrZero()

	// If migration has already occurred then do not attempt to migrate again.
	if m.PostVersion() == version {
		return nil
	}

	// If migration is incompatible with current version then return an error.
	if m.PreVersion() != version {
		return fmt.Errorf("failed to migrate as %q pre migration version did not match config version %q", m.PreVersion(), version)
	}

	if err := m.Do(c.cfg); err != nil {
		return fmt.Errorf("failed to migrate config: %s", err)
	}

	c.Set("", versionKey, m.PostVersion())

	// Then write out our migrated config.
	if err := c.Write(); err != nil {
		return fmt.Errorf("failed to write config after migration: %s", err)
	}

	return nil
}
```

**File:** internal/config/config.go (L218-346)
```go
// AuthConfig is used for interacting with some persistent configuration for gh,
// with knowledge on how to access encrypted storage when neccesarry.
// Behavior is scoped to authentication specific tasks.
type AuthConfig struct {
	cfg                 *ghConfig.Config
	defaultHostOverride func() (string, string)
	hostsOverride       func() []string
	tokenOverride       func(string) (string, string)
}

// ActiveToken will retrieve the active auth token for the given hostname,
// searching environment variables, plain text config, and
// lastly encrypted storage.
func (c *AuthConfig) ActiveToken(hostname string) (string, string) {
	if c.tokenOverride != nil {
		return c.tokenOverride(hostname)
	}
	token, source := ghauth.TokenFromEnvOrConfig(hostname)
	if token == "" {
		var user string
		var err error
		if user, err = c.ActiveUser(hostname); err == nil {
			token, err = c.TokenFromKeyringForUser(hostname, user)
		}
		if err != nil {
			// We should generally be able to find a token for the active user,
			// but in some cases such as if the keyring was set up in a very old
			// version of the CLI, it may only have a unkeyed token, so fallback
			// to it.
			token, err = c.TokenFromKeyring(hostname)
		}
		if err == nil {
			source = "keyring"
		}
	}
	return token, source
}

// HasActiveToken returns true when a token for the hostname is present.
func (c *AuthConfig) HasActiveToken(hostname string) bool {
	token, _ := c.ActiveToken(hostname)
	return token != ""
}

// HasEnvToken returns true when a token has been specified in an
// environment variable, else returns false.
func (c *AuthConfig) HasEnvToken() bool {
	// This will check if there are any environment variable
	// authentication tokens set for enterprise hosts.
	// Any non-github.com hostname is fine here
	hostname := "example.com"
	if c.tokenOverride != nil {
		token, _ := c.tokenOverride(hostname)
		if token != "" {
			return true
		}
	}
	// TODO: This is _extremely_ knowledgeable about the implementation of TokenFromEnvOrConfig
	// It has to use a hostname that is not going to be found in the hosts so that it
	// can guarantee that tokens will only be returned from a set env var.
	// Discussed here, but maybe worth revisiting: https://github.com/cli/cli/pull/7169#discussion_r1136979033
	token, _ := ghauth.TokenFromEnvOrConfig(hostname)
	return token != ""
}

// SetActiveToken will override any token resolution and return the given
// token and source for all calls to ActiveToken. Use for testing purposes only.
func (c *AuthConfig) SetActiveToken(token, source string) {
	c.tokenOverride = func(_ string) (string, string) {
		return token, source
	}
}

// TokenFromKeyring will retrieve the auth token for the given hostname,
// only searching in encrypted storage.
func (c *AuthConfig) TokenFromKeyring(hostname string) (string, error) {
	return keyring.Get(keyringServiceName(hostname), "")
}

// TokenFromKeyringForUser will retrieve the auth token for the given hostname
// and username, only searching in encrypted storage.
//
// An empty username will return an error because the potential to return
// the currently active token under surprising cases is just too high to risk
// compared to the utility of having the function being smart.
func (c *AuthConfig) TokenFromKeyringForUser(hostname, username string) (string, error) {
	if username == "" {
		return "", errors.New("username cannot be blank")
	}

	return keyring.Get(keyringServiceName(hostname), username)
}

// ActiveUser will retrieve the username for the active user at the given hostname.
// This will not be accurate if the oauth token is set from an environment variable.
func (c *AuthConfig) ActiveUser(hostname string) (string, error) {
	return c.cfg.Get([]string{hostsKey, hostname, userKey})
}

func (c *AuthConfig) Hosts() []string {
	if c.hostsOverride != nil {
		return c.hostsOverride()
	}
	return ghauth.KnownHosts()
}

// SetHosts will override any hosts resolution and return the given
// hosts for all calls to Hosts. Use for testing purposes only.
func (c *AuthConfig) SetHosts(hosts []string) {
	c.hostsOverride = func() []string {
		return hosts
	}
}

func (c *AuthConfig) DefaultHost() (string, string) {
	if c.defaultHostOverride != nil {
		return c.defaultHostOverride()
	}
	return ghauth.DefaultHost()
}

// SetDefaultHost will override any host resolution and return the given
// host and source for all calls to DefaultHost. Use for testing purposes only.
func (c *AuthConfig) SetDefaultHost(host, source string) {
	c.defaultHostOverride = func() (string, string) {
		return host, source
	}
}

```

**File:** internal/config/config.go (L548-579)
```go
const defaultConfigStr = `
# The default config file, auto-generated by gh. Run 'gh environment' to learn more about
# environment variables respected by gh and their precedence.

# The current version of the config schema
version: 1
# What protocol to use when performing git operations. Supported values: ssh, https
git_protocol: https
# What editor gh should run when creating issues, pull requests, etc. If blank, will refer to environment.
editor:
# When to interactively prompt. This is a global config that cannot be overridden by hostname. Supported values: enabled, disabled
prompt: enabled
# Preference for editor-based interactive prompting. This is a global config that cannot be overridden by hostname. Supported values: enabled, disabled
prefer_editor_prompt: disabled
# A pager program to send command output to, e.g. "less". If blank, will refer to environment. Set the value to "cat" to disable the pager.
pager:
# Aliases allow you to create nicknames for gh commands
aliases:
  co: pr checkout
# The path to a unix socket through which to send HTTP connections. If blank, HTTP traffic will be handled by net/http.DefaultTransport.
http_unix_socket:
# What web browser gh should use when opening URLs. If blank, will refer to environment.
browser:
# Whether to display labels using their RGB hex color codes in terminals that support truecolor. Supported values: enabled, disabled
color_labels: disabled
# Whether customizable, 4-bit accessible colors should be used. Supported values: enabled, disabled
accessible_colors: disabled
# Whether an accessible prompter should be used. Supported values: enabled, disabled
accessible_prompter: disabled
# Whether to use a animated spinner as a progress indicator. If disabled, a textual progress indicator is used instead. Supported values: enabled, disabled
spinner: enabled
`
```

**File:** pkg/cmd/factory/default.go (L252-262)
```go
func configFunc() func() (gh.Config, error) {
	var cachedConfig gh.Config
	var configError error
	return func() (gh.Config, error) {
		if cachedConfig != nil || configError != nil {
			return cachedConfig, configError
		}
		cachedConfig, configError = config.NewConfig()
		return cachedConfig, configError
	}
}
```

**File:** pkg/cmd/factory/default.go (L293-350)
```go
func ioStreams(f *cmdutil.Factory) *iostreams.IOStreams {
	io := iostreams.System()
	cfg, err := f.Config()
	if err != nil {
		return io
	}

	if _, ghPromptDisabled := os.LookupEnv("GH_PROMPT_DISABLED"); ghPromptDisabled {
		io.SetNeverPrompt(true)
	} else if prompt := cfg.Prompt(""); prompt.Value == "disabled" {
		io.SetNeverPrompt(true)
	}

	falseyValues := []string{"false", "0", "no", ""}

	accessiblePrompterValue, accessiblePrompterIsSet := os.LookupEnv("GH_ACCESSIBLE_PROMPTER")
	if accessiblePrompterIsSet {
		if !slices.Contains(falseyValues, accessiblePrompterValue) {
			io.SetAccessiblePrompterEnabled(true)
		}
	} else if prompt := cfg.AccessiblePrompter(""); prompt.Value == "enabled" {
		io.SetAccessiblePrompterEnabled(true)
	}

	ghSpinnerDisabledValue, ghSpinnerDisabledIsSet := os.LookupEnv("GH_SPINNER_DISABLED")
	if ghSpinnerDisabledIsSet {
		if !slices.Contains(falseyValues, ghSpinnerDisabledValue) {
			io.SetSpinnerDisabled(true)
		}
	} else if spinnerDisabled := cfg.Spinner(""); spinnerDisabled.Value == "disabled" {
		io.SetSpinnerDisabled(true)
	}

	// Pager precedence
	// 1. GH_PAGER
	// 2. pager from config
	// 3. PAGER
	if ghPager, ghPagerExists := os.LookupEnv("GH_PAGER"); ghPagerExists {
		io.SetPager(ghPager)
	} else if pager := cfg.Pager(""); pager.Value != "" {
		io.SetPager(pager.Value)
	}

	if ghColorLabels, ghColorLabelsExists := os.LookupEnv("GH_COLOR_LABELS"); ghColorLabelsExists {
		switch ghColorLabels {
		case "", "0", "false", "no":
			io.SetColorLabels(false)
		default:
			io.SetColorLabels(true)
		}
	} else if prompt := cfg.ColorLabels(""); prompt.Value == "enabled" {
		io.SetColorLabels(true)
	}

	io.SetAccessibleColorsEnabled(xcolor.IsAccessibleColorsEnabled())

	return io
}
```

**File:** pkg/cmdutil/factory.go (L19-38)
```go
type Factory struct {
	AppVersion     string
	ExecutableName string

	Browser          browser.Browser
	ExtensionManager extensions.ExtensionManager
	GitClient        *git.Client
	IOStreams        *iostreams.IOStreams
	Prompter         prompter.Prompter

	BaseRepo   func() (ghrepo.Interface, error)
	Branch     func() (string, error)
	Config     func() (gh.Config, error)
	HttpClient func() (*http.Client, error)
	// PlainHttpClient is a special HTTP client that does not automatically set
	// auth and other headers. This is meant to be used in situations where the
	// client needs to specify the headers itself (e.g. during login).
	PlainHttpClient func() (*http.Client, error)
	Remotes         func() (context.Remotes, error)
}
```

**File:** internal/gh/mock/config.go (L11-86)
```go

// Ensure, that ConfigMock does implement gh.Config.
// If this is not the case, regenerate this file with moq.
var _ gh.Config = &ConfigMock{}

// ConfigMock is a mock implementation of gh.Config.
//
//	func TestSomethingThatUsesConfig(t *testing.T) {
//
//		// make and configure a mocked gh.Config
//		mockedConfig := &ConfigMock{
//			AccessibleColorsFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the AccessibleColors method")
//			},
//			AccessiblePrompterFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the AccessiblePrompter method")
//			},
//			AliasesFunc: func() gh.AliasConfig {
//				panic("mock out the Aliases method")
//			},
//			AuthenticationFunc: func() gh.AuthConfig {
//				panic("mock out the Authentication method")
//			},
//			BrowserFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the Browser method")
//			},
//			CacheDirFunc: func() string {
//				panic("mock out the CacheDir method")
//			},
//			ColorLabelsFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the ColorLabels method")
//			},
//			EditorFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the Editor method")
//			},
//			GetOrDefaultFunc: func(hostname string, key string) o.Option[gh.ConfigEntry] {
//				panic("mock out the GetOrDefault method")
//			},
//			GitProtocolFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the GitProtocol method")
//			},
//			HTTPUnixSocketFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the HTTPUnixSocket method")
//			},
//			MigrateFunc: func(migration gh.Migration) error {
//				panic("mock out the Migrate method")
//			},
//			PagerFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the Pager method")
//			},
//			PreferEditorPromptFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the PreferEditorPrompt method")
//			},
//			PromptFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the Prompt method")
//			},
//			SetFunc: func(hostname string, key string, value string)  {
//				panic("mock out the Set method")
//			},
//			SpinnerFunc: func(hostname string) gh.ConfigEntry {
//				panic("mock out the Spinner method")
//			},
//			VersionFunc: func() o.Option[string] {
//				panic("mock out the Version method")
//			},
//			WriteFunc: func() error {
//				panic("mock out the Write method")
//			},
//		}
//
//		// use mockedConfig in code that requires gh.Config
//		// and then make assertions.
//
//	}
type ConfigMock struct {
	// AccessibleColorsFunc mocks the AccessibleColors method.
```

**File:** internal/config/stub.go (L16-126)
```go
func NewBlankConfig() *ghmock.ConfigMock {
	return NewFromString(defaultConfigStr)
}

func NewFromString(cfgStr string) *ghmock.ConfigMock {
	c := ghConfig.ReadFromString(cfgStr)
	cfg := cfg{c}
	mock := &ghmock.ConfigMock{}
	mock.GetOrDefaultFunc = func(host, key string) o.Option[gh.ConfigEntry] {
		return cfg.GetOrDefault(host, key)
	}
	mock.SetFunc = func(host, key, value string) {
		cfg.Set(host, key, value)
	}
	mock.WriteFunc = func() error {
		return cfg.Write()
	}
	mock.MigrateFunc = func(m gh.Migration) error {
		return cfg.Migrate(m)
	}
	mock.AliasesFunc = func() gh.AliasConfig {
		return &AliasConfig{cfg: c}
	}
	mock.AuthenticationFunc = func() gh.AuthConfig {
		return &AuthConfig{
			cfg: c,
			defaultHostOverride: func() (string, string) {
				return "github.com", "default"
			},
			hostsOverride: func() []string {
				keys, _ := c.Keys([]string{hostsKey})
				return keys
			},
			tokenOverride: func(hostname string) (string, string) {
				token, _ := c.Get([]string{hostsKey, hostname, oauthTokenKey})
				return token, oauthTokenKey
			},
		}
	}
	mock.AccessibleColorsFunc = func(hostname string) gh.ConfigEntry {
		return cfg.AccessibleColors(hostname)
	}
	mock.AccessiblePrompterFunc = func(hostname string) gh.ConfigEntry {
		return cfg.AccessiblePrompter(hostname)
	}
	mock.BrowserFunc = func(hostname string) gh.ConfigEntry {
		return cfg.Browser(hostname)
	}
	mock.ColorLabelsFunc = func(hostname string) gh.ConfigEntry {
		return cfg.ColorLabels(hostname)
	}
	mock.EditorFunc = func(hostname string) gh.ConfigEntry {
		return cfg.Editor(hostname)
	}
	mock.GitProtocolFunc = func(hostname string) gh.ConfigEntry {
		return cfg.GitProtocol(hostname)
	}
	mock.HTTPUnixSocketFunc = func(hostname string) gh.ConfigEntry {
		return cfg.HTTPUnixSocket(hostname)
	}
	mock.PagerFunc = func(hostname string) gh.ConfigEntry {
		return cfg.Pager(hostname)
	}
	mock.PromptFunc = func(hostname string) gh.ConfigEntry {
		return cfg.Prompt(hostname)
	}
	mock.PreferEditorPromptFunc = func(hostname string) gh.ConfigEntry {
		return cfg.PreferEditorPrompt(hostname)
	}
	mock.SpinnerFunc = func(hostname string) gh.ConfigEntry {
		return cfg.Spinner(hostname)
	}
	mock.VersionFunc = func() o.Option[string] {
		return cfg.Version()
	}
	mock.CacheDirFunc = func() string {
		return cfg.CacheDir()
	}
	return mock
}

// NewIsolatedTestConfig sets up a Mock keyring, creates a blank config
// overwrites the ghConfig.Read function that returns a singleton config
// in the real implementation, sets the GH_CONFIG_DIR env var so that
// any call to Write goes to a different location on disk, and then returns
// the blank config and a function that reads any data written to disk.
func NewIsolatedTestConfig(t *testing.T) (*cfg, func(io.Writer, io.Writer)) {
	keyring.MockInit()

	c := ghConfig.ReadFromString("")
	cfg := cfg{c}

	// The real implementation of config.Read uses a sync.Once
	// to read config files and initialise package level variables
	// that are used from then on.
	//
	// This means that tests can't be isolated from each other, so
	// we swap out the function here to return a new config each time.
	ghConfig.Read = func(_ *ghConfig.Config) (*ghConfig.Config, error) {
		return c, nil
	}

	// The config.Write method isn't defined in the same way as Read to allow
	// the function to be swapped out and it does try to write to disk.
	//
	// We should consider whether it makes sense to change that but in the meantime
	// we can use GH_CONFIG_DIR env var to ensure the tests remain isolated.
	readConfigs := StubWriteConfig(t)

	return &cfg, readConfigs
}
```

**File:** internal/config/migration/multi_account.go (L71-137)
```go
type MultiAccount struct {
	// Allow injecting a transport layer in tests.
	Transport http.RoundTripper
}

func (m MultiAccount) PreVersion() string {
	// It is expected that there is no version key since this migration
	// introduces it.
	return ""
}

func (m MultiAccount) PostVersion() string {
	return "1"
}

func (m MultiAccount) Do(c *config.Config) error {
	hostnames, err := c.Keys(hostsKey)
	// [github.com, github.localhost]
	// We wouldn't expect to have a hosts key when this is the first time anyone
	// is logging in with the CLI.
	var keyNotFoundError *config.KeyNotFoundError
	if errors.As(err, &keyNotFoundError) {
		return nil
	}
	if err != nil {
		return CowardlyRefusalError{errors.New("couldn't get hosts configuration")}
	}

	// If there are no hosts then it doesn't matter whether we migrate or not,
	// so lets avoid any confusion and say there's no migration required.
	if len(hostnames) == 0 {
		return nil
	}

	// Otherwise let's get to the business of migrating!
	for _, hostname := range hostnames {
		tokenSource, err := getToken(c, hostname)
		// If no token existed for this host we'll remove the entry from the hosts file
		// by deleting it and moving on to the next one.
		if errors.Is(err, noTokenError) {
			// The only error that can be returned here is the key not existing, which
			// we know can't be true.
			_ = c.Remove(append(hostsKey, hostname))
			continue
		}
		// For any other error we'll error out
		if err != nil {
			return CowardlyRefusalError{fmt.Errorf("couldn't find oauth token for %q: %w", hostname, err)}
		}

		username, err := getUsername(c, hostname, tokenSource.token, m.Transport)
		if err != nil {
			issueURL := "https://github.com/cli/cli/issues/8441"
			return CowardlyRefusalError{fmt.Errorf("couldn't get user name for %q please visit %s for help: %w", hostname, issueURL, err)}
		}

		if err := migrateConfig(c, hostname, username); err != nil {
			return CowardlyRefusalError{fmt.Errorf("couldn't migrate config for %q: %w", hostname, err)}
		}

		if err := migrateToken(hostname, username, tokenSource); err != nil {
			return CowardlyRefusalError{fmt.Errorf("couldn't migrate oauth token for %q: %w", hostname, err)}
		}
	}

	return nil
}
```
