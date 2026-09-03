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
}

// rows is how many terminal rows this entry's text plus reserved thumbnail
// space together occupy: 1 (just the text) for no image or a 1-row image
// sharing the text's own row, or more when the thumbnail is taller than 1
// row (sharing the text's row unless stacked, in which case it's added on
// top of the text's own row).
func (p imagePlan) rows() int {
	if !p.hasImage {
		return 1
	}
	if p.stacked {
		return 1 + p.height
	}
	if p.height > 1 {
		return p.height
	}
	return 1
}

// planProgressiveImages decides each entry's reserved thumbnail height by
// peeking at just enough of each image file's header to read its pixel
// dimensions (see peekImagePixelSize()) -- fast compared to reading and
// base64-encoding the whole file, which renderProgressiveImages() defers
// until after the text listing has already been printed.
func planProgressiveImages(fullPaths []string, imgWidth, imgHeight, termHeight int, stackedFlags []bool) []imagePlan {
	plans := make([]imagePlan, len(fullPaths))
	sem := make(chan struct{}, imagePrefixConcurrency)
	var wg sync.WaitGroup
	for i, p := range fullPaths {
		if !isFile(p) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !imageExtensions[ext] {
			continue
		}
		stacked := stackedFlags != nil && stackedFlags[i]
		plans[i] = imagePlan{hasImage: true, height: minInt(imgHeight, termHeight), stacked: stacked}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p, ext string) {
			defer wg.Done()
			defer func() { <-sem }()
			if pxW, pxH, ok := peekImagePixelSize(p, ext); ok && pxW > 0 && pxH > 0 {
				plans[i].height = minInt(aspectScaledHeight(imgWidth, pxW, pxH), termHeight)
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
func progressiveTextLayout(plans []imagePlan, imgWidth int) (prefixes, suffixes []string, imgColWidth int) {
	imgColWidth = imgWidth + 1
	imgColPad := strings.Repeat(" ", imgColWidth)
	prefixes = make([]string, len(plans))
	suffixes = make([]string, len(plans))
	for i, p := range plans {
		prefixes[i] = imgColPad
		if filler := p.rows() - 1; filler > 0 {
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
func renderProgressiveImages(fullPaths []string, plans []imagePlan, imgWidth, termHeight int) {
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

	fmt.Print("\033[?25l")       // DECTCEM off: hide cursor
	defer fmt.Print("\033[?25h") // DECTCEM on: show cursor again

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, imagePrefixConcurrency)
	for _, i := range order {
		rowsUp := totalRows - starts[i]
		if plans[i].stacked {
			rowsUp--
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
			img := buildImagePrefix(fullPaths[i], imgWidth, plans[i].height, termHeight, false)
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
// image ever gets drawn.
//
// When there's more than one page and standard input is a terminal, it
// pauses after each page but the last with a "-- more --" prompt (see
// waitForContinue()); otherwise (input isn't interactive) it just keeps
// going without pausing, matching how a non-interactive pager falls back
// to a plain dump rather than hanging.
func printPaginated(entryLines []string, plans []imagePlan, fullPaths []string, imgWidth, termHeight int) {
	n := minInt(len(entryLines), len(plans))
	if n == 0 {
		return
	}
	entryLines, plans, fullPaths = entryLines[:n], plans[:n], fullPaths[:n]

	pageCapacity := termHeight - pagerPromptRows
	if pageCapacity < 1 {
		pageCapacity = 1
	}
	canPrompt := term.IsTerminal(int(os.Stdin.Fd()))

	i := 0
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

		fmt.Print(strings.Join(entryLines[start:i], "\n") + "\n")
		renderProgressiveImages(fullPaths[start:i], plans[start:i], imgWidth, termHeight)

		if i < n && canPrompt {
			if !waitForContinue() {
				pagerQuit = true
				return
			}
		}
	}
}

// waitForContinue prints a "-- more --" prompt and blocks for a single
// keypress on standard input, put into raw mode for the duration so the
// key doesn't need Enter and isn't echoed. Space (or Enter) continues to
// the next page; q, Ctrl-C, or Esc quits; anything else is ignored and it
// keeps waiting, same as a traditional more(1)/less(1) prompt. Returns
// false only for the quit case.
func waitForContinue() bool {
	fmt.Print("-- more (space to continue, q to quit) --")
	defer fmt.Print("\r\033[K") // erase the prompt before the next page

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return true
	}
	defer term.Restore(fd, oldState)

	buf := make([]byte, 1)
	for {
		nRead, err := os.Stdin.Read(buf)
		if err != nil || nRead == 0 {
			return true
		}
		switch buf[0] {
		case ' ', '\r', '\n':
			return true
		case 'q', 'Q', 3 /* Ctrl-C */, 27 /* Esc */ :
			return false
		}
	}
}
