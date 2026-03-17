# TIFF QR Checker

Command-line tool built with Bun and TypeScript that scans a folder recursively, opens every `.tif` and `.tiff`, and reports whether a QR code exists in each file.

## Install

```bash
bun install
```

## Run

Scan the current folder:

```bash
bun run src/cli.ts
```

If no folder is passed, the CLI prompts for one interactively.

Scan a specific folder:

```bash
bun run src/cli.ts "C:\\path\\to\\folder"
```

Return JSON instead of plain text:

```bash
bun run src/cli.ts "C:\\path\\to\\folder" --json
```

## Build

```bash
bun run build
```
