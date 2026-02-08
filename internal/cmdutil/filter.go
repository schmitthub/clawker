package cmdutil

import (
	"strings"

	"github.com/spf13/cobra"
)

// Filter is a parsed filter key-value pair from --filter flags.
type Filter struct {
	Key   string
	Value string
}

// ParseFilters parses raw --filter flag values into Filter structs.
// Each string must be "key=value" format, split on the first "=" only
// (the value may contain additional "=" characters). The key must not
// be empty, but the value may be empty.
func ParseFilters(raw []string) ([]Filter, error) {
	filters := make([]Filter, 0, len(raw))
	for _, r := range raw {
		key, value, ok := strings.Cut(r, "=")
		if !ok {
			return nil, FlagErrorf("invalid filter format: %q (expected key=value)", r)
		}
		if key == "" {
			return nil, FlagErrorf("invalid filter: empty key in %q", r)
		}
		filters = append(filters, Filter{
			Key:   key,
			Value: value,
		})
	}
	return filters, nil
}

// ValidateFilterKeys checks that every filter's key is in validKeys.
// Returns a FlagError listing valid keys if an unknown key is found.
func ValidateFilterKeys(filters []Filter, validKeys []string) error {
	valid := make(map[string]struct{}, len(validKeys))
	for _, k := range validKeys {
		valid[k] = struct{}{}
	}
	for _, f := range filters {
		if _, ok := valid[f.Key]; !ok {
			return FlagErrorf("invalid filter key %q; valid keys: %s", f.Key, strings.Join(validKeys, ", "))
		}
	}
	return nil
}

// FilterFlags holds raw --filter flag values and provides parsing.
type FilterFlags struct {
	raw []string
}

// AddFilterFlags registers a repeatable --filter flag on cmd and returns
// the FilterFlags receiver for later parsing.
func AddFilterFlags(cmd *cobra.Command) *FilterFlags {
	ff := &FilterFlags{}
	cmd.Flags().StringArrayVar(&ff.raw, "filter", nil, "Filter output (key=value, repeatable)")
	return ff
}

// Parse parses the raw filter values collected from the command flags.
func (ff *FilterFlags) Parse() ([]Filter, error) {
	return ParseFilters(ff.raw)
}
