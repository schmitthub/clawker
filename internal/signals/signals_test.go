package signals

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetupSignalContext(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())

	ctx, cancel := SetupSignalContext(parent)
	defer cancel()

	// Context should not be done yet
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done yet")
	default:
	}

	// Cancelling parent should cancel derived context
	parentCancel()

	select {
	case <-ctx.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("context should be done after parent cancel")
	}
}

func TestResizeHandler_TriggerResize(t *testing.T) {
	var called atomic.Int32
	var gotHeight, gotWidth uint

	resizeFunc := func(h, w uint) error {
		gotHeight = h
		gotWidth = w
		called.Add(1)
		return nil
	}

	getSize := func() (int, int, error) {
		return 120, 40, nil // width=120, height=40
	}

	rh := NewResizeHandler(resizeFunc, getSize)

	rh.TriggerResize()

	if called.Load() != 1 {
		t.Fatalf("expected resizeFunc called once, got %d", called.Load())
	}
	if gotHeight != 40 || gotWidth != 120 {
		t.Errorf("expected (height=40, width=120), got (height=%d, width=%d)", gotHeight, gotWidth)
	}
}

func TestResizeHandler_NilFunctions(t *testing.T) {
	// Should not panic with nil getSize
	rh := NewResizeHandler(func(h, w uint) error { return nil }, nil)
	rh.TriggerResize() // no panic

	// Should not panic with nil resizeFunc
	rh2 := NewResizeHandler(nil, func() (int, int, error) { return 80, 24, nil })
	rh2.TriggerResize() // no panic
}

func TestResizeHandler_GetSizeError(t *testing.T) {
	var called atomic.Int32

	resizeFunc := func(h, w uint) error {
		called.Add(1)
		return nil
	}

	getSize := func() (int, int, error) {
		return 0, 0, errors.New("no terminal")
	}

	rh := NewResizeHandler(resizeFunc, getSize)
	rh.TriggerResize()

	if called.Load() != 0 {
		t.Error("resizeFunc should not be called when getSize errors")
	}
}

func TestResizeHandler_StartStop(t *testing.T) {
	rh := NewResizeHandler(
		func(h, w uint) error { return nil },
		func() (int, int, error) { return 80, 24, nil },
	)

	rh.Start()
	// Give goroutine time to start
	time.Sleep(10 * time.Millisecond)
	rh.Stop()
	// Should not panic or deadlock
}
