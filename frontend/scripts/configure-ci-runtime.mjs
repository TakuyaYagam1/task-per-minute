import { accessSync, appendFileSync, constants, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptRoot, "..");
const repositoryRoot = resolve(frontendRoot, "..");
const lockPath = join(repositoryRoot, "security", "tools", "release-tools.lock.json");
const lock = JSON.parse(readFileSync(lockPath, "utf8"));
const tools = new Map(lock.tools.map((tool) => [tool.name, tool]));

function executablePath(name) {
  const tool = tools.get(name);
  if (!tool || tool.provisioning?.kind !== "nix_store") {
    throw new Error(`${name} is not backed by the reviewed Nix runtime`);
  }
  const path = join(tool.provisioning.immutable_root, tool.provisioning.relative_path);
  try {
    accessSync(path, constants.X_OK);
  } catch {
    throw new Error(`reviewed ${name} runtime is unavailable: ${path}`);
  }
  return path;
}

function run(path, args) {
  const result = spawnSync(path, args, { encoding: "utf8" });
  if (result.error || result.status !== 0) {
    throw new Error(`reviewed runtime probe failed: ${path}`);
  }
  return result.stdout.trim();
}

const nodePath = executablePath("node");
const npmPath = executablePath("npm");
const playwrightPath = executablePath("playwright");
const chromiumPath = executablePath("chromium");
const npmTool = tools.get("npm");
const playwrightTool = tools.get("playwright");

if (run(nodePath, ["--version"]) !== `v${tools.get("node").version}`) {
  throw new Error("reviewed Node runtime identity mismatch");
}
if (run(nodePath, [npmPath, "--version"]) !== npmTool.runtime_identity.expected_output) {
  throw new Error("reviewed npm runtime identity mismatch");
}
if (run(chromiumPath, ["--version"]) !== tools.get("chromium").runtime_identity.expected_output) {
  throw new Error("reviewed Chromium runtime identity mismatch");
}

if (process.argv.includes("--verify-packages")) {
  const localPlaywrightCli = join(frontendRoot, "node_modules", "playwright", "cli.js");
  try {
    accessSync(localPlaywrightCli, constants.R_OK);
  } catch {
    throw new Error(`installed Playwright CLI is unavailable: ${localPlaywrightCli}`);
  }
  if (run(nodePath, [localPlaywrightCli, "--version"]) !== playwrightTool.runtime_identity.expected_output) {
    throw new Error("installed Playwright runtime identity mismatch");
  }
}

const npmBinPath = join(npmTool.provisioning.immutable_root, "bin");
const pathEntries = [...new Set([dirname(nodePath), npmBinPath])];
if (process.env.GITHUB_PATH) {
  appendFileSync(process.env.GITHUB_PATH, `${pathEntries.join("\n")}\n`, "utf8");
}

process.stdout.write(
  `reviewed frontend runtime ready: node=${nodePath}, npm=${npmPath}, playwright=${playwrightPath}, chromium=${chromiumPath}${process.argv.includes("--verify-packages") ? " (package verified)" : ""}\n`,
);
