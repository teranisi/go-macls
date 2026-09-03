package main

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// displayWidth returns the actual display width of name (2 for full-width
// characters, 1 for half-width). macOS's filesystem returns filenames in
// NFD, so name is normalized to NFC first; any combining characters
// remaining after that are treated as width 0.
func displayWidth(name string) int {
	normalized := norm.NFC.String(name)
	total := 0
	for _, ch := range normalized {
		if isCombining(ch) {
			continue
		}
		switch width.LookupRune(ch).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			total += 2
		default:
			total++
		}
	}
	return total
}

func isCombining(ch rune) bool {
	return unicode.In(ch, unicode.Mn, unicode.Me)
}

// isControl reports whether ch is in Unicode general category C
// (control/format/surrogate/private-use/unassigned), matching Python's
// unicodedata.category(ch)[0] == "C".
func isControl(ch rune) bool {
	return unicode.In(ch, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs)
}

// sanitizeDisplayName replaces control/invisible characters with '?'.
func sanitizeDisplayName(name string) string {
	var b strings.Builder
	for _, ch := range name {
		if isControl(ch) {
			b.WriteByte('?')
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// shellMetachars are characters (beyond whitespace) that make a name unsafe
// to paste unquoted into a POSIX shell.
const shellMetachars = "`$&;|()<>*?[]{}!\"'\\"

func needsShellQuoting(name string) bool {
	if strings.HasPrefix(name, "~") || strings.HasPrefix(name, "#") {
		return true
	}
	for _, ch := range name {
		if unicode.IsSpace(ch) || strings.ContainsRune(shellMetachars, ch) {
			return true
		}
	}
	return false
}

// shellQuote wraps name in POSIX shell quotes, matching GNU ls's
// --quoting-style=shell.
func shellQuote(name string) string {
	if strings.Contains(name, "'") {
		var b strings.Builder
		for _, ch := range name {
			if strings.ContainsRune("$`\"\\", ch) {
				b.WriteByte('\\')
			}
			b.WriteRune(ch)
		}
		return "\"" + b.String() + "\""
	}
	return "'" + name + "'"
}

var ansiCNamedEscapes = map[rune]string{
	0x07: "\\a",
	0x08: "\\b",
	0x09: "\\t",
	0x0A: "\\n",
	0x0B: "\\v",
	0x0C: "\\f",
	0x0D: "\\r",
}

func needsAnsiCQuoting(name string) bool {
	for _, ch := range name {
		if isControl(ch) {
			return true
		}
	}
	return false
}

// ansiCQuote wraps name in bash/zsh ANSI-C quoting ($'...').
func ansiCQuote(name string) string {
	var b strings.Builder
	b.WriteString("$'")
	for _, ch := range name {
		switch {
		case ch == '\\':
			b.WriteString("\\\\")
		case ch == '\'':
			b.WriteString("\\'")
		case isControl(ch):
			if esc, ok := ansiCNamedEscapes[ch]; ok {
				b.WriteString(esc)
			} else if ch <= 0xFF {
				fmt.Fprintf(&b, "\\x%02x", ch)
			} else {
				fmt.Fprintf(&b, "\\u%04x", ch)
			}
		default:
			b.WriteRune(ch)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
