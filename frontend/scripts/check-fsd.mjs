import { accessSync, constants, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptRoot, "..");
const eslintPath = join(frontendRoot, "node_modules", ".bin", process.platform === "win32" ? "eslint.cmd" : "eslint");
const architecturePath = join(scriptRoot, "frontend-architecture.json");
const architecture = JSON.parse(readFileSync(architecturePath, "utf8"));
const layers = architecture.layer_order;

if (
  !Array.isArray(layers)
  || layers.join(",") !== "app,pages,widgets,features,entities,shared"
) {
  throw new Error("frontend architecture layer order is invalid");
}

function fail(message) {
  process.stderr.write(`frontend FSD check: ERROR: ${message}\n`);
  process.exitCode = 1;
}

function restrictedPatterns(layersToRestrict) {
  return layersToRestrict.flatMap((layer) => [
    `@/${layer}`,
    `@/${layer}/**`,
    `**/lib/${layer}`,
    `**/lib/${layer}/**`,
    ...Array.from({ length: 8 }, (_, index) => {
      const prefix = "../".repeat(index + 1);
      return [`${prefix}${layer}`, `${prefix}${layer}/**`];
    }).flat(),
  ]);
}

function ruleFor(layersToRestrict) {
  return `no-restricted-imports:${JSON.stringify([
    "error",
    {
      patterns: [
        {
          group: restrictedPatterns(layersToRestrict),
          message: "imports must follow app -> pages -> widgets -> features -> entities -> shared",
        },
      ],
    },
  ])}`;
}

function runEslint(target, layersToRestrict, { synthetic = false } = {}) {
  const args = [
    ...(synthetic
      ? [
          "--no-eslintrc",
          "--env",
          "es2021",
          "--parser-options",
          JSON.stringify({ ecmaVersion: 2022, sourceType: "module" }),
        ]
      : [
          "--ext",
          ".js,.jsx,.ts,.tsx",
          "--max-warnings=0",
          "--ignore-pattern",
          ".next",
          "--ignore-pattern",
          "node_modules",
          "--ignore-pattern",
          "lib/shared/api/schema.ts",
          "--no-error-on-unmatched-pattern",
        ]),
    "--rule",
    ruleFor(layersToRestrict),
    target,
  ];

  return spawnSync(eslintPath, args, {
    cwd: frontendRoot,
    encoding: "utf8",
  });
}

function assertProcessStarted(result, label) {
  if (result.error) {
    throw new Error(`${label} could not start: ${result.error.message}`);
  }
  if (result.status === null) {
    throw new Error(`${label} ended by signal ${result.signal || "unknown"}`);
  }
}

function runActualChecks() {
  try {
    accessSync(eslintPath, constants.X_OK);
  } catch {
    throw new Error(`local ESLint binary is unavailable: ${eslintPath}`);
  }

  for (const [index, layer] of layers.entries()) {
    if (index === 0) continue;
    const target = layer === "app" ? "app" : `lib/${layer}`;
    const result = runEslint(target, layers.slice(0, index));
    assertProcessStarted(result, `${layer} import check`);
    if (result.status !== 0) {
      process.stdout.write(result.stdout || "");
      process.stderr.write(result.stderr || "");
      throw new Error(`${layer} imports violate the FSD dependency direction`);
    }
  }
}

function runSyntheticChecks() {
  const fixtureRoot = mkdtempSync(join(tmpdir(), "task-per-minute-fsd-"));
  const fixtureLayer = join(fixtureRoot, "entities");
  const validPath = join(fixtureLayer, "valid.js");
  const reversePath = join(fixtureLayer, "reverse.js");

  try {
    mkdirSync(fixtureLayer, { recursive: true });
    writeFileSync(validPath, "import value from '@/shared/value';\nexport default value;\n", "utf8");
    writeFileSync(reversePath, "import value from '@/widgets/value';\nexport default value;\n", "utf8");

    const validResult = runEslint(validPath, ["app", "pages", "widgets", "features"], { synthetic: true });
    assertProcessStarted(validResult, "synthetic valid import check");
    if (validResult.status !== 0) {
      process.stdout.write(validResult.stdout || "");
      process.stderr.write(validResult.stderr || "");
      throw new Error("synthetic valid import was rejected");
    }

    const reverseResult = runEslint(reversePath, ["app", "pages", "widgets", "features"], { synthetic: true });
    assertProcessStarted(reverseResult, "synthetic reverse import check");
    const reverseOutput = `${reverseResult.stdout || ""}${reverseResult.stderr || ""}`;
    if (reverseResult.status === 0 || !reverseOutput.includes("no-restricted-imports")) {
      process.stdout.write(reverseResult.stdout || "");
      process.stderr.write(reverseResult.stderr || "");
      throw new Error("synthetic reverse import was not rejected by no-restricted-imports");
    }
  } finally {
    rmSync(fixtureRoot, { recursive: true, force: true });
  }
}

try {
  runActualChecks();
  runSyntheticChecks();
  process.stdout.write("frontend FSD check passed: actual imports and synthetic direction cases\n");
} catch (error) {
  fail(error instanceof Error ? error.message : String(error));
}
