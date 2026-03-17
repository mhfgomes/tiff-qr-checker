import path from "node:path";
import os from "node:os";
import jsQR from "../node_modules/jsqr/dist/jsQR.js";
import * as UTIF from "../node_modules/utif/UTIF.js";

type ScanResult = {
  filePath: string;
  hasQrCode: boolean;
  pageMatches: number[];
  decodedValues: string[];
  error?: string;
};

type Logger = {
  path: string;
  log: (line: string) => void;
  hasContent: () => boolean;
  flush: () => Promise<void>;
};

const TIFF_EXTENSIONS = new Set([".tif", ".tiff"]);
const QR_SCALES = [1, 0.75, 0.5, 0.4, 0.33, 0.25, 0.2, 1.5];
const SPINNER_FRAMES = ["|", "/", "-", "\\"];
const DEFAULT_CONCURRENCY = Math.max(1, Math.min(4, os.availableParallelism?.() ?? 4));

async function main() {
  const { outputJson, concurrency, inputPath } = await parseCliArgs(Bun.argv.slice(2));
  if (!inputPath) {
    console.error("No folder provided.");
    process.exit(1);
  }

  const target = path.resolve(process.cwd(), inputPath);

  const stat = await safeStat(target);
  if (!stat) {
    console.error(`Path not found: ${target}`);
    process.exit(1);
  }

  const timestamp = formatTimestamp(new Date());
  const logger = createLogger(process.cwd(), `scan_${timestamp}.log`);
  const qrLogger = createLogger(process.cwd(), `qrs_${timestamp}.log`);
  logger.log(`Scan started: ${new Date().toISOString()}`);
  logger.log(`Working directory: ${process.cwd()}`);
  logger.log(`Target: ${target}`);
  logger.log(`Concurrency: ${concurrency}`);
  logger.log("");

  const files = stat.isDirectory() ? await collectTiffFiles(target) : [target];
  const tiffFiles = files.filter((filePath) => isTiff(filePath));

  if (tiffFiles.length === 0) {
    logger.log("No .tif or .tiff files found.");
    await logger.flush();
    console.log(outputJson ? "[]" : "No .tif or .tiff files found.");
    return;
  }

  const results = new Array<ScanResult>(tiffFiles.length);
  const progress = outputJson ? null : createProgressTracker(tiffFiles.length, concurrency);
  await runWithConcurrency(tiffFiles, concurrency, async (filePath, index) => {
    progress?.startFile(index + 1, filePath);

    const result = await scanTiff(filePath, (completedSteps, totalSteps) => {
      progress?.updateFile(filePath, completedSteps, totalSteps);
    });
    results[index] = result;

    progress?.finishFile(filePath, result);
    logResult(logger, result);
    if (result.hasQrCode && !result.error) {
      logResult(qrLogger, result);
    }
  });
  progress?.stop();

  if (outputJson) {
    await logger.flush();
    await flushQrLoggerIfNeeded(qrLogger);
    console.log(JSON.stringify(results, null, 2));
    return;
  }

  printResults(results, logger);
  await logger.flush();
  const qrLogPath = await flushQrLoggerIfNeeded(qrLogger);
  console.log(`Log file: ${logger.path}`);
  if (qrLogPath) {
    console.log(`QR log file: ${qrLogPath}`);
  }
}

async function parseCliArgs(args: string[]) {
  const outputJson = args.includes("--json");
  let concurrency = DEFAULT_CONCURRENCY;
  const positionalArgs: string[] = [];

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--json") {
      continue;
    }

    if (arg === "--concurrency" || arg === "-c") {
      const nextValue = args[index + 1];
      if (nextValue) {
        concurrency = normalizeConcurrency(nextValue);
        index += 1;
      }
      continue;
    }

    if (arg.startsWith("--concurrency=")) {
      concurrency = normalizeConcurrency(arg.slice("--concurrency=".length));
      continue;
    }

    positionalArgs.push(arg);
  }

  return {
    outputJson,
    concurrency,
    inputPath: positionalArgs[0] ?? (await promptForFolder()),
  };
}

function normalizeConcurrency(value: string) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 1) {
    return DEFAULT_CONCURRENCY;
  }

  return Math.max(1, Math.min(32, parsed));
}

async function promptForFolder(): Promise<string | null> {
  process.stdout.write("Enter folder path: ");

  for await (const line of console) {
    const value = line.trim();
    return value.length > 0 ? value : null;
  }

  return null;
}

async function safeStat(targetPath: string) {
  try {
    return await Bun.file(targetPath).stat();
  } catch {
    return null;
  }
}

async function collectTiffFiles(rootDir: string): Promise<string[]> {
  const found: string[] = [];
  const queue = [rootDir];

  while (queue.length > 0) {
    const currentDir = queue.shift();
    if (!currentDir) {
      continue;
    }

    for await (const entry of new Bun.Glob("*").scan({
      cwd: currentDir,
      absolute: true,
      onlyFiles: false,
    })) {
      const stat = await safeStat(entry);
      if (!stat) {
        continue;
      }

      if (stat.isDirectory()) {
        queue.push(entry);
        continue;
      }

      if (isTiff(entry)) {
        found.push(entry);
      }
    }
  }

  found.sort((a, b) => a.localeCompare(b));
  return found;
}

function isTiff(filePath: string) {
  const lower = filePath.toLowerCase();
  return [...TIFF_EXTENSIONS].some((ext) => lower.endsWith(ext));
}

async function scanTiff(
  filePath: string,
  onProgress?: (completedSteps: number, totalSteps: number) => void,
): Promise<ScanResult> {
  try {
    const buffer = await Bun.file(filePath).arrayBuffer();
    const ifds = UTIF.decode(buffer);
    const pageMatches: number[] = [];
    const decodedValues = new Set<string>();
    const totalStepsPerPage = buildRegions(100, 100).length * QR_SCALES.length;
    const totalSteps = Math.max(1, ifds.length * totalStepsPerPage);
    let completedSteps = 0;

    for (let index = 0; index < ifds.length; index += 1) {
      const ifd = ifds[index];
      UTIF.decodeImage(buffer, ifd);
      const rgba = UTIF.toRGBA8(ifd);

      const pageValues = await detectQRCodes(rgba, ifd.width, ifd.height, async () => {
        completedSteps += 1;
        onProgress?.(completedSteps, totalSteps);
        if (completedSteps % 3 === 0) {
          await Bun.sleep(0);
        }
      });
      if (pageValues.length > 0) {
        pageMatches.push(index + 1);
        for (const value of pageValues) {
          decodedValues.add(value);
        }
      }
    }

    return {
      filePath,
      hasQrCode: pageMatches.length > 0,
      pageMatches,
      decodedValues: [...decodedValues],
    };
  } catch (error) {
    return {
      filePath,
      hasQrCode: false,
      pageMatches: [],
      decodedValues: [],
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

async function detectQRCodes(
  rgba: Uint8Array,
  width: number,
  height: number,
  onStep?: () => Promise<void>,
): Promise<string[]> {
  const found = new Set<string>();
  const regions = buildRegions(width, height);

  for (const region of regions) {
    const cropped = cropRgba(rgba, width, region.x, region.y, region.width, region.height);

    for (const scale of QR_SCALES) {
      const scaledWidth = Math.max(32, Math.round(region.width * scale));
      const scaledHeight = Math.max(32, Math.round(region.height * scale));
      const candidate =
        scale === 1
          ? cropped
          : resizeNearest(cropped, region.width, region.height, scaledWidth, scaledHeight);

      const qr = jsQR(candidate, scaledWidth, scaledHeight, {
        inversionAttempts: "attemptBoth",
      });

      if (qr?.data) {
        found.add(qr.data);
      }

      await onStep?.();
    }
  }

  return [...found];
}

async function runWithConcurrency<T>(
  items: T[],
  concurrency: number,
  handler: (item: T, index: number) => Promise<void>,
) {
  let nextIndex = 0;

  async function worker() {
    while (nextIndex < items.length) {
      const currentIndex = nextIndex;
      nextIndex += 1;
      await handler(items[currentIndex], currentIndex);
    }
  }

  const workerCount = Math.min(concurrency, items.length);
  await Promise.all(Array.from({ length: workerCount }, () => worker()));
}

function buildRegions(width: number, height: number) {
  const topThird = Math.max(32, Math.floor(height * 0.35));
  const topHalf = Math.max(32, Math.floor(height * 0.45));
  const leftHalf = Math.floor(width / 2);
  const rightHalf = width - leftHalf;
  const leftWide = Math.max(32, Math.floor(width * 0.6));
  const rightWideX = Math.floor(width * 0.4);
  const rightWide = width - rightWideX;

  return [
    { x: 0, y: 0, width, height },
    { x: 0, y: 0, width, height: topThird },
    { x: 0, y: 0, width: leftHalf, height: topThird },
    { x: leftHalf, y: 0, width: rightHalf, height: topThird },
    { x: 0, y: 0, width: leftWide, height: topHalf },
    { x: rightWideX, y: 0, width: rightWide, height: topHalf },
  ];
}

function cropRgba(
  rgba: Uint8Array,
  sourceWidth: number,
  x: number,
  y: number,
  width: number,
  height: number,
) {
  const out = new Uint8ClampedArray(width * height * 4);

  for (let row = 0; row < height; row += 1) {
    const sourceStart = ((y + row) * sourceWidth + x) * 4;
    const targetStart = row * width * 4;
    out.set(rgba.subarray(sourceStart, sourceStart + width * 4), targetStart);
  }

  return out;
}

function resizeNearest(
  rgba: Uint8ClampedArray,
  sourceWidth: number,
  sourceHeight: number,
  width: number,
  height: number,
) {
  const out = new Uint8ClampedArray(width * height * 4);

  for (let y = 0; y < height; y += 1) {
    const sourceY = Math.min(sourceHeight - 1, Math.floor((y * sourceHeight) / height));
    for (let x = 0; x < width; x += 1) {
      const sourceX = Math.min(sourceWidth - 1, Math.floor((x * sourceWidth) / width));
      const sourceIndex = (sourceY * sourceWidth + sourceX) * 4;
      const targetIndex = (y * width + x) * 4;

      out[targetIndex] = rgba[sourceIndex];
      out[targetIndex + 1] = rgba[sourceIndex + 1];
      out[targetIndex + 2] = rgba[sourceIndex + 2];
      out[targetIndex + 3] = rgba[sourceIndex + 3];
    }
  }

  return out;
}

function createProgressTracker(totalFiles: number, concurrency: number) {
  const activeFiles = new Map<
    string,
    {
      index: number;
      filePath: string;
      step: number;
      totalSteps: number;
      startedAt: number;
    }
  >();
  let completedFiles = 0;
  let frame = 0;
  const startTime = Date.now();

  const render = () => {
    const terminalWidth = process.stdout.columns ?? 100;
    const barWidth = 24;
    let activeProgress = 0;
    const activeEntries = [...activeFiles.values()].sort((left, right) => left.index - right.index);

    for (const entry of activeEntries) {
      activeProgress += entry.totalSteps > 0 ? entry.step / entry.totalSteps : 0;
    }

    const overallProgress = Math.min(
      1,
      (completedFiles + activeProgress) / Math.max(1, totalFiles),
    );
    const filled = Math.floor(overallProgress * barWidth);
    const head = filled < barWidth ? SPINNER_FRAMES[frame % SPINNER_FRAMES.length] : "=";
    const bar = `${"=".repeat(filled)}${head}${" ".repeat(Math.max(0, barWidth - filled - 1))}`;
    const elapsedSeconds = ((Date.now() - startTime) / 1000).toFixed(1);
    const etaSeconds =
      overallProgress > 0
        ? (((Date.now() - startTime) / overallProgress - (Date.now() - startTime)) / 1000).toFixed(1)
        : "?";
    const percent = `${Math.round(overallProgress * 100)}%`.padStart(4, " ");
    const fileLabel = `${completedFiles}/${totalFiles}`;
    const workerLabel = `${activeEntries.length}/${concurrency}`;
    const activeSummary =
      activeEntries.length > 0
        ? activeEntries
            .slice(0, 2)
            .map((entry) => {
              const fileSeconds = ((Date.now() - entry.startedAt) / 1000).toFixed(1);
              return `${basename(entry.filePath)} ${entry.step}/${entry.totalSteps} ${fileSeconds}s`;
            })
            .join(" | ")
        : "idle";
    const fixedWidth =
      barWidth +
      percent.length +
      fileLabel.length +
      workerLabel.length +
      elapsedSeconds.length +
      String(etaSeconds).length +
      34;
    const availableNameWidth = Math.max(12, terminalWidth - fixedWidth);
    const name = truncateMiddle(activeSummary, availableNameWidth);
    process.stdout.write(
      `\x1b[2K\r[${bar}] ${percent} done ${fileLabel} active ${workerLabel} ${name} elapsed ${elapsedSeconds}s eta ${etaSeconds}s`,
    );
    frame += 1;
  };

  return {
    startFile(index: number, filePath: string) {
      activeFiles.set(filePath, {
        index,
        filePath,
        step: 0,
        totalSteps: 1,
        startedAt: Date.now(),
      });
      render();
    },
    updateFile(filePath: string, nextStep: number, nextTotalSteps: number) {
      const entry = activeFiles.get(filePath);
      if (!entry) {
        return;
      }

      entry.step = nextStep;
      entry.totalSteps = nextTotalSteps;
      render();
    },
    finishFile(filePath: string, result: ScanResult) {
      const entry = activeFiles.get(filePath);
      if (!entry) {
        return;
      }

      completedFiles += 1;
      activeFiles.delete(filePath);
      const status = result.error
        ? `error: ${result.error}`
        : `found ${result.decodedValues.length} QR code(s)`;
      const fileDuration = ((Date.now() - entry.startedAt) / 1000).toFixed(1);
      process.stdout.write(
        `\x1b[2K\r[${entry.index}/${totalFiles}] ${basename(filePath)} -> ${status} in ${fileDuration}s\n`,
      );
    },
    stop() {
      process.stdout.write("\x1b[2K\r");
    },
  };
}

function basename(filePath: string) {
  const normalized = filePath.replaceAll("\\", "/");
  const parts = normalized.split("/");
  return parts[parts.length - 1] ?? filePath;
}

function truncateMiddle(value: string, maxLength: number) {
  if (value.length <= maxLength) {
    return value;
  }

  if (maxLength <= 3) {
    return value.slice(0, maxLength);
  }

  const leftLength = Math.ceil((maxLength - 3) / 2);
  const rightLength = Math.floor((maxLength - 3) / 2);
  return `${value.slice(0, leftLength)}...${value.slice(value.length - rightLength)}`;
}

function printResults(results: ScanResult[], logger: Logger) {
  let matches = 0;
  let failures = 0;

  logger.log("");
  logger.log("Results:");

  for (const result of results) {
    if (result.error) {
      failures += 1;
      console.log(`ERROR ${result.filePath}`);
      console.log(`  ${result.error}`);
      continue;
    }

    if (result.hasQrCode) {
      matches += 1;
      console.log(`QR YES ${result.filePath}`);
      console.log(`  Pages: ${result.pageMatches.join(", ")}`);
      if (result.decodedValues.length > 0) {
        console.log(`  Values: ${result.decodedValues.join(" | ")}`);
      }
      continue;
    }

    console.log(`QR NO  ${result.filePath}`);
  }

  console.log("");
  console.log(`Scanned: ${results.length}`);
  console.log(`With QR: ${matches}`);
  console.log(`Without QR: ${results.length - matches - failures}`);
  console.log(`Errors: ${failures}`);

  logger.log("");
  logger.log(`Scanned: ${results.length}`);
  logger.log(`With QR: ${matches}`);
  logger.log(`Without QR: ${results.length - matches - failures}`);
  logger.log(`Errors: ${failures}`);
}

function createLogger(rootDir: string, fileName: string): Logger {
  const logPath = path.join(rootDir, fileName);
  const lines: string[] = [];

  return {
    path: logPath,
    log(line: string) {
      lines.push(line);
    },
    hasContent() {
      return lines.length > 0;
    },
    async flush() {
      await Bun.write(logPath, `${lines.join("\n")}\n`);
    },
  };
}

function formatTimestamp(date: Date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const hours = String(date.getHours()).padStart(2, "0");
  const minutes = String(date.getMinutes()).padStart(2, "0");
  const seconds = String(date.getSeconds()).padStart(2, "0");
  return `${year}${month}${day}_${hours}${minutes}${seconds}`;
}

function logResult(logger: Logger, result: ScanResult) {
  if (result.error) {
    logger.log(`ERROR ${result.filePath}`);
    logger.log(`  ${result.error}`);
    return;
  }

  if (result.hasQrCode) {
    logger.log(`QR YES ${result.filePath}`);
    logger.log(`  Pages: ${result.pageMatches.join(", ")}`);
    if (result.decodedValues.length > 0) {
      logger.log(`  Values: ${result.decodedValues.join(" | ")}`);
    }
    return;
  }

  logger.log(`QR NO  ${result.filePath}`);
}

async function flushQrLoggerIfNeeded(logger: Logger) {
  if (logger.hasContent()) {
    await logger.flush();
    return logger.path;
  }

  return null;
}

await main();
