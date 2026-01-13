package engine

import (
	"testing"

	"github.com/docker/go-connections/nat"
)

func TestParsePortSpecs(t *testing.T) {
	tests := []struct {
		name           string
		specs          []string
		wantPortCount  int
		wantErr        bool
		errContains    string
		checkPortMap   func(nat.PortMap) bool
		checkPortSet   func(nat.PortSet) bool
	}{
		{
			name:          "single container port",
			specs:         []string{"8080"},
			wantPortCount: 1,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok := ps["8080/tcp"]
				return ok
			},
			checkPortMap: func(pm nat.PortMap) bool {
				bindings, ok := pm["8080/tcp"]
				return ok && len(bindings) == 1 && bindings[0].HostPort == ""
			},
		},
		{
			name:          "host:container port",
			specs:         []string{"8080:8080"},
			wantPortCount: 1,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok := ps["8080/tcp"]
				return ok
			},
			checkPortMap: func(pm nat.PortMap) bool {
				bindings, ok := pm["8080/tcp"]
				return ok && len(bindings) == 1 && bindings[0].HostPort == "8080"
			},
		},
		{
			name:          "different host:container ports",
			specs:         []string{"3000:8080"},
			wantPortCount: 1,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok := ps["8080/tcp"]
				return ok
			},
			checkPortMap: func(pm nat.PortMap) bool {
				bindings, ok := pm["8080/tcp"]
				return ok && len(bindings) == 1 && bindings[0].HostPort == "3000"
			},
		},
		{
			name:          "ip:host:container port",
			specs:         []string{"127.0.0.1:8080:8080"},
			wantPortCount: 1,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok := ps["8080/tcp"]
				return ok
			},
			checkPortMap: func(pm nat.PortMap) bool {
				bindings, ok := pm["8080/tcp"]
				return ok && len(bindings) == 1 && bindings[0].HostIP == "127.0.0.1" && bindings[0].HostPort == "8080"
			},
		},
		{
			name:          "udp protocol",
			specs:         []string{"53:53/udp"},
			wantPortCount: 1,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok := ps["53/udp"]
				return ok
			},
			checkPortMap: func(pm nat.PortMap) bool {
				bindings, ok := pm["53/udp"]
				return ok && len(bindings) == 1 && bindings[0].HostPort == "53"
			},
		},
		{
			name:          "tcp protocol explicit",
			specs:         []string{"8080:8080/tcp"},
			wantPortCount: 1,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok := ps["8080/tcp"]
				return ok
			},
		},
		{
			name:          "multiple ports",
			specs:         []string{"8080:8080", "3000:3000"},
			wantPortCount: 2,
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok1 := ps["8080/tcp"]
				_, ok2 := ps["3000/tcp"]
				return ok1 && ok2
			},
		},
		{
			name:          "port range",
			specs:         []string{"24280-24282:24280-24282"},
			wantPortCount: 3, // 24280, 24281, 24282
			checkPortSet: func(ps nat.PortSet) bool {
				_, ok1 := ps["24280/tcp"]
				_, ok2 := ps["24281/tcp"]
				_, ok3 := ps["24282/tcp"]
				return ok1 && ok2 && ok3
			},
			checkPortMap: func(pm nat.PortMap) bool {
				b1, ok1 := pm["24280/tcp"]
				b2, ok2 := pm["24281/tcp"]
				b3, ok3 := pm["24282/tcp"]
				return ok1 && ok2 && ok3 &&
					b1[0].HostPort == "24280" &&
					b2[0].HostPort == "24281" &&
					b3[0].HostPort == "24282"
			},
		},
		{
			name:          "container port range only",
			specs:         []string{"24280-24282"},
			wantPortCount: 3,
			checkPortMap: func(pm nat.PortMap) bool {
				// Host ports should be empty (random)
				b, ok := pm["24280/tcp"]
				return ok && b[0].HostPort == ""
			},
		},
		{
			name:          "port range with ip",
			specs:         []string{"127.0.0.1:8080-8082:8080-8082"},
			wantPortCount: 3,
			checkPortMap: func(pm nat.PortMap) bool {
				b, ok := pm["8080/tcp"]
				return ok && b[0].HostIP == "127.0.0.1" && b[0].HostPort == "8080"
			},
		},
		{
			name:          "empty specs",
			specs:         []string{},
			wantPortCount: 0,
		},
		// Error cases
		{
			name:        "invalid protocol",
			specs:       []string{"8080:8080/sctp"},
			wantErr:     true,
			errContains: "invalid protocol",
		},
		{
			name:        "invalid format - too many colons",
			specs:       []string{"1:2:3:4"},
			wantErr:     true,
			errContains: "invalid format",
		},
		{
			name:        "invalid container port",
			specs:       []string{"abc"},
			wantErr:     true,
			errContains: "invalid container port",
		},
		{
			name:        "invalid host port",
			specs:       []string{"abc:8080"},
			wantErr:     true,
			errContains: "invalid host port",
		},
		{
			name:        "mismatched range sizes",
			specs:       []string{"8080-8082:8080-8081"},
			wantErr:     true,
			errContains: "same size",
		},
		{
			name:        "invalid range format",
			specs:       []string{"8080-8082-8084:8080-8082-8084"},
			wantErr:     true,
			errContains: "invalid range format",
		},
		{
			name:        "inverted range",
			specs:       []string{"8082-8080:8082-8080"},
			wantErr:     true,
			errContains: "start port",
		},
		{
			name:        "port out of range - too high",
			specs:       []string{"70000-70005:70000-70005"},
			wantErr:     true,
			errContains: "between 1 and 65535",
		},
		{
			name:        "port out of range - zero",
			specs:       []string{"0-5:0-5"},
			wantErr:     true,
			errContains: "between 1 and 65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			portMap, portSet, err := ParsePortSpecs(tt.specs)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParsePortSpecs() expected error containing %q, got nil", tt.errContains)
					return
				}
				if tt.errContains != "" && !containsString(err.Error(), tt.errContains) {
					t.Errorf("ParsePortSpecs() error = %q, want error containing %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("ParsePortSpecs() unexpected error: %v", err)
				return
			}

			if len(portSet) != tt.wantPortCount {
				t.Errorf("ParsePortSpecs() port count = %d, want %d", len(portSet), tt.wantPortCount)
			}

			if tt.checkPortSet != nil && !tt.checkPortSet(portSet) {
				t.Errorf("ParsePortSpecs() portSet check failed")
			}

			if tt.checkPortMap != nil && !tt.checkPortMap(portMap) {
				t.Errorf("ParsePortSpecs() portMap check failed")
			}
		})
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantStart int
		wantEnd   int
		wantErr   bool
	}{
		{
			name:      "valid range",
			input:     "8080-8082",
			wantStart: 8080,
			wantEnd:   8082,
		},
		{
			name:      "single port range",
			input:     "8080-8080",
			wantStart: 8080,
			wantEnd:   8080,
		},
		{
			name:      "max valid port",
			input:     "65530-65535",
			wantStart: 65530,
			wantEnd:   65535,
		},
		{
			name:      "min valid port",
			input:     "1-10",
			wantStart: 1,
			wantEnd:   10,
		},
		{
			name:    "invalid - no hyphen",
			input:   "8080",
			wantErr: true,
		},
		{
			name:    "invalid - multiple hyphens",
			input:   "8080-8081-8082",
			wantErr: true,
		},
		{
			name:    "invalid - non-numeric start",
			input:   "abc-8082",
			wantErr: true,
		},
		{
			name:    "invalid - non-numeric end",
			input:   "8080-xyz",
			wantErr: true,
		},
		{
			name:    "invalid - inverted",
			input:   "8082-8080",
			wantErr: true,
		},
		{
			name:    "invalid - port too high",
			input:   "65530-65536",
			wantErr: true,
		},
		{
			name:    "invalid - port zero",
			input:   "0-100",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := parseRange(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseRange(%q) expected error, got nil", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("parseRange(%q) unexpected error: %v", tt.input, err)
				return
			}

			if start != tt.wantStart {
				t.Errorf("parseRange(%q) start = %d, want %d", tt.input, start, tt.wantStart)
			}
			if end != tt.wantEnd {
				t.Errorf("parseRange(%q) end = %d, want %d", tt.input, end, tt.wantEnd)
			}
		})
	}
}

// TestParsePortSpecDockerCompatibility tests compatibility with Docker's port spec format
func TestParsePortSpecDockerCompatibility(t *testing.T) {
	// These are real-world port specs that Docker supports
	realWorldSpecs := []struct {
		name string
		spec string
		desc string
	}{
		{"web server", "80:80", "Standard HTTP port mapping"},
		{"https", "443:443", "Standard HTTPS port mapping"},
		{"localhost only", "127.0.0.1:3000:3000", "Development server on localhost"},
		{"postgres", "5432:5432", "PostgreSQL default port"},
		{"redis", "6379:6379", "Redis default port"},
		{"dns udp", "53:53/udp", "DNS UDP port"},
		{"dns tcp", "53:53/tcp", "DNS TCP port"},
		{"random host port", "8080", "Container port with random host port"},
		{"claude code mcp", "24280-24290:24280-24290", "MCP port range for Claude Code"},
	}

	for _, tt := range realWorldSpecs {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParsePortSpecs([]string{tt.spec})
			if err != nil {
				t.Errorf("ParsePortSpecs(%q) [%s] failed: %v", tt.spec, tt.desc, err)
			}
		})
	}
}

// containsString checks if s contains substr (case-insensitive for error messages)
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && contains(s, substr)))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
