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
- **HEIC/HEIF `-I` thumbnails are decoded, not just embedded raw.** The
  Python original (like every other format it can't parse the header of)
  just embeds a HEIC/HEIF file's raw bytes and hopes the terminal can
  decode it. There's no practical way to decode HEIC in pure Go (it's
  built on patent-encumbered HEVC/H.265 compression, unsupported by both
  the standard library and `golang.org/x/image`), so this port instead
  shells out to macOS's own `sips` command-line tool — the same approach
  already used for `ls -l`'s own output — to convert it to PNG first, then
  runs that through the same downscaling as a real PNG file (see below).
  Falls back to embedding the file unchanged, matching the original
  behavior, if `sips` isn't on `PATH` (e.g. not running on macOS) or the
  conversion fails for any reason.
- **`--paging`, an extra option with no equivalent in the Python
  original.** By default, `-I` behaves like the original: it waits for
  every thumbnail to be read and (as of this port) downscaled before
  printing anything, but the listing itself never pauses partway through.
  `--paging` instead prints the text listing immediately — names, `-l`'s
  permissions/dates, etc. — with each entry's thumbnail filled in
  afterward as it finishes loading, concurrently, via cursor positioning.
  This applies both to `-1`/`-l` (one entry per line) and to multi-column
  output. No effect without `-I`.

  If the listing doesn't fit on one screen, `--paging` output pauses
  after each screenful with a `more(1)`-style prompt:

  ```
  -- more (space to continue, q to quit) --
  ```

  Press space to continue to the next page, or `q` (also Ctrl-C or Esc)
  to stop early. This exists because a thumbnail can only be drawn into a
  row that's still on screen — once a row scrolls into the terminal's
  scrollback there's no way to draw into it — so pagination is what
  guarantees every thumbnail you scroll back to was actually drawn.
  Pagination only kicks in when standard input is a terminal; otherwise
  (e.g. output piped to a file) every page prints back-to-back with no
  pause.

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
./macls --stripe --tag=str
```

## License

MIT — see [LICENSE](LICENSE). Ported from
[ktabe/macls](https://github.com/ktabe/macls), also MIT-licensed.
