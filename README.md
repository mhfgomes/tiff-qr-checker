# TIFF QR Checker

Command-line tool built with Bun and TypeScript that scans a folder recursively, opens every `.tif` and `.tiff`, and reports whether QR codes were found in each file.

## Features

- Recursively scans folders and subfolders
- Supports `.tif` and `.tiff`
- Detects multiple QR codes per image
- Prompts for a folder when no path is provided
- Shows live progress with elapsed time, ETA, and per-file duration
- Supports concurrent scanning with configurable worker count
- Writes plain text scan logs in the current working directory
- Can output JSON for automation

## Requirements

- [Bun](https://bun.sh/)

## Install

```bash
bun install
```

## Run

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

Return JSON instead of terminal output:

```bash
bun run src/cli.ts "C:\path\to\folder" --json
```

## Output

Normal terminal output includes:

- live progress bar
- elapsed time
- ETA
- current active files
- per-file completion lines
- final summary

Example summary:

```text
Scanned: 11
With QR: 11
Without QR: 0
Errors: 0
```

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
