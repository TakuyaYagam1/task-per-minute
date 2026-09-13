import { accessSync, constants, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptRoot, "..");
const packagePath = join(frontendRoot, "package.json");
const lockPath = join(frontendRoot, "package-lock.json");
const architecturePath = join(scriptRoot, "frontend-architecture.json");

function fail(message) {
  process.stderr.write(`frontend baseline: ERROR: ${message}\n`);
  process.exit(1);
}

function readJson(path, label) {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    fail(`cannot read ${label}: ${error.message}`);
  }
}

const packageJson = readJson(packagePath, "package.json");
const packageLock = readJson(lockPath, "package-lock.json");
const architecture = readJson(architecturePath, "frontend architecture baseline");
const lockRoot = packageLock.packages?.[""];

if (packageLock.lockfileVersion !== 3) fail("package-lock.json must use lockfileVersion 3");
if (!lockRoot || lockRoot.name !== packageJson.name || lockRoot.version !== packageJson.version) {
  fail("package-lock root metadata does not match package.json");
}

const requiredScripts = [
  { name: "typecheck", marker: "tsc", packageName: "typescript" },
  { name: "lint", marker: "eslint", packageName: "eslint" },
  { name: "build", marker: "next build", packageName: "next" },
  { name: "test:e2e", marker: "run-e2e.mjs", packageName: "@playwright/test" },
];

for (const requirement of requiredScripts) {
  const command = packageJson.scripts?.[requirement.name];
  if (typeof command !== "string" || command.trim() === "") {
    fail(`required npm script is missing: ${requirement.name}`);
  }
  if (!command.includes(requirement.marker)) {
    fail(`npm script ${requirement.name} does not invoke ${requirement.marker}`);
  }

  const declared = packageJson.dependencies?.[requirement.packageName] ?? packageJson.devDependencies?.[requirement.packageName];
  const lockedDeclared = lockRoot.dependencies?.[requirement.packageName] ?? lockRoot.devDependencies?.[requirement.packageName];
  if (typeof declared !== "string" || declared !== lockedDeclared) {
    fail(`${requirement.packageName} declaration differs between package.json and package-lock.json`);
  }

  const lockEntry = packageLock.packages?.[`node_modules/${requirement.packageName}`];
  if (!lockEntry || typeof lockEntry.version !== "string") {
    fail(`package-lock entry is missing for ${requirement.packageName}`);
  }
}

const wrapperPath = join(frontendRoot, "scripts", "run-e2e.mjs");
const wrapperSource = readFileSync(wrapperPath, "utf8");
if (!wrapperSource.includes("verify-security-tools.sh") || !wrapperSource.includes("--scope") || !wrapperSource.includes("node_modules") || !wrapperSource.includes("playwright")) {
  fail("test:e2e wrapper must run the canonical frontend security preflight and local Playwright binary");
}

for (const packageName of ["playwright", "playwright-core"]) {
  const lockEntry = packageLock.packages?.[`node_modules/${packageName}`];
  if (!lockEntry || typeof lockEntry.version !== "string") {
    fail(`package-lock Playwright runtime entry is missing for ${packageName}`);
  }
}

for (const binary of [
  join(frontendRoot, "node_modules", ".bin", process.platform === "win32" ? "tsc.cmd" : "tsc"),
  join(frontendRoot, "node_modules", ".bin", process.platform === "win32" ? "eslint.cmd" : "eslint"),
  join(frontendRoot, "node_modules", ".bin", process.platform === "win32" ? "next.cmd" : "next"),
  join(frontendRoot, "node_modules", ".bin", process.platform === "win32" ? "playwright.cmd" : "playwright"),
]) {
  try {
    accessSync(binary, constants.X_OK);
  } catch {
    fail(`installed command binary is unavailable: ${binary}`);
  }
}

if (
  architecture.schema_version !== 1
  || !Array.isArray(architecture.layer_order)
  || architecture.layer_order.join(",") !== "app,pages,widgets,features,entities,shared"
  || !Array.isArray(architecture.routes)
  || architecture.routes.length < 1
  || !Array.isArray(architecture.legacy_entrypoints)
  || !Array.isArray(architecture.preserved_public_surfaces)
) {
  fail("frontend architecture baseline has an invalid structure");
}

const trackedPaths = new Set([
  ...architecture.routes.flatMap((route) => [route.entry, route.owner]),
  ...architecture.legacy_entrypoints.map((entry) => entry.path),
  ...architecture.preserved_public_surfaces,
]);

for (const relativePath of trackedPaths) {
  if (typeof relativePath !== "string" || relativePath.trim() === "") {
    fail("frontend architecture baseline contains an invalid path");
  }
  try {
    accessSync(join(frontendRoot, relativePath), constants.R_OK);
  } catch {
    fail(`frontend architecture baseline path is missing: ${relativePath}`);
  }
}

process.stdout.write(
  `frontend baseline passed: commands, lockfile, ${architecture.routes.length} routes, and module boundaries are aligned\n`,
);
