package cmdutil

import (
	"encoding/json"
	"io"
)

// WriteJSON encodes data as pretty-printed JSON to the given writer.
// Used by list commands when --format json or --json is specified.
func WriteJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}
