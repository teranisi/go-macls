package main

import (
	"fmt"
	"os"
)

// debugPagingEnabled gates debugLogPaging()'s diagnostic output on the
// MACLS_DEBUG_PAGING environment variable -- temporary instrumentation for
// tracking down a real-world --paging layout report (thumbnail landing on
// the wrong row, or a page cutting off after a single entry) that hasn't
// reproduced in a sandboxed/simulated terminal, not meant to stay relevant
// once that's resolved.
func debugPagingEnabled() bool {
	return os.Getenv("MACLS_DEBUG_PAGING") != ""
}

// debugLogPaging prints, to standard error, the terminal-height-derived
// page capacity and every entry's own imagePlan (in particular textRows,
// height, and the rows() they add up to) right before printPaginated()
// starts filling pages with them -- everything list.go's own textWidth/
// textRows computation and planProgressiveImages() decided, in one place,
// to compare against what the real terminal is actually doing.
func debugLogPaging(termHeight, pageCapacity int, entryLines []string, plans []imagePlan) {
	if !debugPagingEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "MACLS_DEBUG_PAGING: termWidth=%d termHeight=%d pageCapacity=%d entries=%d\n",
		getTerminalWidth(), termHeight, pageCapacity, len(plans))
	for i, p := range plans {
		line := ""
		if i < len(entryLines) {
			line = entryLines[i]
			if len(line) > 80 {
				line = line[:80] + "..."
			}
		}
		fmt.Fprintf(os.Stderr, "  [%d] textRows=%d height=%d hasImage=%v stacked=%v rows()=%d line=%q\n",
			i, p.textRows, p.height, p.hasImage, p.stacked, p.rows(), line)
	}
}
