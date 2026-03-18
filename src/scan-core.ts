import jsQR from "../node_modules/jsqr/dist/jsQR.js";
import * as UTIF from "../node_modules/utif/UTIF.js";

export type ScanResult = {
  filePath: string;
  hasQrCode: boolean;
  error?: string;
};

export type ScanMode = "default" | "aggressive";

const DEFAULT_QR_SCALES = [1];
const AGGRESSIVE_FALLBACK_QR_SCALES = [0.25, 0.75, 0.4, 0.33, 0.2, 1.5];

export function estimateScanSteps(mode: ScanMode) {
  return buildRegions(100, 100).length * getScalePasses(mode).flat().length;
}

export async function scanTiff(
  filePath: string,
  mode: ScanMode,
  onProgress?: (completedSteps: number, totalSteps: number) => Promise<void> | void,
): Promise<ScanResult> {
  try {
    const buffer = await Bun.file(filePath).arrayBuffer();
    const ifds = UTIF.decode(buffer);
    const totalSteps = Math.max(1, ifds.length * estimateScanSteps(mode));
    let completedSteps = 0;

    for (let index = 0; index < ifds.length; index += 1) {
      const ifd = ifds[index];
      UTIF.decodeImage(buffer, ifd);
      const rgba = UTIF.toRGBA8(ifd);

      const pageValues = await detectQRCodes(rgba, ifd.width, ifd.height, mode, async () => {
        completedSteps += 1;
        await onProgress?.(completedSteps, totalSteps);
        if (completedSteps % 3 === 0) {
          await Bun.sleep(0);
        }
      });
      if (pageValues.length > 0) {
        return {
          filePath,
          hasQrCode: true,
        };
      }
    }

    return {
      filePath,
      hasQrCode: false,
    };
  } catch (error) {
    return {
      filePath,
      hasQrCode: false,
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

async function detectQRCodes(
  rgba: Uint8Array,
  width: number,
  height: number,
  mode: ScanMode,
  onStep?: () => Promise<void>,
): Promise<string[]> {
  const found = new Set<string>();
  const regions = buildRegions(width, height);

  for (const scales of getScalePasses(mode)) {
    for (const region of regions) {
      const cropped = cropRgba(rgba, width, region.x, region.y, region.width, region.height);

      for (const scale of scales) {
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
          return [qr.data];
        }

        await onStep?.();
      }
    }
  }

  return [...found];
}

function getScalePasses(mode: ScanMode) {
  if (mode === "aggressive") {
    return [[1, 0.5], AGGRESSIVE_FALLBACK_QR_SCALES];
  }

  return [DEFAULT_QR_SCALES];
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
