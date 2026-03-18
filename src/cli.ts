import path from "node:path";
import os from "node:os";
import { type ScanMode, type ScanResult } from "./scan-core";

type Logger = {
  path: string;
  log: (line: string) => void;
  hasContent: () => boolean;
  flush: () => Promise<void>;
};

type WorkerStats = {
  workerId: number;
  filesCompleted: number;
  activeTimeMs: number;
};

type PoolStats = {
  totalWallTimeMs: number;
  fileTimeMs: number[];
  workerStats: WorkerStats[];
};

const TIFF_EXTENSIONS = new Set([".tif", ".tiff"]);
const SPINNER_FRAMES = ["|", "/", "-", "\\"];
const DEFAULT_CONCURRENCY = Math.max(1, Math.min(4, os.availableParallelism?.() ?? 4));

async function main() {
  const { outputJson, concurrency, inputPath, scanMode } = await parseCliArgs(Bun.argv.slice(2));
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
  logger.log(`Scan mode: ${scanMode}`);
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
  const fileDurationsMs = new Array<number>(tiffFiles.length);
  const progress = outputJson ? null : createProgressTracker(tiffFiles.length, concurrency);

  const callbacks = {
    onStart(filePath: string, index: number) {
      progress?.startFile(index + 1, filePath);
    },
    onProgress(filePath: string, completedSteps: number, totalSteps: number) {
      progress?.updateFile(filePath, completedSteps, totalSteps);
    },
    onResult(filePath: string, index: number, result: ScanResult) {
      results[index] = result;
      progress?.finishFile(filePath, result);
      logResult(logger, result);
      if (result.hasQrCode && !result.error) {
        logResult(qrLogger, result);
      }
    },
  };

  const poolStats = await runWithWorkerPool(tiffFiles, concurrency, scanMode, {
    ...callbacks,
    onResult(filePath, index, result, durationMs) {
      fileDurationsMs[index] = durationMs;
      callbacks.onResult(filePath, index, result);
    },
  });
  progress?.stop();

  if (outputJson) {
    await logger.flush();
    await flushQrLoggerIfNeeded(qrLogger);
    console.log(JSON.stringify(results, null, 2));
    return;
  }

  printResults(results, logger, buildTimingSummary(poolStats, fileDurationsMs));
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
  let scanMode: ScanMode = "default";
  const positionalArgs: string[] = [];

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--json") {
      continue;
    }

    if (arg === "--aggressive") {
      scanMode = "aggressive";
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
    scanMode,
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

async function runWithWorkerPool(
  items: string[],
  concurrency: number,
  scanMode: ScanMode,
  callbacks: {
    onStart: (filePath: string, index: number) => void;
    onProgress: (filePath: string, completedSteps: number, totalSteps: number) => void;
    onResult: (filePath: string, index: number, result: ScanResult, durationMs: number) => void;
  },
): Promise<PoolStats> {
  let nextIndex = 0;
  const workerCount = Math.min(concurrency, items.length);
  if (workerCount === 0) {
    return {
      totalWallTimeMs: 0,
      fileTimeMs: [],
      workerStats: [],
    };
  }

  const assignments = new Map<string, number>();
  const startedAtByFile = new Map<string, number>();
  const workerStats = Array.from({ length: workerCount }, (_, index) => ({
    workerId: index + 1,
    filesCompleted: 0,
    activeTimeMs: 0,
  }));
  const fileTimeMs: number[] = [];
  let completed = 0;
  const poolStartedAt = performance.now();

  await new Promise<void>((resolve, reject) => {
    const workers = Array.from(
      { length: workerCount },
      (_, index) => ({
        instance: new Worker(new URL("./scan-worker.ts", import.meta.url).href),
        stats: workerStats[index],
      }),
    );

    const cleanup = async () => {
      await Promise.allSettled(
        workers.map(async ({ instance }) => {
          const worker = instance;
          worker.postMessage({ type: "stop" });
          await worker.terminate();
        }),
      );
    };

    const dispatchNext = (worker: Worker) => {
      const currentIndex = nextIndex;
      if (currentIndex >= items.length) {
        return false;
      }

      nextIndex += 1;
      const filePath = items[currentIndex];
      assignments.set(filePath, currentIndex);
      startedAtByFile.set(filePath, performance.now());
      callbacks.onStart(filePath, currentIndex);
      worker.postMessage({
        type: "scan",
        filePath,
        mode: scanMode,
      });
      return true;
    };

    const handleFailure = async (error: unknown) => {
      await cleanup();
      reject(error);
    };

    for (const { instance, stats } of workers) {
      const worker = instance;
      worker.addEventListener("message", (event) => {
        const message = event.data;
        if (!message || typeof message !== "object" || !("type" in message)) {
          return;
        }

        if (message.type === "progress") {
          callbacks.onProgress(message.filePath, message.completedSteps, message.totalSteps);
          return;
        }

        if (message.type === "error") {
          void handleFailure(new Error(`Worker scan failed for ${message.filePath}: ${message.error}`));
          return;
        }

        if (message.type !== "result") {
          return;
        }

        const index = assignments.get(message.result.filePath);
        if (index === undefined) {
          return;
        }

        assignments.delete(message.result.filePath);
        const startedAt = startedAtByFile.get(message.result.filePath) ?? performance.now();
        startedAtByFile.delete(message.result.filePath);
        const durationMs = performance.now() - startedAt;
        fileTimeMs.push(durationMs);
        stats.filesCompleted += 1;
        stats.activeTimeMs += durationMs;
        callbacks.onResult(message.result.filePath, index, message.result, durationMs);
        completed += 1;

        if (completed >= items.length) {
          void cleanup().then(resolve, reject);
          return;
        }

        dispatchNext(worker);
      });

      worker.addEventListener("error", (event) => {
        void handleFailure(event.error ?? new Error(event.message));
      });
    }

    for (const { instance } of workers) {
      if (!dispatchNext(instance)) {
        break;
      }
    }
  });

  return {
    totalWallTimeMs: performance.now() - poolStartedAt,
    fileTimeMs,
    workerStats,
  };
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
      const status = result.error ? `error: ${result.error}` : result.hasQrCode ? "QR YES" : "QR NO";
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

function printResults(results: ScanResult[], logger: Logger, timingSummary: string[]) {
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
      continue;
    }

    console.log(`QR NO  ${result.filePath}`);
  }

  console.log("");
  console.log(`Scanned: ${results.length}`);
  console.log(`With QR: ${matches}`);
  console.log(`Without QR: ${results.length - matches - failures}`);
  console.log(`Errors: ${failures}`);
  for (const line of timingSummary) {
    console.log(line);
  }

  logger.log("");
  logger.log(`Scanned: ${results.length}`);
  logger.log(`With QR: ${matches}`);
  logger.log(`Without QR: ${results.length - matches - failures}`);
  logger.log(`Errors: ${failures}`);
  for (const line of timingSummary) {
    logger.log(line);
  }
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

function buildTimingSummary(poolStats: PoolStats, fileDurationsMs: number[]) {
  const durations = fileDurationsMs.filter((value) => Number.isFinite(value));
  const totalFileTimeMs = durations.reduce((sum, value) => sum + value, 0);
  const averageFileTimeMs = durations.length > 0 ? totalFileTimeMs / durations.length : 0;
  const sortedDurations = [...durations].sort((left, right) => left - right);
  const medianFileTimeMs =
    sortedDurations.length === 0
      ? 0
      : sortedDurations[Math.floor(sortedDurations.length / 2)];

  return [
    `Total time: ${formatDuration(poolStats.totalWallTimeMs)}`,
    `Average file time: ${formatDuration(averageFileTimeMs)}`,
    `Median file time: ${formatDuration(medianFileTimeMs)}`,
    `Combined file time: ${formatDuration(totalFileTimeMs)}`,
    ...poolStats.workerStats.map(
      (stats) =>
        `Worker ${stats.workerId}: ${stats.filesCompleted} file(s), active ${formatDuration(stats.activeTimeMs)}`,
    ),
  ];
}

function formatDuration(durationMs: number) {
  const totalSeconds = durationMs / 1000;
  if (totalSeconds < 60) {
    return `${totalSeconds.toFixed(1)}s`;
  }

  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds - minutes * 60;
  return `${minutes}m ${seconds.toFixed(1)}s`;
}

await main();
