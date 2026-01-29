package cmdutil

import (
	"errors"
)

// ErrAborted is returned when user cancels an operation.
var ErrAborted = errors.New("operation aborted by user")
