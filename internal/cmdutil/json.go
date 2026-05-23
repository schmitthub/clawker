package cmdutil

import (
	"encoding/json"
	"io"
)

// WriteJSON encodes data as compact, single-line JSON to the given writer.
// Used by list commands when --format json or --json is specified. Pipe
// through `jq` for human-readable output. HTML escaping is disabled so
// values like `<none>:<none>` are written literally, not unicode-escaped.
func WriteJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(data)
}
