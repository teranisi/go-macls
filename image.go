package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".tiff": true, ".tif": true, ".webp": true, ".heic": true, ".heif": true,
	".pdf": true,
}

// defaultQLExtensions are the extensions -I treats as Quick Look thumbnail
// candidates (see qlExtensions, --ql-ext) beyond imageExtensions' own image
// files, unless --ql-ext overrides them. Word/Excel/PowerPoint's binary and
// OOXML formats: real Quick Look generators for these ship with macOS
// itself (Preview.app etc.), no Office installation needed. Quick Look
// itself isn't limited to Office documents, so any other extension with a
// real (non-generic-icon) Quick Look generator is a candidate to add here
// later.
var defaultQLExtensions = map[string]bool{
	".docx": true, ".xlsx": true, ".pptx": true,
	".doc": true, ".xls": true, ".ppt": true,
}

// qlExtensions selects, beyond imageExtensions, which extensions
// buildImagePrefix() tries a qlmanage(1) Quick Look preview for -- see
// --ql-ext/resolveQLExtensions(). all is the --ql-ext=all sentinel ("any
// extension not already in imageExtensions" is a candidate); otherwise
// membership in exts decides (exts is empty, matching no extension, for
// --ql-ext=off).
type qlExtensions struct {
	all  bool
	exts map[string]bool
}

func (q qlExtensions) isCandidate(ext string) bool {
	if q.all {
		return true
	}
	return q.exts[ext]
}

// resolveQLExtensions turns opts.qlExtMode/opts.qlExtExtra (see --ql-ext)
// into the single qlExtensions value buildImagePrefix() and friends
// actually take.
func resolveQLExtensions(opts *Options) qlExtensions {
	switch opts.qlExtMode {
	case "off":
		return qlExtensions{}
	case "all":
		return qlExtensions{all: true}
	case "list":
		exts := make(map[string]bool, len(defaultQLExtensions)+len(opts.qlExtExtra))
		for e := range defaultQLExtensions {
			exts[e] = true
		}
		for _, e := range opts.qlExtExtra {
			exts[e] = true
		}
		return qlExtensions{exts: exts}
	default:
		return qlExtensions{exts: defaultQLExtensions}
	}
}

// isQLCandidate reports whether path (with lowercased extension ext) is a
// Quick Look thumbnail candidate under ql -- i.e. build_image_prefix()'s
// is_ql in the Python original. A "~$name.ext"-style lock file Word/Excel/
// PowerPoint leaves next to a document while it's open elsewhere isn't
// actually a valid document (just a small owner-info stub with the same
// extension) -- qlmanage has been observed to hang well past its own
// timeout trying to preview one, so these are excluded entirely rather than
// left to fail slowly. A 0-byte file is excluded the same way: there's no
// content for Quick Look to render, and an empty file has also been
// observed to make qlmanage hang rather than fail fast. An extension-less
// file is always excluded too -- ql.all (--ql-ext=all) would otherwise
// treat having no extension as "not in imageExtensions" and so a candidate.
func isQLCandidate(path, ext string, ql qlExtensions) bool {
	if ext == "" || imageExtensions[ext] {
		return false
	}
	if strings.HasPrefix(filepath.Base(path), "~$") {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 {
		return false
	}
	return ql.isCandidate(ext)
}

// hasThumbnailCandidate reports whether path is an -I thumbnail candidate
// at all -- either a real image (imageExtensions) or a Quick Look candidate
// under ql -- without doing the actual (much more expensive) thumbnail
// work. Used by the progressive-rendering paths (see progressive.go) to
// decide, cheaply, which entries need a reserved thumbnail slot at all.
func hasThumbnailCandidate(path string, ql qlExtensions) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExtensions[ext] || isQLCandidate(path, ext, ql)
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
// boxDownsampleMaxSamplesPerAxis bounds how many source pixels
// boxDownsample() samples per axis for each destination pixel's box,
// regardless of how large that box actually is. Averaging every source
// pixel is O(source width * source height) in total -- for a multi-
// megapixel photo shrunk to a thumbnail a few dozen pixels across, the
// vast majority of that work goes into boxes with thousands of source
// pixels each, and image.Image.At() (a per-pixel interface call --
// color-converting from YCbCr for a decoded JPEG, in particular) isn't
// cheap to call millions of times. Sampling a bounded, evenly-spaced grid
// per box instead makes the total cost O(destination width * destination
// height), independent of the source image's resolution, at a barely
// perceptible quality cost for something displayed this small (iTerm2
// also does its own final scaling/antialiasing when it draws the
// thumbnail).
const boxDownsampleMaxSamplesPerAxis = 4

// sampleCoords returns up to maxSamples evenly spaced integer coordinates
// covering [lo, hi), or every coordinate in that range if it's already no
// more than maxSamples wide.
func sampleCoords(lo, hi, maxSamples int) []int {
	n := hi - lo
	if n <= maxSamples {
		coords := make([]int, n)
		for i := range coords {
			coords[i] = lo + i
		}
		return coords
	}
	coords := make([]int, maxSamples)
	for i := 0; i < maxSamples; i++ {
		coords[i] = lo + i*n/maxSamples
	}
	return coords
}

func boxDownsample(src image.Image, newW, newH int) *image.RGBA {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))

	xSamples := make([][]int, newW)
	for x := 0; x < newW; x++ {
		sx0 := bounds.Min.X + x*srcW/newW
		sx1 := bounds.Min.X + (x+1)*srcW/newW
		if sx1 <= sx0 {
			sx1 = sx0 + 1
		}
		xSamples[x] = sampleCoords(sx0, sx1, boxDownsampleMaxSamplesPerAxis)
	}

	for y := 0; y < newH; y++ {
		sy0 := bounds.Min.Y + y*srcH/newH
		sy1 := bounds.Min.Y + (y+1)*srcH/newH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		ySamples := sampleCoords(sy0, sy1, boxDownsampleMaxSamplesPerAxis)
		for x := 0; x < newW; x++ {
			var r, g, b, a, n uint64
			for _, sy := range ySamples {
				for _, sx := range xSamples[x] {
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

// preAspectMaxDimSafetyFactor inflates convertHeicForThumbnail()'s and
// qlmanageThumbnail()'s resize target (see their own doc comments) beyond a
// flat width*thumbnailPxPerCell, to still cover a strongly portrait-
// oriented source (a phone photo, an A4/letter document page) adequately:
// the target is picked before the source's own aspect ratio is known
// (avoiding a separate metadata-only sips/qlmanage call just to learn it up
// front), so this errs generous rather than risk shortchanging the
// thumbnail's longer dimension. 3x covers up to a 3:1 aspect ratio at full
// requested resolution -- beyond typical even for a portrait phone photo or
// document page -- while still being a tiny fraction of a real source's own
// resolution.
const preAspectMaxDimSafetyFactor = 3

// convertHeicForThumbnail converts a HEIC/HEIF file to a PNG already
// downscaled to fit within maxDim x maxDim, using macOS's built-in `sips`
// command-line tool -- Apple's own HEIC/HEIF decoder. There's no
// practical way to decode HEIC in pure Go (it's built on HEVC/H.265 video
// compression, patent-encumbered and complex enough that neither the
// standard library nor golang.org/x/image support it), so unlike PNG/
// JPEG/GIF this can't reuse image.Decode(); shelling out to a real
// decoder already installed on the target platform matches how this port
// already delegates ls(1)'s own output rather than reimplementing it.
//
// Doing the resize as part of the same sips invocation (-Z), rather than
// converting at full resolution and letting downscaleForThumbnail()
// shrink it afterward in Go, means Apple's own decode path never has to
// materialize the image at full resolution just to have it immediately
// shrunk again -- a real speed difference for a multi-megapixel phone
// photo. The resulting PNG is already at or under maxDim on both axes, so
// downscaleForThumbnail() downstream (see buildImagePrefix()) becomes a
// cheap no-op for it rather than a second real resize.
//
// Returns ok=false if sips isn't on PATH (e.g. not running on macOS) or
// the conversion fails for any reason, in which case the caller falls
// back to embedding the original HEIC/HEIF file unchanged -- same as any
// other format this port can't do anything special with.
func convertHeicForThumbnail(path string, maxDim int) ([]byte, bool) {
	sipsPath, err := exec.LookPath("sips")
	if err != nil {
		return nil, false
	}
	tmp, err := os.CreateTemp("", "macls-heic-*.png")
	if err != nil {
		return nil, false
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command(sipsPath, "-Z", strconv.Itoa(maxDim), "-s", "format", "png", path, "--out", tmpPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// qlmanageTimeout bounds how long qlmanageThumbnail() waits for qlmanage(1)
// before giving up and treating the thumbnail as failed. Kept short: some
// files/extensions (confirmed with plain .out/.err log files, most likely
// whenever Quick Look has no real generator for the content and falls
// through to some slow default path) make qlmanage hang seemingly
// indefinitely rather than fail fast, and a directory can easily contain
// many such files -- especially under --ql-ext=all, which tries every
// non-image extension. 1s is well above qlmanage's normal few-hundred-ms
// case but short enough that a run of hangs doesn't stall a whole listing.
const qlmanageTimeout = 1 * time.Second

// qlmanageThumbnail renders path's first-page/sheet/slide preview via
// macOS's qlmanage(1) (Quick Look's own command-line interface), scaled to
// fit within maxPx on its longer side, and returns it as PNG data.
// qlmanage only writes into a directory (-o), naming its output
// "<original filename>.png" inside it, so this uses a dedicated temporary
// directory per call.
//
// Returns ok=false on any failure -- qlmanage not on PATH (e.g. not running
// on macOS), a non-zero exit, the timeout above, or the expected output
// file missing/empty -- in which case the caller has no thumbnail for this
// entry at all: unlike an image format buildImagePrefix() can't parse the
// header of, there's no "embed the original file and hope the terminal can
// decode it" fallback that makes sense for a document format an OSC 1337
// client can't render directly.
func qlmanageThumbnail(path string, maxPx int) ([]byte, bool) {
	qlPath, err := exec.LookPath("qlmanage")
	if err != nil {
		return nil, false
	}
	tmpDir, err := os.MkdirTemp("", "macls-ql-*")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithTimeout(context.Background(), qlmanageTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, qlPath, "-t", "-s", strconv.Itoa(maxPx), "-o", tmpDir, path)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	outPath := filepath.Join(tmpDir, filepath.Base(path)+".png")
	data, err := os.ReadFile(outPath)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
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
//
// ql (see --ql-ext) selects which non-image extensions are Quick Look
// candidates, rendered via qlmanage(1) (see qlmanageThumbnail()) instead of
// embedded directly -- an imageExtensions file is never treated as a Quick
// Look candidate regardless of ql, even under --ql-ext=all or a --ql-ext
// list that happens to include one of its extensions, since those already
// have a strictly better dedicated path (their own real pixel data, not a
// qlmanage-rendered preview).
func buildImagePrefix(path string, width, height, termHeight int, allowAspectHeight bool, ql qlExtensions) string {
	ext := strings.ToLower(filepath.Ext(path))
	isImage := imageExtensions[ext]
	isQL := !isImage && isQLCandidate(path, ext, ql)
	if !isImage && !isQL {
		return ""
	}

	var data []byte
	if isQL {
		// No "embed the original file" fallback makes sense here (an OSC
		// 1337 client can't render a .docx), so any qlmanage failure just
		// means no thumbnail for this entry.
		maxPx := width * thumbnailPxPerCell * preAspectMaxDimSafetyFactor
		converted, ok := qlmanageThumbnail(path, maxPx)
		if !ok {
			return ""
		}
		data, ext = converted, ".png"
	} else {
		d, err := os.ReadFile(path)
		if err != nil || len(d) == 0 {
			return ""
		}
		data = d

		if ext == ".heic" || ext == ".heif" {
			heicMaxDim := width * thumbnailPxPerCell * preAspectMaxDimSafetyFactor
			if converted, ok := convertHeicForThumbnail(path, heicMaxDim); ok {
				data, ext = converted, ".png"
			}
		}
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
func buildImagePrefixes(fullPaths []string, optI bool, width, height int, stackedFlags []bool, singleLine bool, ql qlExtensions) ([]string, []string, int) {
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
			imgs[i] = buildImagePrefix(p, width, height, termHeight, singleLine, ql)
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
