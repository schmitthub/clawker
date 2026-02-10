// Package keyring wraps the zalando/go-keyring package with timeouts and
// provides a service-credential registry for fetching, parsing, and validating
// secrets stored in the OS keychain.
//
// Raw operations (Set, Get, Delete) live in this file.
// Service definitions and the generic pipeline live in service.go.
// Per-service types and accessors live in their own files (e.g. claude_code.go).
//
// TODO: Give attribution to gh authors
package keyring

import (
	"errors"
	"time"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when no secret exists for the given service+user.
var ErrNotFound = errors.New("secret not found in keyring")

// TimeoutError is returned when a keyring operation exceeds the deadline.
type TimeoutError struct {
	message string
}

func (e *TimeoutError) Error() string {
	return e.message
}

// Set stores a secret in the keyring for the given service and user.
func Set(service, user, secret string) error {
	ch := make(chan error, 1)
	go func() {
		defer close(ch)
		ch <- keyring.Set(service, user, secret)
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		return &TimeoutError{"timeout while trying to set secret in keyring"}
	}
}

// Get retrieves a secret from the keyring for the given service and user.
func Get(service, user string) (string, error) {
	ch := make(chan struct {
		val string
		err error
	}, 1)
	go func() {
		defer close(ch)
		val, err := keyring.Get(service, user)
		ch <- struct {
			val string
			err error
		}{val, err}
	}()
	select {
	case res := <-ch:
		if errors.Is(res.err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return res.val, res.err
	case <-time.After(3 * time.Second):
		return "", &TimeoutError{"timeout while trying to get secret from keyring"}
	}
}

// Delete removes a secret from the keyring for the given service and user.
func Delete(service, user string) error {
	ch := make(chan error, 1)
	go func() {
		defer close(ch)
		ch <- keyring.Delete(service, user)
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		return &TimeoutError{"timeout while trying to delete secret from keyring"}
	}
}

// MockInit sets up an in-memory keyring backend for tests.
func MockInit() {
	keyring.MockInit()
}

// MockInitWithError sets up an in-memory keyring backend that returns err for every operation.
func MockInitWithError(err error) {
	keyring.MockInitWithError(err)
}
