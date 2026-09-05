package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// plainDisplayWidth is displayWidth(), but for a string that may contain
// ANSI escape sequences (SGR color codes, OSC 8 "\033]8;;url\033\\...\033]8;;\033\\"
// hyperlinks around a clickable path) -- the kind buildFinalEntries()/
// renderMultiColumnLayout() actually print, as opposed to the plain name
// text displayWidth() is normally fed. The escape bytes themselves take no
// screen width, so a CSI sequence ("\033[" ... a final byte in 0x40-0x7E)
// or an OSC sequence ("\033]" ... BEL or the two-byte ST "\033\\") is
// skipped entirely rather than measured rune-by-rune.
func plainDisplayWidth(s string) int {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1B && i+1 < len(s) && (s[i+1] == '[' || s[i+1] == ']') {
			isOSC := s[i+1] == ']'
			j := i + 2
			for j < len(s) {
				if isOSC {
					if s[j] == 0x07 {
						j++
						break
					}
					if s[j] == 0x1B && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
						break
					}
				} else if s[j] >= 0x40 && s[j] <= 0x7E {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
	}
	return displayWidth(b.String())
}

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
