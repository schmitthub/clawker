package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// Validator validates a Config for correctness
type Validator struct {
	workDir  string
	errors   []error
	warnings []string
}

// NewValidator creates a new validator for the given working directory
func NewValidator(workDir string) *Validator {
	return &Validator{
		workDir:  workDir,
		errors:   []error{},
		warnings: []string{},
	}
}

// Validate checks the configuration for errors and returns all found issues
func (v *Validator) Validate(cfg *Project) error {
	v.errors = []error{}
	v.warnings = []string{}

	v.validateVersion(cfg)
	v.validateBuild(cfg)
	v.validateWorkspace(cfg)
	v.validateSecurity(cfg)
	v.validateAgent(cfg)

	if len(v.errors) > 0 {
		return &MultiValidationError{Errors: v.errors}
	}
	return nil
}

func (v *Validator) addError(field, message string, value interface{}) {
	v.errors = append(v.errors, &ValidationError{
		Field:   field,
		Message: message,
		Value:   value,
	})
}

func (v *Validator) addWarning(field, message string) {
	warning := fmt.Sprintf("%s: %s", field, message)
	v.warnings = append(v.warnings, warning)
	// Log to file
	logger.Warn().
		Str("field", field).
		Msg(message)
}

// Warnings returns the list of validation warnings
func (v *Validator) Warnings() []string {
	return v.warnings
}

func (v *Validator) validateVersion(cfg *Project) {
	if cfg.Version == "" {
		v.addError("version", "is required", nil)
		return
	}
	if cfg.Version != "1" {
		v.addError("version", "must be '1' (only supported version)", cfg.Version)
	}
}

func (v *Validator) validateBuild(cfg *Project) {
	if cfg.Build.Image == "" && cfg.Build.Dockerfile == "" {
		v.addError("build.image", "either 'image' or 'dockerfile' is required", nil)
		return
	}

	// If dockerfile is specified, check it exists
	if cfg.Build.Dockerfile != "" {
		dockerfilePath := cfg.Build.Dockerfile
		if !filepath.IsAbs(dockerfilePath) {
			dockerfilePath = filepath.Join(v.workDir, dockerfilePath)
		}
		if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
			v.addError("build.dockerfile", "file does not exist", cfg.Build.Dockerfile)
		}
	}

	// Validate build context if specified
	if cfg.Build.Context != "" {
		contextPath := cfg.Build.Context
		if !filepath.IsAbs(contextPath) {
			contextPath = filepath.Join(v.workDir, contextPath)
		}
		info, err := os.Stat(contextPath)
		if os.IsNotExist(err) {
			v.addError("build.context", "directory does not exist", cfg.Build.Context)
		} else if err == nil && !info.IsDir() {
			v.addError("build.context", "must be a directory", cfg.Build.Context)
		}
	}

	// Validate instructions if specified
	v.validateInstructions(cfg)

	// Validate inject if specified
	v.validateInject(cfg)
}

func (v *Validator) validateWorkspace(cfg *Project) {
	if cfg.Workspace.RemotePath == "" {
		v.addError("workspace.remote_path", "is required", nil)
		return
	}

	if !filepath.IsAbs(cfg.Workspace.RemotePath) {
		v.addError("workspace.remote_path", "must be an absolute path", cfg.Workspace.RemotePath)
	}

	// Validate default mode
	if cfg.Workspace.DefaultMode != "" {
		if _, err := ParseMode(cfg.Workspace.DefaultMode); err != nil {
			v.addError("workspace.default_mode", "must be 'bind' or 'snapshot'", cfg.Workspace.DefaultMode)
		}
	}
}

func (v *Validator) validateSecurity(cfg *Project) {
	// Validate that firewall capabilities are present if firewall is enabled
	if cfg.Security.FirewallEnabled() {
		hasNetAdmin := false
		hasNetRaw := false
		for _, cap := range cfg.Security.CapAdd {
			if cap == "NET_ADMIN" {
				hasNetAdmin = true
			}
			if cap == "NET_RAW" {
				hasNetRaw = true
			}
		}
		if !hasNetAdmin || !hasNetRaw {
			logger.Debug().
				Bool("has_NET_ADMIN", hasNetAdmin).
				Bool("has_NET_RAW", hasNetRaw).
				Msg("firewall enabled; required capabilities will be added automatically if missing")
		}
	}

	// Validate firewall domain configuration
	if cfg.Security.Firewall != nil {
		fw := cfg.Security.Firewall

		// Warn if override_domains is set alongside add_domains or remove_domains
		// The behavior is well-defined (override wins), but user should know their add/remove are ignored
		if len(fw.OverrideDomains) > 0 && (len(fw.AddDomains) > 0 || len(fw.RemoveDomains) > 0) {
			v.addWarning("security.firewall", "override_domains is set; add_domains and remove_domains will be ignored")
		}

		// Validate add_domains format
		for i, domain := range fw.AddDomains {
			v.validateDomainFormat(fmt.Sprintf("security.firewall.add_domains[%d]", i), domain)
		}

		// Validate remove_domains format
		for i, domain := range fw.RemoveDomains {
			v.validateDomainFormat(fmt.Sprintf("security.firewall.remove_domains[%d]", i), domain)
		}

		// Validate override_domains format
		for i, domain := range fw.OverrideDomains {
			v.validateDomainFormat(fmt.Sprintf("security.firewall.override_domains[%d]", i), domain)
		}
	}
}

// validateDomainFormat checks if a domain string has a valid format
func (v *Validator) validateDomainFormat(fieldPath, domain string) {
	if strings.ContainsAny(domain, " \t\n") {
		v.addError(fieldPath, "contains whitespace", domain)
	}
	// Basic hostname pattern check (not exhaustive, but catches obvious errors)
	if domain == "" {
		v.addError(fieldPath, "empty domain", domain)
	}
}

func (v *Validator) validateAgent(cfg *Project) {
	// Validate include paths exist
	for i, include := range cfg.Agent.Includes {
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(v.workDir, includePath)
		}
		if _, err := os.Stat(includePath); os.IsNotExist(err) {
			v.addError(fmt.Sprintf("agent.includes[%d]", i), "file does not exist", include)
		}
	}

	// Validate env variable names
	for key := range cfg.Agent.Env {
		if strings.ContainsAny(key, " \t\n=") {
			v.addError("agent.env", "invalid environment variable name", key)
		}
	}
}

func (v *Validator) validateInstructions(cfg *Project) {
	if cfg.Build.Instructions == nil {
		return
	}
	inst := cfg.Build.Instructions

	// Validate COPY instructions
	for i, cp := range inst.Copy {
		v.validateCopyInstruction(i, cp)
	}

	// Validate ENV variable names
	for key := range inst.Env {
		if strings.ContainsAny(key, " \t\n=") {
			v.addError("build.instructions.env", "invalid environment variable name", key)
		}
	}

	// Validate LABEL keys
	for key := range inst.Labels {
		if strings.ContainsAny(key, " \t\n") {
			v.addError("build.instructions.labels", "invalid label key (contains whitespace)", key)
		}
	}

	// Validate EXPOSE ports
	for i, port := range inst.Expose {
		v.validateExposePort(i, port)
	}

	// Validate ARG definitions
	for i, arg := range inst.Args {
		v.validateArgDefinition(i, arg)
	}

	// Validate WORKDIR
	if inst.Workdir != "" && !filepath.IsAbs(inst.Workdir) {
		v.addError("build.instructions.workdir", "must be an absolute path", inst.Workdir)
	}

	// Validate HEALTHCHECK
	if inst.Healthcheck != nil {
		v.validateHealthcheck(inst.Healthcheck)
	}

	// Validate RUN commands
	for i, run := range inst.UserRun {
		v.validateRunInstruction(i, run, "build.instructions.user_run")
	}
	for i, run := range inst.RootRun {
		v.validateRunInstruction(i, run, "build.instructions.root_run")
	}
}

func (v *Validator) validateCopyInstruction(idx int, cp CopyInstruction) {
	field := fmt.Sprintf("build.instructions.copy[%d]", idx)

	if cp.Src == "" {
		v.addError(field+".src", "is required", nil)
	}
	if cp.Dest == "" {
		v.addError(field+".dest", "is required", nil)
	}

	// Security: prevent path traversal
	if strings.Contains(cp.Src, "..") {
		v.addError(field+".src", "must not contain '..' (path traversal)", cp.Src)
	}

	// Validate chown format (user:group or UID:GID)
	if cp.Chown != "" && !isValidChown(cp.Chown) {
		v.addError(field+".chown", "must be 'user:group' or 'UID:GID' format", cp.Chown)
	}

	// Validate chmod format (octal)
	if cp.Chmod != "" && !isValidChmod(cp.Chmod) {
		v.addError(field+".chmod", "must be valid chmod format (e.g., '755')", cp.Chmod)
	}
}

func (v *Validator) validateExposePort(idx int, port ExposePort) {
	field := fmt.Sprintf("build.instructions.expose[%d]", idx)

	if port.Port < 1 || port.Port > 65535 {
		v.addError(field+".port", "must be between 1 and 65535", port.Port)
	}

	if port.Protocol != "" && port.Protocol != "tcp" && port.Protocol != "udp" {
		v.addError(field+".protocol", "must be 'tcp' or 'udp'", port.Protocol)
	}
}

func (v *Validator) validateArgDefinition(idx int, arg ArgDefinition) {
	field := fmt.Sprintf("build.instructions.args[%d]", idx)

	if arg.Name == "" {
		v.addError(field+".name", "is required", nil)
	}

	// ARG names must be valid identifiers
	if arg.Name != "" && !isValidIdentifier(arg.Name) {
		v.addError(field+".name", "must be a valid identifier (alphanumeric and underscore)", arg.Name)
	}
}

func (v *Validator) validateHealthcheck(hc *HealthcheckConfig) {
	field := "build.instructions.healthcheck"

	if len(hc.Cmd) == 0 {
		v.addError(field+".cmd", "is required", nil)
	}

	// Validate duration formats
	if hc.Interval != "" && !isValidDuration(hc.Interval) {
		v.addError(field+".interval", "must be a valid duration (e.g., '30s', '1m')", hc.Interval)
	}
	if hc.Timeout != "" && !isValidDuration(hc.Timeout) {
		v.addError(field+".timeout", "must be a valid duration (e.g., '10s')", hc.Timeout)
	}
	if hc.StartPeriod != "" && !isValidDuration(hc.StartPeriod) {
		v.addError(field+".start_period", "must be a valid duration (e.g., '5s')", hc.StartPeriod)
	}

	if hc.Retries < 0 {
		v.addError(field+".retries", "must be non-negative", hc.Retries)
	}
}

func (v *Validator) validateRunInstruction(idx int, run RunInstruction, fieldPrefix string) {
	field := fmt.Sprintf("%s[%d]", fieldPrefix, idx)

	// Must have at least one of cmd, alpine, or debian
	if run.Cmd == "" && run.Alpine == "" && run.Debian == "" {
		v.addError(field, "must specify 'cmd', 'alpine', or 'debian'", nil)
		return
	}

	// Cannot have both generic cmd and OS-specific
	if run.Cmd != "" && (run.Alpine != "" || run.Debian != "") {
		v.addError(field, "cannot specify both 'cmd' and OS-specific commands (alpine/debian)", nil)
	}

	// Warn about potentially dangerous commands (non-blocking)
	cmds := []string{run.Cmd, run.Alpine, run.Debian}
	for _, cmd := range cmds {
		if cmd != "" {
			warnIfDangerousCommand(field, cmd)
		}
	}
}

func (v *Validator) validateInject(cfg *Project) {
	if cfg.Build.Inject == nil {
		return
	}

	inject := cfg.Build.Inject

	// Validate each injection point
	v.validateInjectionLines("build.inject.after_from", inject.AfterFrom)
	v.validateInjectionLines("build.inject.after_packages", inject.AfterPackages)
	v.validateInjectionLines("build.inject.after_user_setup", inject.AfterUserSetup)
	v.validateInjectionLines("build.inject.after_user_switch", inject.AfterUserSwitch)
	v.validateInjectionLines("build.inject.after_claude_install", inject.AfterClaudeInstall)
	v.validateInjectionLines("build.inject.before_entrypoint", inject.BeforeEntrypoint)
}

func (v *Validator) validateInjectionLines(field string, lines []string) {
	for i, line := range lines {
		lineField := fmt.Sprintf("%s[%d]", field, i)

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			v.addError(lineField, "cannot be empty", nil)
			continue
		}

		// Check for valid Dockerfile instruction prefix
		validPrefixes := []string{
			"RUN", "COPY", "ADD", "ENV", "ARG", "LABEL", "EXPOSE",
			"VOLUME", "USER", "WORKDIR", "ONBUILD", "STOPSIGNAL",
			"HEALTHCHECK", "SHELL", "#",
		}

		upperLine := strings.ToUpper(trimmed)
		hasValidPrefix := false
		for _, prefix := range validPrefixes {
			if strings.HasPrefix(upperLine, prefix+" ") || strings.HasPrefix(upperLine, prefix+"\t") || upperLine == prefix {
				hasValidPrefix = true
				break
			}
		}

		if !hasValidPrefix {
			displayLine := trimmed
			if len(displayLine) > 50 {
				displayLine = displayLine[:50] + "..."
			}
			v.addError(lineField, "must be a valid Dockerfile instruction", displayLine)
		}

		// Warn about dangerous patterns
		warnIfDangerousCommand(lineField, line)
	}
}

// Helper functions for validation

func isValidChown(s string) bool {
	// Valid formats: user, user:group, uid, uid:gid
	pattern := regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]*(:[a-zA-Z0-9_][a-zA-Z0-9_-]*)?$`)
	return pattern.MatchString(s)
}

func isValidChmod(s string) bool {
	// Valid formats: octal (e.g., 755, 0644)
	pattern := regexp.MustCompile(`^0?[0-7]{3,4}$`)
	return pattern.MatchString(s)
}

func isValidIdentifier(s string) bool {
	pattern := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	return pattern.MatchString(s)
}

func isValidDuration(s string) bool {
	// Valid formats: number followed by ns, us, ms, s, m, h
	pattern := regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h)$`)
	return pattern.MatchString(s)
}

// Dangerous command patterns to warn about
var dangerousPatterns = []struct {
	pattern *regexp.Regexp
	warning string
}{
	{regexp.MustCompile(`curl.*\|.*sh`), "piping curl to shell is risky - consider downloading and inspecting first"},
	{regexp.MustCompile(`wget.*\|.*sh`), "piping wget to shell is risky - consider downloading and inspecting first"},
	{regexp.MustCompile(`chmod.*777`), "chmod 777 grants excessive permissions"},
	{regexp.MustCompile(`rm\s+-rf\s+/[^/\s]`), "recursive deletion from root is dangerous"},
}

func warnIfDangerousCommand(field, cmd string) {
	for _, dp := range dangerousPatterns {
		if dp.pattern.MatchString(cmd) {
			logger.Warn().
				Str("field", field).
				Str("warning", dp.warning).
				Msg("potentially dangerous command detected in configuration")
		}
	}
}

// MultiValidationError holds multiple validation errors
type MultiValidationError struct {
	Errors []error
}

func (e *MultiValidationError) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("found %d configuration errors:\n", len(e.Errors)))
	for i, err := range e.Errors {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Error()))
	}
	return sb.String()
}

// ValidationErrors returns the individual errors
func (e *MultiValidationError) ValidationErrors() []error {
	return e.Errors
}
