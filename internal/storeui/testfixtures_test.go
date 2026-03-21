package storeui

import "time"

// Shared test fixture types used by reflect_test.go and value_test.go.

type simpleStruct struct {
	Name    string `yaml:"name"`
	Enabled bool   `yaml:"enabled"`
	Count   int    `yaml:"count"`
}

type nestedStruct struct {
	Build buildSection `yaml:"build"`
}

type buildSection struct {
	Image    string   `yaml:"image"`
	Packages []string `yaml:"packages,omitempty"`
}

type triStateStruct struct {
	Enabled *bool `yaml:"enabled,omitempty"`
}

type nilPtrStructParent struct {
	Loop *loopSection `yaml:"loop,omitempty"`
}

type loopSection struct {
	MaxLoops  int    `yaml:"max_loops,omitempty"`
	HooksFile string `yaml:"hooks_file,omitempty"`
}

type durationStruct struct {
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

type complexStruct struct {
	Name string            `yaml:"name"`
	Env  map[string]string `yaml:"env,omitempty"`
}

type yamlTagStruct struct {
	ImageName string `yaml:"image,omitempty"`
	NoTag     string
	Skipped   string `yaml:"-"`
}

// ptr is a generic helper for creating pointer values in tests.
func ptr[T any](v T) *T {
	return &v
}
