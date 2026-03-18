# TIFF QR Checker

Command-line tool built with Bun and TypeScript that scans folders recursively, reads `.tif` and `.tiff` files, and reports whether each image has a QR code.

## Features

- Recursively scans folders and subfolders
- Supports `.tif` and `.tiff`
- Stops on the first QR found in each image
- Prompts for a folder when no path is provided
- Supports concurrent scanning with configurable worker count
- Shows live progress with elapsed time, ETA, and per-file duration
- Writes plain text logs in the current working directory
- Supports optional size filtering with `SKIPPED` output
- Supports JSON output for automation

## Requirements

- [Bun](https://bun.sh/)

## Install

```bash
bun install
```

## Usage

Show help:

```bash
bun run src/cli.ts --help
```

Prompt for a folder:

```bash
bun run src/cli.ts
```

Scan a specific folder:

```bash
bun run src/cli.ts "C:\path\to\folder"
```

Scan with custom concurrency:

```bash
bun run src/cli.ts "C:\path\to\folder" --concurrency 5
```

Short form:

```bash
bun run src/cli.ts "C:\path\to\folder" -c 5
```

Use aggressive fallback scanning:

```bash
bun run src/cli.ts "C:\path\to\folder" --aggressive
```

Skip TIFFs larger than a limit in KB:

```bash
bun run src/cli.ts "C:\path\to\folder" --max-size 500
```

Short form:

```bash
bun run src/cli.ts "C:\path\to\folder" -m 500
```

Return JSON instead of terminal output:

```bash
bun run src/cli.ts "C:\path\to\folder" --json
```

Show version:

```bash
bun run src/cli.ts --version
```

## Options

- `-c, --concurrency <n>`: number of worker processes
- `-m, --max-size <kb>`: skip TIFFs larger than this size in KB
- `--aggressive`: use slower fallback scan stages
- `--json`: print JSON output
- `-v, --version`: print current version
- `-h, --help`: show usage help

## Output

Normal terminal output includes:

- live progress bar
- elapsed time
- ETA
- current active files
- per-file completion lines
- final summary

Per-file results use:

- `QR YES <path>`
- `QR NO <path>`
- `SKIPPED <path>`
- `ERROR <path>`

Example summary:

```text
Scanned: 100
With QR: 4
Without QR: 50
Skipped: 46
Errors: 0
Total time: 13.1s
Average file time: 0.1s
Median file time: 0.1s
Combined file time: 6.6s
```

`Scanned` always reflects the total number of TIFF files found, even when `--max-size` is used.

## Logs

The tool writes logs to the directory where the command is run.

Generated every run:

- `scan_YYYYMMDD_HHMMSS.log`

Generated only when at least one QR code is found:

- `qrs_YYYYMMDD_HHMMSS.log`

## Build

Build the Bun-targeted executable:

```bash
bun run build
```

Build platform binaries:

```bash
bun run build:win
bun run build:linux
bun run build:mac
```

Artifacts are written to `dist/`.
