package whail

import (
	"testing"
)

func TestMergeLabels(t *testing.T) {
	tests := []struct {
		name string
		maps []map[string]string
		want map[string]string
	}{
		{
			name: "empty input",
			maps: nil,
			want: map[string]string{},
		},
		{
			name: "single map",
			maps: []map[string]string{{"a": "1", "b": "2"}},
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "multiple maps no overlap",
			maps: []map[string]string{{"a": "1"}, {"b": "2"}, {"c": "3"}},
			want: map[string]string{"a": "1", "b": "2", "c": "3"},
		},
		{
			name: "later maps override",
			maps: []map[string]string{{"a": "1", "b": "2"}, {"b": "override"}},
			want: map[string]string{"a": "1", "b": "override"},
		},
		{
			name: "nil maps are skipped",
			maps: []map[string]string{{"a": "1"}, nil, {"b": "2"}},
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "empty maps are valid",
			maps: []map[string]string{{"a": "1"}, {}, {"b": "2"}},
			want: map[string]string{"a": "1", "b": "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeLabels(tt.maps...)
			if len(got) != len(tt.want) {
				t.Errorf("MergeLabels() length = %d, want %d", len(got), len(tt.want))
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("MergeLabels()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestLabelConfig_ContainerLabels(t *testing.T) {
	cfg := LabelConfig{
		Default:   map[string]string{"default": "true"},
		Container: map[string]string{"container": "true"},
	}

	got := cfg.ContainerLabels()
	if got["default"] != "true" {
		t.Errorf("ContainerLabels() missing default label")
	}
	if got["container"] != "true" {
		t.Errorf("ContainerLabels() missing container label")
	}
}

func TestLabelConfig_ContainerLabelsWithExtra(t *testing.T) {
	cfg := LabelConfig{
		Default:   map[string]string{"default": "true"},
		Container: map[string]string{"container": "true"},
	}

	extra := map[string]string{"extra": "value", "container": "override"}
	got := cfg.ContainerLabels(extra)

	if got["default"] != "true" {
		t.Errorf("ContainerLabels() missing default label")
	}
	if got["extra"] != "value" {
		t.Errorf("ContainerLabels() missing extra label")
	}
	if got["container"] != "override" {
		t.Errorf("ContainerLabels() extra should override container label")
	}
}

func TestLabelConfig_VolumeLabels(t *testing.T) {
	cfg := LabelConfig{
		Default: map[string]string{"default": "true"},
		Volume:  map[string]string{"volume": "true"},
	}

	got := cfg.VolumeLabels()
	if got["default"] != "true" {
		t.Errorf("VolumeLabels() missing default label")
	}
	if got["volume"] != "true" {
		t.Errorf("VolumeLabels() missing volume label")
	}
}

func TestLabelConfig_NetworkLabels(t *testing.T) {
	cfg := LabelConfig{
		Default: map[string]string{"default": "true"},
		Network: map[string]string{"network": "true"},
	}

	got := cfg.NetworkLabels()
	if got["default"] != "true" {
		t.Errorf("NetworkLabels() missing default label")
	}
	if got["network"] != "true" {
		t.Errorf("NetworkLabels() missing network label")
	}
}

func TestLabelConfig_ImageLabels(t *testing.T) {
	cfg := LabelConfig{
		Default: map[string]string{"default": "true"},
		Image:   map[string]string{"image": "true"},
	}

	got := cfg.ImageLabels()
	if got["default"] != "true" {
		t.Errorf("ImageLabels() missing default label")
	}
	if got["image"] != "true" {
		t.Errorf("ImageLabels() missing image label")
	}
}

func TestLabelFilter(t *testing.T) {
	args := LabelFilter("com.example.key", "value")

	// Verify filter was added (Filters is map[string]map[string]bool)
	labelFilters, ok := args["label"]
	if !ok {
		t.Error("LabelFilter() should contain label filter")
	}

	// Check the filter value
	if len(labelFilters) != 1 {
		t.Errorf("LabelFilter() should have 1 filter, got %d", len(labelFilters))
	}
	expectedFilter := "com.example.key=value"
	if _, exists := labelFilters[expectedFilter]; !exists {
		t.Errorf("LabelFilter() missing expected filter %q", expectedFilter)
	}
}

func TestLabelFilterMultiple(t *testing.T) {
	labels := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}
	args := LabelFilterMultiple(labels)

	// Verify filters were added
	labelFilters, ok := args["label"]
	if !ok {
		t.Error("LabelFilterMultiple() should contain label filter")
	}

	if len(labelFilters) != 2 {
		t.Errorf("LabelFilterMultiple() should have 2 filters, got %d", len(labelFilters))
	}
}

func TestAddLabelFilter(t *testing.T) {
	args := LabelFilter("key1", "value1")
	args = AddLabelFilter(args, "key2", "value2")

	labelFilters := args["label"]
	if len(labelFilters) != 2 {
		t.Errorf("AddLabelFilter() should have 2 filters, got %d", len(labelFilters))
	}
}
