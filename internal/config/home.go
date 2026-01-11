package config

import (
	"os"
	"path/filepath"
)

const (
	// ClauckerHomeEnv is the environment variable for the claucker home directory
	ClauckerHomeEnv = "CLAUCKER_HOME"
	// DefaultClauckerDir is the default directory name under user home
	DefaultClauckerDir = ".claucker"
	// MonitorSubdir is the subdirectory for monitoring stack configuration
	MonitorSubdir = "monitor"
	// BuildSubdir is the subdirectory for build artifacts (versions.json, dockerfiles)
	BuildSubdir = "build"
	// DockerfilesSubdir is the subdirectory for generated Dockerfiles
	DockerfilesSubdir = "dockerfiles"
	// ClauckerNetwork is the name of the shared Docker network
	ClauckerNetwork = "claucker-net"
)

// ClauckerHome returns the claucker home directory.
// It checks CLAUCKER_HOME environment variable first, then defaults to ~/.claucker
func ClauckerHome() (string, error) {
	if home := os.Getenv(ClauckerHomeEnv); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultClauckerDir), nil
}

// MonitorDir returns the monitor stack directory (~/.claucker/monitor)
func MonitorDir() (string, error) {
	home, err := ClauckerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, MonitorSubdir), nil
}

// BuildDir returns the build artifacts directory (~/.claucker/build)
func BuildDir() (string, error) {
	home, err := ClauckerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, BuildSubdir), nil
}

// DockerfilesDir returns the dockerfiles directory (~/.claucker/build/dockerfiles)
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
