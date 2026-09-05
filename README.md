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

  **Experimental, no equivalent in the Python original either:** with
  `-I`, hovering the mouse over a thumbnail at the `-- more --` prompt and
  pressing space opens that entry in a real Quick Look window
  (`qlmanage -p`) instead of advancing — the prompt keeps waiting at the
  same page either way, so this doesn't cost you your place in the
  listing. It turns on xterm-style mouse motion reporting only for the
  duration of that one prompt (asking the terminal for its cursor
  position first, via a Device Status Report, to know where each entry's
  own row actually is on screen), so it has no effect once you move past
  the prompt, and none at all without `--paging` (nothing here reads the
  mouse otherwise). This is a rough prototype — mouse reporting and
  Device Status Report are both widely supported, but this has only been
  exercised with synthetic escape sequences in a sandbox with no real
  iTerm2, a real mouse, or a real Quick Look window to check against.

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
