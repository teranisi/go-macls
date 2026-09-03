package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// aspectScaledHeight computes a thumbnail's height (in cells) from an
// image's real pixel size, for a given cell width, via CELL_ASPECT_RATIO.
// Always at least 1.
func aspectScaledHeight(width, pxW, pxH int) int {
	h := int(round(float64(width) * (float64(pxH) / float64(pxW)) / cellAspectRatio))
	if h < 1 {
		h = 1
	}
	return h
}

// imagePeekBytes bounds how much of an image file peekImagePixelSize()
// reads to determine its pixel dimensions -- generous enough to reach the
// PNG/GIF/BMP header (always near the very start of the file) or a JPEG's
// SOF marker (usually within the first few embedded metadata segments),
// while staying far cheaper than reading and base64-encoding the entire
// file (which buildImagePrefix() still does once the image is actually
// drawn).
const imagePeekBytes = 256 * 1024

// peekImagePixelSize is like getImagePixelSize(), but reads only a bounded
// prefix of path's contents rather than the whole file -- for callers (see
// planProgressiveImages()) that need an image's pixel dimensions cheaply,
// well before they're ready to pay for reading and encoding the whole
// file. A JPEG whose SOF marker lies beyond imagePeekBytes returns
// ok=false, same as an unparseable format -- the caller falls back to a
// flat, non-aspect-scaled height for that one image.
func peekImagePixelSize(path, ext string) (w, h int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	buf := make([]byte, imagePeekBytes)
	n, _ := io.ReadFull(f, buf)
	if n <= 0 {
		return 0, 0, false
	}
	return getImagePixelSize(buf[:n], ext)
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

// thumbnailPxPerCell is the assumed pixel density (generous, for a sharp
// result even on a hidpi display) per terminal cell used to size
// downscaleForThumbnail()'s target -- see buildImagePrefix().
const thumbnailPxPerCell = 64

// downscaleForThumbnail decodes image file contents data (PNG/JPEG/GIF --
// the formats Go's standard library can decode without an extra
// dependency) and, if its pixel dimensions exceed maxDim on the longer
// side, resizes it down (a simple box-average downsample -- more than
// adequate at the tiny size a terminal thumbnail actually renders at) and
// re-encodes it as PNG. Returns ok=false, meaning the caller should keep
// using the original data unchanged, if ext isn't decodable, the data
// doesn't parse, the image is already small enough that resizing wouldn't
// shrink it, or re-encoding somehow didn't come out smaller.
//
// This exists because embedding the entire original file, however large,
// sends it -- base64-encoded, inline in an OSC 1337 escape sequence --
// over the terminal's own I/O channel even though the displayed thumbnail
// is only a few dozen pixels across at most: a multi-megapixel photo can
// be megabytes of data sent (and, over a slow pty/SSH link, waited on)
// just to end up shrunk to a few cells wide.
func downscaleForThumbnail(data []byte, ext string, maxDim int) ([]byte, bool) {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif":
	default:
		return nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || (w <= maxDim && h <= maxDim) {
		return nil, false
	}

	scale := float64(maxDim) / float64(w)
	if s := float64(maxDim) / float64(h); s < scale {
		scale = s
	}
	newW := maxInt(1, int(float64(w)*scale))
	newH := maxInt(1, int(float64(h)*scale))

	var buf bytes.Buffer
	if err := png.Encode(&buf, boxDownsample(img, newW, newH)); err != nil {
		return nil, false
	}
	if buf.Len() >= len(data) {
		return nil, false
	}
	return buf.Bytes(), true
}

// boxDownsample resizes src to exactly newW x newH by averaging each
// destination pixel's corresponding source box.
func boxDownsample(src image.Image, newW, newH int) *image.RGBA {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		sy0 := bounds.Min.Y + y*srcH/newH
		sy1 := bounds.Min.Y + (y+1)*srcH/newH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < newW; x++ {
			sx0 := bounds.Min.X + x*srcW/newW
			sx1 := bounds.Min.X + (x+1)*srcW/newW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, b, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					pr, pg, pb, pa := src.At(sx, sy).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					b += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			// src.At().RGBA() and color.RGBA are both alpha-premultiplied,
			// so averaging in that space and truncating to 8 bits needs no
			// unpremultiply/re-premultiply step.
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / n >> 8),
				G: uint8(g / n >> 8),
				B: uint8(b / n >> 8),
				A: uint8(a / n >> 8),
			})
		}
	}
	return dst
}

// buildImagePrefix returns the escape sequence that renders the image at
// path as a thumbnail of `width` cells wide using iTerm2's inline image
// protocol (OSC 1337). Returns "" if the file isn't an image or can't be
// read. termHeight is the terminal's height (see getTerminalHeight()),
// passed in rather than queried here so a caller building many prefixes at
// once (see buildImagePrefixes()) only pays for that syscall once.
//
// allowAspectHeight lets a portrait image grow past `height` rows to match
// its own aspect ratio (see the single_line thumbnails in
// buildImagePrefixes()). It must be false in multi-column output: several
// entries there share one physical terminal line, drawn as one continuous
// stream with no cursor tricks between them, so a thumbnail taller than 1
// row would drag the cursor down mid-line and misplace everything printed
// after it in that row -- including, for the entry the tall thumbnail
// belongs to, its own name.
func buildImagePrefix(path string, width, height, termHeight int, allowAspectHeight bool) string {
	ext := strings.ToLower(filepath.Ext(path))
	if !imageExtensions[ext] {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}

	if allowAspectHeight {
		if pxW, pxH, ok := getImagePixelSize(data, ext); ok && pxW > 0 && pxH > 0 {
			height = aspectScaledHeight(width, pxW, pxH)
		}
	}

	if height > termHeight {
		height = termHeight
	}

	maxDim := maxInt(width, height) * thumbnailPxPerCell
	if small, ok := downscaleForThumbnail(data, ext, maxDim); ok {
		data = small
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

// imagePrefixConcurrency bounds how many thumbnails are read/encoded at
// once (see buildImagePrefixes()): each one is an independent file read
// plus base64 encode, dominated by I/O wait, so running them concurrently
// cuts -I's wall-clock time roughly in proportion to this bound instead of
// to the number of images in the listing. Bounded (rather than one
// goroutine per entry) to avoid exhausting file descriptors/memory on a
// directory with thousands of images.
const imagePrefixConcurrency = 16

// buildImagePrefixes builds a thumbnail prefix/suffix pair for each entry.
// See _build_image_prefixes() in the Python original for the layout
// rationale. Returns (prefixes, suffixes, colWidth). The per-entry
// buildImagePrefix() work (reading and base64-encoding each image file) is
// independent, so it runs concurrently across entries.
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

	termHeight := getTerminalHeight()
	imgs := make([]string, len(fullPaths))
	sem := make(chan struct{}, imagePrefixConcurrency)
	var wg sync.WaitGroup
	for i, p := range fullPaths {
		if !isFile(p) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p string) {
			defer wg.Done()
			defer func() { <-sem }()
			imgs[i] = buildImagePrefix(p, width, height, termHeight, singleLine)
		}(i, p)
	}
	wg.Wait()

	for i, img := range imgs {
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
