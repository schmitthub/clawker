package consts_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
)

func TestDockerHostSocketPath(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		wantPath string
		wantOK   bool
	}{
		{name: "unset", env: "", wantPath: "", wantOK: false},
		{
			name:     "rootless unix socket",
			env:      "unix:///run/user/1003/docker.sock",
			wantPath: "/run/user/1003/docker.sock",
			wantOK:   true,
		},
		{
			name:     "default unix socket",
			env:      "unix:///var/run/docker.sock",
			wantPath: "/var/run/docker.sock",
			wantOK:   true,
		},
		{name: "tcp scheme ignored", env: "tcp://127.0.0.1:2375", wantPath: "", wantOK: false},
		{name: "ssh scheme ignored", env: "ssh://user@host", wantPath: "", wantOK: false},
		{name: "unix scheme with empty path ignored", env: "unix://", wantPath: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(consts.EnvDockerHost, tt.env)
			got, ok := consts.DockerHostSocketPath()
			if ok != tt.wantOK || got != tt.wantPath {
				t.Errorf("DockerHostSocketPath() = (%q, %v), want (%q, %v)",
					got, ok, tt.wantPath, tt.wantOK)
			}
		})
	}
}
