package config

import (
	"github.com/schmitthub/clawker/internal/consts"
)

// ConfigDir delegates to consts.ConfigDir. Kept for backward compatibility —
// new callers should import internal/consts directly.
func ConfigDir() string { return consts.ConfigDir() }

// DataDir delegates to consts.DataDir.
func DataDir() string { return consts.DataDir() }

// StateDir delegates to consts.StateDir.
func StateDir() string { return consts.StateDir() }
