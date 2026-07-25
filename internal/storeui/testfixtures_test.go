package storeui

import (
	"time"

	"github.com/schmitthub/clawker/internal/storage"
)

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

type timeStruct struct {
	SeenAt time.Time `yaml:"seen_at,omitempty"`
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

// kindsStruct carries one field per storage.FieldKind the editor renders, with
// the `default` tags the unset-versus-set-empty display depends on.
type kindsStruct struct {
	Mode      string               `yaml:"mode"      label:"Workspace Mode" desc:"How the workspace is mounted" default:"bind"`
	Enabled   *bool                `yaml:"enabled"                                                              default:"true"`
	Count     int                  `yaml:"count"`
	Timeout   time.Duration        `yaml:"timeout"                                                              default:"30s"`
	Packages  []string             `yaml:"packages"                                                             default:"git,curl"`
	Env       map[string]string    `yaml:"env"`
	Rules     []ruleEntry          `yaml:"rules"`
	Harnesses map[string]ruleEntry `yaml:"harnesses"`
	SeenAt    time.Time            `yaml:"seen_at"`
}

type ruleEntry struct {
	Dst string `yaml:"dst"`
}

// Schema implementations for test fixture types (required by Store[T Schema] constraint).
func (s simpleStruct) Fields() storage.FieldSet       { return storage.NormalizeFields(s) }
func (n nestedStruct) Fields() storage.FieldSet       { return storage.NormalizeFields(n) }
func (t triStateStruct) Fields() storage.FieldSet     { return storage.NormalizeFields(t) }
func (n nilPtrStructParent) Fields() storage.FieldSet { return storage.NormalizeFields(n) }
func (d durationStruct) Fields() storage.FieldSet     { return storage.NormalizeFields(d) }
func (c complexStruct) Fields() storage.FieldSet      { return storage.NormalizeFields(c) }
func (y yamlTagStruct) Fields() storage.FieldSet      { return storage.NormalizeFields(y) }

//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (k kindsStruct) Fields() storage.FieldSet { return storage.NormalizeFields(k) }

// ptr is a generic helper for creating pointer values in tests.
func ptr[T any](v T) *T {
	return &v
}
