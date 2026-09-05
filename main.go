package main

import (
	"fmt"
	"os"
	"sort"
)

// sortDirArgs sorts multiple directory arguments for display order. Real
// macls.py uses locale.strxfrm() to match sort(1)'s collation; Go's
// standard library has no locale-aware collation, so this uses a plain
// byte-wise string sort instead (an approximation, matching the common
// C/POSIX-locale case exactly).
func sortDirArgs(dirArgs []string) []string {
	sorted := append([]string{}, dirArgs...)
	sort.Strings(sorted)
	return sorted
}

func run() int {
	installSignalCleanup()

	argv := os.Args[1:]
	opts, positional := parseOptions(argv)

	switch opts.color {
	case "always":
		opts.useColor = true
	case "never":
		opts.useColor = false
	default:
		opts.useColor = isStdoutTTY() || os.Getenv("CLICOLOR_FORCE") != ""
	}

	if opts.theme == "auto" {
		dark := detectDarkBackground()
		if dark == nil {
			// COLORFGBG is an iTerm2/rxvt-family convention that plenty of
			// otherwise-capable terminals (Terminal.app included) simply
			// don't set -- ask the terminal itself instead of assuming
			// light, which would otherwise fade old files toward a dark
			// foreground invisible against an actually-dark background.
			if d, ok := queryBackgroundIsDark(); ok {
				dark = &d
			}
		}
		if dark != nil && *dark {
			opts.theme = "dark"
		} else {
			opts.theme = "light"
		}
	}

	opts.useTruecolor = opts.useColor && supportsTruecolor()

	if opts.i && !isStdoutTTY() {
		opts.i = false
	} else if opts.i && !iterm2Supported() {
		fmt.Fprintf(os.Stderr, "%s: -I requires iTerm2; disabling thumbnails\n", prog)
		opts.i = false
	}

	args := positional
	if len(args) == 0 {
		args = []string{"."}
	}

	exitCode := 0
	var validArgs []string
	for _, a := range args {
		if !lexists(a) {
			fmt.Fprintf(os.Stderr, "%s: %s: No such file or directory\n", prog, a)
			exitCode = 1
			continue
		}
		validArgs = append(validArgs, a)
	}

	if len(validArgs) == 0 {
		return exitCode
	}

	showHeaders := len(args) > 1

	var fileArgs, dirArgs []string
	for _, a := range validArgs {
		if !opts.d && isDirFollow(a) {
			dirArgs = append(dirArgs, a)
		} else {
			fileArgs = append(fileArgs, a)
		}
	}

	if len(dirArgs) > 1 {
		dirArgs = sortDirArgs(dirArgs)
	}

	sectionPrinted := false

	if len(fileArgs) > 0 {
		listTarget("files", false, fileArgs, opts)
		sectionPrinted = true
	}

	for _, d := range dirArgs {
		if pagerQuit {
			break
		}
		if sectionPrinted {
			fmt.Println()
		}
		listTarget("dir", showHeaders, []string{d}, opts)
		sectionPrinted = true
	}

	return exitCode
}

func main() {
	os.Exit(run())
}
