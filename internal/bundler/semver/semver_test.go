package semver

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *Version
		wantErr bool
	}{
		{
			name:  "full semver 1.2.3",
			input: "1.2.3",
			want: &Version{
				Major:    1,
				Minor:    2,
				Patch:    3,
				Original: "1.2.3",
			},
		},
		{
			name:  "major only",
			input: "2",
			want: &Version{
				Major:    2,
				Minor:    -1,
				Patch:    -1,
				Original: "2",
			},
		},
		{
			name:  "major.minor",
			input: "2.1",
			want: &Version{
				Major:    2,
				Minor:    1,
				Patch:    -1,
				Original: "2.1",
			},
		},
		{
			name:  "with prerelease",
			input: "1.0.0-alpha",
			want: &Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				Prerelease: "alpha",
				Original:   "1.0.0-alpha",
			},
		},
		{
			name:  "with build metadata",
			input: "1.0.0+build.123",
			want: &Version{
				Major:    1,
				Minor:    0,
				Patch:    0,
				Build:    "build.123",
				Original: "1.0.0+build.123",
			},
		},
		{
			name:  "with prerelease and build",
			input: "1.0.0-beta+build",
			want: &Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				Prerelease: "beta",
				Build:      "build",
				Original:   "1.0.0-beta+build",
			},
		},
		{
			name:  "zero version",
			input: "0.0.0",
			want: &Version{
				Major:    0,
				Minor:    0,
				Patch:    0,
				Original: "0.0.0",
			},
		},
		{
			name:  "large numbers",
			input: "100.200.300",
			want: &Version{
				Major:    100,
				Minor:    200,
				Patch:    300,
				Original: "100.200.300",
			},
		},
		{
			name:    "invalid: v prefix",
			input:   "v1.0.0",
			wantErr: true,
		},
		{
			name:    "invalid: 4 parts",
			input:   "1.0.0.0",
			wantErr: true,
		},
		{
			name:    "invalid: non-numeric",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "invalid: leading zero",
			input:   "01.0.0",
			wantErr: true,
		},
		{
			name:    "invalid: empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if got.Major != tt.want.Major {
				t.Errorf("Major = %d, want %d", got.Major, tt.want.Major)
			}
			if got.Minor != tt.want.Minor {
				t.Errorf("Minor = %d, want %d", got.Minor, tt.want.Minor)
			}
			if got.Patch != tt.want.Patch {
				t.Errorf("Patch = %d, want %d", got.Patch, tt.want.Patch)
			}
			if got.Prerelease != tt.want.Prerelease {
				t.Errorf("Prerelease = %q, want %q", got.Prerelease, tt.want.Prerelease)
			}
			if got.Build != tt.want.Build {
				t.Errorf("Build = %q, want %q", got.Build, tt.want.Build)
			}
		})
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1.2.3", true},
		{"1", true},
		{"1.0", true},
		{"1.0.0-alpha", true},
		{"v1.0.0", false},
		{"1.0.0.0", false},
		{"abc", false},
		{"01.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsValid(tt.input); got != tt.want {
				t.Errorf("IsValid(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"1.0.0 < 2.0.0", "1.0.0", "2.0.0", -1},
		{"2.0.0 > 1.0.0", "2.0.0", "1.0.0", 1},
		{"1.0.0 == 1.0.0", "1.0.0", "1.0.0", 0},
		{"1.0.0 < 1.1.0", "1.0.0", "1.1.0", -1},
		{"1.0.0 < 1.0.1", "1.0.0", "1.0.1", -1},
		{"1 < 2", "1", "2", -1},
		{"1.1 > 1.0", "1.1", "1.0", 1},
		{"prerelease < release", "1.0.0-alpha", "1.0.0", -1},
		{"release > prerelease", "1.0.0", "1.0.0-alpha", 1},
		{"alpha < beta", "1.0.0-alpha", "1.0.0-beta", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := MustParse(tt.a)
			b := MustParse(tt.b)
			if got := Compare(a, b); got != tt.want {
				t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSortStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "basic sorting",
			input: []string{"2.0.0", "1.0.1", "1.1.0", "1.0.0"},
			want:  []string{"1.0.0", "1.0.1", "1.1.0", "2.0.0"},
		},
		{
			name:  "patch versions",
			input: []string{"1.0.10", "1.0.2", "1.0.1", "1.0.11"},
			want:  []string{"1.0.1", "1.0.2", "1.0.10", "1.0.11"},
		},
		{
			name:  "with prereleases",
			input: []string{"1.0.0", "1.0.0-alpha", "1.0.0-beta"},
			want:  []string{"1.0.0-alpha", "1.0.0-beta", "1.0.0"},
		},
		{
			name:  "filter invalid",
			input: []string{"1.0.0", "invalid", "2.0.0", "v1.0"},
			want:  []string{"1.0.0", "2.0.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SortStrings(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SortStrings() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortStringsDesc(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "descending",
			input: []string{"1.0.0", "2.0.0", "1.0.1", "1.1.0"},
			want:  []string{"2.0.0", "1.1.0", "1.0.1", "1.0.0"},
		},
		{
			name:  "descending with patches",
			input: []string{"1.0.10", "1.0.2", "1.0.1", "1.0.11"},
			want:  []string{"1.0.11", "1.0.10", "1.0.2", "1.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SortStringsDesc(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SortStringsDesc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	versions := []string{"1.0.0", "1.0.1", "1.1.0", "1.1.1", "2.0.0", "2.0.1", "2.1.0", "2.1.1"}

	tests := []struct {
		name     string
		versions []string
		target   string
		want     string
		wantOk   bool
	}{
		{
			name:     "exact match",
			versions: versions,
			target:   "1.0.0",
			want:     "1.0.0",
			wantOk:   true,
		},
		{
			name:     "match major 1 -> highest 1.x.x",
			versions: versions,
			target:   "1",
			want:     "1.1.1",
			wantOk:   true,
		},
		{
			name:     "match major 2 -> highest 2.x.x",
			versions: versions,
			target:   "2",
			want:     "2.1.1",
			wantOk:   true,
		},
		{
			name:     "match minor 1.0 -> highest 1.0.x",
			versions: versions,
			target:   "1.0",
			want:     "1.0.1",
			wantOk:   true,
		},
		{
			name:     "match minor 2.0 -> highest 2.0.x",
			versions: versions,
			target:   "2.0",
			want:     "2.0.1",
			wantOk:   true,
		},
		{
			name:     "no match",
			versions: versions,
			target:   "3",
			want:     "",
			wantOk:   false,
		},
		{
			name:     "match skips prereleases",
			versions: []string{"1.0.0", "1.0.1-rc1", "1.0.1", "1.0.2-beta"},
			target:   "1.0",
			want:     "1.0.1",
			wantOk:   true,
		},
		{
			name:     "exact prerelease match",
			versions: []string{"1.0.0", "1.0.1-rc1", "1.0.1"},
			target:   "1.0.1-rc1",
			want:     "1.0.1-rc1",
			wantOk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Match(tt.versions, tt.target)
			if ok != tt.wantOk {
				t.Errorf("Match() ok = %v, wantOk %v", ok, tt.wantOk)
				return
			}
			if got != tt.want {
				t.Errorf("Match() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersion_HasMinor(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1", false},
		{"1.0", true},
		{"1.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := MustParse(tt.input)
			if got := v.HasMinor(); got != tt.want {
				t.Errorf("HasMinor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVersion_HasPatch(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1", false},
		{"1.0", false},
		{"1.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := MustParse(tt.input)
			if got := v.HasPatch(); got != tt.want {
				t.Errorf("HasPatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVersion_String(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.2.3", "1.2.3"},
		{"1.0.0-alpha", "1.0.0-alpha"},
		{"1.0.0+build", "1.0.0+build"},
		{"1.0.0-beta+build", "1.0.0-beta+build"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := MustParse(tt.input)
			if got := v.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFilterValid(t *testing.T) {
	input := []string{"1.0.0", "invalid", "2.0.0", "v1.0", "3.0.0-alpha"}
	want := []string{"1.0.0", "2.0.0", "3.0.0-alpha"}

	got := FilterValid(input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterValid() = %v, want %v", got, want)
	}
}

func BenchmarkParse(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = Parse("1.2.3-alpha+build")
	}
}

func BenchmarkCompare(b *testing.B) {
	a := MustParse("1.2.3")
	c := MustParse("1.2.4")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Compare(a, c)
	}
}

func BenchmarkSortStrings(b *testing.B) {
	versions := []string{
		"2.0.0", "1.0.1", "1.1.0", "1.0.0", "3.0.0", "2.1.0",
		"1.0.10", "1.0.2", "1.0.11", "2.0.1", "2.1.1", "3.1.0",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Make a copy since sort modifies in place
		v := make([]string, len(versions))
		copy(v, versions)
		SortStrings(v)
	}
}
