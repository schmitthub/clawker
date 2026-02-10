package keyring

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"strings"
	"time"
)

// Sentinel errors returned by the service-credential pipeline.
var (
	// ErrTokenExpired indicates the credential's expiry timestamp is in the past.
	ErrTokenExpired = errors.New("token has expired")

	// ErrInvalidSchema indicates the raw keyring value could not be parsed into the
	// expected credential struct (e.g. malformed JSON).
	ErrInvalidSchema = errors.New("credential data does not match expected schema")

	// ErrEmptyCredential indicates the keyring entry exists but contains an empty value.
	ErrEmptyCredential = errors.New("credential is empty")
)

// ServiceDef describes how to fetch, parse, and validate a credential of type T.
//
// Each service (Claude Code, GitHub CLI, etc.) defines one of these as a package-level
// var and exposes a thin public function that calls getCredential.
type ServiceDef[T any] struct {
	// ServiceName is the keyring service identifier (e.g. "Claude Code-credentials").
	ServiceName string

	// User returns the keyring username for this service.
	// Most services use currentOSUser; some may hard-code or derive a value.
	User func() (string, error)

	// Parse converts the raw keyring string into a typed credential.
	// Return an error if the data does not match the expected schema.
	Parse func(raw string) (*T, error)

	// Validate performs service-specific checks on the parsed credential
	// (e.g. expiry). Nil means no validation.
	Validate func(*T) error
}

// getCredential is the generic fetch → parse → validate pipeline.
//
// Error pipeline (fast-fail):
//  1. User() fails      → wrapped error
//  2. Get() fails       → ErrNotFound (no entry) or *TimeoutError
//  3. raw == ""         → ErrEmptyCredential (entry exists but blank)
//  4. Parse() fails     → ErrInvalidSchema wrapping the parse error
//  5. Validate() fails  → returns validation error directly (e.g. ErrTokenExpired)
func getCredential[T any](def ServiceDef[T]) (*T, error) {
	u, err := def.User()
	if err != nil {
		return nil, fmt.Errorf("resolve keyring user: %w", err)
	}

	raw, err := Get(def.ServiceName, u)
	if err != nil {
		return nil, err // ErrNotFound or *TimeoutError — pass through
	}

	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%w: service %q", ErrEmptyCredential, def.ServiceName)
	}

	cred, err := def.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: service %q: %w", ErrInvalidSchema, def.ServiceName, err)
	}

	if def.Validate != nil {
		if err := def.Validate(cred); err != nil {
			return nil, err
		}
	}

	return cred, nil
}

// ---------------------------------------------------------------------------
// Reusable helpers — any ServiceDef can reference these.
// ---------------------------------------------------------------------------

// currentOSUser returns the current OS username. Suitable for ServiceDef.User.
func currentOSUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// jsonParse returns a Parse function that JSON-unmarshals raw into *T.
func jsonParse[T any](raw string) (*T, error) {
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// isExpired reports whether a unix-millisecond timestamp is in the past.
// A zero or negative value is treated as "no expiry" and returns false.
func isExpired(unixMillis int64) bool {
	return unixMillis > 0 && time.Now().After(time.UnixMilli(unixMillis))
}
