package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// buildLsFlags returns the subset of opts that also apply to the real
// ls(1) invocations list_target() shells out to. opts.x (-X) is
// deliberately never passed through: macOS's own ls -X means something
// unrelated (see the Python original's docstring). opts.reverse is passed
// through as ls(1)'s own -r only when opts.x is not set.
func buildLsFlags(opts *Options) []string {
	var flags []string
	if opts.a {
		flags = append(flags, "-a")
	} else if opts.A {
		flags = append(flags, "-A")
	}
	if opts.t {
		flags = append(flags, "-t")
	}
	if opts.s {
		flags = append(flags, "-S")
	}
	if opts.reverse && !opts.x {
		flags = append(flags, "-r")
	}
	if opts.d {
		flags = append(flags, "-d")
	}
	if opts.h {
		flags = append(flags, "-h")
	}
	return flags
}

// extensionSortKey returns -X's sort key: the text after the last '.' in
// the raw name, or "" if there is none.
func extensionSortKey(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		return ""
	}
	return name[idx+1:]
}

// fetchMtimes returns (now, mtimes) for each path, fetched together for
// coloring the foreground. Skipped (mtimes all nil) when useColor is
// false.
func fetchMtimes(fullPaths []string, useColor bool) (int64, []*int64) {
	if !useColor {
		return 0, make([]*int64, len(fullPaths))
	}
	now := time.Now().Unix()
	mtimes := make([]*int64, len(fullPaths))
	for i, p := range fullPaths {
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		mt := statMtime(info)
		mtimes[i] = &mt
	}
	return now, mtimes
}

// computeQuoting decides, for --quote, the sanitized display name, whether
// each name needs quoting, whether it needs ANSI-C quoting, and whether any
// name in the listing needs quoting at all (see the Python original's
// docstring for the hanging-indent rationale).
func computeQuoting(names []string, optQuote, isTty bool) (sanitized []string, needsQuote, ansiCNeeded []bool, anyQuoted bool) {
	n := len(names)
	sanitized = make([]string, n)
	needsQuote = make([]bool, n)
	ansiCNeeded = make([]bool, n)
	for i, name := range names {
		ansiCNeeded[i] = optQuote && isTty && needsAnsiCQuoting(name)
		if ansiCNeeded[i] {
			sanitized[i] = ansiCQuote(name)
		} else if isTty {
			sanitized[i] = sanitizeDisplayName(name)
		} else {
			sanitized[i] = name
		}
	}
	for i := range names {
		needsQuote[i] = !ansiCNeeded[i] && optQuote && needsShellQuoting(sanitized[i])
		if ansiCNeeded[i] || needsQuote[i] {
			anyQuoted = true
		}
	}
	return sanitized, needsQuote, ansiCNeeded, anyQuoted
}

type entryMeta struct {
	dispNames    []string
	hangPrefixes []string
	suffixes     []string
	isDirs       []bool
	bgNums       []*int
	dotTagnums   [][]int
	entryTags    [][]finderTag
	namelen      []int
	plainlen     []int
}

// buildEntries computes each entry's pre-color display metadata, before
// the multi-column layout is computed.
func buildEntries(names, fullPaths, sanitizedNames []string, needsQuote, ansiCNeeded []bool, anyQuoted bool, opts *Options, imgColWidth int) entryMeta {
	n := len(names)
	m := entryMeta{
		dispNames:    make([]string, n),
		hangPrefixes: make([]string, n),
		suffixes:     make([]string, n),
		isDirs:       make([]bool, n),
		bgNums:       make([]*int, n),
		dotTagnums:   make([][]int, n),
		entryTags:    make([][]finderTag, n),
		namelen:      make([]int, n),
		plainlen:     make([]int, n),
	}

	var tagLookups []tagLookup
	needTags := opts.tag != "off" && (opts.useColor || opts.tag == "str")
	if needTags {
		tagLookups = fetchTagLookups(fullPaths, opts.tag)
	}

	for i := range names {
		p := fullPaths[i]
		dispName := sanitizedNames[i]
		hangPrefix := ""
		if needsQuote[i] {
			dispName = shellQuote(dispName)
		} else if ansiCNeeded[i] {
			// already fully quoted and escaped by ansiCQuote()
		} else if opts.quote && anyQuoted {
			hangPrefix = " "
		}
		suffix := ""
		if opts.f {
			suffix = typeSuffix(p)
		}
		isDirectory := isDir(p)

		var bgNum *int
		var dotTagnums []int
		var tags []finderTag
		tagExtra := 0
		if needTags {
			lookup := tagLookups[i]
			bgNum, dotTagnums = lookup.bgNum, lookup.dotTagnums
			if !opts.useColor {
				bgNum, dotTagnums = nil, nil
			}
			if opts.tag == "str" {
				tags = lookup.allTags
				_, tagExtra = buildTagLabel(tags, false, opts.useTruecolor, opts.tagColors, "")
			}
		}

		dispLen := len(hangPrefix) + displayWidth(dispName) + len(suffix) + dotExtraWidth(dotTagnums) + tagExtra + imgColWidth
		m.dispNames[i] = dispName
		m.hangPrefixes[i] = hangPrefix
		m.suffixes[i] = suffix
		m.isDirs[i] = isDirectory
		m.bgNums[i] = bgNum
		m.dotTagnums[i] = dotTagnums
		m.entryTags[i] = tags
		m.namelen[i] = dispLen
		m.plainlen[i] = dispLen
	}
	return m
}

// buildFinalEntries builds the final colored and hyperlinked display
// string for each entry. colOfIdx (from computeMultiColumnLayout, or nil
// outside multi-column output) supplies each entry's starting column for
// --stripe striping.
func buildFinalEntries(names, fullPaths []string, m entryMeta, imgPrefixes, imgSuffixes []string, mtimes []*int64, now int64, isTty bool, opts *Options, colOfIdx []int) []string {
	final := make([]string, len(names))
	for i := range names {
		var stripeCol *int
		if opts.stripe && colOfIdx != nil {
			c := colOfIdx[i]
			stripeCol = &c
		} else if opts.stripe && colOfIdx == nil && i%2 == 1 {
			c := i
			stripeCol = &c
		}

		colored, _ := buildColoredName(colorNameOpts{
			name:         m.dispNames[i],
			mtime:        mtimes[i],
			now:          now,
			useColor:     opts.useColor,
			bold:         opts.b && m.isDirs[i],
			suffix:       m.suffixes[i],
			theme:        opts.theme,
			useTruecolor: opts.useTruecolor,
			tagColors:    opts.tagColors,
			bgNum:        m.bgNums[i],
			dotTagnums:   m.dotTagnums[i],
			stripe:       opts.stripe,
			stripeCol:    stripeCol,
			suffixColor:  opts.suffixColor,
			fgMode:       opts.fgMode,
			baseFg:       opts.baseFg,
		})

		tagLabel := ""
		if len(m.entryTags[i]) > 0 {
			stripeAlt := stripeCol != nil && *stripeCol%2 == 0
			stripeColumn := opts.useColor && opts.stripe && stripeCol != nil
			bgPart := ""
			if stripeColumn {
				bgPart = stripeSGR(opts.useTruecolor, opts.theme, stripeAlt)
			}
			tagLabel, _ = buildTagLabel(m.entryTags[i], opts.useColor, opts.useTruecolor, opts.tagColors, bgPart)
		}
		if isTty {
			colored = buildHyperlink(fullPaths[i], colored)
		}
		final[i] = imgPrefixes[i] + m.hangPrefixes[i] + colored + tagLabel + imgSuffixes[i]
	}
	return final
}

// renderLongFormat renders -l output: splices the colored name into each
// of plainL's real permissions/owner/size/date lines, striping odd rows'
// whole line when opts.stripe. order, when given (opts.groupDirsFirst /
// opts.x), is the permutation list_target() already applied to
// names/fullPaths to reorder them; order[i] is that entry's position in
// plainL, which is still in the original ls order.
func renderLongFormat(names []string, plainL, final, imgPrefixes []string, opts *Options, order []int) []string {
	var output []string
	li := 0
	if len(plainL) > 0 && strings.HasPrefix(plainL[0], "total ") {
		output = append(output, plainL[0])
		li = 1
	}
	for i, name := range names {
		pos := i
		if order != nil {
			pos = order[i]
		}
		idx := li + pos
		if idx >= len(plainL) {
			break
		}
		surroundSGR := ""
		if opts.stripe && opts.useColor && i%2 == 1 {
			surroundSGR = stripeSGR(opts.useTruecolor, opts.theme, false)
		}
		spliced := spliceColoredName(name, plainL[idx], final[i][len(imgPrefixes[i]):], surroundSGR)
		output = append(output, imgPrefixes[i]+spliced)
	}
	return output
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// listTarget processes and outputs one section for a group of targets.
// mode is "dir" (list the contents of a single directory) or "files"
// (list the explicitly given files/directories themselves).
func listTarget(mode string, showHeader bool, paths []string, opts *Options) {
	if pagerQuit {
		return
	}
	var output []string
	joinIsDir := mode == "dir"
	var targetDir string
	if joinIsDir {
		targetDir = paths[0]
	}

	if joinIsDir && showHeader {
		output = append(output, targetDir+":")
	}

	lsFlags := buildLsFlags(opts)
	names := runLs([]string{"-1"}, lsFlags, paths)

	var fullPaths []string
	if joinIsDir {
		fullPaths = make([]string, len(names))
		for i, nm := range names {
			fullPaths[i] = targetDir + "/" + nm
		}
	} else {
		fullPaths = append([]string{}, names...)
	}

	var order []int
	if opts.x || opts.groupDirsFirst {
		order = make([]int, len(names))
		for i := range order {
			order[i] = i
		}
		if opts.x {
			sort.SliceStable(order, func(a, b int) bool {
				return extensionSortKey(names[order[a]]) < extensionSortKey(names[order[b]])
			})
			if opts.reverse {
				for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
					order[i], order[j] = order[j], order[i]
				}
			}
		}
		if opts.groupDirsFirst {
			sort.SliceStable(order, func(a, b int) bool {
				ai := 1
				if isDirFollow(fullPaths[order[a]]) {
					ai = 0
				}
				bi := 1
				if isDirFollow(fullPaths[order[b]]) {
					bi = 0
				}
				return ai < bi
			})
		}
		newNames := make([]string, len(names))
		newFullPaths := make([]string, len(names))
		for i, o := range order {
			newNames[i] = names[o]
			newFullPaths[i] = fullPaths[o]
		}
		names, fullPaths = newNames, newFullPaths
	}

	now, mtimes := fetchMtimes(fullPaths, opts.useColor)

	isTty := isStdoutTTY()

	multi := !opts.l && !opts.one && (opts.c || isStdoutTTY())

	sanitizedNames, needsQuote, ansiCNeeded, anyQuoted := computeQuoting(names, opts.quote, isTty)

	var plainL []string
	if opts.l {
		plainL = runLs([]string{"-l"}, lsFlags, paths)
	}

	scaleApplies := opts.i && !multi
	scale := 1
	if scaleApplies {
		scale = opts.scale
	}
	termWidth := getTerminalWidth()
	if scaleApplies && scale > 1 {
		scale = minInt(scale, maxInt(1, termWidth/itermImgWidth))
	}
	imgWidth := itermImgWidth * scale
	imgHeight := itermImgHeight * scale

	var stackedFlags []bool
	if scaleApplies && scale > 1 {
		if opts.l && plainL != nil {
			entryLines := plainL
			if len(plainL) > 0 && strings.HasPrefix(plainL[0], "total ") {
				entryLines = plainL[1:]
			}
			textWidth := func(i int) int {
				idx := i
				if order != nil {
					idx = order[i]
				}
				if idx < len(entryLines) {
					return displayWidth(entryLines[idx])
				}
				return 0
			}
			stackedFlags = make([]bool, len(names))
			for i := range names {
				stackedFlags[i] = (imgWidth + textWidth(i)) > termWidth
			}
		} else {
			nameExtra := 0
			if opts.f {
				nameExtra = 1
			}
			stackedFlags = make([]bool, len(names))
			for i := range names {
				stackedFlags[i] = (imgWidth + displayWidth(sanitizedNames[i]) + nameExtra) > termWidth
			}
		}
	}

	progressive := opts.i && scaleApplies && opts.paging
	progressiveMulti := opts.i && multi && opts.paging
	var imgPrefixes, imgSuffixes []string
	var imgColWidth int
	var progressivePlans []imagePlan
	var hasImageMulti []bool
	termHeight := getTerminalHeight()
	ql := resolveQLExtensions(opts)
	switch {
	case progressive:
		progressivePlans = planProgressiveImages(fullPaths, imgWidth, imgHeight, termHeight, stackedFlags, ql)
		imgPrefixes, imgSuffixes, imgColWidth = progressiveTextLayout(progressivePlans, imgWidth)
	case progressiveMulti:
		imgColWidth = imgWidth + 1
		imgColPad := strings.Repeat(" ", imgColWidth)
		imgPrefixes = make([]string, len(fullPaths))
		imgSuffixes = make([]string, len(fullPaths))
		hasImageMulti = make([]bool, len(fullPaths))
		for i, p := range fullPaths {
			imgPrefixes[i] = imgColPad
			hasImageMulti[i] = isFile(p) && hasThumbnailCandidate(p, ql)
		}
	default:
		imgPrefixes, imgSuffixes, imgColWidth = buildImagePrefixes(fullPaths, opts.i, imgWidth, imgHeight, stackedFlags, scaleApplies, ql)
	}

	m := buildEntries(names, fullPaths, sanitizedNames, needsQuote, ansiCNeeded, anyQuoted, opts, imgColWidth)

	effectiveStripe := opts.stripe && opts.useColor

	var layout columnLayout
	var colOfIdx []int
	if multi {
		layout = computeMultiColumnLayout(m.namelen, opts.f, opts.columns, getTerminalWidth(), effectiveStripe)
		colOfIdx = layout.colOfIdx
	}

	final := buildFinalEntries(names, fullPaths, m, imgPrefixes, imgSuffixes, mtimes, now, isTty, opts, colOfIdx)

	preambleCount := len(output) // header line, if any
	hasTotalLine := opts.l && len(plainL) > 0 && strings.HasPrefix(plainL[0], "total ")

	switch {
	case progressiveMulti && multi && len(final) > 0:
		hangWidth := 0
		if opts.quote && anyQuoted {
			hangWidth = 1
		}
		lines := renderMultiColumnLayout(layout, final, m.plainlen, effectiveStripe, opts.theme, opts.useTruecolor, hangWidth)
		rowOfIdx, colOffsetOfIdx := computeImageCellOffsets(layout, effectiveStripe)
		if preambleCount > 0 {
			fmt.Print(strings.Join(output[:preambleCount], "\n") + "\n")
		}
		printPaginatedMulti(lines, hasImageMulti, rowOfIdx, colOffsetOfIdx, fullPaths, imgWidth, termHeight, ql)
	case opts.l:
		output = append(output, renderLongFormat(names, plainL, final, imgPrefixes, opts, order)...)
		if progressive {
			if hasTotalLine {
				preambleCount++
			}
			if preambleCount > 0 && preambleCount <= len(output) {
				fmt.Print(strings.Join(output[:preambleCount], "\n") + "\n")
			}
			printPaginated(output[preambleCount:], progressivePlans, fullPaths, imgWidth, termHeight, ql)
		} else if len(output) > 0 {
			fmt.Print(strings.Join(output, "\n") + "\n")
		}
	case multi && len(final) > 0:
		hangWidth := 0
		if opts.quote && anyQuoted {
			hangWidth = 1
		}
		output = append(output, renderMultiColumnLayout(layout, final, m.plainlen, effectiveStripe, opts.theme, opts.useTruecolor, hangWidth)...)
		if len(output) > 0 {
			fmt.Print(strings.Join(output, "\n") + "\n")
		}
	default:
		output = append(output, final...)
		if progressive {
			if preambleCount > 0 && preambleCount <= len(output) {
				fmt.Print(strings.Join(output[:preambleCount], "\n") + "\n")
			}
			printPaginated(output[preambleCount:], progressivePlans, fullPaths, imgWidth, termHeight, ql)
		} else if len(output) > 0 {
			fmt.Print(strings.Join(output, "\n") + "\n")
		}
	}

	if pagerQuit {
		return
	}

	if joinIsDir && opts.r && !opts.d {
		for i, name := range names {
			if name == "." || name == ".." {
				continue
			}
			if !m.isDirs[i] {
				continue
			}
			fmt.Println()
			listTarget("dir", true, []string{fullPaths[i]}, opts)
			if pagerQuit {
				return
			}
		}
	}
}
