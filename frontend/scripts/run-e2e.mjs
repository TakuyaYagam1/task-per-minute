import { accessSync, constants } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptRoot, "..");
const repositoryRoot = resolve(frontendRoot, "..");
const playwrightPath = join(
  frontendRoot,
  "node_modules",
  ".bin",
  process.platform === "win32" ? "playwright.cmd" : "playwright",
);
const securityPreflightPath = join(repositoryRoot, "scripts", "release", "verify-security-tools.sh");
const playwrightArgs = process.argv.slice(2);

function fail(message) {
  process.stderr.write(`frontend e2e: ERROR: ${message}\n`);
  process.exit(1);
}

function assertExecutable(path, label) {
  try {
    accessSync(path, constants.X_OK);
  } catch {
    fail(`${label} is unavailable: ${path}`);
  }
}

function run(command, args, options) {
  const result = spawnSync(command, args, {
    cwd: frontendRoot,
    env: process.env,
    ...options,
  });
  if (result.error) fail(`could not start ${command}: ${result.error.message}`);
  if (result.status === null) fail(`${command} ended by signal ${result.signal || "unknown"}`);
  return result;
}

function testCount(value) {
  if (Array.isArray(value)) return value.reduce((count, item) => count + testCount(item), 0);
  if (!value || typeof value !== "object") return 0;

  let count = Array.isArray(value.tests) ? value.tests.length : 0;
  for (const [key, child] of Object.entries(value)) {
    if (key !== "tests") count += testCount(child);
  }
  return count;
}

function parseDiscovery(stdout) {
  const firstObject = stdout.indexOf("{");
  const lastObject = stdout.lastIndexOf("}");
  if (firstObject < 0 || lastObject < firstObject) {
    throw new Error("Playwright discovery did not return JSON");
  }

  try {
    return JSON.parse(stdout.slice(firstObject, lastObject + 1));
  } catch (error) {
    throw new Error(`Playwright discovery returned malformed JSON: ${error.message}`);
  }
}

function runSecurityPreflight() {
  const result = run("bash", [securityPreflightPath, "--scope", "frontend"], {
    stdio: "inherit",
  });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

function verifyNonEmptySuite() {
  const result = run(
    playwrightPath,
    ["test", ...playwrightArgs, "--list", "--reporter=json"],
    {
      stdio: ["ignore", "pipe", "pipe"],
      encoding: "utf8",
    },
  );
  if (result.status !== 0) {
    try {
      const report = parseDiscovery(result.stdout || "");
      const noTestsReported = Array.isArray(report.errors) && report.errors.some(
        (error) => typeof error?.message === "string" && error.message.includes("No tests found"),
      );
      if (noTestsReported && testCount(report) < 1) {
        fail("Playwright suite is empty for the requested selection");
      }
    } catch {
      // The concise status below is more useful than replaying reporter output.
    }
    process.stderr.write(`frontend e2e: Playwright discovery failed with status ${result.status}\n`);
    process.exit(result.status ?? 1);
  }

  let report;
  try {
    report = parseDiscovery(result.stdout || "");
  } catch (error) {
    fail(error instanceof Error ? error.message : String(error));
  }

  const count = testCount(report);
  if (count < 1) {
    fail("Playwright suite is empty for the requested selection");
  }
  process.stdout.write(`frontend e2e discovery passed: ${count} test(s)\n`);
}

assertExecutable(playwrightPath, "local Playwright binary");
runSecurityPreflight();
verifyNonEmptySuite();

const result = run(playwrightPath, ["test", ...playwrightArgs], { stdio: "inherit" });
process.exit(result.status ?? 1);
