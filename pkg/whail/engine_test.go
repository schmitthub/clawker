package whail

import (
	"context"
	"testing"
)

func TestHealthCheck(t *testing.T) {
	ctx := context.Background()

	err := testEngine.HealthCheck(ctx)
	if err != nil {
		t.Errorf("HealthCheck failed: %v", err)
	}
}

func TestManagedLabelAccessors(t *testing.T) {
	key := testEngine.ManagedLabelKey()
	if key != testLabelPrefix+".managed" {
		t.Errorf("ManagedLabelKey() = %q, want %q", key, testLabelPrefix+".managed")
	}

	value := testEngine.ManagedLabelValue()
	if value != "true" {
		t.Errorf("ManagedLabelValue() = %q, want %q", value, "true")
	}
}

func TestEngineClose(t *testing.T) {
	err := testEngine.Close()
	if err != nil {
		t.Errorf("Engine Close() failed: %v", err)
	}
}

// TODO: write addional tests for other engine methods
