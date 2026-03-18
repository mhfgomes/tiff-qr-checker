import { scanTiff, type ScanMode, type ScanResult } from "./scan-core";

declare var self: Worker;

type ScanJobMessage = {
  type: "scan";
  filePath: string;
  mode: ScanMode;
};

type StopMessage = {
  type: "stop";
};

type WorkerMessage = ScanJobMessage | StopMessage;

type ProgressEvent = {
  type: "progress";
  filePath: string;
  completedSteps: number;
  totalSteps: number;
};

type ResultEvent = {
  type: "result";
  result: ScanResult;
};

type ErrorEvent = {
  type: "error";
  filePath: string;
  error: string;
};

self.onmessage = async (event: MessageEvent<WorkerMessage>) => {
  const message = event.data;

  if (message.type === "stop") {
    self.close();
    return;
  }

  if (message.type !== "scan") {
    return;
  }

  try {
    const result = await scanTiff(message.filePath, message.mode, (completedSteps, totalSteps) => {
      postMessage({
        type: "progress",
        filePath: message.filePath,
        completedSteps,
        totalSteps,
      } satisfies ProgressEvent);
    });

    postMessage({
      type: "result",
      result,
    } satisfies ResultEvent);
  } catch (error) {
    postMessage({
      type: "error",
      filePath: message.filePath,
      error: error instanceof Error ? error.message : String(error),
    } satisfies ErrorEvent);
  }
};
