# qrcheck

`qrcheck` is a Go CLI/TUI for scanning image files and folders for QR-code presence.

## Features

- Recursive file and directory scanning
- PNG, JPEG, TIFF, GIF, BMP, and WebP support
- Multi-page TIFF scanning
- Plain text and JSON output
- Auto-launching TUI for interactive batch scans
- Pure-Go QR detection by default
- Native engine scaffold behind `native_zxing` build tags

## Build

```bash
go build ./cmd/qrcheck
```

## Usage

```bash
qrcheck [flags] <path>...
```

Common examples:

```bash
qrcheck ./images
qrcheck --format json ./images
qrcheck --no-tui --include "*.tif" --exclude "*thumb*" ./images
qrcheck --strict-errors file1.png file2.tiff
```

## Flags

```text
--tui
--no-tui
--format text|json
--engine auto|go|native
--workers N
--thorough
--include GLOB
--exclude GLOB
--no-recursive
--strict-errors
--quiet
```

## Exit Codes

- `0`: at least one QR code found
- `1`: scan completed with no QR codes found
- `2`: fatal runtime/usage error, or file-level errors under `--strict-errors`

## Notes

- The default build is pure Go and does not require CGO.
- The native engine path is intentionally scaffolded but not implemented yet.
