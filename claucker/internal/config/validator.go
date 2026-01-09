package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Validator validates a Config for correctness
type Validator struct {
	workDir string
	errors  []error
}

// NewValidator creates a new validator for the given working directory
func NewValidator(workDir string) *Validator {
	return &Validator{
		workDir: workDir,
		errors:  []error{},
	}
}

// Validate checks the configuration for errors and returns all found issues
func (v *Validator) Validate(cfg *Config) error {
	v.errors = []error{}

	v.validateVersion(cfg)
	v.validateProject(cfg)
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

func (v *Validator) validateVersion(cfg *Config) {
	if cfg.Version == "" {
		v.addError("version", "is required", nil)
		return
	}
	if cfg.Version != "1" {
		v.addError("version", "must be '1' (only supported version)", cfg.Version)
	}
}

func (v *Validator) validateProject(cfg *Config) {
	if cfg.Project == "" {
		v.addError("project", "is required", nil)
		return
	}

	// Project name should be a valid container name component
	if strings.ContainsAny(cfg.Project, " \t\n/\\:*?\"<>|") {
		v.addError("project", "contains invalid characters (no spaces or special characters allowed)", cfg.Project)
	}

	if len(cfg.Project) > 64 {
		v.addError("project", "must be 64 characters or less", cfg.Project)
	}
}

func (v *Validator) validateBuild(cfg *Config) {
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
}

func (v *Validator) validateWorkspace(cfg *Config) {
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

func (v *Validator) validateSecurity(cfg *Config) {
	// Validate that firewall capabilities are present if firewall is enabled
	if cfg.Security.EnableFirewall {
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
			// This is a warning, not an error - we'll add the caps automatically
		}
	}

	// Validate allowed domains format
	for i, domain := range cfg.Security.AllowedDomains {
		if strings.ContainsAny(domain, " \t\n") {
			v.addError(fmt.Sprintf("security.allowed_domains[%d]", i), "contains whitespace", domain)
		}
	}
}

func (v *Validator) validateAgent(cfg *Config) {
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
