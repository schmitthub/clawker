package projecttest

import (
	"fmt"
)

// RegisterCall captures one Register invocation.
type RegisterCall struct {
	DisplayName string
	RootDir     string
}

// RegistrarFunc is a function adapter that implements project.Registrar.
// Use this for one-off custom behavior in tests without creating a struct.
type RegistrarFunc func(displayName, rootDir string) (string, error)

// Register delegates to the underlying function.
func (fn RegistrarFunc) Register(displayName, rootDir string) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("registrar func is nil")
	}
	return fn(displayName, rootDir)
}

// MockRegistrar is a configurable test double for project.Registrar.
// It records calls and supports either a custom RegisterFunc or simple return values.
type MockRegistrar struct {
	Calls []RegisterCall

	RegisterFunc RegistrarFunc
	DefaultSlug  string
	Err          error
}

// Register records the call and returns behavior from RegisterFunc/Err/DefaultSlug.
func (m *MockRegistrar) Register(displayName, rootDir string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("mock registrar is nil")
	}

	m.Calls = append(m.Calls, RegisterCall{DisplayName: displayName, RootDir: rootDir})

	if m.RegisterFunc != nil {
		return m.RegisterFunc(displayName, rootDir)
	}
	if m.Err != nil {
		return "", m.Err
	}
	if m.DefaultSlug != "" {
		return m.DefaultSlug, nil
	}
	return "test-project", nil
}

// Reset clears recorded calls and configured outcomes.
func (m *MockRegistrar) Reset() {
	if m == nil {
		return
	}
	m.Calls = nil
	m.RegisterFunc = nil
	m.DefaultSlug = ""
	m.Err = nil
}
