import path from "node:path";

const rootDir = process.cwd();

async function main() {
  await patchUtif();

  if (process.platform === "win32") {
    await normalizeWindowsFile("node_modules/jsqr/dist/jsQR.js");
    await normalizeWindowsFile("node_modules/utif/UTIF.js");
    await normalizeWindowsFile("node_modules/pako/dist/pako.js");
  }
}

async function patchUtif() {
  const utifPath = path.join(rootDir, "node_modules", "utif", "UTIF.js");
  const file = Bun.file(utifPath);
  if (!(await file.exists())) {
    return;
  }

  const content = await file.text();
  const nextContent = content.replace(
    'if (typeof require == "function") {pako = require("pako");}',
    'if (typeof require == "function") {pako = require("../pako/dist/pako.js");}',
  );

  if (nextContent !== content) {
    await Bun.write(utifPath, nextContent);
  }
}

async function normalizeWindowsFile(relativePath: string) {
  const fullPath = path.join(rootDir, relativePath);
  const file = Bun.file(fullPath);
  if (!(await file.exists())) {
    return;
  }

  const content = await file.bytes();
  await Bun.write(fullPath, content);
}

await main();
