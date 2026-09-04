package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const (
	prog    = "macls"
	version = "1.0.0"
)

// Options holds every option list_target() and its helpers need. See the
// Python original's Options docstring for field semantics.
type Options struct {
	a, A, l, h, f, one, c, i, t, d, b, r, s, x, reverse bool
	quote, groupDirsFirst, stripe, paging               bool

	color       string
	theme       string
	tagColors   string
	columns     string
	tag         string
	suffixColor string
	fgMode      string
	baseFg      *rgb
	scale       int
	qlExtMode   string   // "default", "off", "all", "list" -- see --ql-ext
	qlExtExtra  []string // --ql-ext=list form's extensions, each ".ext" lowercase

	useColor     bool
	useTruecolor bool
}

func newOptions() *Options {
	return &Options{
		color:       "auto",
		theme:       "auto",
		tagColors:   "pastel",
		columns:     "compact",
		tag:         "bg",
		suffixColor: "off",
		fgMode:      "date",
		scale:       1,
		qlExtMode:   "default",
	}
}

type modeOption struct {
	name      string
	set       func(o *Options, v string)
	bareValue string
	choices   []string
}

var modeOptions = []modeOption{
	{"--color", func(o *Options, v string) { o.color = v }, "always", []string{"always", "auto", "never"}},
	{"--theme", func(o *Options, v string) { o.theme = v }, "auto", []string{"light", "dark", "auto"}},
	{"--tag-colors", func(o *Options, v string) { o.tagColors = v }, "pastel", []string{"vivid", "pastel"}},
	{"--columns", func(o *Options, v string) { o.columns = v }, "compact", []string{"compact", "classic"}},
	{"--tag", func(o *Options, v string) { o.tag = v }, "bg", []string{"bg", "dot", "str", "off"}},
	{"--suffix-color", func(o *Options, v string) { o.suffixColor = v }, "off", []string{"off", "type"}},
	{"--fg-mode", func(o *Options, v string) { o.fgMode = v }, "date", []string{"date", "off"}},
}

// maclsOnlyLongOpts are long options macls recognizes that plain ls(1)
// doesn't know about, used by stripMaclsOnlyOptions for the plain-ls
// fallback.
var maclsOnlyLongOpts = func() []string {
	opts := []string{}
	for _, m := range modeOptions {
		opts = append(opts, m.name)
	}
	return append(opts, "--quote", "--group-directories-first", "--stripe", "--base-fg", "--scale", "--paging", "--ql-ext")
}()

func stripMaclsOnlyOptions(argv []string) []string {
	var result []string
	stop := false
	for _, arg := range argv {
		if stop {
			result = append(result, arg)
			continue
		}
		if arg == "--" {
			stop = true
			result = append(result, arg)
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			result = append(result, arg)
			continue
		}
		matched := false
		for _, opt := range maclsOnlyLongOpts {
			if arg == opt || strings.HasPrefix(arg, opt+"=") {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if arg != "-1" && len(arg) > 1 && isAllDigits(arg[1:]) {
			// -N, parseOptions's own shorthand for --scale=N -- real ls(1)
			// has no such option, so this needs stripping here too, the
			// same as maclsOnlyLongOpts' long options.
			continue
		}
		result = append(result, arg)
	}
	return result
}

// preScanOptColor scans argv for a --color/--color=value ahead of
// parseOptions's own left-to-right pass, so an unsupported option earlier
// in argv can still fall back colorized correctly.
func preScanOptColor(argv []string) string {
	value := "auto"
	for _, arg := range argv {
		if arg == "--" {
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			continue
		}
		if arg == "--color" || strings.HasPrefix(arg, "--color=") {
			candidate := "always"
			if idx := strings.Index(arg, "="); idx >= 0 {
				candidate = arg[idx+1:]
			}
			if candidate == "always" || candidate == "auto" || candidate == "never" {
				value = candidate
			}
		}
	}
	return value
}

func dieInvalidValue(option, value string, choices []string) {
	fmt.Fprintf(os.Stderr, "%s: invalid value '%s' for %s (must be one of: %s)\n",
		prog, value, option, strings.Join(choices, ", "))
	os.Exit(2)
}

// parseHexRGB parses a 6-hex-digit RRGGBB string into an (r, g, b) value,
// ok=false if value isn't exactly 6 hex digits.
func parseHexRGB(value string) (rgb, bool) {
	if len(value) != 6 {
		return rgb{}, false
	}
	var c rgb
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseInt(value[i*2:i*2+2], 16, 32)
		if err != nil {
			return rgb{}, false
		}
		c[i] = int(v)
	}
	return c, true
}

func dieInvalidBaseFg(value string) {
	fmt.Fprintf(os.Stderr, "%s: invalid value '%s' for --base-fg (must be 6 hex digits, e.g. 808080)\n", prog, value)
	os.Exit(2)
}

// parsePositiveInt parses value as a positive (>=1) base-10 integer, or
// ok=false if it isn't one.
func parsePositiveInt(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func dieInvalidScale(value string) {
	fmt.Fprintf(os.Stderr, "%s: invalid value '%s' for --scale (must be a positive integer, e.g. 2)\n", prog, value)
	os.Exit(2)
}

// isAllDigits reports whether s is non-empty and every rune in it is an
// ASCII digit.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// parseQLExt parses --ql-ext's value into (mode, extra):
// "off" -> ("off", nil): disables -I's Quick Look thumbnails (see
// defaultQLExtensions) entirely, leaving imageExtensions files unaffected.
// "all" -> ("all", nil): every extension not already in imageExtensions
// becomes a Quick Look candidate, not just defaultQLExtensions' own curated
// list.
// Anything else -> ("list", extra), where extra is the comma-separated
// extensions in value, each normalized to lowercase with a leading dot
// (e.g. "foo,BAR" -> [".foo", ".bar"]), added on top of
// defaultQLExtensions rather than replacing them.
// ok is false if value is empty or contains an empty entry (e.g. "foo,,bar"
// or a trailing/leading comma).
func parseQLExt(value string) (mode string, extra []string, ok bool) {
	if value == "off" {
		return "off", nil, true
	}
	if value == "all" {
		return "all", nil, true
	}
	if value == "" {
		return "", nil, false
	}
	parts := strings.Split(value, ",")
	extra = make([]string, len(parts))
	for i, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			return "", nil, false
		}
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		extra[i] = p
	}
	return "list", extra, true
}

func dieInvalidQLExt(value string) {
	fmt.Fprintf(os.Stderr, "%s: invalid value '%s' for --ql-ext (must be \"off\", \"all\", or a comma-separated list of extensions, e.g. foo,bar)\n", prog, value)
	os.Exit(2)
}

func valueOf(arg string) string {
	if idx := strings.Index(arg, "="); idx >= 0 {
		return arg[idx+1:]
	}
	return ""
}

// parseOptions parses short options (combinable) and long options,
// GNU-style: options may be freely mixed with positional arguments in any
// order. Falls back to plain ls (colorized) if an unsupported option is
// found; exits with an error if a recognized mode-style option is given a
// value it doesn't understand. Returns (opts, positional).
func parseOptions(argv []string) (*Options, []string) {
	opts := newOptions()
	opts.color = preScanOptColor(argv)
	var positional []string
	stopOptions := false
	i := 0
	for i < len(argv) {
		arg := argv[i]
		if stopOptions {
			positional = append(positional, arg)
			i++
			continue
		}
		if arg == "--help" {
			printHelp()
			os.Exit(0)
		}
		if arg == "--version" {
			fmt.Printf("%s %s\n", prog, version)
			os.Exit(0)
		}
		matchedMode := false
		for _, m := range modeOptions {
			if arg == m.name || strings.HasPrefix(arg, m.name+"=") {
				value := m.bareValue
				if idx := strings.Index(arg, "="); idx >= 0 {
					value = arg[idx+1:]
				}
				if !contains(m.choices, value) {
					dieInvalidValue(m.name, value, m.choices)
				}
				m.set(opts, value)
				matchedMode = true
				break
			}
		}
		if matchedMode {
			i++
			continue
		}
		if arg == "--quote" {
			opts.quote = true
			i++
			continue
		}
		if arg == "--group-directories-first" {
			opts.groupDirsFirst = true
			i++
			continue
		}
		if arg == "--stripe" {
			opts.stripe = true
			i++
			continue
		}
		if arg == "--paging" {
			opts.paging = true
			i++
			continue
		}
		if arg == "--base-fg" || strings.HasPrefix(arg, "--base-fg=") {
			value := valueOf(arg)
			c, ok := parseHexRGB(value)
			if !ok {
				dieInvalidBaseFg(value)
			}
			opts.baseFg = &c
			i++
			continue
		}
		if arg == "--scale" || strings.HasPrefix(arg, "--scale=") {
			value := valueOf(arg)
			n, ok := parsePositiveInt(value)
			if !ok {
				dieInvalidScale(value)
			}
			opts.scale = n
			i++
			continue
		}
		if arg == "--ql-ext" || strings.HasPrefix(arg, "--ql-ext=") {
			value := valueOf(arg)
			mode, extra, ok := parseQLExt(value)
			if !ok {
				dieInvalidQLExt(value)
			}
			opts.qlExtMode = mode
			opts.qlExtExtra = extra
			i++
			continue
		}
		if arg != "-1" && len(arg) > 1 && isAllDigits(arg[1:]) {
			// -N shorthand for --scale=N (see --scale above), for any N
			// other than 1: "-1" itself already means single-column output
			// (opts.one, in the per-character loop below) and keeps that
			// meaning -- there'd be nothing for "-1" as a scale shortcut to
			// do anyway, since --scale=1 is already the unscaled base size.
			value := arg[1:]
			n, ok := parsePositiveInt(value)
			if !ok {
				dieInvalidScale(value)
			}
			opts.scale = n
			i++
			continue
		}
		if arg == "--" {
			stopOptions = true
			i++
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			i++
			continue
		}
		chars := arg[1:]
		j := 0
		for j < len(chars) {
			ch := chars[j]
			if ch >= '0' && ch <= '9' {
				// -N shorthand for --scale=N (see --scale above), combined
				// with other short flags in any order/position -- e.g.
				// "-Il2" (-I -l --scale=2) and "-2I" (--scale=2 -I) both
				// work, each consuming the maximal run of digits starting
				// here as N before resuming normal flag-by-flag scanning
				// right after it. A run that's exactly "1" is exempted
				// (falls through to the 'l' == '1' case below) -- that's
				// -1's own existing single-column meaning, not worth
				// shadowing for --scale=1, which is --scale's own default
				// anyway.
				k := j
				for k < len(chars) && chars[k] >= '0' && chars[k] <= '9' {
					k++
				}
				run := chars[j:k]
				if run != "1" {
					n, ok := parsePositiveInt(run)
					if !ok {
						dieInvalidScale(run)
					}
					opts.scale = n
					j = k
					continue
				}
			}
			switch ch {
			case 'a':
				opts.a = true
			case 'A':
				opts.A = true
			case 'l':
				opts.l = true
				opts.one = false
				opts.c = false
			case 'h':
				opts.h = true
			case '1':
				opts.one = true
				opts.l = false
				opts.c = false
			case 'C':
				opts.c = true
				opts.l = false
				opts.one = false
			case 'F':
				opts.f = true
			case 'I':
				opts.i = true
			case 't':
				opts.t = true
			case 'd':
				opts.d = true
			case 'B':
				opts.b = true
			case 'R':
				opts.r = true
			case 'S':
				opts.s = true
			case 'X':
				opts.x = true
			case 'r':
				opts.reverse = true
			default:
				argvFb, envFb := plainLsFallbackArgvEnv(opts.color)
				fullArgv := append(append([]string{}, argvFb...), stripMaclsOnlyOptions(argv)...)
				execFallback(fullArgv, envFb)
			}
			j++
		}
		i++
	}
	return opts, positional
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// plainLsFallbackArgvEnv returns (argv, env) to exec into for the
// unsupported-option fallback. See the Python original's docstring for the
// full rationale (BSD vs. GNU ls color flags, CLICOLOR_FORCE).
func plainLsFallbackArgvEnv(optColor string) ([]string, []string) {
	var colorize bool
	switch optColor {
	case "always":
		colorize = true
	case "never":
		colorize = false
	default:
		colorize = isStdoutTTY() || os.Getenv("CLICOLOR_FORCE") != ""
	}
	env := os.Environ()
	if !colorize {
		return []string{"ls"}, env
	}
	if isDarwin() {
		if os.Getenv("CLICOLOR_FORCE") == "" {
			env = append(env, "CLICOLOR_FORCE=1")
		}
		return []string{"ls", "-G"}, env
	}
	return []string{"ls", "--color=always"}, env
}

// execFallback replaces the current process with ls, matching Python's
// os.execvpe("ls", ...). argv[0] is the program name to search PATH for
// and the argv[0] the child process sees.
func execFallback(argv []string, env []string) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", prog, err)
		os.Exit(127)
	}
	if err := syscall.Exec(path, argv, env); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", prog, err)
		os.Exit(127)
	}
}

func printHelp() {
	fmt.Printf(`Usage: %s [-a] [-A] [-l] [-h] [-1] [-C] [-F] [-I] [--scale=n | -n] [--ql-ext=spec] [--paging] [-t] [-S] [-X] [-r] [-d] [-R] [-B] [--color=when] [--theme=mode] [--tag-colors=mode] [--columns=mode] [--tag=mode] [--stripe] [--suffix-color=mode] [--fg-mode=mode] [--base-fg=RRGGBB] [--quote] [--group-directories-first] [--version] [path...]

Options:
  -a        Show all files, including . and ..
  -A        Show all files except . and ..
  -l        Use long format (permissions, owner, size, date, etc.)
  -h        With -l, show file sizes in human-readable form (e.g. 1.0K,
            234M, 2.3G) instead of raw byte counts. No effect without -l.
  -1        Force single-column, one-entry-per-line output
  -C        Force multi-column output, even when standard output isn't
            a terminal
  -F        Append entry type indicators (/ @ * = |)
  -I        Show image thumbnails using iTerm2's inline image protocol.
            Ignored outside iTerm2, or when standard output isn't a
            terminal.
  --scale=n Multiply the -I thumbnail's width and height by n. Has an
            effect only in -1/-l (the only contexts -I itself is ever
            active in, since it's disabled outright on non-tty output).
            Omitting n is the same as 1. No effect without -I. -n (n a
            positive integer other than 1) is shorthand for --scale=n --
            e.g. -2 is the same as --scale=2, and it combines with other
            short flags in any position (-Il2, -2Il, -I2l all mean the
            same thing). -1 itself keeps its own existing meaning
            (single-column output) rather than becoming --scale=1, which
            --scale's own default already is.
  --ql-ext=spec
            Adjusts which extensions -I tries a Quick Look preview for
            (Word/Excel/PowerPoint documents by default) beyond image
            files, which are unaffected by this option either way. spec
            is one of:
              off        Disables Quick Look thumbnails entirely.
              all        Every extension not already treated as an image
                         file becomes a Quick Look candidate, not just
                         the curated Office default -- can be noticeably
                         slower over a directory with many non-image
                         files.
              ext,ext,...
                         A comma-separated list of extensions (with or
                         without a leading dot, e.g. "md,rtf") added on
                         top of the Office default, not replacing it.
            Has no effect without -I. There's no bare "--ql-ext" form
            (unlike --scale/--tag/etc.) -- a value is always required.
  --paging  Pause with a more(1)-style "-- more --" prompt (space for the
            next page, return for one more line, q to quit) whenever the
            listing doesn't fit on one screen -- with -I, thumbnails are
            also filled in afterward as they finish loading, page by
            page, instead of every thumbnail in the whole listing being
            read before anything prints. Without --paging (the
            default), the whole listing (and, with -I, every thumbnail)
            prints without ever pausing partway through.
  -t        Sort by modification time, newest first
  -S        Sort by file size, largest first
  -X        Sort by extension
  -r        Reverse the sort order
  -d        List directories themselves, not their contents
  -R        Recursively list subdirectories encountered
  -B        Show directories in bold
  --color=when
            Control when to colorize output (always/auto/never).
            Omitting when is the same as always.
  --theme=mode
            Select the color gradient for the terminal's background
            (light/dark/auto). auto detects it from COLORFGBG, falling
            back to light if that isn't set. This is the default.
  --tag-colors=mode
            Select the Finder tag color palette (vivid/pastel), used
            only in 24-bit truecolor mode. Omitting mode is the same as
            pastel, the default.
  --columns=mode
            Select the multi-column layout mode (compact/classic).
            compact (the default) lets an unusually long name span
            multiple columns on its own row, so it doesn't force every
            column to widen -- but falls back to classic if that
            wouldn't actually fit more columns. classic always behaves
            like plain ls -C. Omitting mode is the same as compact.
  --tag=mode
            Select how Finder tags are shown (bg/dot/str/off). bg (the
            default) uses the color of the entry's last Finder tag, if
            any, as the entry's own background; extra tags beyond that
            one show as dots after the name. dot never sets a
            background from a tag; every tag (not just the extras)
            shows as a dot instead. str appends every tag's name after
            the entry as a bracketed comma-separated list (e.g.
            "report.pdf [Work, Urgent]"), each name colored with its
            own tag color where it has one, and never sets a
            background either. off shows no tag information at all.
            Omitting mode is the same as bg.
  --stripe  Tint every entry's background, filling the entry's full
            column width in multi-column output (alternating tint by
            column), or every odd row's whole line in -1/-l.
  --suffix-color=mode
            Select the color of the -F type indicator (/ @ * = |)
            (off/type). off (the default) colors it the same as
            the entry's name. type instead colors it by which
            character it is, matching /bin/ls -G's default per-type
            colors (/ blue, @ magenta, = green, | yellow, * red).
            Has no effect without -F.
  --fg-mode=mode
            Select whether a name's own foreground color is set from
            its recency gradient (date/off). date (the default) colors
            each name by how recently it was modified. off leaves
            names in the terminal's default foreground color; Finder
            tag and stripe backgrounds are unaffected either way.
            Omitting mode is the same as date.
  --base-fg=RRGGBB
            Override the color the oldest files fade to in the recency
            gradient (default: a guess based on --theme's light/dark
            setting), as 6 hex digits, e.g. 808080. The whole gradient
            is recomputed as a straight line from the cyan/magenta
            starting color to RRGGBB. No effect with --fg-mode=off.
  --quote   Quote a name whenever it contains whitespace, a shell
            metacharacter, or a control character, so it's safe to
            paste into a shell.
  --group-directories-first
            List directories before other entries, keeping whatever
            sort order (name/-t/-S/-X, each possibly reversed by -r)
            was already in effect within each group. A symlink to a
            directory counts as a directory here (matching GNU ls).
  --help    Show this help and exit
  --version Show the version number and exit

If an unsupported option is passed, it falls back to the standard ls command.
`, prog)
}

func isDarwin() bool {
	return runtime.GOOS == "darwin"
}
