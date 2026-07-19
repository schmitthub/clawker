// Vendored from github.com/compose-spec/compose-go/v2@v2.13.0 (dotenv
// package, MIT — see LICENSE). Modified for clawker: the parser carries a
// MissingFn so unresolvable interpolation references surface to the caller
// instead of being logged (see package doc in godotenv.go), and functions are
// restructured to satisfy this repo's linters.

package dotenv

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	charComment       = '#'
	prefixSingleQuote = '\''
	prefixDoubleQuote = '"'

	charNextLine         = 0x85 // NEL: treated as space, not line break
	charNonBreakingSpace = 0xA0
)

var (
	escapeSeqRegex = regexp.MustCompile(`(\\(?:[abcfnrtv$"\\]|0\d{0,3}))`)
	exportRegex    = regexp.MustCompile(`^export\s+`)
)

type parser struct {
	line      int
	onMissing MissingFn
}

func newParser(onMissing MissingFn) *parser {
	return &parser{
		line:      1,
		onMissing: onMissing,
	}
}

func (p *parser) parse(src string, out map[string]string, lookupFn LookupFn) error {
	cutset := src
	if lookupFn == nil {
		lookupFn = noLookup
	}
	for {
		cutset = p.getStatementStart(cutset)
		if cutset == "" {
			// reached end of file
			break
		}

		rest, err := p.parseStatement(cutset, out, lookupFn)
		if err != nil {
			return err
		}
		cutset = rest
	}

	return nil
}

// parseStatement consumes one `KEY=value` (or inherited `KEY`) statement and
// returns the rest of the slice.
func (p *parser) parseStatement(cutset string, out map[string]string, lookupFn LookupFn) (string, error) {
	key, left, inherited, err := p.locateKeyName(cutset)
	if err != nil {
		return "", err
	}
	if strings.Contains(key, " ") {
		return "", fmt.Errorf("line %d: key cannot contain a space", p.line)
	}

	if inherited {
		value, ok := lookupFn(key)
		if ok {
			out[key] = value
		}
		return left, nil
	}

	value, left, err := p.extractVarValue(left, out, lookupFn)
	if err != nil {
		return "", err
	}

	out[key] = value
	return left, nil
}

// getStatementPosition returns position of statement begin.
//
// It skips any comment line or non-whitespace character.
func (p *parser) getStatementStart(src string) string {
	pos := p.indexOfNonSpaceChar(src)
	if pos == -1 {
		return ""
	}

	src = src[pos:]
	if src[0] != charComment {
		return src
	}

	// skip comment section
	pos = strings.IndexFunc(src, isCharFunc('\n'))
	if pos == -1 {
		return ""
	}
	return p.getStatementStart(src[pos:])
}

// locateKeyName locates and parses key name and returns rest of slice
func (p *parser) locateKeyName(src string) (string, string, bool, error) {
	// trim "export" and space at beginning
	if exportRegex.MatchString(src) {
		// we use a `strings.trim` to preserve the pointer to the same underlying memory.
		// a regexp replace would copy the string.
		src = strings.TrimLeftFunc(strings.TrimPrefix(src, "export"), isSpace)
	}

	key, offset, inherited, err := p.scanKeyEnd(src)
	if err != nil {
		return "", "", inherited, err
	}

	if src == "" {
		return "", "", inherited, errors.New("zero length string")
	}

	if inherited && strings.IndexByte(key, ' ') == -1 {
		p.line++
	}

	// trim whitespace
	key = strings.TrimRightFunc(key, unicode.IsSpace)
	cutset := strings.TrimLeftFunc(src[offset:], isSpace)
	return key, cutset, inherited, nil
}

// scanKeyEnd locates the key terminator (`=`, `:`, or newline for inherited
// keys), validating key characters along the way. Returns the raw key, the
// offset just past the terminator, and whether the key is inherited.
func (p *parser) scanKeyEnd(src string) (string, int, bool, error) {
	for i, r := range src {
		if isSpace(r) {
			continue
		}

		switch r {
		case '=', ':', '\n':
			// library also supports yaml-style value declaration
			return src[0:i], i + 1, r == '\n', nil
		default:
			if isValidKeyRune(r) {
				continue
			}

			return "", 0, false, fmt.Errorf(
				`line %d: unexpected character %q in variable name %q`,
				p.line, string(r), strings.Split(src, "\n")[0])
		}
	}
	return "", 0, false, nil
}

// isValidKeyRune reports whether the rune may appear in a variable name
// ([A-Za-z0-9_.-] plus bracket indexing).
func isValidKeyRune(r rune) bool {
	switch r {
	case '_', '.', '-', '[', ']':
		return true
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

// extractVarValue extracts variable value and returns rest of slice
func (p *parser) extractVarValue(src string, envMap map[string]string, lookupFn LookupFn) (string, string, error) {
	quote, isQuoted := hasQuotePrefix(src)
	if !isQuoted {
		// unquoted value - read until new line
		value, rest, _ := strings.Cut(src, "\n")
		p.line++

		// Remove inline comments on unquoted lines
		value, _, _ = strings.Cut(value, " #")
		value = strings.TrimRightFunc(value, unicode.IsSpace)
		retVal, err := p.expandVariables(value, envMap, lookupFn)
		return retVal, rest, err
	}

	value, rest, ok := p.extractQuotedValue(src, quote)
	if !ok {
		// return formatted error if quoted string is not terminated
		valEndIndex := strings.IndexFunc(src, isCharFunc('\n'))
		if valEndIndex == -1 {
			valEndIndex = len(src)
		}

		return "", "", fmt.Errorf("line %d: unterminated quoted value %s", p.line, src[:valEndIndex])
	}

	if quote == prefixDoubleQuote {
		// expand standard shell escape sequences & then interpolate
		// variables on the result
		retVal, err := p.expandVariables(expandEscapes(value), envMap, lookupFn)
		if err != nil {
			return "", "", err
		}
		value = retVal
	}

	return value, rest, nil
}

// extractQuotedValue scans src (which starts with quote) up to the closing
// quote, unescaping escaped quote characters. Returns the raw value, the rest
// of the slice, and false when the quoted string is not terminated.
func (p *parser) extractQuotedValue(src string, quote byte) (string, string, bool) {
	previousCharIsEscape := false
	var chars []byte
	for i := 1; i < len(src); i++ {
		char := src[i]
		if char == '\n' {
			p.line++
		}

		if previousCharIsEscape {
			previousCharIsEscape = false
			// an escaped quote symbol (\" or \', depends on quote) drops its
			// backslash; any other escaped character keeps it
			if char != quote {
				chars = append(chars, '\\')
			}
			chars = append(chars, char)
			continue
		}

		switch char {
		case '\\':
			previousCharIsEscape = true
		case quote:
			return string(chars), src[i+1:], true
		default:
			chars = append(chars, char)
		}
	}

	return "", "", false
}

func expandEscapes(str string) string {
	out := escapeSeqRegex.ReplaceAllStringFunc(str, func(match string) string {
		if match == `\$` {
			// `\$` is not a Go escape sequence, the expansion parser uses
			// the special `$$` syntax
			// both `FOO=\$bar` and `FOO=$$bar` are valid in an env file and
			// will result in FOO w/ literal value of "$bar" (no interpolation)
			return "$$"
		}

		if strings.HasPrefix(match, `\0`) {
			// octal escape sequences in Go are not prefixed with `\0`, so
			// rewrite the prefix, e.g. `\0123` -> `\123` -> literal value "S"
			match = strings.Replace(match, `\0`, `\`, 1)
		}

		// use Go to unquote (unescape) the literal
		// see https://go.dev/ref/spec#Rune_literals
		//
		// NOTE: Go supports ADDITIONAL escapes like `\x` & `\u` & `\U`!
		// These are NOT supported, which is why we use a regex to find
		// only matches we support and then use `UnquoteChar` instead of a
		// `Unquote` on the entire value
		v, _, _, err := strconv.UnquoteChar(match, '"')
		if err != nil {
			return match
		}
		return string(v)
	})
	return out
}

func (p *parser) indexOfNonSpaceChar(src string) int {
	return strings.IndexFunc(src, func(r rune) bool {
		if r == '\n' {
			p.line++
		}
		return !unicode.IsSpace(r)
	})
}

// hasQuotePrefix reports whether charset starts with single or double quote and returns quote character
func hasQuotePrefix(src string) (byte, bool) {
	if src == "" {
		return 0, false
	}

	switch quote := src[0]; quote {
	case prefixDoubleQuote, prefixSingleQuote:
		return quote, true // isQuoted
	default:
		return 0, false
	}
}

func isCharFunc(char rune) func(rune) bool {
	return func(v rune) bool {
		return v == char
	}
}

// isSpace reports whether the rune is a space character but not line break character
//
// this differs from [unicode.IsSpace], which also applies line break as space
func isSpace(r rune) bool {
	switch r {
	case '\t', '\v', '\f', '\r', ' ', charNextLine, charNonBreakingSpace:
		return true
	}
	return false
}
