# go-macls

A Go port of [ktabe/macls](https://github.com/ktabe/macls) (`macls.py`), a
colorized drop-in replacement for macOS's `ls`.

Like the original, it colors filenames by how recently they were modified,
shows Finder tags as background colors, lays out multi-column output more
compactly than plain `ls -C`, and (in iTerm2) shows inline image thumbnails
and makes filenames clickable. Enumerating/sorting directory contents and
`-l`'s long-format output are still delegated to the real `ls(1)`, exactly
like the Python original; everything else (Finder tag lookup, recency
colors, display width, multi-column layout) is native Go.

See [macls.md](https://github.com/ktabe/macls/blob/main/macls.md) in the
original repository for the full option and color reference — this port
implements the same CLI surface.

## Differences from macls.py

- **Not a single dependency-free file.** Go's standard library has no
  Unicode East-Asian-width table, no NFC normalization, and no libc xattr
  binding, so this port uses `golang.org/x/text`, `golang.org/x/sys`, and
  `golang.org/x/term` for those, plus `howett.net/plist` to parse the
  binary-plist Finder tag attribute. All are pure Go, no cgo.
- **`sort_dir_args`** (ordering multiple directory arguments) uses a plain
  byte-wise sort rather than `locale.strxfrm()`-based collation, since Go's
  standard library has no locale-aware string collation.
- Everything else — every flag, every color rule, the compact column
  layout algorithm, `--quote`/ANSI-C quoting, the unsupported-option
  fallback to real `ls` — is a direct, behavior-for-behavior port.
- **HEIC/HEIF `-I` thumbnails always go through `sips`, not just when the
  file happens to be large.** Originally, the Python original (like every
  format it can't parse the header of) just embedded a HEIC/HEIF file's
  raw bytes and hoped the terminal could decode it; it has since gained
  its own `sips`-based shrink step (for large images generally — any
  format above a size threshold, always re-encoded as JPEG, HEIC
  included) that independently arrived at the same fix for the same
  "`-I` feels slow over a directory of large photos" problem this port
  also ran into. This port takes a narrower, format-specific version of
  that idea: there's no practical way to decode HEIC in pure Go (it's
  built on patent-encumbered HEVC/H.265 compression, unsupported by both
  the standard library and `golang.org/x/image`), so HEIC/HEIF
  specifically is always converted via `sips` regardless of file size —
  a small HEIC file has no header this port (or the original) can parse
  at all, so skipping the conversion below some size threshold would
  leave it embedded raw either way — re-encoded as PNG (matching the
  rest of this port's own downscaling pipeline, which decodes PNG/JPEG/
  GIF natively in Go rather than shelling out) rather than JPEG. Falls
  back to embedding the file unchanged, matching the original's own
  fallback, if `sips` isn't on `PATH` (e.g. not running on macOS) or the
  conversion fails for any reason.
- **Quick Look (`--ql-ext`) thumbnails come back from `qlmanage` as PNG,
  not re-encoded to JPEG.** The Python original renders a Word/Excel/
  PowerPoint preview via `qlmanage -t`, then always re-encodes it through
  its own `sips`-based shrink step (JPEG output). This port already has a
  native Go PNG/JPEG/GIF downscaling pipeline it reuses for every other
  oversized thumbnail (see the HEIC entry above), so it leaves `qlmanage`'s
  own PNG output as-is (further downscaled in Go if still larger than the
  target cell size) instead of adding a second shell-out just to change
  format — no user-visible difference, since both end up sized the same
  and iTerm2 renders either format identically.
- **`--paging`, an extra option with no equivalent in the Python
  original.** The Python original has since gained its own default
  streaming behavior for `-1`/`-l`: each entry's line prints as soon as
  its own thumbnail is ready, without waiting on the rest of the
  directory, but always in order — an entry's own image is already part
  of its line by the time that line prints, so it never needs to place
  anything after the fact and never risks a thumbnail landing on an
  entry that's already scrolled off. This port's default is still the
  older, simpler behavior: read every thumbnail (and, as of this port,
  downscale it) before printing anything, with the listing itself never
  pausing partway through. `--paging` instead prints the entire text
  listing immediately — names, `-l`'s permissions/dates, etc., not just
  one entry at a time — with every entry's thumbnail filled in
  afterward, concurrently and out of order, via cursor positioning. This
  applies both to `-1`/`-l` (one entry per line) and to multi-column
  output, which the original's own streaming doesn't cover either.

  `--paging` isn't only about `-I`, though: whenever the listing doesn't
  fit on one screen, it pauses after each screenful with a `more(1)`-style
  prompt, whether or not `-I` is active —

  ```
  -- more (space to continue, return for one line, q to quit) --
  ```

  — space advances to the next full page, return advances just one more
  line (holding it down steps through the listing one line at a time,
  same as `more`/`less`), and `q` (also Ctrl-C or Esc) stops early. With
  `-I`, this is also what guarantees every thumbnail you scroll back to
  was actually drawn: a thumbnail can only be drawn into a row that's
  still on screen, so a page never holds more rows than the terminal can
  show at once. Pagination only kicks in when standard input is a
  terminal; otherwise (e.g. output piped to a file) every page prints
  back-to-back with no pause.

## Build

```bash
go build -o macls .
```

## Install

```bash
go install .
```

Or alias it in your shell config, same as the original:

```bash
alias ls='macls -BF --stripe --suffix-color=type --fg-mode=date --tag=bg --quote'
```

## Usage

```bash
./macls
./macls -la ~/Desktop
./macls -I -1 --scale=2 ~/Pictures
./macls -Il2 --ql-ext=md,rtf ~/Documents
./macls --stripe --tag=str
```

## License

MIT — see [LICENSE](LICENSE). Ported from
[ktabe/macls](https://github.com/ktabe/macls), also MIT-licensed.
