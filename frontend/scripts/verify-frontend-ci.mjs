import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptRoot, "..");
const repositoryRoot = resolve(frontendRoot, "..");
const reportDirectory = resolve(
  process.env.FRONTEND_CI_REPORT_DIR || join(tmpdir(), "task-per-minute-frontend-ci"),
);
const reportPath = join(reportDirectory, "frontend-verify.json");
const npmCommand = process.platform === "win32" ? "npm.cmd" : "npm";

mkdirSync(reportDirectory, { recursive: true });

function gitProbe(args) {
  const result = spawnSync("git", args, {
    cwd: repositoryRoot,
    encoding: "utf8",
  });
  if (result.error || result.status !== 0) return null;
  return result.stdout.trim();
}

function sourceIdentity() {
  const revision = gitProbe(["rev-parse", "HEAD"]);
  const status = gitProbe(["status", "--porcelain=v1", "--untracked-files=no"]);
  return {
    revision: revision && /^[0-9a-f]{40}$/.test(revision) ? revision : null,
    modified: status === null ? null : status.length > 0,
  };
}

const identity = sourceIdentity();
const report = {
  schema_version: 1,
  source_revision: identity.revision,
  source_modified: identity.modified,
  status: "not_started",
  exit_code: null,
  steps: [],
};

function writeReport() {
  writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`, {
    encoding: "utf8",
    mode: 0o600,
  });
}

function commandText(command, args) {
  return [command, ...args].join(" ");
}

function runCommand(name, command, args, cwd = frontendRoot) {
  const startedAt = new Date().toISOString();
  const result = spawnSync(command, args, {
    cwd,
    env: process.env,
    stdio: "inherit",
  });
  const exitCode = result.error ? 1 : result.status === null ? 1 : result.status;
  report.steps.push({
    name,
    command: commandText(command, args),
    status: exitCode === 0 ? "pass" : "fail",
    exit_code: exitCode,
    signal: result.signal || null,
    started_at: startedAt,
    finished_at: new Date().toISOString(),
  });
  writeReport();
  if (exitCode !== 0) {
    throw new Error(`${name} failed with exit code ${exitCode}`);
  }
}

function verifyProductionBuild() {
  const manifestPath = join(frontendRoot, ".next", "server", "app-paths-manifest.json");
  if (!existsSync(manifestPath)) {
    throw new Error("production app paths manifest is missing");
  }

  let manifest;
  try {
    manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  } catch (error) {
    throw new Error(`production app paths manifest is invalid: ${error.message}`);
  }

  const paths = Object.keys(manifest);
  const requiredRoutes = [
    "/page",
    "/admin/page",
    "/leaderboard/page",
    "/health/route",
    "/csp-report/route",
  ];
  const missingRoutes = requiredRoutes.filter((route) => !paths.includes(route));
  if (missingRoutes.length > 0) {
    throw new Error(`production route manifest is missing: ${missingRoutes.join(", ")}`);
  }

  const forbiddenMarkers = ["e2e/", "fixtures/", "lib/pages/home", "legacy"];
  const forbiddenPaths = paths.filter((path) =>
    forbiddenMarkers.some((marker) => path.toLowerCase().includes(marker)),
  );
  if (forbiddenPaths.length > 0) {
    throw new Error(`production route manifest contains forbidden entries: ${forbiddenPaths.join(", ")}`);
  }

  const forbiddenBundleMarkers = [
    "lib/pages/home",
    "frontend/e2e",
    "e2e/fixtures",
    "__fixture__",
    "frontend/legacy",
    "/legacy/",
  ];
  const bundleFiles = [];
  const cacheDirectory = join(frontendRoot, ".next", "cache");
  function visitBundle(directory) {
    for (const entry of readdirSync(directory)) {
      const path = join(directory, entry);
      if (path === cacheDirectory || path.startsWith(`${cacheDirectory}/`)) continue;
      if (statSync(path).isDirectory()) visitBundle(path);
      else bundleFiles.push(path);
    }
  }
  const buildDirectories = [
    join(frontendRoot, ".next", "server", "app"),
    join(frontendRoot, ".next", "server", "chunks"),
    join(frontendRoot, ".next", "server", "pages"),
    join(frontendRoot, ".next", "server", "vendor-chunks"),
    join(frontendRoot, ".next", "static"),
  ];
  for (const directory of buildDirectories) {
    if (existsSync(directory)) visitBundle(directory);
  }
  const forbiddenBundleFiles = [];
  for (const path of bundleFiles) {
    const source = readFileSync(path, "utf8");
    if (forbiddenBundleMarkers.some((marker) => source.includes(marker))) {
      forbiddenBundleFiles.push(path);
    }
  }
  if (forbiddenBundleFiles.length > 0) {
    throw new Error(
      `production bundle contains fixture or legacy modules: ${forbiddenBundleFiles
        .map((path) => path.slice(frontendRoot.length + 1))
        .join(", ")}`,
    );
  }

  report.steps.push({
    name: "production routes",
    command: "inspect .next/server/app-paths-manifest.json",
    status: "pass",
    exit_code: 0,
    route_count: paths.length,
    started_at: new Date().toISOString(),
    finished_at: new Date().toISOString(),
  });
  writeReport();
}

function failWithReport(error, exitCode = 1) {
  report.status = "failed";
  report.exit_code = exitCode;
  report.error = error instanceof Error ? error.message : String(error);
  report.finished_at = new Date().toISOString();
  writeReport();
  process.stderr.write(`frontend CI verification: ERROR: ${report.error}\n`);
  process.exit(exitCode);
}

for (const [signal, exitCode] of [["SIGINT", 130], ["SIGTERM", 143]]) {
  process.once(signal, () => failWithReport(`verification interrupted by ${signal}`, exitCode));
}

if (process.argv.includes("--init")) {
  report.status = "not_started";
  writeReport();
  process.stdout.write(`frontend CI evidence initialized: ${reportPath}\n`);
  process.exit(0);
}

if (process.argv.includes("--finalize")) {
  let shouldWriteReport = false;
  if (existsSync(reportPath)) {
    try {
      Object.assign(report, JSON.parse(readFileSync(reportPath, "utf8")));
    } catch {
      report.status = "failed";
      report.exit_code = 1;
      report.error = "frontend CI evidence is unreadable";
      report.finished_at = new Date().toISOString();
      shouldWriteReport = true;
    }
  }
  if (report.status === "not_started" || report.status === "running") {
    report.status = "failed";
    report.exit_code = 1;
    report.error = process.env.FRONTEND_CI_ERROR || "frontend CI stopped before verification completed";
    report.finished_at = new Date().toISOString();
    shouldWriteReport = true;
  }
  if (shouldWriteReport) {
    writeReport();
  }
  process.stdout.write(`frontend CI evidence finalized: ${reportPath}\n`);
  process.exit(0);
}

try {
  report.status = "running";
  writeReport();
  runCommand("frontend baseline", npmCommand, ["run", "validate:frontend"]);
  runCommand("generated REST types", npmCommand, ["run", "openapi:generate"]);
  runCommand(
    "generated schema drift",
    "git",
    ["diff", "--exit-code", "--", "frontend/lib/shared/api/schema.ts"],
    repositoryRoot,
  );
  runCommand("typecheck", npmCommand, ["run", "typecheck"]);
  runCommand("lint", npmCommand, ["run", "lint"]);
  runCommand("production build", npmCommand, ["run", "build"]);
  verifyProductionBuild();
  runCommand("mocked browser suite", npmCommand, ["run", "test:e2e", "--", "--max-failures=10"]);
  report.status = "passed";
  report.exit_code = 0;
  report.finished_at = new Date().toISOString();
  writeReport();
  process.stdout.write(`frontend CI verification passed: ${reportPath}\n`);
} catch (error) {
  failWithReport(error);
}
