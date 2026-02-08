package whail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatBuildDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"},
		{200 * time.Millisecond, "0.2s"},
		{4100 * time.Millisecond, "4.1s"},
		{59 * time.Second, "59.0s"},
		{72 * time.Second, "1m 12s"},
		{3661 * time.Second, "1h 1m"},
		{-1 * time.Second, "0.0s"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatBuildDuration(tt.d)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsInternalStep(t *testing.T) {
	assert.True(t, IsInternalStep("[internal] load build definition from Dockerfile"))
	assert.True(t, IsInternalStep("[internal] load .dockerignore"))
	assert.True(t, IsInternalStep("[internal] load metadata for docker.io/library/node:20-slim"))
	assert.False(t, IsInternalStep("[stage-2 1/7] FROM node:20-slim"))
	assert.False(t, IsInternalStep("RUN npm install"))
	assert.False(t, IsInternalStep(""))
}

func TestCleanStepName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no mount flag",
			input: "[stage-2 3/7] RUN apt-get update",
			want:  "[stage-2 3/7] RUN apt-get update",
		},
		{
			name:  "mount flag stripped",
			input: "[stage-2 3/7] RUN --mount=type=cache,target=/var/cache/apt apt-get install -y git",
			want:  "[stage-2 3/7] RUN apt-get install -y git",
		},
		{
			name:  "multiple mount flags",
			input: "[stage-2 3/7] RUN --mount=type=cache,target=/root/.npm --mount=type=bind,source=package.json npm install",
			want:  "[stage-2 3/7] RUN npm install",
		},
		{
			name:  "whitespace collapsed",
			input: "[stage-2 3/7] RUN   apt-get  update  &&  apt-get  install",
			want:  "[stage-2 3/7] RUN apt-get update && apt-get install",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "simple name unchanged",
			input: "short",
			want:  "short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanStepName(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseBuildStage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"standard stage", "[stage-2 3/7] RUN apt-get update", "stage-2"},
		{"named stage", "[socket-server-builder 1/3] FROM golang:1.21", "socket-server-builder"},
		{"no bracket prefix", "RUN npm install", ""},
		{"empty string", "", ""},
		{"stage without step number", "[builder] FROM golang:1.21", "builder"},
		{"unclosed bracket", "[stage-2 3/7 RUN apt-get", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBuildStage(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
