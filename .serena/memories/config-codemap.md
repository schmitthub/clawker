## GitHub CLI Configuration Architecture
This codemap traces the GitHub CLI's configuration architecture from domain interfaces through implementation, showing how hierarchical configuration sources are resolved, environment variables take precedence, dependency injection works via factories, and testing infrastructure supports isolated test scenarios. Key locations include the core Config interface [1a], hierarchical resolution logic [2c], environment variable precedence [3a], factory-based dependency injection [4b], mock generation setup [5a], and migration system [6b].
### 1. Domain Interface Definition
Core configuration contract and domain abstractions
### 1a. Config Interface Definition (`gh.go:32`)
Main configuration contract with methods for reading/writing config values
```text
type Config interface {
```
### 1b. Core Lookup Method (`gh.go:34`)
Primary method for hierarchical configuration resolution
```text
GetOrDefault(hostname string, key string) o.Option[ConfigEntry]
```
### 1c. Config Value Wrapper (`gh.go:24`)
Encapsulates configuration values with their source metadata
```text
type ConfigEntry struct {
```
### 1d. Authentication Interface (`gh.go:103`)
Specialized interface for authentication-specific operations
```text
type AuthConfig interface {
```
### 2. Configuration Loading and Resolution
How configuration sources are loaded and merged with precedence
### 2a. Config Factory (`config.go:39`)
Creates new configuration instance with fallback defaults
```text
func NewConfig() (gh.Config, error) {
```
### 2b. Load Configuration (`config.go:40`)
Reads config files and merges with fallback defaults
```text
c, err := ghConfig.Read(fallbackConfig())
```
### 2c. Hierarchical Resolution (`config.go:68`)
Implements precedence: host-specific -> global -> defaults
```text
func (c *cfg) GetOrDefault(hostname, key string) o.Option[gh.ConfigEntry] {
```
### 2d. Host-specific Lookup (`config.go:69`)
First checks for host-scoped configuration values
```text
if val := c.get(hostname, key); val.IsSome() {
```
### 2e. Default Fallback (`config.go:74`)
Finally falls back to hardcoded application defaults
```text
if defaultVal := defaultFor(key); defaultVal.IsSome() {
```
### 3. Environment Variable Precedence
How environment variables override configuration files
### 3a. Environment First (`config.go:235`)
Authentication checks environment variables before files
```text
token, source := ghauth.TokenFromEnvOrConfig(hostname)
```
### 3b. Feature Override (`default.go:300`)
Environment variables can disable features regardless of config
```text
if _, ghPromptDisabled := os.LookupEnv("GH_PROMPT_DISABLED"); ghPromptDisabled {
```
### 3c. Pager Precedence (`default.go:330`)
GH_PAGER env var takes precedence over config file setting
```text
if ghPager, ghPagerExists := os.LookupEnv("GH_PAGER"); ghPagerExists {
```
### 4. Dependency Injection via Factory
How configuration is injected through factory pattern
### 4a. Factory Structure (`factory.go:19`)
Contains function pointers for lazy dependency injection
```text
type Factory struct {
```
### 4b. Config Function Field (`factory.go:31`)
Function-based injection for configuration dependency
```text
Config     func() (gh.Config, error)
```
### 4c. Factory Initialization (`default.go:32`)
Sets up lazy-loading config function in factory
```text
f.Config = configFunc()
```
### 4d. Config Closure (`default.go:252`)
Returns cached config function for singleton pattern
```text
func configFunc() func() (gh.Config, error) {
```
### 5. Testing Infrastructure
Mock generation and test configuration helpers
### 5a. Mock Generation (`gh.go:31`)
Auto-generates mock implementations using moq tool
```text
//go:generate moq -rm -pkg ghmock -out mock/config.go . Config
```
### 5b. Blank Config Stub (`stub.go:16`)
Creates mock config with default values for testing
```text
func NewBlankConfig() *ghmock.ConfigMock {
```
### 5c. Isolated Test Setup (`stub.go:102`)
Creates fully isolated test environment with temp dirs
```text
func NewIsolatedTestConfig(t *testing.T) (*cfg, func(io.Writer, io.Writer)) {
```
### 5d. Singleton Override (`stub.go:114`)
Overrides singleton config reading for test isolation
```text
ghConfig.Read = func(_ *ghConfig.Config) (*ghConfig.Config, error) {
```
### 6. Configuration Migration System
Versioned schema migration infrastructure
### 6a. Migration Interface (`gh.go:91`)
Defines contract for config schema migrations
```text
type Migration interface {
```
### 6b. Migration Execution (`config.go:176`)
Applies migrations with version checking and rollback safety
```text
func (c *cfg) Migrate(m gh.Migration) error {
```
### 6c. Version Check (`config.go:182`)
Prevents re-applying already completed migrations
```text
if m.PostVersion() == version {
```
### 6d. Migration Logic (`multi_account.go:86`)
Example migration transforming single-user to multi-user config
```text
func (m MultiAccount) Do(c *config.Config) error {
```