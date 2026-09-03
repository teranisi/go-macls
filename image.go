package main

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".tiff": true, ".tif": true, ".webp": true, ".heic": true, ".heif": true,
	".pdf": true,
}

// Base thumbnail size (--scale=1): a height of 1 cell, matching iTerm2's
// imgls defaults.
const (
	itermImgWidth   = 2
	itermImgHeight  = 1
	cellAspectRatio = 2.0
)

func pngPixelSize(data []byte) (int, int, bool) {
	if len(data) < 24 || string(data[:8]) != "\x89PNG\r\n\x1a\n" || string(data[12:16]) != "IHDR" {
		return 0, 0, false
	}
	w := binary.BigEndian.Uint32(data[16:20])
	h := binary.BigEndian.Uint32(data[20:24])
	return int(w), int(h), true
}

func gifPixelSize(data []byte) (int, int, bool) {
	if len(data) < 10 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0, 0, false
	}
	w := binary.LittleEndian.Uint16(data[6:8])
	h := binary.LittleEndian.Uint16(data[8:10])
	return int(w), int(h), true
}

func bmpPixelSize(data []byte) (int, int, bool) {
	if len(data) < 26 || string(data[:2]) != "BM" {
		return 0, 0, false
	}
	w := int32(binary.LittleEndian.Uint32(data[18:22]))
	h := int32(binary.LittleEndian.Uint32(data[22:26]))
	if w < 0 {
		w = -w
	}
	if h < 0 {
		h = -h
	}
	return int(w), int(h), true
}

// jpegSOFMarkers are the Start Of Frame marker codes; 0xC4/0xC8/0xCC are
// excluded (DHT/JPG/DAC, not SOF) despite falling in the same range.
func isJpegSOFMarker(m byte) bool {
	if m < 0xC0 || m > 0xCF {
		return false
	}
	return m != 0xC4 && m != 0xC8 && m != 0xCC
}

func isJpegMarkerWithoutSegment(m byte) bool {
	return m == 0xD8 || m == 0xD9 || (m >= 0xD0 && m <= 0xD7)
}

func jpegPixelSize(data []byte) (int, int, bool) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0, false
	}
	i := 2
	n := len(data)
	for i < n-1 {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if isJpegMarkerWithoutSegment(marker) {
			i += 2
			continue
		}
		if i+4 > n {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if isJpegSOFMarker(marker) {
			if i+9 > n {
				break
			}
			h := binary.BigEndian.Uint16(data[i+5 : i+7])
			w := binary.BigEndian.Uint16(data[i+7 : i+9])
			return int(w), int(h), true
		}
		i += 2 + segLen
	}
	return 0, 0, false
}

// getImagePixelSize returns (width, height) in pixels for image file
// contents data with extension ext, or ok=false if ext isn't one of the
// supported formats or data couldn't be parsed as one.
func getImagePixelSize(data []byte, ext string) (w, h int, ok bool) {
	defer func() {
		if recover() != nil {
			w, h, ok = 0, 0, false
		}
	}()
	switch ext {
	case ".png":
		return pngPixelSize(data)
	case ".gif":
		return gifPixelSize(data)
	case ".bmp":
		return bmpPixelSize(data)
	case ".jpg", ".jpeg":
		return jpegPixelSize(data)
	}
	return 0, 0, false
}

// buildImagePrefix returns the escape sequence that renders the image at
// path as a thumbnail of `width` cells wide using iTerm2's inline image
// protocol (OSC 1337). Returns "" if the file isn't an image or can't be
// read.
func buildImagePrefix(path string, width, height int) string {
	ext := strings.ToLower(filepath.Ext(path))
	if !imageExtensions[ext] {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}

	if pxW, pxH, ok := getImagePixelSize(data, ext); ok && pxW > 0 && pxH > 0 {
		h := int(round(float64(width) * (float64(pxH) / float64(pxW)) / cellAspectRatio))
		if h < 1 {
			h = 1
		}
		height = h
	}

	if termH := getTerminalHeight(); height > termH {
		height = termH
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	nameB64 := base64.StdEncoding.EncodeToString([]byte(filepath.Base(path)))
	params := strings.Join([]string{
		"inline=1",
		"width=" + strconv.Itoa(width),
		"height=" + strconv.Itoa(height),
		"preserveAspectRatio=1",
		"size=" + strconv.Itoa(len(data)),
		"name=" + nameB64,
	}, ";")
	return "\033]1337;File=" + params + ":" + encoded + "\a"
}

// buildImagePrefixes builds a thumbnail prefix/suffix pair for each entry.
// See _build_image_prefixes() in the Python original for the layout
// rationale. Returns (prefixes, suffixes, colWidth).
func buildImagePrefixes(fullPaths []string, optI bool, width, height int, stackedFlags []bool, singleLine bool) ([]string, []string, int) {
	if !optI {
		prefixes := make([]string, len(fullPaths))
		suffixes := make([]string, len(fullPaths))
		return prefixes, suffixes, 0
	}
	imgColWidth := width + 1
	imgColPad := strings.Repeat(" ", imgColWidth)
	prefixes := make([]string, len(fullPaths))
	suffixes := make([]string, len(fullPaths))
	for i, p := range fullPaths {
		img := ""
		if isFile(p) {
			img = buildImagePrefix(p, width, height)
		}
		if !singleLine {
			if img != "" {
				prefixes[i] = img + " "
			} else {
				prefixes[i] = imgColPad
			}
			suffixes[i] = ""
			continue
		}
		prefixes[i] = imgColPad
		if img == "" {
			suffixes[i] = ""
		} else if stackedFlags != nil && stackedFlags[i] {
			suffixes[i] = "\n" + img
		} else {
			suffixes[i] = "\r" + img
		}
	}
	return prefixes, suffixes, imgColWidth
}
