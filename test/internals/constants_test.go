package internals

import (
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// containerHomeDir is the standard home directory for the claude user inside test containers.
const containerHomeDir = "/home/claude"

// _testCfg is a package-level blank config providing label constants and default values.
var _testCfg = configmocks.NewBlankConfig()
