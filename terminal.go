package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// detectDarkBackground is a best-effort detection of whether the terminal's
// background is dark, based on COLORFGBG ("fg;bg", standard 16-color ANSI
// numbers). Returns nil if unknown -- COLORFGBG is an iTerm2/rxvt-family
// convention, not something every terminal sets (notably including
// Terminal.app and many others), so nil is a common case queryBackground()
// exists to cover.
func detectDarkBackground() *bool {
	value := os.Getenv("COLORFGBG")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ";")
	bg, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return nil
	}
	// 0-6 are the dark half of the standard 16-color palette; 8 is bright
	// black (gray), also dark. The rest (7, 9-15) are light.
	dark := bg == 0 || bg == 1 || bg == 2 || bg == 3 || bg == 4 || bg == 5 || bg == 6 || bg == 8
	return &dark
}

// queryBackgroundIsDark is detectDarkBackground()'s fallback for a terminal
// that doesn't set COLORFGBG at all: it asks the terminal directly for its
// actual background color via OSC 11 (\033]11;?\033\\), a query most
// terminal emulators answer (including several that never set COLORFGBG),
// and computes whether that color reads as dark from its luma. Returns
// ok=false on any read/parse failure, a terminal that doesn't answer OSC 11
// within a short timeout, or if standard input/output aren't both real
// terminals to run the query against in the first place -- --theme=auto
// then falls back to its own preexisting default (light) exactly as before
// this existed.
func queryBackgroundIsDark() (dark bool, ok bool) {
	if !isStdoutTTY() {
		return false, false
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return false, false
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false, false
	}
	defer term.Restore(fd, oldState)

	os.Stdout.WriteString("\033]11;?\033\\")
	deadline := time.Now().Add(200 * time.Millisecond)
	var buf []byte
	b := make([]byte, 1)
	for time.Now().Before(deadline) {
		if !waitReadable(os.Stdin, 50*time.Millisecond) {
			continue
		}
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			return false, false
		}
		buf = append(buf, b[0])
		// Terminated by BEL (an older convention some terminals still use)
		// or ST (ESC \, the modern one -- see the query's own trailing
		// "\033\\" above).
		if b[0] == 0x07 {
			break
		}
		if len(buf) >= 2 && buf[len(buf)-2] == 0x1B && b[0] == '\\' {
			break
		}
		if len(buf) > 64 {
			return false, false
		}
	}
	r, g, bl, ok := parseOSC11Reply(buf)
	if !ok {
		return false, false
	}
	luma := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
	return luma < 128, true
}

// parseOSC11Reply extracts the background color from an OSC 11 reply body
// (everything between the initial "\033]11;" and its BEL/ST terminator,
// e.g. "rgb:1e1e/2b2b/3838" or the shorter "rgb:1e/2b/38"), returning each
// component scaled to 0-255. A component given as more than 2 hex digits
// (the common case: terminals often report 4 digits, i.e. 16-bit-per-
// channel precision) uses just its most significant byte.
func parseOSC11Reply(buf []byte) (r, g, b int, ok bool) {
	s := string(buf)
	i := strings.Index(s, "rgb:")
	if i < 0 {
		return 0, 0, 0, false
	}
	s = s[i+len("rgb:"):]
	end := strings.IndexAny(s, "\x07\x1b")
	if end >= 0 {
		s = s[:end]
	}
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	component := func(hex string) (int, bool) {
		if len(hex) == 0 {
			return 0, false
		}
		if len(hex) > 2 {
			hex = hex[:2]
		}
		v, err := strconv.ParseInt(hex, 16, 32)
		if err != nil {
			return 0, false
		}
		return int(v), true
	}
	var okR, okG, okB bool
	if r, okR = component(parts[0]); !okR {
		return 0, 0, 0, false
	}
	if g, okG = component(parts[1]); !okG {
		return 0, 0, 0, false
	}
	if b, okB = component(parts[2]); !okB {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

func supportsTruecolor() bool {
	v := strings.ToLower(os.Getenv("COLORTERM"))
	return v == "truecolor" || v == "24bit"
}

func iterm2Supported() bool {
	return os.Getenv("TERM_PROGRAM") == "iTerm.app" || os.Getenv("LC_TERMINAL") == "iTerm2"
}

// runLs shells out to real ls(1) with flags + lsFlags + paths and returns
// its stdout as a list of lines (no trailing empty line).
func runLs(flags, lsFlags, paths []string) []string {
	args := append([]string{}, flags...)
	args = append(args, lsFlags...)
	args = append(args, "--")
	args = append(args, paths...)
	cmd := exec.Command("ls", args...)
	out, _ := cmd.Output()
	text := string(out)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isStdoutTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func getTerminalSize() (width, height int, ok bool) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// getTerminalWidth returns the terminal width to lay out multi-column
// output for: the actual terminal width if stdout is a tty, else COLUMNS,
// else 80.
func getTerminalWidth() int {
	if w, _, ok := getTerminalSize(); ok && w > 0 {
		return w
	}
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 {
		return w
	}
	return 80
}

// getTerminalHeight is analogous to getTerminalWidth, falling back to LINES
// then 24.
func getTerminalHeight() int {
	if _, h, ok := getTerminalSize(); ok && h > 0 {
		return h
	}
	if h, err := strconv.Atoi(os.Getenv("LINES")); err == nil && h > 0 {
		return h
	}
	return 24
}
