package docker

import (
	"errors"
	"fmt"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/pkg/whail"
)

func TestParseContainers(t *testing.T) {
	tests := []struct {
		name string
		in   []container.Summary
		want []Container
	}{
		{
			name: "empty list",
			in:   []container.Summary{},
			want: []Container{},
		},
		{
			name: "single container with all labels",
			in: []container.Summary{
				{
					ID:    "abc123",
					Names: []string{"/clawker.myapp.ralph"},
					Labels: map[string]string{
						LabelProject: "myapp",
						LabelAgent:   "ralph",
						LabelImage:   "node:20",
						LabelWorkdir: "/workspace",
					},
					State:   "running",
					Created: 1700000000,
				},
			},
			want: []Container{
				{
					ID:      "abc123",
					Name:    "clawker.myapp.ralph",
					Project: "myapp",
					Agent:   "ralph",
					Image:   "node:20",
					Workdir: "/workspace",
					Status:  "running",
					Created: 1700000000,
				},
			},
		},
		{
			name: "multiple containers",
			in: []container.Summary{
				{
					ID:    "aaa",
					Names: []string{"/clawker.proj.agent1"},
					Labels: map[string]string{
						LabelProject: "proj",
						LabelAgent:   "agent1",
					},
					State:   "running",
					Created: 100,
				},
				{
					ID:    "bbb",
					Names: []string{"/clawker.proj.agent2"},
					Labels: map[string]string{
						LabelProject: "proj",
						LabelAgent:   "agent2",
					},
					State:   "exited",
					Created: 200,
				},
			},
			want: []Container{
				{
					ID:      "aaa",
					Name:    "clawker.proj.agent1",
					Project: "proj",
					Agent:   "agent1",
					Status:  "running",
					Created: 100,
				},
				{
					ID:      "bbb",
					Name:    "clawker.proj.agent2",
					Project: "proj",
					Agent:   "agent2",
					Status:  "exited",
					Created: 200,
				},
			},
		},
		{
			name: "missing labels returns empty strings",
			in: []container.Summary{
				{
					ID:      "ccc",
					Names:   []string{"/some-container"},
					Labels:  map[string]string{},
					State:   "created",
					Created: 300,
				},
			},
			want: []Container{
				{
					ID:      "ccc",
					Name:    "some-container",
					Project: "",
					Agent:   "",
					Image:   "",
					Workdir: "",
					Status:  "created",
					Created: 300,
				},
			},
		},
		{
			name: "name without leading slash",
			in: []container.Summary{
				{
					ID:     "ddd",
					Names:  []string{"no-slash"},
					Labels: map[string]string{},
					State:  "running",
				},
			},
			want: []Container{
				{
					ID:     "ddd",
					Name:   "no-slash",
					Status: "running",
				},
			},
		},
		{
			name: "no names at all",
			in: []container.Summary{
				{
					ID:     "eee",
					Names:  []string{},
					Labels: map[string]string{},
					State:  "running",
				},
			},
			want: []Container{
				{
					ID:     "eee",
					Name:   "",
					Status: "running",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseContainers(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseContainers() returned %d containers, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("container[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "DockerError with not found message",
			err:  &whail.DockerError{Op: "inspect", Message: "container not found"},
			want: true,
		},
		{
			name: "DockerError with No such message",
			err:  &whail.DockerError{Op: "inspect", Message: "No such container: abc123"},
			want: true,
		},
		{
			name: "DockerError with unrelated message",
			err:  &whail.DockerError{Op: "build", Message: "permission denied"},
			want: false,
		},
		{
			name: "raw error with not found",
			err:  fmt.Errorf("container not found"),
			want: true,
		},
		{
			name: "raw error with No such",
			err:  fmt.Errorf("No such image: foo"),
			want: true,
		},
		{
			name: "raw error unrelated",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
		{
			name: "wrapped DockerError with not found",
			err:  fmt.Errorf("operation failed: %w", &whail.DockerError{Op: "remove", Message: "not found"}),
			want: true,
		},
		{
			name: "wrapped raw error with not found",
			err:  fmt.Errorf("cleanup: %w", errors.New("volume not found")),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundError(tt.err)
			if got != tt.want {
				t.Errorf("isNotFoundError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
