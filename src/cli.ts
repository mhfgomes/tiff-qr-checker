import path from "node:path";
import os from "node:os";
import { spawn } from "node:child_process";
import readline from "node:readline";
import { scanTiff, type ScanMode, type ScanResult } from "./scan-core";
import packageJson from "../package.json" with { type: "json" };

type Logger = {
  path: string;
  log: (line: string) => void;
  hasContent: () => boolean;
  flush: () => Promise<void>;
};

type ScanItem = {
  filePath: string;
  skipped: boolean;
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

type WorkerRequestMessage = {
  type: "scan";
  id: string;
  filePath: string;
  mode: ScanMode;
};

type WorkerStopMessage = {
  type: "stop";
};

type WorkerMessage = WorkerRequestMessage | WorkerStopMessage;

type WorkerProgressMessage = {
  type: "progress";
  id: string;
  filePath: string;
  completedSteps: number;
  totalSteps: number;
};

type WorkerResultMessage = {
  type: "result";
  id: string;
  result: ScanResult;
  durationMs: number;
};

type WorkerErrorMessage = {
  type: "error";
  id: string;
  filePath: string;
  error: string;
};

type WorkerResponseMessage = WorkerProgressMessage | WorkerResultMessage | WorkerErrorMessage;

const TIFF_EXTENSIONS = new Set([".tif", ".tiff"]);
const SPINNER_FRAMES = ["|", "/", "-", "\\"];
const DEFAULT_CONCURRENCY = Math.max(1, Math.min(4, os.availableParallelism?.() ?? 4));
const PROGRESS_RENDER_INTERVAL_MS = 80;
const WORKER_PROGRESS_STEP_INTERVAL = 3;
const WORKER_PROGRESS_TIME_INTERVAL_MS = 100;

async function main() {
  const {
    outputJson,
    concurrency,
    inputPath,
    maxSizeBytes,
    scanMode,
    showVersion,
    showHelp,
    internalWorker,
  } = await parseCliArgs(process.argv.slice(2));
  if (showVersion) {
    console.log(packageJson.version);
    return;
  }

  if (showHelp) {
    printHelp();
    return;
  }

  if (internalWorker) {
    await runInternalWorkerLoop();
    return;
  }

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
  if (maxSizeBytes !== null) {
    logger.log(`Max size: ${formatFileSize(maxSizeBytes)}`);
  }
  logger.log("");

  const files = stat.isDirectory() ? await collectTiffFiles(target, maxSizeBytes) : [target];
  const scanItems =
    stat.isDirectory()
      ? files
      : await buildScanItems([target], maxSizeBytes);

  if (scanItems.length === 0) {
    const message =
      maxSizeBytes === null
        ? "No .tif or .tiff files found."
        : `No .tif or .tiff files found at or below ${formatFileSize(maxSizeBytes)}.`;
    logger.log(message);
    await logger.flush();
    console.log(outputJson ? "[]" : message);
    return;
  }

  const results = new Array<ScanResult>(scanItems.length);
  const fileDurationsMs = new Array<number>(scanItems.length);
  const progress = outputJson ? null : createProgressTracker(scanItems.length, concurrency);

  for (let index = 0; index < scanItems.length; index += 1) {
    const item = scanItems[index];
    if (!item.skipped) {
      continue;
    }

    const skippedResult: ScanResult = {
      filePath: item.filePath,
      hasQrCode: false,
      error: "SKIPPED",
    };
    results[index] = skippedResult;
    fileDurationsMs[index] = 0;
    logResult(logger, skippedResult);
  }

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

  const filesToScan = scanItems
    .map((item, index) => ({ ...item, index }))
    .filter((item) => !item.skipped);

  const poolStats = await runWithWorkerPool(filesToScan, concurrency, scanMode, {
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
  let maxSizeBytes: number | null = null;
  let scanMode: ScanMode = "default";
  let showVersion = false;
  let showHelp = false;
  let internalWorker = false;
  const positionalArgs: string[] = [];

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--json") {
      continue;
    }

    if (arg === "--version" || arg === "-v") {
      showVersion = true;
      continue;
    }

    if (arg === "--help" || arg === "-h") {
      showHelp = true;
      continue;
    }

    if (arg === "--internal-worker") {
      internalWorker = true;
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

    if (arg === "--max-size" || arg === "-m") {
      const nextValue = args[index + 1];
      if (nextValue) {
        maxSizeBytes = normalizeMaxSize(nextValue);
        index += 1;
      }
      continue;
    }

    if (arg.startsWith("--max-size=")) {
      maxSizeBytes = normalizeMaxSize(arg.slice("--max-size=".length));
      continue;
    }

    positionalArgs.push(arg);
  }

  const inputPath =
    showVersion || showHelp || internalWorker ? null : positionalArgs[0] ?? (await promptForFolder());

  return {
    outputJson,
    concurrency,
    maxSizeBytes,
    scanMode,
    showVersion,
    showHelp,
    internalWorker,
    inputPath,
  };
}

function printHelp() {
  console.log(`TIFF QR Checker ${packageJson.version}

Usage:
  bun run src/cli.ts [folder] [options]
  tiff-qr-checker-win.exe [folder] [options]

Options:
  -c, --concurrency <n>  Number of worker processes to use
  -m, --max-size <kb>    Skip TIFFs larger than this size in KB
      --aggressive       Use slower fallback scan stages for harder files
      --json             Print JSON output instead of terminal text
  -v, --version          Show current version
  -h, --help             Show this help

Notes:
  If no folder is provided, the tool prompts for one.
  Files skipped by --max-size still count toward the total and are shown as SKIPPED.`);
}

function normalizeConcurrency(value: string) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 1) {
    return DEFAULT_CONCURRENCY;
  }

  return Math.max(1, Math.min(32, parsed));
}

function normalizeMaxSize(value: string) {
  const parsed = Number.parseFloat(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return null;
  }

  return Math.floor(parsed * 1024);
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

async function collectTiffFiles(rootDir: string, maxSizeBytes: number | null): Promise<ScanItem[]> {
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
  return buildScanItems(found, maxSizeBytes);
}

function isTiff(filePath: string) {
  const lower = filePath.toLowerCase();
  return [...TIFF_EXTENSIONS].some((ext) => lower.endsWith(ext));
}

async function buildScanItems(filePaths: string[], maxSizeBytes: number | null) {
  const items: ScanItem[] = [];

  for (const filePath of filePaths) {
    if (!isTiff(filePath)) {
      continue;
    }

    const stat = await safeStat(filePath);
    if (stat) {
      items.push({
        filePath,
        skipped: !matchesMaxSize(stat.size, maxSizeBytes),
      });
    }
  }

  return items;
}

function matchesMaxSize(sizeBytes: number, maxSizeBytes: number | null) {
  return maxSizeBytes === null || sizeBytes <= maxSizeBytes;
}

async function runWithWorkerPool(
  items: Array<{ filePath: string; index: number }>,
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
  const spawnSpec = getWorkerSpawnSpec();

  await new Promise<void>((resolve, reject) => {
    let workers: ReturnType<typeof createPersistentWorker>[] = [];

    const cleanup = async () => {
      await Promise.allSettled(workers.map((worker) => worker.close()));
    };

    const handleFailure = async (error: unknown) => {
      await cleanup();
      reject(error);
    };

    workers = Array.from({ length: workerCount }, (_, index) =>
      createPersistentWorker(spawnSpec, index + 1, workerStats[index], callbacks.onProgress, handleFailure),
    );

    const dispatchNext = (worker: ReturnType<typeof createPersistentWorker>) => {
      const currentIndex = nextIndex;
      if (currentIndex >= items.length) {
        return false;
      }

      nextIndex += 1;
      const { filePath, index } = items[currentIndex];
      assignments.set(filePath, index);
      startedAtByFile.set(filePath, performance.now());
      callbacks.onStart(filePath, index);
      worker
        .run(filePath, scanMode)
        .then(({ result, durationMs }) => {
          const index = assignments.get(result.filePath);
          if (index === undefined) {
            return;
          }

          assignments.delete(result.filePath);
          startedAtByFile.delete(result.filePath);
          fileTimeMs.push(durationMs);
          callbacks.onResult(result.filePath, index, result, durationMs);
          completed += 1;

          if (completed >= items.length) {
            void cleanup().then(resolve, reject);
            return;
          }

          dispatchNext(worker);
        })
        .catch((error) => {
          void handleFailure(error);
        });
      return true;
    };

    for (const worker of workers) {
      if (!dispatchNext(worker)) {
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

function getWorkerSpawnSpec() {
  const execPath = process.execPath;
  const scriptPath = process.argv[1];
  const isScriptRun =
    typeof scriptPath === "string" &&
    scriptPath.length > 0 &&
    path.resolve(scriptPath) !== path.resolve(execPath);

  if (isScriptRun) {
    return {
      command: execPath,
      baseArgs: [scriptPath, "--internal-worker"],
    };
  }

  return {
    command: execPath,
    baseArgs: ["--internal-worker"],
  };
}

function createPersistentWorker(
  spawnSpec: { command: string; baseArgs: string[] },
  workerId: number,
  stats: WorkerStats,
  onProgress: (filePath: string, completedSteps: number, totalSteps: number) => void,
  onFailure: (error: unknown) => void,
) {
  const child = spawn(spawnSpec.command, spawnSpec.baseArgs, {
    stdio: ["pipe", "pipe", "pipe"],
    windowsHide: true,
  });
  const stdoutReader = readline.createInterface({
    input: child.stdout,
    crlfDelay: Infinity,
  });
  const pending = new Map<
    string,
    {
      filePath: string;
      resolve: (value: { result: ScanResult; durationMs: number }) => void;
      reject: (error: Error) => void;
    }
  >();
  let stderr = "";

  const rejectAll = (error: Error) => {
    for (const request of pending.values()) {
      request.reject(error);
    }
    pending.clear();
  };

  stdoutReader.on("line", (line) => {
    if (!line.trim()) {
      return;
    }

    let message: WorkerResponseMessage;
    try {
      message = JSON.parse(line) as WorkerResponseMessage;
    } catch (error) {
      rejectAll(
        new Error(
          `Worker ${workerId} produced invalid output: ${
            error instanceof Error ? error.message : String(error)
          }`,
        ),
      );
      return;
    }

    if (message.type === "progress") {
      onProgress(message.filePath, message.completedSteps, message.totalSteps);
      return;
    }

    const request = pending.get(message.id);
    if (!request) {
      return;
    }

    if (message.type === "error") {
      pending.delete(message.id);
      request.reject(new Error(`Worker ${workerId} scan failed for ${request.filePath}: ${message.error}`));
      return;
    }

    pending.delete(message.id);
    stats.filesCompleted += 1;
    stats.activeTimeMs += message.durationMs;
    request.resolve({
      result: message.result,
      durationMs: message.durationMs,
    });
  });

  child.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });

  child.on("error", (error) => {
    rejectAll(error instanceof Error ? error : new Error(String(error)));
    onFailure(error);
  });

  child.on("close", (code) => {
    if (pending.size === 0 && code === 0) {
      return;
    }

    const error = new Error(
      `Worker ${workerId} stopped unexpectedly: ${stderr.trim() || `exit code ${code ?? "unknown"}`}`,
    );
    rejectAll(error);
    if (pending.size > 0 || code !== 0) {
      onFailure(error);
    }
  });

  return {
    run(filePath: string, mode: ScanMode) {
      return new Promise<{ result: ScanResult; durationMs: number }>((resolve, reject) => {
        const id = `${workerId}-${stats.filesCompleted + pending.size + 1}-${Date.now()}`;
        pending.set(id, {
          filePath,
          resolve,
          reject,
        });
        const payload: WorkerRequestMessage = {
          type: "scan",
          id,
          filePath,
          mode,
        };
        child.stdin.write(`${JSON.stringify(payload)}\n`);
      });
    },
    async close() {
      if (child.killed) {
        return;
      }

      try {
        const payload: WorkerStopMessage = { type: "stop" };
        child.stdin.write(`${JSON.stringify(payload)}\n`);
        child.stdin.end();
      } catch {}

      await new Promise<void>((resolve) => {
        child.once("close", () => resolve());
      });
    },
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
  let lastRenderAt = 0;

  const render = (force = false) => {
    const now = Date.now();
    if (!force && now - lastRenderAt < PROGRESS_RENDER_INTERVAL_MS) {
      return;
    }

    lastRenderAt = now;
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
      render(true);
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
      render(true);
    },
    stop() {
      render(true);
      process.stdout.write("\x1b[2K\r");
    },
  };
}

async function runInternalWorkerLoop() {
  const input = readline.createInterface({
    input: process.stdin,
    crlfDelay: Infinity,
  });

  for await (const line of input) {
    if (!line.trim()) {
      continue;
    }

    let message: WorkerMessage;
    try {
      message = JSON.parse(line) as WorkerMessage;
    } catch (error) {
      console.log(
        JSON.stringify({
          type: "error",
          id: "unknown",
          filePath: "",
          error: `Invalid worker message: ${error instanceof Error ? error.message : String(error)}`,
        } satisfies WorkerErrorMessage),
      );
      continue;
    }

    if (message.type === "stop") {
      break;
    }

    const startedAt = performance.now();
    let lastProgressStep = 0;
    let lastProgressSentAt = 0;

    try {
      const result = await scanTiff(message.filePath, message.mode, (completedSteps, totalSteps) => {
        const now = Date.now();
        const shouldSend =
          completedSteps === 1 ||
          completedSteps === totalSteps ||
          completedSteps - lastProgressStep >= WORKER_PROGRESS_STEP_INTERVAL ||
          now - lastProgressSentAt >= WORKER_PROGRESS_TIME_INTERVAL_MS;

        if (!shouldSend) {
          return;
        }

        lastProgressStep = completedSteps;
        lastProgressSentAt = now;
        const payload: WorkerProgressMessage = {
          type: "progress",
          id: message.id,
          filePath: message.filePath,
          completedSteps,
          totalSteps,
        };
        process.stdout.write(`${JSON.stringify(payload)}\n`);
      });

      const payload: WorkerResultMessage = {
        type: "result",
        id: message.id,
        result,
        durationMs: performance.now() - startedAt,
      };
      process.stdout.write(`${JSON.stringify(payload)}\n`);
    } catch (error) {
      const payload: WorkerErrorMessage = {
        type: "error",
        id: message.id,
        filePath: message.filePath,
        error: error instanceof Error ? error.message : String(error),
      };
      process.stdout.write(`${JSON.stringify(payload)}\n`);
    }
  }
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
  let skipped = 0;

  logger.log("");
  logger.log("Results:");

  for (const result of results) {
    if (result.error === "SKIPPED") {
      skipped += 1;
      console.log(`SKIPPED ${result.filePath}`);
      continue;
    }

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
  console.log(`Without QR: ${results.length - matches - failures - skipped}`);
  console.log(`Skipped: ${skipped}`);
  console.log(`Errors: ${failures}`);
  for (const line of timingSummary) {
    console.log(line);
  }

  logger.log("");
  logger.log(`Scanned: ${results.length}`);
  logger.log(`With QR: ${matches}`);
  logger.log(`Without QR: ${results.length - matches - failures - skipped}`);
  logger.log(`Skipped: ${skipped}`);
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
  if (result.error === "SKIPPED") {
    logger.log(`SKIPPED ${result.filePath}`);
    return;
  }

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

function formatFileSize(sizeBytes: number) {
  if (sizeBytes < 1024) {
    return `${sizeBytes} B`;
  }

  return `${(sizeBytes / 1024).toFixed(0)} KB`;
}

await main();
