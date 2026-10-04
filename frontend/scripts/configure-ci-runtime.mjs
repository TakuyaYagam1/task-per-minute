import { accessSync, constants, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const readJSON = (path) => JSON.parse(readFileSync(path, "utf8"));

export function verifyPackages(root = frontendRoot) {
  const lock = readJSON(join(root, "package-lock.json"));
  const names = ["@playwright/test", "playwright", "playwright-core"];
  const versions = names.map((name) => {
    const expected = lock.packages?.[`node_modules/${name}`];
    const installed = readJSON(join(root, "node_modules", name, "package.json"));
    if (!expected?.integrity || installed.version !== expected.version) {
      throw new Error(`${name} does not match package-lock.json; run npm ci`);
    }
    return installed.version;
  });
  if (new Set(versions).size !== 1) throw new Error("Playwright packages have different versions");
  return versions[0];
}

function run(command, args) {
  const result = spawnSync(command, args, { encoding: "utf8", timeout: 15_000, maxBuffer: 1024 * 1024 });
  if (result.error || result.status !== 0) throw new Error(`Runtime probe failed: ${command}`);
  return result.stdout.trim();
}

export function verifyBrowser(root = frontendRoot) {
  verifyPackages(root);
  const require = createRequire(join(root, "package.json"));
  const { chromium } = require("playwright-core");
  const metadata = readJSON(join(root, "node_modules/playwright-core/browsers.json"));
  const expected = metadata.browsers.find((browser) => browser.name === "chromium");
  const executable = chromium.executablePath();
  accessSync(executable, constants.X_OK);
  const version = run(executable, ["--version"]).match(/\b\d+\.\d+\.\d+\.\d+\b/)?.[0];
  if (!expected?.browserVersion || version !== expected.browserVersion) {
    throw new Error("Chromium does not match the installed Playwright revision");
  }
  return version;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    // Use the same Node major as the application image, regardless of host OS.
    const dockerfile = readFileSync(join(frontendRoot, "Dockerfile"), "utf8");
    const nodeMajor = dockerfile.match(/^FROM node:(\d+)/m)?.[1];
    if (!nodeMajor || process.versions.node.split(".")[0] !== nodeMajor) {
      throw new Error(`Node ${nodeMajor || "image version"} is required`);
    }
    const npmVersion = run("npm", ["--version"]);
    if (!/^\d+\.\d+\.\d+$/.test(npmVersion)) throw new Error("Invalid npm version");
    if (process.argv.includes("--verify-packages")) verifyPackages();
    if (process.argv.includes("--verify-browser")) verifyBrowser();
    console.log(`Frontend runtime ready: Node ${process.versions.node}, npm ${npmVersion}`);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
