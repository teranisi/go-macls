package main

import "sort"

// Grid cell sentinels for compact mode: a non-negative value is an index
// into final/plainlen; noneCell is an empty slot with nothing in it at all
// (e.g. the grid's last column short of entries); blockedCell is a slot
// claimed by an earlier column's spanning entry.
const (
	noneCell    = -1
	blockedCell = -2
)

type columnLayout struct {
	mode     string // "empty", "single", "classic", "compact"
	colOfIdx []int

	// classic
	rows, cols, colwidth int

	// compact
	grid             [][]int
	occupied         []int
	baseColwidth     int
	effectiveLastCol int
}

// ceilDiv computes ceil(a/b) for non-negative a and positive b. Go's
// integer division truncates toward zero (unlike Python's floor "//"), so
// this can't reuse Python's -(-a//b) trick directly.
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func maxInts(xs []int) int {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

// computeMultiColumnLayout computes the down-then-across grid layout from
// entry widths alone. See the Python original's docstring for the "classic"
// vs. "compact" (default) mode rationale.
func computeMultiColumnLayout(namelen []int, optF bool, optColumns string, width int, stripe bool) columnLayout {
	n := len(namelen)
	if n == 0 {
		return columnLayout{mode: "empty"}
	}
	if n == 1 {
		return columnLayout{mode: "single", colOfIdx: []int{0}}
	}

	computeClassic := func(rowsWidth int) (rows, cols, colwidth int) {
		colwidth = maxInts(namelen) + 1
		if optF {
			colwidth++
		}
		rows, cols = n, 1
		for candidateRows := 1; candidateRows <= n; candidateRows++ {
			candidateCols := ceilDiv(n, candidateRows)
			if candidateCols*colwidth <= rowsWidth {
				rows, cols = candidateRows, candidateCols
				break
			}
		}
		return rows, cols, colwidth
	}

	classicLayout := func() columnLayout {
		rows, cols, colwidth := computeClassic(width)
		colOfIdx := make([]int, n)
		for idx := 0; idx < n; idx++ {
			colOfIdx[idx] = idx / rows
		}
		return columnLayout{mode: "classic", rows: rows, cols: cols, colwidth: colwidth, colOfIdx: colOfIdx}
	}

	if optColumns == "classic" {
		return classicLayout()
	}

	extraF := 0
	if optF {
		extraF = 1
	}
	cellwidth := make([]int, n)
	for i := range namelen {
		cellwidth[i] = namelen[i] + 1 + extraF
	}

	sortedCw := append([]int{}, cellwidth...)
	sort.Ints(sortedCw)
	medianCw := sortedCw[len(sortedCw)/2]
	longThreshold := medianCw * 2
	var normalCw []int
	for _, w := range cellwidth {
		if w <= longThreshold {
			normalCw = append(normalCw, w)
		}
	}
	var baseColwidth int
	if len(normalCw) > 0 {
		baseColwidth = maxInts(normalCw)
	} else {
		baseColwidth = maxInts(cellwidth)
	}

	span := make([]int, n)
	occupied := make([]int, n)
	for i := 0; i < n; i++ {
		span[i] = ceilDiv(cellwidth[i], baseColwidth)
		occupied[i] = span[i] * baseColwidth
	}

	buildGrid := func(rows int) [][]int {
		var columns [][]int
		blockedNext := map[int]map[int]bool{}
		idx := 0
		c := 0
		for idx < n || len(blockedNext) > 0 {
			col := make([]int, rows)
			for i := range col {
				col[i] = noneCell
			}
			blockedHere := blockedNext[c]
			delete(blockedNext, c)
			for br := range blockedHere {
				col[br] = blockedCell
			}
			r := 0
			for r < rows && idx < n {
				if blockedHere[r] {
					r++
					continue
				}
				col[r] = idx
				s := span[idx]
				for k := 1; k < s; k++ {
					if blockedNext[c+k] == nil {
						blockedNext[c+k] = map[int]bool{}
					}
					blockedNext[c+k][r] = true
				}
				idx++
				r++
			}
			columns = append(columns, col)
			c++
		}
		return columns
	}

	stripeActive := stripe

	effectiveLastColumn := func(grid [][]int) int {
		for c := len(grid) - 1; c >= 0; c-- {
			for _, v := range grid[c] {
				if v >= 0 {
					return c
				}
			}
		}
		return 0
	}

	colHasContent := func(col []int) bool {
		for _, v := range col {
			if v >= 0 {
				return true
			}
		}
		return false
	}

	gridFits := func(grid [][]int, rows int) bool {
		effectiveLast := effectiveLastColumn(grid)
		for r := 0; r < rows; r++ {
			total := 0
			for c, col := range grid {
				v := col[r]
				var w int
				if v >= 0 {
					isLast := c+span[v]-1 >= effectiveLast
					if isLast && !stripeActive {
						w = cellwidth[v]
					} else {
						w = occupied[v]
					}
				} else if v == noneCell && stripeActive && colHasContent(col) {
					w = baseColwidth
				} else {
					continue
				}
				if w <= width {
					total += w
				}
			}
			if total > width {
				return false
			}
		}
		return true
	}

	lastRealColumnCount := func(grid [][]int) int {
		for c := len(grid) - 1; c >= 0; c-- {
			count := 0
			for _, v := range grid[c] {
				if v >= 0 {
					count++
				}
			}
			if count > 0 {
				return count
			}
		}
		return 0
	}

	rows := n
	grid := buildGrid(n)
	fallbackRows := -1
	var fallbackGrid [][]int
	found := false
	for candidateRows := 1; candidateRows < n; candidateRows++ {
		candidateGrid := buildGrid(candidateRows)
		if !gridFits(candidateGrid, candidateRows) {
			continue
		}
		if fallbackRows == -1 {
			fallbackRows, fallbackGrid = candidateRows, candidateGrid
		}
		threshold := candidateRows / 2
		if threshold < 1 {
			threshold = 1
		}
		if lastRealColumnCount(candidateGrid) >= threshold {
			rows, grid = candidateRows, candidateGrid
			found = true
			break
		}
	}
	if !found && fallbackRows != -1 {
		rows, grid = fallbackRows, fallbackGrid
	}

	compactCols := 0
	for _, col := range grid {
		if colHasContent(col) {
			compactCols++
		}
	}
	_, classicCols, _ := computeClassic(width)
	if compactCols <= classicCols {
		return classicLayout()
	}

	colOfIdx := make([]int, n)
	for c, col := range grid {
		for _, v := range col {
			if v >= 0 {
				colOfIdx[v] = c
			}
		}
	}

	return columnLayout{
		mode:             "compact",
		rows:             rows,
		grid:             grid,
		occupied:         occupied,
		baseColwidth:     baseColwidth,
		colOfIdx:         colOfIdx,
		effectiveLastCol: effectiveLastColumn(grid),
	}
}

// renderMultiColumnLayout renders final (colored entry strings) into lines,
// arranged per layout. hangWidth is the width of the unquoted-name
// hanging-indent space (see --quote) when at least one name in the listing
// needed quoting, 0 otherwise.
func renderMultiColumnLayout(layout columnLayout, final []string, plainlen []int, stripe bool, theme string, useTruecolor bool, hangWidth int) []string {
	mode := layout.mode
	if mode == "empty" {
		return nil
	}
	if mode == "single" {
		return []string{final[0]}
	}

	stripeActive := stripe

	padding := func(width, c int, rowEnd bool, hang int) string {
		if width <= 0 {
			return ""
		}
		if stripeActive {
			lead := ""
			if hang > 0 && hang < width {
				lead = repeatSpace(hang)
				width -= hang
			}
			stripePadSGR := stripeSGR(useTruecolor, theme, c%2 == 0)
			if rowEnd {
				return lead + "\033[" + stripePadSGR + "m" + repeatSpace(width) + "\033[0m"
			}
			coloredWidth := width - 1
			if coloredWidth <= 0 {
				return lead + repeatSpace(width)
			}
			return lead + "\033[" + stripePadSGR + "m" + repeatSpace(coloredWidth) + "\033[0m "
		}
		return repeatSpace(width)
	}

	if mode == "classic" {
		n := len(final)
		rows, cols, colwidth := layout.rows, layout.cols, layout.colwidth
		var lines []string
		for r := 0; r < rows; r++ {
			type rowItem struct {
				c   int
				idx int // -1 for none
			}
			var rowItems []rowItem
			for c := 0; c < cols; c++ {
				idx := c*rows + r
				if idx < n {
					rowItems = append(rowItems, rowItem{c, idx})
				} else if stripeActive {
					rowItems = append(rowItems, rowItem{c, noneCell})
				}
			}
			var b []byte
			for i, ri := range rowItems {
				rowIsLast := i == len(rowItems)-1
				colIsLast := ri.c == cols-1
				if ri.idx == noneCell {
					b = append(b, padding(colwidth, ri.c, colIsLast, hangWidth)...)
					continue
				}
				item := final[ri.idx]
				length := plainlen[ri.idx]
				b = append(b, item...)
				if rowIsLast && !stripeActive {
					// no padding
				} else if stripeActive {
					b = append(b, padding(colwidth-length, ri.c, colIsLast, 0)...)
				} else {
					b = append(b, padding(colwidth-length, ri.c, false, 0)...)
				}
			}
			lines = append(lines, string(b))
		}
		return lines
	}

	rows, grid, occupied, baseColwidth := layout.rows, layout.grid, layout.occupied, layout.baseColwidth
	effectiveLastCol := layout.effectiveLastCol
	colHasContent := make([]bool, len(grid))
	for c, col := range grid {
		for _, v := range col {
			if v >= 0 {
				colHasContent[c] = true
				break
			}
		}
	}

	var lines []string
	for r := 0; r < rows; r++ {
		type rowItem struct {
			c   int
			idx int // -1 for none
		}
		var rowItems []rowItem
		for c, col := range grid {
			v := col[r]
			if v >= 0 || (v == noneCell && stripeActive && colHasContent[c]) {
				rowItems = append(rowItems, rowItem{c, v})
			}
		}
		var b []byte
		for i, ri := range rowItems {
			rowIsLast := i == len(rowItems)-1
			spanHere := 1
			if ri.idx >= 0 {
				spanHere = occupied[ri.idx] / baseColwidth
			}
			colIsLast := ri.c+spanHere-1 >= effectiveLastCol
			if ri.idx == noneCell {
				b = append(b, padding(baseColwidth, ri.c, colIsLast, hangWidth)...)
				continue
			}
			item := final[ri.idx]
			length := plainlen[ri.idx]
			b = append(b, item...)
			if rowIsLast && !stripeActive {
				// no padding
			} else if stripeActive {
				b = append(b, padding(occupied[ri.idx]-length, ri.c, colIsLast, 0)...)
			} else {
				b = append(b, padding(occupied[ri.idx]-length, ri.c, false, 0)...)
			}
		}
		lines = append(lines, string(b))
	}
	return lines
}

// computeImageCellOffsets returns, for each entry, which rendered line
// (0-indexed, matching renderMultiColumnLayout()'s returned lines) it
// falls on and the cell offset within that line where its own block --
// and so its img_prefix, always the very first thing in that block --
// begins. Used to progressively render -I thumbnails in multi-column
// output: knowing exactly where each entry's reserved image slot sits
// lets a deferred draw jump the cursor there precisely, without needing
// the rendered text itself.
//
// Mirrors renderMultiColumnLayout()'s own row-walking exactly (including
// the classic/compact split), since an offset is only meaningful if it
// agrees with what was actually printed.
func computeImageCellOffsets(layout columnLayout, stripe bool) (rowOfIdx, colOffsetOfIdx []int) {
	switch layout.mode {
	case "empty":
		return nil, nil
	case "single":
		return []int{0}, []int{0}
	case "classic":
		n := len(layout.colOfIdx)
		rowOfIdx = make([]int, n)
		colOffsetOfIdx = make([]int, n)
		for idx := 0; idx < n; idx++ {
			rowOfIdx[idx] = idx % layout.rows
			colOffsetOfIdx[idx] = layout.colOfIdx[idx] * layout.colwidth
		}
		return rowOfIdx, colOffsetOfIdx
	}

	// compact
	stripeActive := stripe
	rows, grid, occupied, baseColwidth := layout.rows, layout.grid, layout.occupied, layout.baseColwidth
	colHasContent := make([]bool, len(grid))
	for c, col := range grid {
		for _, v := range col {
			if v >= 0 {
				colHasContent[c] = true
				break
			}
		}
	}
	n := len(layout.colOfIdx)
	rowOfIdx = make([]int, n)
	colOffsetOfIdx = make([]int, n)
	for r := 0; r < rows; r++ {
		offset := 0
		for c, col := range grid {
			v := col[r]
			if v >= 0 {
				rowOfIdx[v] = r
				colOffsetOfIdx[v] = offset
				offset += occupied[v]
			} else if v == noneCell && stripeActive && colHasContent[c] {
				offset += baseColwidth
			}
		}
	}
	return rowOfIdx, colOffsetOfIdx
}

func repeatSpace(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
