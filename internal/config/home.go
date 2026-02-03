package config

import (
	"os"
	"path/filepath"
)

const (
	// ClawkerHomeEnv is the environment variable for the clawker home directory
	ClawkerHomeEnv = "CLAWKER_HOME"
	// DefaultClawkerDir is the default directory path under user home
	DefaultClawkerDir = ".local/clawker"
	// MonitorSubdir is the subdirectory for monitoring stack configuration
	MonitorSubdir = "monitor"
	// BuildSubdir is the subdirectory for build artifacts (versions.json, dockerfiles)
	BuildSubdir = "build"
	// DockerfilesSubdir is the subdirectory for generated Dockerfiles
	DockerfilesSubdir = "dockerfiles"
	// ClawkerNetwork is the name of the shared Docker network
	ClawkerNetwork = "clawker-net"
	// LogsSubdir is the subdirectory for log files
	LogsSubdir = "logs"
)

// ClawkerHome returns the clawker home directory.
// It checks CLAWKER_HOME environment variable first, then defaults to ~/.clawker
func ClawkerHome() (string, error) {
	if home := os.Getenv(ClawkerHomeEnv); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultClawkerDir), nil
}

// MonitorDir returns the monitor stack directory (~/.clawker/monitor)
func MonitorDir() (string, error) {
	home, err := ClawkerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, MonitorSubdir), nil
}

// BuildDir returns the build artifacts directory (~/.clawker/build)
func BuildDir() (string, error) {
	home, err := ClawkerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, BuildSubdir), nil
}

// DockerfilesDir returns the dockerfiles directory (~/.clawker/build/dockerfiles)
func DockerfilesDir() (string, error) {
	buildDir, err := BuildDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(buildDir, DockerfilesSubdir), nil
}

// EnsureDir creates a directory if it doesn't exist
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// LogsDir returns the logs directory (~/.local/clawker/logs)
func LogsDir() (string, error) {
	home, err := ClawkerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, LogsSubdir), nil
}

// HostProxyPIDFile returns the path to the host proxy PID file (~/.local/clawker/hostproxy.pid)
func HostProxyPIDFile() (string, error) {
	home, err := ClawkerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "hostproxy.pid"), nil
}

// HostProxyLogFile returns the path to the host proxy log file (~/.local/clawker/logs/hostproxy.log)
func HostProxyLogFile() (string, error) {
	logsDir, err := LogsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, "hostproxy.log"), nil
}
