// Package dotenv parses .env files with docker-compose semantics.
//
// Vendored from github.com/compose-spec/compose-go/v2@v2.13.0 (dotenv
// package, MIT — see LICENSE), itself a fork of github.com/joho/godotenv,
// a Go port of the ruby dotenv library (https://github.com/bkeepers/dotenv).
//
// Modified for clawker:
//   - removed the process-env loading APIs (Load, Read, ReadFile, Unmarshal*)
//     and the compose file-format registry (env.go, format.go) — clawker only
//     parses, it never mutates its own environment
//   - unset-variable references are reported through an injectable MissingFn
//     instead of logrus logging (see template.WithMissingHandler)
package dotenv

import (
	"bytes"
	"fmt"
	"io"

	"github.com/schmitthub/clawker/internal/dotenv/template"
)

const utf8BOM = "\uFEFF"

// LookupFn represents a lookup function to resolve variables from
type LookupFn func(string) (string, bool)

func noLookup(_ string) (string, bool) {
	return "", false
}

// MissingFn is called once per interpolation reference that resolves through
// neither the lookup function nor keys already parsed from the file, after
// default/required operators have had their chance to apply — i.e. only for
// references that actually collapse to an empty string.
type MissingFn func(name string)

// Parse reads an env file from an [io.Reader], returning a map of keys and values.
func Parse(r io.Reader) (map[string]string, error) {
	return ParseWithLookup(r, nil, nil)
}

// ParseWithLookup reads an env file from an [io.Reader], returning a map of
// keys and values. onMissing (optional) observes unresolvable references.
func ParseWithLookup(r io.Reader, lookupFn LookupFn, onMissing MissingFn) (map[string]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading env file: %w", err)
	}

	// seek past the UTF-8 BOM if it exists (particularly on Windows, some
	// editors tend to add it, and it'll cause parsing to fail)
	data = bytes.TrimPrefix(data, []byte(utf8BOM))

	vars := map[string]string{}
	err = newParser(onMissing).parse(string(data), vars, lookupFn)
	return vars, err
}

func (p *parser) expandVariables(value string, envMap map[string]string, lookupFn LookupFn) (string, error) {
	retVal, err := template.SubstituteWithOptions(value, func(k string) (string, bool) {
		if v, ok := lookupFn(k); ok {
			return v, true
		}
		v, ok := envMap[k]
		return v, ok
	}, template.WithMissingHandler(template.MissingFn(p.onMissing)))
	if err != nil {
		return value, fmt.Errorf("expanding variables: %w", err)
	}
	return retVal, nil
}
