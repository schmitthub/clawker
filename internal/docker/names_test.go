package docker

import (
	"strings"
	"testing"
)

func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
		errMsg  string // substring expected in error message
	}{
		// Valid names
		{"dev", false, ""},
		{"my-agent", false, ""},
		{"agent_1", false, ""},
		{"Agent.2", false, ""},
		{"a", false, ""},
		{"A1-b2_c3.d4", false, ""},
		{"test123", false, ""},
		{strings.Repeat("a", 200), false, ""}, // no engine-level length cap

		// Invalid: empty
		{"", true, "cannot be empty"},

		// Invalid: starts with hyphen (flag-like values)
		{"--rm", true, "cannot start with a hyphen"},
		{"-it", true, "cannot start with a hyphen"},
		{"-v", true, "cannot start with a hyphen"},

		// Invalid: starts with non-alphanumeric
		{".hidden", true, "only [a-zA-Z0-9]"},
		{"_private", true, "only [a-zA-Z0-9]"},

		// Invalid: contains illegal characters
		{"my agent", true, "only [a-zA-Z0-9]"},
		{"my@agent", true, "only [a-zA-Z0-9]"},
		{"my/agent", true, "only [a-zA-Z0-9]"},
		{"my:agent", true, "only [a-zA-Z0-9]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResourceName(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateResourceName(%q) = nil, want error containing %q", tt.name, tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateResourceName(%q) error = %q, want error containing %q", tt.name, err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("ValidateResourceName(%q) = %v, want nil", tt.name, err)
			}
		})
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		project string
		agent   string
		want    string
		wantErr bool
	}{
		{"myproject", "myagent", "clawker.myproject.myagent", false},
		{"test", "agent1", "clawker.test.agent1", false},
		{"backend", "worker", "clawker.backend.worker", false},
		{"", "dev", "clawker.dev", false},

		// Error cases
		{"myproject", "--rm", "", true},
		{"myproject", "", "", true},
		{"--bad", "agent", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.project+"_"+tt.agent, func(t *testing.T) {
			got, err := ContainerName(tt.project, tt.agent)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ContainerName(%q, %q) = %q, nil; want error", tt.project, tt.agent, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ContainerName(%q, %q) unexpected error: %v", tt.project, tt.agent, err)
				return
			}
			if got != tt.want {
				t.Errorf("ContainerName(%q, %q) = %q, want %q", tt.project, tt.agent, got, tt.want)
			}
		})
	}
}

func TestContainerNamesFromAgents(t *testing.T) {
	tests := []struct {
		name    string
		project string
		agents  []string
		want    []string
		wantErr bool
	}{
		{
			name:    "multiple agents with project",
			project: "myproject",
			agents:  []string{"dev", "worker"},
			want:    []string{"clawker.myproject.dev", "clawker.myproject.worker"},
		},
		{
			name:    "empty agents slice",
			project: "myproject",
			agents:  []string{},
			want:    []string{},
		},
		{
			name:    "nil agents slice",
			project: "myproject",
			agents:  nil,
			want:    nil,
		},
		{
			name:    "empty project gives 2-segment names",
			project: "",
			agents:  []string{"dev", "worker"},
			want:    []string{"clawker.dev", "clawker.worker"},
		},
		{
			name:    "invalid agent name returns error",
			project: "myproject",
			agents:  []string{"dev", "--rm"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ContainerNamesFromAgents(tt.project, tt.agents)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ContainerNamesFromAgents(%q, %v) = %v, nil; want error", tt.project, tt.agents, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ContainerNamesFromAgents(%q, %v) unexpected error: %v", tt.project, tt.agents, err)
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ContainerNamesFromAgents(%q, %v) returned %d items, want %d", tt.project, tt.agents, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ContainerNamesFromAgents(%q, %v)[%d] = %q, want %q", tt.project, tt.agents, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestContainerNamePrefix(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"myproject", "clawker.myproject."},
		{"test", "clawker.test."},
		{"", "clawker."},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			got := ContainerNamePrefix(tt.project)
			if got != tt.want {
				t.Errorf("ContainerNamePrefix(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}

func TestVolumeName(t *testing.T) {
	tests := []struct {
		project string
		agent   string
		purpose string
		want    string
		wantErr bool
	}{
		{"myproject", "myagent", "workspace", "clawker.myproject.myagent-workspace", false},
		{"test", "agent1", "config", "clawker.test.agent1-config", false},
		{"backend", "worker", "history", "clawker.backend.worker-history", false},
		{"", "dev", "workspace", "clawker.dev-workspace", false},

		// Error cases
		{"myproject", "--rm", "config", "", true},
		{"myproject", "", "config", "", true},
		{"--bad", "agent", "config", "", true},
	}

	for _, tt := range tests {
		name := tt.project + "_" + tt.agent + "_" + tt.purpose
		t.Run(name, func(t *testing.T) {
			got, err := VolumeName(tt.project, tt.agent, tt.purpose)
			if tt.wantErr {
				if err == nil {
					t.Errorf("VolumeName(%q, %q, %q) = %q, nil; want error", tt.project, tt.agent, tt.purpose, got)
				}
				return
			}
			if err != nil {
				t.Errorf("VolumeName(%q, %q, %q) unexpected error: %v", tt.project, tt.agent, tt.purpose, err)
				return
			}
			if got != tt.want {
				t.Errorf("VolumeName(%q, %q, %q) = %q, want %q", tt.project, tt.agent, tt.purpose, got, tt.want)
			}
		})
	}
}

func TestHarnessVolumeName(t *testing.T) {
	tests := []struct {
		name    string
		project string
		agent   string
		harness string
		volume  string
		want    string
		wantErr bool
	}{
		{
			name: "project scoped", project: "myapp", agent: "dev", harness: "claude",
			volume: "config", want: "clawker.myapp.dev-claude.config", wantErr: false,
		},
		{
			name: "global scoped", project: "", agent: "dev", harness: "codex",
			volume: "config", want: "clawker.dev-codex.config", wantErr: false,
		},
		{
			name: "hyphenated harness", project: "myapp", agent: "dev", harness: "my-fork",
			volume: "config", want: "clawker.myapp.dev-my-fork.config", wantErr: false,
		},
		{
			name: "clawker lifecycle purpose", project: "myapp", agent: "dev", harness: "claude",
			volume: "clawker", want: "clawker.myapp.dev-claude.clawker", wantErr: false,
		},

		// The harness segment is the exact selection spelling — for an
		// installed-bundle harness that is the qualified
		// namespace.bundle.component address (loadHarnessResolved), so a
		// dotted three-segment spelling MUST compose.
		{
			name: "qualified bundle harness", project: "myapp", agent: "dev", harness: "acme.tools.myharness",
			volume: "config", want: "clawker.myapp.dev-acme.tools.myharness.config", wantErr: false,
		},
		{
			name: "qualified global scoped", project: "", agent: "dev", harness: "acme.tools.myharness",
			volume: "config", want: "clawker.dev-acme.tools.myharness.config", wantErr: false,
		},

		// Segment validation (consts.ValidateHarnessRef for the harness,
		// consts.ValidateName for the volume) is what keeps the
		// (harness, volume) → name composition injective for a fixed
		// (project, agent): every token is dot-free, so the joined purpose
		// has exactly one dot (bare) or three (qualified) and splitting
		// recovers the pair. Cross-agent aliasing (agent "dev" + harness
		// "my-fork" vs agent "dev-my" + harness "fork") is out of the
		// proof's scope and is refused by EnsureHarnessVolume's ownership
		// check instead.
		{
			name: "empty harness rejected", project: "myapp", agent: "dev", harness: "",
			volume: "config", want: "", wantErr: true,
		},
		{
			name: "two-segment harness rejected", project: "myapp", agent: "dev", harness: "a.b",
			volume: "config", want: "", wantErr: true,
		},
		{
			name: "uppercase harness rejected", project: "myapp", agent: "dev", harness: "Claude",
			volume: "config", want: "", wantErr: true,
		},
		{
			name: "dotted volume rejected", project: "myapp", agent: "dev", harness: "claude",
			volume: "b.c.d", want: "", wantErr: true,
		},
		{
			name: "uppercase volume rejected", project: "myapp", agent: "dev", harness: "claude",
			volume: "Config", want: "", wantErr: true,
		},
		{
			name: "invalid agent rejected", project: "myapp", agent: "--rm", harness: "claude",
			volume: "config", want: "", wantErr: true,
		},
		{
			name: "invalid project rejected", project: "--bad", agent: "dev", harness: "claude",
			volume: "config", want: "", wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HarnessVolumeName(tt.project, tt.agent, tt.harness, tt.volume)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("HarnessVolumeName(%q, %q, %q, %q) error = %v, wantErr %v",
					tt.project, tt.agent, tt.harness, tt.volume, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("HarnessVolumeName(%q, %q, %q, %q) = %q, want %q",
					tt.project, tt.agent, tt.harness, tt.volume, got, tt.want)
			}
		})
	}
}

// TestHarnessVolumeName_DistinctPairsDistinctNames pins the injectivity of
// the composition — one harness landing on another's volume is exactly what
// the harness segment exists to prevent.
//
// Hyphen case: hyphens are legal inside harness names, so a "-" join would
// let harness "a" + volume "b-c" collide with harness "a-b" + volume "c";
// the "." join does not.
//
// Qualified case: a bare harness "a" with volume "b.c.d" would compose the
// same name as qualified harness "a.b.c" with volume "d" — that collision is
// closed by making dotted volume names unrepresentable (consts.ValidateName
// on the volume segment), which the test asserts directly.
func TestHarnessVolumeName_DistinctPairsDistinctNames(t *testing.T) {
	first, err := HarnessVolumeName("p", "dev", "a", "b-c")
	if err != nil {
		t.Fatalf("HarnessVolumeName(a, b-c) unexpected error: %v", err)
	}
	second, err := HarnessVolumeName("p", "dev", "a-b", "c")
	if err != nil {
		t.Fatalf("HarnessVolumeName(a-b, c) unexpected error: %v", err)
	}
	if first == second {
		t.Errorf("distinct (harness, volume) pairs composed the same volume name %q", first)
	}

	qualified, err := HarnessVolumeName("p", "dev", "a.b.c", "d")
	if err != nil {
		t.Fatalf("HarnessVolumeName(a.b.c, d) unexpected error: %v", err)
	}
	if aliased, aliasErr := HarnessVolumeName("p", "dev", "a", "b.c.d"); aliasErr == nil {
		t.Errorf("HarnessVolumeName(a, b.c.d) = %q, nil; want error — a dotted volume name could alias %q",
			aliased, qualified)
	}
}

func TestImageTag(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"myproject", "clawker-myproject:latest"},
		{"test", "clawker-test:latest"},
		{"", "clawker:latest"},
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

func TestBaseImageTag(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"myproject", "clawker-myproject:base"},
		{"", "clawker:base"},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			got := BaseImageTag(tt.project)
			if got != tt.want {
				t.Errorf("BaseImageTag(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}
