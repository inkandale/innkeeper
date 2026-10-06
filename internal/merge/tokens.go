// Package merge compares library and app copies of a card field by field.
// It is pure: no database or plugin access.
package merge

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// sentences splits prose so separate edits to one paragraph diff as separate
// hunks; the tokens always rejoin to s exactly.
func sentences(s string) []string {
	var out []string
	start, term := 0, false
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(r):
			j := i
			for j < len(s) {
				r2, n2 := utf8.DecodeRuneInString(s[j:])
				if !unicode.IsSpace(r2) {
					break
				}
				j += n2
			}
			if term || strings.Contains(s[i:j], "\n") {
				out = append(out, s[start:j])
				start = j
			}
			term = false
			i = j
			continue
		case strings.ContainsRune(".!?…", r):
			term = true
		case strings.ContainsRune("\"'”’*)", r):
			// closers keep a pending terminator alive
		default:
			term = false
		}
		i += n
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// words splits after each whitespace run; the tokens always rejoin to s.
func words(s string) []string {
	var out []string
	start, inSpace, seenWord := 0, false, false
	for i, r := range s {
		sp := unicode.IsSpace(r)
		if !sp && inSpace && seenWord {
			out = append(out, s[start:i])
			start = i
		}
		if !sp {
			seenWord = true
		}
		inSpace = sp
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
