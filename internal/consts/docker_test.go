package consts_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
)

func TestDockerHostSocketPath(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "unset", env: "", want: ""},
		{
			name: "rootless unix socket",
			env:  "unix:///run/user/1003/docker.sock",
			want: "/run/user/1003/docker.sock",
		},
		{
			name: "default unix socket",
			env:  "unix:///var/run/docker.sock",
			want: "/var/run/docker.sock",
		},
		{name: "non-unix value passes through verbatim", env: "tcp://127.0.0.1:2375", want: "tcp://127.0.0.1:2375"},
		{name: "fd value passes through verbatim", env: "fd://", want: "fd://"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(consts.EnvDockerHost, tt.env)
			if got := consts.DockerHostSocketPath(); got != tt.want {
				t.Errorf("DockerHostSocketPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
