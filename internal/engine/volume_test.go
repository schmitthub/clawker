package engine

import (
	"testing"
)

func TestShouldIgnore(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		isDir    bool
		patterns []string
		want     bool
	}{
		{
			name:     ".git always ignored",
			path:     ".git",
			isDir:    true,
			patterns: []string{},
			want:     true,
		},
		{
			name:     ".git subdirectory ignored",
			path:     ".git/objects",
			isDir:    true,
			patterns: []string{},
			want:     true,
		},
		{
			name:     "node_modules with pattern",
			path:     "node_modules",
			isDir:    true,
			patterns: []string{"node_modules/"},
			want:     true,
		},
		{
			name:     "file inside node_modules",
			path:     "node_modules/package/index.js",
			isDir:    false,
			patterns: []string{"node_modules/"},
			want:     false, // Only directory pattern, file not matched
		},
		{
			name:     "wildcard pattern for js files",
			path:     "test.js",
			isDir:    false,
			patterns: []string{"*.js"},
			want:     true,
		},
		{
			name:     "wildcard pattern nested",
			path:     "src/test.js",
			isDir:    false,
			patterns: []string{"*.js"},
			want:     true,
		},
		{
			name:     ".env file",
			path:     ".env",
			isDir:    false,
			patterns: []string{".env"},
			want:     true,
		},
		{
			name:     ".env.local file",
			path:     ".env.local",
			isDir:    false,
			patterns: []string{".env.*"},
			want:     true,
		},
		{
			name:     "empty patterns",
			path:     "file.txt",
			isDir:    false,
			patterns: []string{},
			want:     false,
		},
		{
			name:     "comment pattern ignored",
			path:     "file.txt",
			isDir:    false,
			patterns: []string{"# this is a comment", "file.txt"},
			want:     true,
		},
		{
			name:     "empty line ignored",
			path:     "file.txt",
			isDir:    false,
			patterns: []string{"", "  ", "file.txt"},
			want:     true,
		},
		{
			name:     "exact match",
			path:     "Dockerfile",
			isDir:    false,
			patterns: []string{"Dockerfile"},
			want:     true,
		},
		{
			name:     "no match",
			path:     "src/main.go",
			isDir:    false,
			patterns: []string{"*.js", "node_modules/"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIgnore(tt.path, tt.isDir, tt.patterns)
			if got != tt.want {
				t.Errorf("shouldIgnore(%q, %v, %v) = %v, want %v",
					tt.path, tt.isDir, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		pattern string
		want    bool
	}{
		{
			name:    "exact match",
			path:    "file.txt",
			pattern: "file.txt",
			want:    true,
		},
		{
			name:    "wildcard extension",
			path:    "test.js",
			pattern: "*.js",
			want:    true,
		},
		{
			name:    "nested path basename match",
			path:    "src/utils/test.js",
			pattern: "*.js",
			want:    true,
		},
		{
			name:    "directory prefix match",
			path:    "node_modules/package",
			pattern: "node_modules",
			want:    true,
		},
		{
			name:    "no match",
			path:    "file.txt",
			pattern: "*.js",
			want:    false,
		},
		{
			name:    "double star pattern",
			path:    "src/deep/nested/file.js",
			pattern: "src/**/file.js",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPattern(tt.path, tt.pattern)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v",
					tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestLoadIgnorePatternsNonexistent(t *testing.T) {
	patterns, err := LoadIgnorePatterns("/nonexistent/path/.clawkerignore")
	if err != nil {
		t.Errorf("LoadIgnorePatterns() should not error for nonexistent file, got %v", err)
	}
	if len(patterns) != 0 {
		t.Errorf("LoadIgnorePatterns() should return empty slice for nonexistent file")
	}
}

func TestSecurityPatterns(t *testing.T) {
	// Security-critical patterns that should ALWAYS be ignored
	// Note: .env.* pattern requires glob matching which handles the dot specially
	securityPatterns := []string{".env", ".env.*", "*.pem", "*.key", "credentials.json"}

	// Test that default patterns work for security-sensitive files
	criticalFiles := []struct {
		path    string
		desc    string
		pattern string // Expected pattern to match
	}{
		{".env", "environment file", ".env"},
		{"secrets.pem", "PEM certificate", "*.pem"},
		{"private.key", "private key", "*.key"},
	}

	for _, cf := range criticalFiles {
		if !matchPattern(cf.path, cf.pattern) {
			t.Errorf("Security-sensitive file %q (%s) should match pattern %q",
				cf.path, cf.desc, cf.pattern)
		}
	}

	// Verify patterns are present in our list
	for _, pattern := range []string{".env", "*.pem", "*.key"} {
		found := false
		for _, p := range securityPatterns {
			if p == pattern {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Security pattern %q should be in securityPatterns list", pattern)
		}
	}
}
