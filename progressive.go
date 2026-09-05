package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/term"
)

// pagerQuit is set once the user quits out of a --more-style pagination
// prompt (see waitForContinue()). listTarget() checks it before doing any
// further work -- including further pages of the current section, further
// -R recursion, and (see main.go's directory loop) any remaining directory
// arguments -- so quitting stops the whole listing, not just the current
// page.
var pagerQuit bool

// imagePlan is the reserved layout for one entry's -I thumbnail in
// progressive mode (see renderProgressiveImages()): decided from a cheap
// header peek (see peekImagePixelSize()), before the entry's text is ever
// printed, so that text output doesn't have to wait for the full image
// read+encode.
type imagePlan struct {
	hasImage bool
	height   int  // reserved thumbnail height in rows; meaningless if !hasImage
	stacked  bool // image goes on its own line below the text, not beside it
	textRows int  // physical rows this entry's own printed text (plus any
	// reserved image-column padding) occupies once the
	// terminal wraps it -- almost always 1; see list.go's own
	// textRows computation. <1 (the zero value included)
	// means "1", same as textRowCount() below.
}

// textRowCount is p.textRows, normalized to its effective minimum of 1.
func (p imagePlan) textRowCount() int {
	if p.textRows < 1 {
		return 1
	}
	return p.textRows
}

// rows is how many terminal rows this entry's text plus reserved thumbnail
// space together occupy: just the text's own row count for no image or a
// 1-row image sharing the text's own last row, or more when the thumbnail
// is taller than that (sharing the text's own last row unless stacked, in
// which case it's added on top of the text's own row(s)).
func (p imagePlan) rows() int {
	textRows := p.textRowCount()
	if !p.hasImage {
		return textRows
	}
	if p.stacked {
		return textRows + p.height
	}
	if p.height > textRows {
		return p.height
	}
	return textRows
}

// planProgressiveImages decides each entry's reserved thumbnail height by
// peeking at just enough of each image file's header to read its pixel
// dimensions (see peekImagePixelSize()) -- fast compared to reading and
// base64-encoding the whole file, which renderProgressiveImages() defers
// until after the text listing has already been printed.
func planProgressiveImages(fullPaths []string, imgWidth, imgHeight, termHeight int, stackedFlags []bool, textRows []int, ql qlExtensions) []imagePlan {
	plans := make([]imagePlan, len(fullPaths))
	for i := range plans {
		tr := 1
		if textRows != nil && i < len(textRows) {
			tr = textRows[i]
		}
		plans[i].textRows = tr
	}
	sem := make(chan struct{}, imagePrefixConcurrency)
	var wg sync.WaitGroup
	for i, p := range fullPaths {
		if !isFileFollow(p) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !imageExtensions[ext] && !isQLCandidate(p, ext, ql) {
			continue
		}
		stacked := stackedFlags != nil && stackedFlags[i]
		plans[i].hasImage = true
		plans[i].height = minInt(imgHeight, termHeight)
		plans[i].stacked = stacked
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p, ext string) {
			defer wg.Done()
			defer func() { <-sem }()
			if pxW, pxH, ok := peekImagePixelSize(p, ext); ok && pxW > 0 && pxH > 0 {
				plans[i].height = minInt(aspectScaledHeight(imgWidth, pxW, pxH), termHeight)
				if debugPagingEnabled() {
					fmt.Fprintf(os.Stderr, "MACLS_DEBUG_PAGING: entry=%d ext=%s pxW=%d pxH=%d -> height=%d path=%s\n", i, ext, pxW, pxH, plans[i].height, p)
				}
			}
		}(i, p, ext)
	}
	wg.Wait()
	return plans
}

// progressiveTextLayout builds the imgPrefixes/imgSuffixes/imgColWidth
// buildEntries()/buildFinalEntries() need to lay out and print the text
// listing immediately: a blank prefix reserving the thumbnail's column
// (same as the non-progressive path), and a suffix of bare newlines
// reserving each entry's thumbnail rows -- no image data yet, so nothing
// here waits on file I/O.
//
// The filler is p.rows() minus the entry's own text row count, not p.rows()
// minus a flat 1: an entry whose printed line is wide enough to wrap on its
// own (a long "-> target" on a symlink, say) already gets those extra rows
// from the terminal's own line wrapping, with no "\n" of ours involved --
// reserving p.rows()-1 regardless would double up on that entry's own
// wrapped rows, an extra blank line the terminal never actually needed.
func progressiveTextLayout(plans []imagePlan, imgWidth int) (prefixes, suffixes []string, imgColWidth int) {
	imgColWidth = imgWidth + 1
	imgColPad := strings.Repeat(" ", imgColWidth)
	prefixes = make([]string, len(plans))
	suffixes = make([]string, len(plans))
	for i, p := range plans {
		prefixes[i] = imgColPad
		if filler := p.rows() - p.textRowCount(); filler > 0 {
			suffixes[i] = strings.Repeat("\n", filler)
		}
	}
	return prefixes, suffixes, imgColWidth
}

// renderProgressiveImages draws each entry's thumbnail into the rows
// progressiveTextLayout() already reserved for it, after that text has
// been printed: it reads and base64-encodes the actual image file (the
// slow part -- see buildImagePrefix()) concurrently per entry, then jumps
// the cursor up to the entry's reserved row, draws over the blank padding
// left there, and returns the cursor to where printing left off (DECSC/
// DECRC, under a mutex so concurrent draws don't interleave their escape
// sequences).
//
// An entry whose reserved row has already scrolled out of the terminal's
// visible height is skipped outright: ANSI cursor-up can't reach into
// scrollback, so there's no way to draw there without corrupting whatever
// now occupies that row. Entries are processed nearest-to-bottom (most
// likely still on screen) first.
//
// The cursor is hidden (DECTCEM, CSI ?25l) for the duration of the whole
// concurrent drawing pass and shown again once every draw has finished:
// without this, each entry's own save/jump/draw/restore is visibly
// distracting -- the cursor appears to hop around the screen as thumbnails
// land -- even though the final result is correct either way.
func renderProgressiveImages(fullPaths []string, plans []imagePlan, imgWidth, termHeight int, ql qlExtensions) {
	starts := make([]int, len(plans))
	totalRows := 0
	for i, p := range plans {
		starts[i] = totalRows
		totalRows += p.rows()
	}

	var order []int
	for i, p := range plans {
		if p.hasImage {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return
	}
	sort.SliceStable(order, func(a, b int) bool { return starts[order[a]] > starts[order[b]] })

	fmt.Print("\033[?25l") // DECTCEM off: hide cursor
	setCursorHidden(true)
	defer func() {
		fmt.Print("\033[?25h") // DECTCEM on: show cursor again
		setCursorHidden(false)
	}()

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, imagePrefixConcurrency)
	for _, i := range order {
		rowsUp := totalRows - starts[i]
		if plans[i].stacked {
			rowsUp -= plans[i].textRowCount()
		}
		if rowsUp >= termHeight {
			// Already scrolled off; unreachable without risking
			// drawing over the wrong row.
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i, rowsUp int) {
			defer wg.Done()
			defer func() { <-sem }()
			img := buildImagePrefix(fullPaths[i], imgWidth, plans[i].height, termHeight, false, ql)
			if img == "" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			fmt.Print("\0337") // DECSC: save cursor position
			if rowsUp > 0 {
				fmt.Printf("\033[%dA", rowsUp)
			}
			fmt.Print("\r" + img)
			fmt.Print("\0338") // DECRC: restore cursor position
		}(i, rowsUp)
	}
	wg.Wait()
}

// pagerPromptRows is how many terminal rows printPaginated() reserves for
// its own "-- more --" prompt at the bottom of each page, so the prompt
// itself never pushes the page's own last row off screen.
const pagerPromptRows = 1

// printPaginated prints entryLines (one already-rendered line per entry,
// each possibly containing embedded "\n"s for an entry's own reserved
// thumbnail rows -- see progressiveTextLayout()) a screenful at a time,
// rendering each page's thumbnails (see renderProgressiveImages()) before
// moving on. Every entry within a page is guaranteed to still be on screen
// when its thumbnail is drawn (a page never holds more than a terminal
// height's worth of rows), unlike a single unpaginated dump of the whole
// listing, where entries past the first screenful scroll off before their
// image ever gets drawn. plans may all have hasImage false (no -I), in
// which case this is plain more(1)-style text pagination with no image
// work at all.
//
// When there's more than one page and standard input is a terminal, it
// pauses after each page but the last with a "-- more --" prompt (see
// waitForContinue()): space advances a full page, return advances a single
// entry (then prompts again, so holding return steps through the listing
// one entry at a time); otherwise (input isn't interactive) it just keeps
// going without pausing, matching how a non-interactive pager falls back to
// a plain dump rather than hanging. Even a listing that fits on one screen
// still gets this same final prompt if anything on it has a thumbnail --
// clicking one there and pressing space still opens Quick Look (see
// preview.go) -- but not otherwise, matching this function's own pre-
// Quick-Look behavior for a plain text listing that never needed more
// than one page.
func printPaginated(entryLines []string, plans []imagePlan, fullPaths []string, imgWidth, termHeight int, ql qlExtensions) {
	n := minInt(len(entryLines), len(plans))
	if n == 0 {
		return
	}
	entryLines, plans, fullPaths = entryLines[:n], plans[:n], fullPaths[:n]

	pageCapacity := termHeight - pagerPromptRows
	debugLogPaging(termHeight, pageCapacity, entryLines, plans)
	if pageCapacity < 1 {
		pageCapacity = 1
	}
	canPrompt := term.IsTerminal(int(os.Stdin.Fd()))

	renderPage := func(start, end int) {
		fmt.Print(strings.Join(entryLines[start:end], "\n") + "\n")
		renderProgressiveImages(fullPaths[start:end], plans[start:end], imgWidth, termHeight, ql)
	}

	i := 0
outer:
	for i < n {
		start := i
		rows := 0
		for i < n {
			r := plans[i].rows()
			if rows > 0 && rows+r > pageCapacity {
				break
			}
			rows += r
			i++
		}
		renderPage(start, i)

		for canPrompt {
			lookup := singleColumnClickLookup(fullPaths, plans, imgWidth, start, i)
			if i >= n && lookup == nil {
				// Nothing left to page through, and nothing on screen to
				// click either -- no reason to prompt at all, matching
				// --paging's older behavior for a listing that never
				// needed more than one page.
				break
			}
			switch waitForContinue(lookup) {
			case pagerActionQuit:
				pagerQuit = true
				return
			case pagerActionLine:
				if i < n {
					renderPage(i, i+1)
					i++
				} else {
					// Nothing left to advance by a line; same as a page
					// advance below, just finish.
					continue outer
				}
			default: // pagerActionPage
				continue outer
			}
		}
	}
}

// pagerAction is waitForContinue()'s result: which way the user asked the
// pager to advance, or to stop entirely.
type pagerAction int

const (
	pagerActionPage pagerAction = iota // space: a full page
	pagerActionLine                    // return: a single line/entry
	pagerActionQuit                    // q, Ctrl-C, Esc
)

// waitForContinue prints a "-- more --" prompt and blocks for input on
// standard input, put into raw mode for the duration so a key doesn't need
// Enter and isn't echoed. Space continues to the next full page; return (or
// a newline) continues just one line/entry, same as more(1)/less(1)'s own
// line-at-a-time key; q, Ctrl-C, or Esc quits; anything else is ignored and
// it keeps waiting. A read error/EOF on standard input is treated the same
// as space, so an unexpectedly closed input can't hang the listing.
//
// lookup is nil for a page with no thumbnails at all, in which case this
// is exactly the plain keys-only prompt above. Otherwise (experimental --
// see preview.go) it also turns on mouse click reporting for the duration
// of this one prompt: clicking a thumbnail and then pressing space opens
// it in a real Quick Look window (qlmanage -p) instead of advancing, and
// the prompt keeps waiting at the same page.
func waitForContinue(lookup clickEntry) pagerAction {
	if lookup == nil {
		return waitForContinuePlain()
	}
	return waitForContinueClick(lookup)
}

func waitForContinuePlain() pagerAction {
	fmt.Print("-- more (space to continue, return for one line, q to quit) --")
	defer fmt.Print("\r\033[K") // erase the prompt before the next page

	fd := int(os.Stdin.Fd())
	oldState, err := enterRawMode(fd)
	if err != nil {
		return pagerActionPage
	}
	defer exitRawMode(fd, oldState)

	buf := make([]byte, 1)
	for {
		nRead, err := os.Stdin.Read(buf)
		if err != nil || nRead == 0 {
			return pagerActionPage
		}
		switch buf[0] {
		case ' ':
			return pagerActionPage
		case '\r', '\n':
			return pagerActionLine
		case 'q', 'Q', 3 /* Ctrl-C */, 27 /* Esc */ :
			return pagerActionQuit
		}
	}
}

// waitForContinueClick is waitForContinue()'s thumbnail-aware counterpart
// (see preview.go). It additionally queries the cursor's current row (DSR)
// and turns on mouse click reporting so that pressing space right after
// clicking a thumbnail opens that entry in Quick Look and keeps waiting at
// the same prompt instead of advancing; clicking elsewhere first clears
// that selection, and a space with nothing selected behaves exactly like
// the plain prompt.
func waitForContinueClick(lookup clickEntry) pagerAction {
	// Same prompt text as the plain (no-thumbnail) prompt -- the
	// click-to-Quick-Look behavior isn't spelled out here (see README).
	fmt.Print("-- more (space to continue, return for one line, q to quit) --")
	defer fmt.Print("\r\033[K")

	fd := int(os.Stdin.Fd())
	oldState, err := enterRawMode(fd)
	if err != nil {
		return pagerActionPage
	}
	defer exitRawMode(fd, oldState)

	promptRow, haveRow := queryCursorRow(os.Stdin)

	fmt.Print(mouseTrackingEnable)
	setMouseTrackingOn(true)
	defer func() {
		fmt.Print(mouseTrackingDisable)
		setMouseTrackingOn(false)
	}()

	r := newEscReader(os.Stdin)
	clicked := ""
	for {
		kind, key, col, mouseRow := r.next()
		switch kind {
		case escEventEOF:
			return pagerActionPage
		case escEventMouseClick:
			if !haveRow {
				continue
			}
			rowsUp := promptRow - mouseRow
			path, ok := lookup(rowsUp, col)
			if ok {
				clicked = path
			} else {
				clicked = "" // clicked elsewhere: deselect
			}
		case escEventKey:
			switch key {
			case ' ':
				if clicked != "" {
					launchQuickLook(clicked)
					continue
				}
				return pagerActionPage
			case '\r', '\n':
				return pagerActionLine
			case 'q', 'Q', 3 /* Ctrl-C */, 27 /* Esc */ :
				return pagerActionQuit
			}
		}
	}
}

// lineRowCounts is how many physical terminal rows each of lines (one
// rendered multi-column grid row, already containing any color/OSC-8
// escapes -- see renderMultiColumnLayout()) occupies once termWidth wraps
// it -- almost always 1, except a row holding one oversized entry alone
// (computeMultiColumnLayout()'s own "let it through" case for an entry
// too wide for any column arrangement), which wraps like any other long
// printed line. Escapes are measured via plainDisplayWidth(), not
// displayWidth(), since they carry no screen width of their own.
func lineRowCounts(lines []string, termWidth int) []int {
	counts := make([]int, len(lines))
	for i, l := range lines {
		counts[i] = maxInt(1, ceilDiv(plainDisplayWidth(l), termWidth))
	}
	return counts
}

// cumulativeRows returns, for each entry of lineRows, how many physical
// rows precede it (starts[i]), plus the grand total -- the physical-row
// counterpart of a plain running index sum, used to convert a logical
// grid-row index into an actual on-screen row offset.
func cumulativeRows(lineRows []int) (starts []int, total int) {
	starts = make([]int, len(lineRows))
	acc := 0
	for i, r := range lineRows {
		starts[i] = acc
		acc += r
	}
	return starts, acc
}

// renderProgressiveMultiImages is renderProgressiveImages()'s multi-column
// counterpart. Unlike -1/-l, several entries there can share one rendered
// line, so a deferred draw needs a horizontal jump (colOffsetOfIdx, see
// computeImageCellOffsets()) as well as the vertical one -- and since a
// thumbnail is already forced to exactly 1 row in multi-column output (see
// buildImagePrefix()'s allowAspectHeight), there's no stacked/multi-row
// case to account for here (an oversized entry's own wrapped continuation
// rows never carry any image, just more of its own text). rowOfIdx and
// colOffsetOfIdx are page-relative: logical row 0 is the first line
// printed for the page currently being drawn into; lineRows holds that
// same page's own per-line physical row counts (see lineRowCounts()), so a
// row that itself wrapped doesn't throw off every later entry's placement.
func renderProgressiveMultiImages(fullPaths []string, hasImage []bool, rowOfIdx, colOffsetOfIdx []int, imgWidth int, lineRows []int, termHeight int, ql qlExtensions) {
	starts, totalRows := cumulativeRows(lineRows)
	var order []int
	for i, has := range hasImage {
		if has {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return
	}
	sort.SliceStable(order, func(a, b int) bool { return rowOfIdx[order[a]] > rowOfIdx[order[b]] })

	fmt.Print("\033[?25l") // DECTCEM off: hide cursor
	setCursorHidden(true)
	defer func() {
		fmt.Print("\033[?25h") // DECTCEM on: show cursor again
		setCursorHidden(false)
	}()

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, imagePrefixConcurrency)
	for _, i := range order {
		rowsUp := totalRows - starts[rowOfIdx[i]]
		if rowsUp >= termHeight {
			// Already scrolled off; see renderProgressiveImages().
			continue
		}
		colRight := colOffsetOfIdx[i]
		wg.Add(1)
		sem <- struct{}{}
		go func(i, rowsUp, colRight int) {
			defer wg.Done()
			defer func() { <-sem }()
			img := buildImagePrefix(fullPaths[i], imgWidth, 1, termHeight, false, ql)
			if img == "" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			fmt.Print("\0337") // DECSC: save cursor position
			if rowsUp > 0 {
				fmt.Printf("\033[%dA", rowsUp)
			}
			fmt.Print("\r")
			if colRight > 0 {
				fmt.Printf("\033[%dC", colRight)
			}
			fmt.Print(img)
			fmt.Print("\0338") // DECRC: restore cursor position
		}(i, rowsUp, colRight)
	}
	wg.Wait()
}

// printPaginatedMulti is printPaginated()'s multi-column counterpart:
// pages are grouped by rendered LINE (a line can hold many entries' worth
// of columns) rather than by per-entry row count, and each entry's
// thumbnail is placed via renderProgressiveMultiImages() at its own
// (row, column) cell within the page rather than always column 0. A page
// is filled by physical row count, not raw line count -- same as
// printPaginated()'s own plans[i].rows() accumulation -- since a line
// holding one oversized entry (see computeMultiColumnLayout()'s "let it
// through" case) wraps to more than one physical row on its own, same as
// printPaginated()'s wrapped-line entries (see lineRowCounts()). Same
// final-prompt-even-on-one-page behavior as printPaginated() when
// anything on screen has a thumbnail -- see its own doc comment.
func printPaginatedMulti(lines []string, hasImage []bool, rowOfIdx, colOffsetOfIdx []int, fullPaths []string, imgWidth, termWidth, termHeight int, ql qlExtensions) {
	n := len(lines)
	if n == 0 {
		return
	}
	lineRows := lineRowCounts(lines, termWidth)
	pageCapacity := termHeight - pagerPromptRows
	if pageCapacity < 1 {
		pageCapacity = 1
	}
	canPrompt := term.IsTerminal(int(os.Stdin.Fd()))

	renderPage := func(start, end int) {
		fmt.Print(strings.Join(lines[start:end], "\n") + "\n")

		var pFullPaths []string
		var pHasImage []bool
		var pRowOfIdx []int
		var pColOffset []int
		for i := range fullPaths {
			if !hasImage[i] || rowOfIdx[i] < start || rowOfIdx[i] >= end {
				continue
			}
			pFullPaths = append(pFullPaths, fullPaths[i])
			pHasImage = append(pHasImage, true)
			pRowOfIdx = append(pRowOfIdx, rowOfIdx[i]-start)
			pColOffset = append(pColOffset, colOffsetOfIdx[i])
		}
		renderProgressiveMultiImages(pFullPaths, pHasImage, pRowOfIdx, pColOffset, imgWidth, lineRows[start:end], termHeight, ql)
	}

	start := 0
outer:
	for start < n {
		end := start
		rows := 0
		for end < n {
			r := lineRows[end]
			if rows > 0 && rows+r > pageCapacity {
				break
			}
			rows += r
			end++
		}
		renderPage(start, end)
		start = end

		for canPrompt {
			lookup := multiColumnClickLookup(fullPaths, hasImage, rowOfIdx, colOffsetOfIdx, imgWidth, lineRows, start)
			if start >= n && lookup == nil {
				// Nothing left to page through, and nothing on screen to
				// click either -- no reason to prompt at all, matching
				// --paging's older behavior for a listing that never
				// needed more than one page.
				break
			}
			switch waitForContinue(lookup) {
			case pagerActionQuit:
				pagerQuit = true
				return
			case pagerActionLine:
				if start < n {
					renderPage(start, start+1)
					start++
				} else {
					// Nothing left to advance by a line; same as a page
					// advance below, just finish.
					continue outer
				}
			default: // pagerActionPage
				continue outer
			}
		}
	}
}
