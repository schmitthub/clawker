package engine

import (
	"testing"
)

func TestImageTag(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"my-project", "clawker/my-project:latest"},
		{"test", "clawker/test:latest"},
		{"project123", "clawker/project123:latest"},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			got := ImageTag(tt.project)
			if got != tt.want {
				t.Errorf("ImageTag(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}

func TestIsAlpineImage(t *testing.T) {
	tests := []struct {
		imageRef string
		want     bool
	}{
		{"alpine:latest", true},
		{"alpine:3.19", true},
		{"node:alpine", true},
		{"node:20-alpine", true},
		{"python:3.12-alpine", true},
		{"ALPINE:LATEST", true}, // case insensitive
		{"node:20-slim", false},
		{"debian:bookworm", false},
		{"ubuntu:22.04", false},
		{"python:3.12", false},
	}

	for _, tt := range tests {
		t.Run(tt.imageRef, func(t *testing.T) {
			got := IsAlpineImage(tt.imageRef)
			if got != tt.want {
				t.Errorf("IsAlpineImage(%q) = %v, want %v", tt.imageRef, got, tt.want)
			}
		})
	}
}

func TestIsDebianImage(t *testing.T) {
	tests := []struct {
		imageRef string
		want     bool
	}{
		{"debian:bookworm", true},
		{"debian:bullseye", true},
		{"debian:trixie", true},
		{"ubuntu:22.04", true},
		{"node:20-slim", true},
		{"python:3.12-slim", true},
		{"DEBIAN:BOOKWORM", true}, // case insensitive
		{"alpine:latest", false},
		{"node:20-alpine", false},
		{"busybox:latest", false},
	}

	for _, tt := range tests {
		t.Run(tt.imageRef, func(t *testing.T) {
			got := IsDebianImage(tt.imageRef)
			if got != tt.want {
				t.Errorf("IsDebianImage(%q) = %v, want %v", tt.imageRef, got, tt.want)
			}
		})
	}
}

func TestImageClassification(t *testing.T) {
	// Test that common images are correctly classified
	// This is important for generating correct Dockerfiles

	alpineImages := []string{
		"alpine:latest",
		"alpine:3.19",
		"node:20-alpine",
		"python:3.12-alpine3.19",
	}

	debianImages := []string{
		"node:20-slim",
		"python:3.12-slim-bookworm",
		"debian:bookworm-slim",
		"ubuntu:24.04",
	}

	for _, img := range alpineImages {
		if !IsAlpineImage(img) {
			t.Errorf("Image %q should be classified as Alpine", img)
		}
		if IsDebianImage(img) {
			t.Errorf("Image %q should NOT be classified as Debian", img)
		}
	}

	for _, img := range debianImages {
		if IsAlpineImage(img) {
			t.Errorf("Image %q should NOT be classified as Alpine", img)
		}
		if !IsDebianImage(img) {
			t.Errorf("Image %q should be classified as Debian", img)
		}
	}
}

func TestImageLabels(t *testing.T) {
	project := "my-project"
	version := "1.2.3"
	labels := ImageLabels(project, version)
	expectedLabels := map[string]string{
		LabelManaged: "true",
		LabelProject: project,
		LabelVersion: version,
	}

	for key, expectedValue := range expectedLabels {
		if val, ok := labels[key]; !ok || val != expectedValue {
			t.Errorf("Expected label %q to be %q, got %q", key, expectedValue, val)
		}
	}
}
