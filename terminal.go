package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// detectDarkBackground is a best-effort detection of whether the terminal's
// background is dark, based on COLORFGBG ("fg;bg", standard 16-color ANSI
// numbers). Returns nil if unknown.
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
