package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// typeSuffix is the -F type indicator (/ @ * = |), or "" if none applies.
func typeSuffix(path string) string {
	if isSymlink(path) {
		return "@"
	}
	if isDir(path) {
		return "/"
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	mode := info.Mode()
	switch {
	case mode&os.ModeNamedPipe != 0:
		return "|"
	case mode&os.ModeSocket != 0:
		return "="
	case mode.IsRegular() && isExecutable(path):
		return "*"
	}
	return ""
}

func isExecutable(path string) bool {
	if err := unixAccessX(path); err == nil {
		return true
	}
	return false
}

// buildHyperlink wraps text in an OSC 8 hyperlink escape sequence pointing
// at path's file:// URL.
func buildHyperlink(path, text string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	u := "file://" + (&url.URL{Path: abs}).EscapedPath()
	return "\033]8;;" + u + "\033\\" + text + "\033]8;;\033\\"
}

// buildColoredName builds the colored display for name. Returns (colored
// string, extra display width added).
type colorNameOpts struct {
	name         string
	mtime        *int64
	now          int64
	useColor     bool
	bold         bool
	suffix       string
	theme        string
	useTruecolor bool
	tagColors    string
	bgNum        *int
	dotTagnums   []int
	stripe       bool
	stripeCol    *int
	suffixColor  string
	fgMode       string
	baseFg       *rgb
}

func buildColoredName(o colorNameOpts) (string, int) {
	if !o.useColor {
		return o.name + o.suffix, 0
	}

	var fgRGB *rgb
	if o.fgMode == "date" {
		fgRGB = dateColorRGB(o.mtime, o.now, o.bgNum, o.theme, o.baseFg)
	}

	stripeColumn := o.stripe && o.stripeCol != nil
	stripeAlt := o.stripeCol != nil && *o.stripeCol%2 == 0
	useStripe := o.bgNum == nil && stripeColumn

	var bgPart string
	if o.bgNum != nil {
		bgPart = finderSGR(*o.bgNum, "48", o.useTruecolor, o.tagColors)
	} else if useStripe {
		bgPart = stripeSGR(o.useTruecolor, o.theme, stripeAlt)
	}

	var parts []string
	if o.bold {
		parts = append(parts, "1")
	}
	if fgRGB != nil {
		parts = append(parts, fgSGR(*fgRGB, o.useTruecolor))
	}
	if bgPart != "" {
		parts = append(parts, bgPart)
	}

	var suffixTypeCode string
	if o.suffixColor == "type" {
		suffixTypeCode = suffixTypeSGR[o.suffix]
	}
	suffixSGR := suffixTypeCode
	if suffixTypeCode != "" && bgPart != "" {
		suffixSGR = suffixTypeCode + ";" + bgPart
	}

	var out string
	if len(parts) > 0 {
		sgr := strings.Join(parts, ";")
		if suffixSGR != "" {
			out = "\033[" + sgr + "m" + o.name + "\033[0m\033[" + suffixSGR + "m" + o.suffix + "\033[0m"
		} else {
			out = "\033[" + sgr + "m" + o.name + o.suffix + "\033[0m"
		}
	} else if suffixSGR != "" {
		out = o.name + "\033[" + suffixSGR + "m" + o.suffix + "\033[0m"
	} else {
		out = o.name + o.suffix
	}

	var dotSGRSuffix string
	if stripeColumn {
		dotSGRSuffix = ";" + stripeSGR(o.useTruecolor, o.theme, stripeAlt)
	}

	extra := 0
	for i := len(o.dotTagnums) - 1; i >= 0; i-- {
		tagnum := o.dotTagnums[i]
		if extra == 0 {
			if stripeColumn {
				out += "\033[" + stripeSGR(o.useTruecolor, o.theme, stripeAlt) + "m \033[0m"
			} else {
				out += " "
			}
			extra = 1
		}
		out += "\033[" + finderSGR(tagnum, "38", o.useTruecolor, o.tagColors) + dotSGRSuffix + "m●\033[0m"
		extra++
	}

	return out, extra
}

// spliceColoredName replaces the trailing name in a line of ls -l output
// with colored. Also handles the symlink "name -> target" form. Returns
// line unchanged if neither matches. surroundSGR, if non-empty, wraps
// everything OTHER than colored in that SGR code.
func spliceColoredName(name, line, colored, surroundSGR string) string {
	wrap := func(s string) string {
		if s == "" || surroundSGR == "" {
			return s
		}
		return "\033[" + surroundSGR + "m" + s + "\033[0m"
	}

	n := len(name)
	if len(line) >= n && line[len(line)-n:] == name {
		return wrap(line[:len(line)-n]) + colored
	}

	marker := name + " -> "
	last := -1
	searchFrom := 0
	for {
		p := strings.Index(line[searchFrom:], marker)
		if p == -1 {
			break
		}
		p += searchFrom
		last = p
		searchFrom = p + 1
	}
	if last != -1 {
		prefix := line[:last]
		restAfterName := line[last+n:]
		return wrap(prefix) + colored + wrap(restAfterName)
	}

	return wrap(line)
}
