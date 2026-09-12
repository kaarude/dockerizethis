// Package source provides conservative lexical evidence for source detectors.
// It does not evaluate code or resolve framework routing graphs.
package source

import (
	"regexp"
	"strings"
)

type Text struct {
	Literal string // comments removed, string literals retained
	Code    string // comments and strings removed at the same byte offsets
}

// Read masks comments and quoted text so a declaration inside a string cannot
// look like executable code. Unsupported raw/interpolated strings are opaque.
func Read(input string) Text {
	literal, code := []byte(input), []byte(input)
	blank := func(buf []byte, start, end int) {
		for i := start; i < end; i++ {
			if buf[i] != '\n' {
				buf[i] = ' '
			}
		}
	}
	for i := 0; i < len(input); {
		start := i
		switch {
		case strings.HasPrefix(input[i:], "//"):
			for i < len(input) && input[i] != '\n' {
				i++
			}
			blank(literal, start, i)
			blank(code, start, i)
		case strings.HasPrefix(input[i:], "/*"):
			i += 2
			depth := 1
			for i < len(input) && depth > 0 {
				if strings.HasPrefix(input[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(input[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			blank(literal, start, i)
			blank(code, start, i)
		case input[i] == '"':
			endMarker := "\""
			raw := false
			if strings.HasPrefix(input[i:], `"""`) {
				endMarker = `"""`
				raw = true
			} else {
				j := i - 1
				for j >= 0 && input[j] == '#' {
					j--
				}
				if j >= 0 && input[j] == 'r' {
					endMarker += input[j+1 : i]
					raw = true
				}
			}
			verbatim := i > 0 && input[i-1] == '@'
			i += len(strings.TrimRight(endMarker, "#"))
			for i < len(input) {
				if strings.HasPrefix(input[i:], endMarker) {
					if verbatim && strings.HasPrefix(input[i:], `""`) {
						i += 2
						continue
					}
					i += len(endMarker)
					break
				}
				if !raw && !verbatim && input[i] == '\\' {
					i = min(i+2, len(input))
				} else {
					i++
				}
			}
			blank(code, start, i)
			// Raw strings cannot be route arguments supported by these detectors.
			if raw || verbatim {
				blank(literal, start, i)
			}
		case input[i] == '\'' && i+2 < len(input) && (input[i+2] == '\'' || input[i+1] == '\\'):
			i++
			for i < len(input) {
				if input[i] == '\\' {
					i = min(i+2, len(input))
					continue
				}
				i++
				if input[i-1] == '\'' {
					break
				}
			}
			blank(literal, start, i)
			blank(code, start, i)
		default:
			i++
		}
	}
	return Text{Literal: string(literal), Code: string(code)}
}

// Matches returns only expressions that begin in code, outside a quoted value.
func (s Text) Matches(pattern *regexp.Regexp) [][]string {
	var matches [][]string
	for _, loc := range pattern.FindAllStringSubmatchIndex(s.Literal, -1) {
		start := loc[0]
		for start < loc[1] && (s.Literal[start] == ' ' || s.Literal[start] == '\n' || s.Literal[start] == '\t') {
			start++
		}
		if start == loc[1] || s.Code[start] == ' ' || s.Code[start] == '\n' {
			continue
		}
		var match []string
		for i := 0; i < len(loc); i += 2 {
			if loc[i] < 0 {
				match = append(match, "")
			} else {
				match = append(match, s.Literal[loc[i]:loc[i+1]])
			}
		}
		matches = append(matches, match)
	}
	return matches
}

func IsTestPath(path string) bool {
	for _, part := range strings.FieldsFunc(strings.ToLower(path), func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "test" || part == "tests" || part == "examples" || part == "benches" || strings.HasSuffix(part, ".tests") || strings.HasSuffix(part, "test.java") || strings.HasSuffix(part, "test.cs") || strings.HasSuffix(part, "tests.cs") || strings.HasSuffix(part, "tests.fs") || strings.HasSuffix(part, "_test.rs") || strings.HasSuffix(part, "test.kt") {
			return true
		}
	}
	return false
}
