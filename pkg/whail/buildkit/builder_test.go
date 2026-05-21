package buildkit

import (
	"testing"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
)

func TestExtractImageID(t *testing.T) {
	tests := []struct {
		name string
		resp *bkclient.SolveResponse
		want string
	}{
		{
			name: "nil response",
			resp: nil,
			want: "",
		},
		{
			name: "exporter omits digest key",
			resp: &bkclient.SolveResponse{ExporterResponse: map[string]string{
				"image.name": "myimage:latest",
			}},
			want: "",
		},
		{
			name: "digest present",
			resp: &bkclient.SolveResponse{ExporterResponse: map[string]string{
				exptypes.ExporterImageDigestKey: "sha256:abc",
			}},
			want: "sha256:abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractImageID(tt.resp)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
