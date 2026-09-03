package main

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
	"howett.net/plist"
)

const finderTagAttr = "com.apple.metadata:_kMDItemUserTags"

type finderTag struct {
	name string
	num  int
}

// getFinderTags returns the Finder tags for path as (name, colorNum) pairs,
// in assignment order. colorNum is 0 for a tag with no color assigned.
// Returns nil if there are none or retrieval fails.
func getFinderTags(path string) []finderTag {
	size, err := unix.Getxattr(path, finderTagAttr, nil)
	if err != nil || size <= 0 {
		return nil
	}
	buf := make([]byte, size)
	n, err := unix.Getxattr(path, finderTagAttr, buf)
	if err != nil || n < 0 {
		return nil
	}
	var tags []string
	if _, err := plist.Unmarshal(buf[:n], &tags); err != nil {
		return nil
	}
	result := make([]finderTag, 0, len(tags))
	for _, tag := range tags {
		name, suffix := tag, ""
		if idx := strings.LastIndex(tag, "\n"); idx >= 0 {
			name, suffix = tag[:idx], tag[idx+1:]
		}
		num := 0
		if allDigits(suffix) {
			if v, err := strconv.Atoi(suffix); err == nil {
				num = v
			}
		}
		result = append(result, finderTag{name, num})
	}
	return result
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// getFinderTagNums returns the Finder tag color numbers for path, in
// assignment order (tags without a color are skipped).
func getFinderTagNums(path string) []int {
	tags := getFinderTags(path)
	nums := make([]int, 0, len(tags))
	for _, t := range tags {
		if t.num != 0 {
			nums = append(nums, t.num)
		}
	}
	return nums
}

// getDisplayTagInfo returns (tagnums, bgNum, dotTagnums, allTags) for path.
// tagMode is opts.tag ("bg", "dot", "str", or "off").
func getDisplayTagInfo(path string, tagMode string) (tagnums []int, bgNum *int, dotTagnums []int, allTags []finderTag) {
	if isFile(path) || isDir(path) || isSymlink(path) {
		allTags = getFinderTags(path)
	}
	for _, t := range allTags {
		if t.num != 0 {
			tagnums = append(tagnums, t.num)
		}
	}
	if len(tagnums) == 0 || tagMode == "str" || tagMode == "off" {
		return tagnums, nil, nil, allTags
	}
	if tagMode == "dot" {
		return tagnums, nil, tagnums, allTags
	}
	last := tagnums[len(tagnums)-1]
	return tagnums, &last, tagnums[:len(tagnums)-1], allTags
}

// dotExtraWidth returns the display width added by build_colored_name's
// tag-dot rendering.
func dotExtraWidth(dotTagnums []int) int {
	if len(dotTagnums) == 0 {
		return 0
	}
	return len(dotTagnums) + 1
}

// buildTagLabel builds the --tag display for allTags: a leading space
// followed by every Finder tag name, comma-separated inside brackets.
// bgPart, if given (a "48;..." SGR parameter string), is painted behind the
// whole label. Returns (label, extraWidth).
func buildTagLabel(allTags []finderTag, useColor, useTruecolor bool, tagColors string, bgPart string) (string, int) {
	if len(allTags) == 0 {
		return "", 0
	}
	names := make([]string, len(allTags))
	for i, t := range allTags {
		names[i] = sanitizeDisplayName(t.name)
	}
	plainLabel := " [" + strings.Join(names, ", ") + "]"
	if !useColor {
		return plainLabel, displayWidth(plainLabel)
	}

	lit := func(s string) string {
		if bgPart != "" {
			return "\033[" + bgPart + "m" + s + "\033[0m"
		}
		return s
	}

	coloredNames := make([]string, len(allTags))
	for i, t := range allTags {
		disp := names[i]
		if t.num != 0 {
			sgr := finderSGR(t.num, "38", useTruecolor, tagColors)
			spanSGR := sgr
			if bgPart != "" {
				spanSGR = sgr + ";" + bgPart
			}
			coloredNames[i] = "\033[" + spanSGR + "m" + disp + "\033[0m"
		} else if bgPart != "" {
			coloredNames[i] = "\033[" + bgPart + "m" + disp + "\033[0m"
		} else {
			coloredNames[i] = disp
		}
	}
	coloredLabel := lit(" [") + strings.Join(coloredNames, lit(", ")) + lit("]")
	return coloredLabel, displayWidth(plainLabel)
}
