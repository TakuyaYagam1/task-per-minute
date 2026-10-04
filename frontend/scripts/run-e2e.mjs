import { accessSync, chmodSync, constants, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";

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

function withoutReporterArgs(args) {
  const result = [];
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === "--reporter") {
      index += 1;
      continue;
    }
    if (args[index].startsWith("--reporter=")) continue;
    result.push(args[index]);
  }
  return result;
}

function executionTests(report) {
  const tests = [];
  function visit(value) {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value.specs)) {
      for (const spec of value.specs) {
        if (Array.isArray(spec?.tests)) tests.push(...spec.tests);
      }
    }
    if (Array.isArray(value.suites)) value.suites.forEach(visit);
  }
  visit(report);
  return tests;
}

function reportExecutionSummary(report) {
  // Never persist assertion values, source snippets, attachments, cookies or response bodies.
  const location = (value) => {
    if (typeof value?.file !== "string") return null;
    const file = relative(frontendRoot, resolve(report.config?.rootDir || frontendRoot, value.file));
    if (!file || file.startsWith("..") || isAbsolute(file)) return null;
    return {
      file,
      line: Number.isSafeInteger(value.line) ? value.line : null,
      column: Number.isSafeInteger(value.column) ? value.column : null,
    };
  };
  const tests = [];
  function visit(suite) {
    for (const spec of suite.specs ?? []) {
      for (const test of spec.tests ?? []) {
        const results = test.results ?? [];
        tests.push({
          ...location(spec),
          title: typeof spec.title === "string" ? spec.title.slice(0, 300) : "unknown test",
          status: test.status,
          expected_status: test.expectedStatus,
          attempts: results.map((result) => ({
            status: result.status,
            error_locations: [result.errorLocation, ...(result.errors ?? []).map((error) => error.location)]
              .map(location).filter(Boolean),
          })),
        });
      }
    }
    for (const child of suite.suites ?? []) visit(child);
  }
  visit(report);
  const summary = {
    schema_version: 1,
    tests,
    error_count: report.errors?.length ?? 0,
    error_locations: (report.errors ?? []).map((error) => location(error.location)).filter(Boolean),
  };
  if (process.env.E2E_SUMMARY_FILE) {
    const path = resolve(process.env.E2E_SUMMARY_FILE);
    mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
    writeFileSync(path, `${JSON.stringify(summary, null, 2)}\n`, { mode: 0o600 });
    chmodSync(path, 0o600);
  }
  for (const test of tests.filter((item) => item.status === "unexpected" || item.status === "flaky").slice(0, 10)) {
    process.stderr.write(`frontend e2e failure: ${JSON.stringify(test)}\n`);
  }
  if (summary.error_count > 0) {
    process.stderr.write(`frontend e2e runner errors: ${summary.error_count}; locations: ${JSON.stringify(summary.error_locations)}\n`);
  }
}

function parseExecutionReport(outputPath, stdout) {
  let raw;
  try {
    raw = readFileSync(outputPath, "utf8");
  } catch {
    raw = stdout;
  }
  if (!raw || raw.trim() === "") {
    throw new Error("Playwright execution did not produce JSON results");
  }
  try {
    return JSON.parse(raw);
  } catch (error) {
    throw new Error(`Playwright execution returned malformed JSON: ${error.message}`);
  }
}

function assertExecution(report, discoveredCount) {
  const tests = executionTests(report);
  if (tests.length < 1) throw new Error("Playwright execution contained no tests");
  if (tests.length !== discoveredCount) {
    throw new Error(
      `Playwright execution count ${tests.length} differs from discovery count ${discoveredCount}`,
    );
  }

  const skipped = tests.filter((test) => {
    const lastResult = test.results?.at(-1);
    return test.expectedStatus === "skipped"
      || test.status === "skipped"
      || lastResult?.status === "skipped";
  });
  if (skipped.length > 0) {
    throw new Error(`Playwright execution skipped ${skipped.length} test(s)`);
  }

  const notExecuted = tests.filter((test) => !Array.isArray(test.results) || test.results.length < 1);
  if (notExecuted.length > 0) {
    throw new Error(`Playwright execution did not execute ${notExecuted.length} discovered test(s)`);
  }

  const unexpected = tests.filter((test) => test.status === "unexpected" || test.status === "flaky");
  if (unexpected.length > 0) {
    throw new Error(`Playwright execution has ${unexpected.length} unexpected or flaky test(s)`);
  }
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
  return count;
}

assertExecutable(playwrightPath, "local Playwright binary");
runSecurityPreflight();
const discoveredCount = verifyNonEmptySuite();
const executionRoot = mkdtempSync(join(tmpdir(), "task-per-minute-e2e-"));
const executionReportPath = join(executionRoot, "results.json");
let executionError = null;

try {
  const result = run(
    playwrightPath,
    ["test", ...withoutReporterArgs(playwrightArgs), "--reporter=json"],
    {
      env: {
        ...process.env,
        PLAYWRIGHT_JSON_OUTPUT_NAME: executionReportPath,
      },
      stdio: ["ignore", "ignore", "pipe"],
      encoding: "utf8",
    },
  );
  if (result.stderr) process.stderr.write(result.stderr);
  const report = parseExecutionReport(executionReportPath, "");
  reportExecutionSummary(report);
  if (result.status !== 0) {
    throw new Error(`Playwright execution failed with status ${result.status}`);
  }
  assertExecution(report, discoveredCount);
} catch (error) {
  executionError = error instanceof Error ? error.message : String(error);
} finally {
  rmSync(executionRoot, { recursive: true, force: true });
}

if (executionError) fail(executionError);
process.stdout.write(`frontend e2e execution passed: ${discoveredCount} test(s)\n`);
