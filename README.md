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
  layout algorithm, `--quote`/ANSI-C quoting, `-I` thumbnails, the
  unsupported-option fallback to real `ls` — is a direct, behavior-for-
  behavior port.

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
