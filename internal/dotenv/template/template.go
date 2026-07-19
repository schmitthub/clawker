/*
   Copyright 2020 The Compose Specification Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

// Vendored from github.com/compose-spec/compose-go/v2@v2.13.0 (template
// package, Apache-2.0 — see LICENSE and NOTICE). Modified for clawker:
// logrus logging replaced with an injectable missing-variable handler
// (MissingFn / WithMissingHandler); the unused ExtractVariables API
// (variables.go) is omitted.

package template

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	delimiter          = "\\$"
	substitutionNamed  = "[_a-z][_a-z0-9]*"
	substitutionBraced = "[_a-z][_a-z0-9]*(?::?[-+?](.*))?"
	groupEscaped       = "escaped"
	groupNamed         = "named"
	groupBraced        = "braced"
	groupInvalid       = "invalid"
)

var DefaultPattern = regexp.MustCompile(fmt.Sprintf(
	"%s(?i:(?P<%s>%s)|(?P<%s>%s)|{(?:(?P<%s>%s)}|(?P<%s>)))",
	delimiter,
	groupEscaped, delimiter,
	groupNamed, substitutionNamed,
	groupBraced, substitutionBraced,
	groupInvalid,
))

// InvalidTemplateError is returned when a variable template is not in a valid
// format
type InvalidTemplateError struct {
	Template string
}

func (e InvalidTemplateError) Error() string {
	return fmt.Sprintf("Invalid template: %#v", e.Template)
}

// MissingRequiredError is returned when a variable template is missing
type MissingRequiredError struct {
	Variable string
	Reason   string
}

func (e MissingRequiredError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("required variable %s is missing a value: %s", e.Variable, e.Reason)
	}
	return fmt.Sprintf("required variable %s is missing a value", e.Variable)
}

// Mapping is a user-supplied function which maps from variable names to values.
// Returns the value as a string and a bool indicating whether
// the value is present, to distinguish between an empty string
// and the absence of a value.
type Mapping func(string) (string, bool)

// SubstituteFunc is a user-supplied function that apply substitution.
// Returns the value as a string, a bool indicating if the function could apply
// the substitution and an error.
type SubstituteFunc func(string, Mapping) (string, bool, error)

// ReplacementFunc is a user-supplied function that is apply to the matching
// substring. Returns the value as a string and an error.
type ReplacementFunc func(string, Mapping, *Config) (string, error)

// MissingFn observes a variable reference that resolved through neither the
// mapping nor a default/required operator, collapsing to an empty string.
type MissingFn func(name string)

type Config struct {
	pattern         *regexp.Regexp
	substituteFunc  SubstituteFunc
	replacementFunc ReplacementFunc
	onMissing       MissingFn
}

type Option func(*Config)

func WithPattern(pattern *regexp.Regexp) Option {
	return func(cfg *Config) {
		cfg.pattern = pattern
	}
}

func WithSubstitutionFunction(subsFunc SubstituteFunc) Option {
	return func(cfg *Config) {
		cfg.substituteFunc = subsFunc
	}
}

func WithReplacementFunction(replacementFunc ReplacementFunc) Option {
	return func(cfg *Config) {
		cfg.replacementFunc = replacementFunc
	}
}

// WithMissingHandler observes unresolvable variable references. Replaces the
// upstream logrus warning.
func WithMissingHandler(onMissing MissingFn) Option {
	return func(cfg *Config) {
		cfg.onMissing = onMissing
	}
}

// SubstituteWithOptions substitute variables in the string with their values.
// It accepts additional options such as a custom function or pattern.
func SubstituteWithOptions(template string, mapping Mapping, options ...Option) (string, error) {
	var returnErr error

	cfg := &Config{
		pattern:         DefaultPattern,
		substituteFunc:  nil,
		replacementFunc: DefaultReplacementFunc,
		onMissing:       nil,
	}
	for _, o := range options {
		o(cfg)
	}

	result := cfg.pattern.ReplaceAllStringFunc(template, func(substring string) string {
		replacement, err := cfg.replacementFunc(substring, mapping, cfg)
		if err != nil {
			// Save the first error to be returned
			if returnErr == nil {
				returnErr = annotateTemplateError(err, template)
			}
		}
		return replacement
	})

	return result, returnErr
}

// annotateTemplateError stamps the full template onto an
// [InvalidTemplateError] that carries none.
func annotateTemplateError(err error, template string) error {
	var tmplErr *InvalidTemplateError
	if errors.As(err, &tmplErr) && tmplErr.Template == "" {
		tmplErr.Template = template
	}
	return err
}

func DefaultReplacementFunc(substring string, mapping Mapping, cfg *Config) (string, error) {
	value, _, err := DefaultReplacementAppliedFunc(substring, mapping, cfg)
	return value, err
}

func DefaultReplacementAppliedFunc(substring string, mapping Mapping, cfg *Config) (string, bool, error) {
	pattern := cfg.pattern
	subsFunc := cfg.substituteFunc
	if subsFunc == nil {
		_, subsFunc = getSubstitutionFunctionForTemplate(substring)
	}

	substring, rest := splitAtClosingBrace(substring)

	groups := matchGroups(pattern.FindStringSubmatch(substring), pattern)
	if escaped := groups[groupEscaped]; escaped != "" {
		return escaped, true, nil
	}

	braced := false
	substitution := groups[groupNamed]
	if substitution == "" {
		substitution = groups[groupBraced]
		braced = true
	}

	if substitution == "" {
		return "", false, &InvalidTemplateError{}
	}

	if braced {
		value, applied, err := applyBracedSubstitution(substitution, rest, subsFunc, mapping, pattern)
		if err != nil || applied {
			return value, applied, err
		}
	}

	value, ok := mapping(substitution)
	if !ok && cfg.onMissing != nil {
		cfg.onMissing(substitution)
	}

	return value, ok, nil
}

// splitAtClosingBrace splits s after the first balanced closing brace,
// returning the head and the rest (empty when no closing brace exists).
func splitAtClosingBrace(s string) (string, string) {
	i := getFirstBraceClosingIndex(s)
	if i == -1 {
		return s, ""
	}
	return s[:i+1], s[i+1:]
}

// applyBracedSubstitution runs the operator substitution function for a
// ${...} reference and, when it applies, interpolates the remainder of the
// substring after the closing brace.
func applyBracedSubstitution(
	substitution, rest string,
	subsFunc SubstituteFunc,
	mapping Mapping,
	pattern *regexp.Regexp,
) (string, bool, error) {
	value, applied, err := subsFunc(substitution, mapping)
	if err != nil {
		return "", false, err
	}
	if !applied {
		return "", false, nil
	}
	interpolatedNested, err := SubstituteWith(rest, mapping, pattern)
	if err != nil {
		return "", false, err
	}
	return value + interpolatedNested, true, nil
}

// SubstituteWith substitute variables in the string with their values.
// It accepts additional substitute function.
func SubstituteWith(
	template string,
	mapping Mapping,
	pattern *regexp.Regexp,
	subsFuncs ...SubstituteFunc,
) (string, error) {
	options := []Option{
		WithPattern(pattern),
	}
	if len(subsFuncs) > 0 {
		options = append(options, WithSubstitutionFunction(subsFuncs[0]))
	}

	return SubstituteWithOptions(template, mapping, options...)
}

func getSubstitutionFunctionForTemplate(template string) (string, SubstituteFunc) {
	interpolationMapping := []struct {
		string
		SubstituteFunc
	}{
		{":?", requiredErrorWhenEmptyOrUnset},
		{"?", requiredErrorWhenUnset},
		{":-", defaultWhenEmptyOrUnset},
		{"-", defaultWhenUnset},
		{":+", defaultWhenNotEmpty},
		{"+", defaultWhenSet},
	}
	sort.Slice(interpolationMapping, func(i, j int) bool {
		idxI := strings.Index(template, interpolationMapping[i].string)
		idxJ := strings.Index(template, interpolationMapping[j].string)
		if idxI < 0 {
			return false
		}
		if idxJ < 0 {
			return true
		}
		return idxI < idxJ
	})

	return interpolationMapping[0].string, interpolationMapping[0].SubstituteFunc
}

func getFirstBraceClosingIndex(s string) int {
	openVariableBraces := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '}' {
			openVariableBraces--
			if openVariableBraces == 0 {
				return i
			}
		}
		if s[i] == '{' {
			openVariableBraces++
			i++
		}
	}
	return -1
}

// Substitute variables in the string with their values
func Substitute(template string, mapping Mapping) (string, error) {
	return SubstituteWith(template, mapping, DefaultPattern)
}

// Soft default (fall back if unset or empty)
func defaultWhenEmptyOrUnset(substitution string, mapping Mapping) (string, bool, error) {
	return withDefaultWhenAbsence(substitution, mapping, true)
}

// Hard default (fall back if-and-only-if empty)
func defaultWhenUnset(substitution string, mapping Mapping) (string, bool, error) {
	return withDefaultWhenAbsence(substitution, mapping, false)
}

func defaultWhenNotEmpty(substitution string, mapping Mapping) (string, bool, error) {
	return withDefaultWhenPresence(substitution, mapping, true)
}

func defaultWhenSet(substitution string, mapping Mapping) (string, bool, error) {
	return withDefaultWhenPresence(substitution, mapping, false)
}

func requiredErrorWhenEmptyOrUnset(substitution string, mapping Mapping) (string, bool, error) {
	return withRequired(substitution, mapping, ":?", func(v string) bool { return v != "" })
}

func requiredErrorWhenUnset(substitution string, mapping Mapping) (string, bool, error) {
	return withRequired(substitution, mapping, "?", func(_ string) bool { return true })
}

func withDefaultWhenPresence(substitution string, mapping Mapping, notEmpty bool) (string, bool, error) {
	sep := "+"
	if notEmpty {
		sep = ":+"
	}
	if !strings.Contains(substitution, sep) {
		return "", false, nil
	}
	name, defaultValue := partition(substitution, sep)
	value, ok := mapping(name)
	if ok && (!notEmpty || (notEmpty && value != "")) {
		resolved, err := Substitute(defaultValue, mapping)
		if err != nil {
			return "", false, err
		}
		return resolved, true, nil
	}
	return value, true, nil
}

func withDefaultWhenAbsence(substitution string, mapping Mapping, emptyOrUnset bool) (string, bool, error) {
	sep := "-"
	if emptyOrUnset {
		sep = ":-"
	}
	if !strings.Contains(substitution, sep) {
		return "", false, nil
	}
	name, defaultValue := partition(substitution, sep)
	value, ok := mapping(name)
	if !ok || (emptyOrUnset && value == "") {
		resolved, err := Substitute(defaultValue, mapping)
		if err != nil {
			return "", false, err
		}
		return resolved, true, nil
	}
	return value, true, nil
}

func withRequired(substitution string, mapping Mapping, sep string, valid func(string) bool) (string, bool, error) {
	if !strings.Contains(substitution, sep) {
		return "", false, nil
	}
	name, errorMessage := partition(substitution, sep)
	value, ok := mapping(name)
	if !ok || !valid(value) {
		resolved, err := Substitute(errorMessage, mapping)
		if err != nil {
			return "", false, err
		}
		return "", true, &MissingRequiredError{
			Reason:   resolved,
			Variable: name,
		}
	}
	return value, true, nil
}

func matchGroups(matches []string, pattern *regexp.Regexp) map[string]string {
	groups := make(map[string]string)
	for i, name := range pattern.SubexpNames()[1:] {
		groups[name] = matches[i+1]
	}
	return groups
}

// Split the string at the first occurrence of sep, and return the part before the separator,
// and the part after the separator.
//
// If the separator is not found, return the string itself, followed by an empty string.
func partition(s, sep string) (string, string) {
	before, after, _ := strings.Cut(s, sep)
	return before, after
}
