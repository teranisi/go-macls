package main

import (
	"fmt"
)

type rgb = [3]int

// Finder tag color number (1-7) -> ANSI 256-color palette number.
var finderColorCodes = map[int]string{
	1: "244", // Gray
	2: "2",   // Green
	3: "5",   // Purple
	4: "33",  // Blue
	5: "3",   // Yellow
	6: "1",   // Red
	7: "208", // Orange
}

var finderColorRGBVivid = map[int]rgb{
	1: {128, 128, 128},
	2: {0, 128, 0},
	3: {128, 0, 128},
	4: {0, 135, 255},
	5: {128, 128, 0},
	6: {128, 0, 0},
	7: {255, 135, 0},
}

var finderColorRGBPastel = map[int]rgb{
	1: {190, 190, 190},
	2: {152, 251, 152},
	3: {216, 191, 216},
	4: {173, 216, 230},
	5: {255, 255, 153},
	6: {255, 160, 160},
	7: {255, 200, 150},
}

var finderColorRGBByMode = map[string]map[int]rgb{
	"vivid":  finderColorRGBVivid,
	"pastel": finderColorRGBPastel,
}

// --suffix-color=type's per-type color for -F's type indicator, matching
// /bin/ls -G's default LSCOLORS: plain ANSI 8-color SGR codes, unaffected
// by --color/--tag-colors or light/dark background.
var suffixTypeSGR = map[string]string{
	"/": "34", // directory: blue
	"@": "35", // symlink: magenta
	"=": "32", // socket: green
	"|": "33", // pipe (FIFO): yellow
	"*": "31", // executable: red
}

// Indexed as [theme][alt] ("light"/"dark", alt selects the alternate tint
// used for even-indexed columns).
var stripeBgRGB = map[string][2]rgb{
	"light": {{235, 239, 222}, {215, 223, 189}},
	"dark":  {{35, 35, 35}, {20, 20, 20}},
}

var stripeBgCode = map[string][2]string{
	"light": {"254", "187"},
	"dark":  {"236", "238"},
}

const (
	age5Minutes  = 300
	age30Minutes = 1800
	age1Hour     = 3600
	age2Hours    = 7200
	age1Day      = 86400
	age1Week     = 604800
	age1Month    = 2592000
)

type colorStop struct {
	maxAge int // -1 means "no upper bound"
	rgb    rgb
}

var dateColorStopsCyanLightbg = []colorStop{
	{age5Minutes, rgb{0, 255, 255}},
	{age30Minutes, rgb{0, 255, 215}},
	{age1Hour, rgb{0, 215, 215}},
	{age2Hours, rgb{0, 215, 175}},
	{age1Day, rgb{0, 175, 175}},
	{age1Week, rgb{0, 135, 135}},
	{age1Month, rgb{0, 95, 95}},
	{-1, rgb{0, 0, 0}},
}

var dateColorStopsCyanDarkbg = []colorStop{
	{age5Minutes, rgb{0, 255, 255}},
	{age30Minutes, rgb{40, 230, 230}},
	{age1Hour, rgb{80, 210, 210}},
	{age2Hours, rgb{110, 195, 195}},
	{age1Day, rgb{140, 180, 180}},
	{age1Week, rgb{165, 175, 175}},
	{age1Month, rgb{185, 185, 185}},
	{-1, rgb{200, 200, 200}},
}

var dateColorStopsMagentaLightbg = []colorStop{
	{age5Minutes, rgb{255, 0, 255}},
	{age30Minutes, rgb{255, 0, 215}},
	{age1Hour, rgb{215, 0, 215}},
	{age2Hours, rgb{215, 0, 175}},
	{age1Day, rgb{175, 0, 175}},
	{age1Week, rgb{135, 0, 135}},
	{age1Month, rgb{95, 0, 95}},
	{-1, rgb{0, 0, 0}},
}

var dateColorStopsMagentaDarkbg = []colorStop{
	{age5Minutes, rgb{255, 0, 255}},
	{age30Minutes, rgb{230, 40, 230}},
	{age1Hour, rgb{210, 80, 210}},
	{age2Hours, rgb{195, 110, 195}},
	{age1Day, rgb{180, 140, 180}},
	{age1Week, rgb{175, 165, 165}},
	{age1Month, rgb{185, 185, 185}},
	{-1, rgb{200, 200, 200}},
}

var dateColorStops = map[string]map[string][]colorStop{
	"cyan": {
		"light": dateColorStopsCyanLightbg,
		"dark":  dateColorStopsCyanDarkbg,
	},
	"magenta": {
		"light": dateColorStopsMagentaLightbg,
		"dark":  dateColorStopsMagentaDarkbg,
	},
}

var fgFamilyStartRGB = map[string]rgb{
	"cyan":    {0, 255, 255},
	"magenta": {255, 0, 255},
}

// The thresholds DATE_COLOR_STOPS_* itself uses, so buildDateColorStops can
// reuse the exact same shape for a --base-fg-interpolated table.
var dateColorAgeThresholds = []int{
	age5Minutes, age30Minutes, age1Hour, age2Hours,
	age1Day, age1Week, age1Month, -1,
}

// buildDateColorStops linearly interpolates from start to end across
// dateColorAgeThresholds, for --base-fg's override of the color the oldest
// files fade to.
func buildDateColorStops(start, end rgb) []colorStop {
	n := len(dateColorAgeThresholds)
	stops := make([]colorStop, n)
	for i, maxAge := range dateColorAgeThresholds {
		var c rgb
		for k := 0; k < 3; k++ {
			c[k] = int(round(float64(start[k]) + float64(end[k]-start[k])*float64(i)/float64(n-1)))
		}
		stops[i] = colorStop{maxAge, c}
	}
	return stops
}

func round(f float64) float64 {
	if f >= 0 {
		return float64(int(f + 0.5))
	}
	return float64(int(f - 0.5))
}

// Which foreground family ("cyan" or "magenta") to use per background
// (Finder tag number). Numbers not listed use the cyan family.
var fgFamilyForBg = map[int]string{
	1: "cyan",
	2: "magenta",
	3: "cyan",
	4: "magenta",
	5: "cyan",
	6: "cyan",
	7: "cyan",
}

const noBgFgFamily = "magenta"

func finderColorCode(num int) string {
	return finderColorCodes[num]
}

// rgbToAnsi256 converts an arbitrary 24-bit color to the nearest ANSI
// 256-color palette number. (0,0,0) maps to 0 rather than the nearest
// 6x6x6-cube black (16).
func rgbToAnsi256(c rgb) string {
	r, g, b := c[0], c[1], c[2]
	if r == 0 && g == 0 && b == 0 {
		return "0"
	}
	if r == g && g == b {
		if r < 8 {
			return "16"
		}
		if r > 248 {
			return "231"
		}
		return fmt.Sprintf("%d", int(round(float64(r-8)/247*24))+232)
	}
	ri := int(round(float64(r) / 255 * 5))
	gi := int(round(float64(g) / 255 * 5))
	bi := int(round(float64(b) / 255 * 5))
	return fmt.Sprintf("%d", 16+36*ri+6*gi+bi)
}

// dateColorRGB maps recency of modification to a 24-bit color. bgNum is the
// Finder tag number used for the background, or nil for no background.
// baseFg, if non-nil, overrides the color the oldest files fade to.
func dateColorRGB(mtime *int64, now int64, bgNum *int, theme string, baseFg *rgb) *rgb {
	if mtime == nil {
		return nil
	}
	family := noBgFgFamily
	if bgNum != nil {
		if f, ok := fgFamilyForBg[*bgNum]; ok {
			family = f
		} else {
			family = "cyan"
		}
	}
	var stops []colorStop
	if baseFg != nil {
		stops = buildDateColorStops(fgFamilyStartRGB[family], *baseFg)
	} else {
		stops = dateColorStops[family][theme]
	}
	age := now - *mtime
	if age < 0 {
		age = 0
	}
	for _, s := range stops {
		if s.maxAge == -1 || age <= int64(s.maxAge) {
			c := s.rgb
			return &c
		}
	}
	c := stops[len(stops)-1].rgb
	return &c
}

func fgSGR(c rgb, useTruecolor bool) string {
	if useTruecolor {
		return fmt.Sprintf("38;2;%d;%d;%d", c[0], c[1], c[2])
	}
	return fmt.Sprintf("38;5;%s", rgbToAnsi256(c))
}

// finderSGR returns the SGR parameter string for Finder tag color num.
// ground is "38" (foreground, for extra-tag dots) or "48" (background).
func finderSGR(num int, ground string, useTruecolor bool, tagColors string) string {
	if useTruecolor {
		table, ok := finderColorRGBByMode[tagColors]
		if !ok {
			table = finderColorRGBVivid
		}
		c, ok := table[num]
		if !ok {
			c = rgb{0, 0, 0}
		}
		return fmt.Sprintf("%s;2;%d;%d;%d", ground, c[0], c[1], c[2])
	}
	code := finderColorCode(num)
	return fmt.Sprintf("%s;5;%s", ground, code)
}

// stripeSGR returns the background SGR parameter string for --stripe's
// tint. alt selects which of the two color variants to use.
func stripeSGR(useTruecolor bool, theme string, alt bool) string {
	idx := 0
	if alt {
		idx = 1
	}
	if useTruecolor {
		c := stripeBgRGB[theme][idx]
		return fmt.Sprintf("48;2;%d;%d;%d", c[0], c[1], c[2])
	}
	return fmt.Sprintf("48;5;%s", stripeBgCode[theme][idx])
}
