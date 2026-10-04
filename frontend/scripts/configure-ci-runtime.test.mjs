import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { verifyBrowser, verifyPackages } from "./configure-ci-runtime.mjs";

function fixture(t, { installedVersion = "1.59.1", browserVersion = "147.0.7727.15", missingBrowser = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), "frontend-runtime-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const writeJSON = (path, value) => writeFileSync(path, JSON.stringify(value));
  const packages = {};
  for (const name of ["@playwright/test", "playwright", "playwright-core"]) {
    const directory = join(root, "node_modules", name);
    mkdirSync(directory, { recursive: true });
    packages[`node_modules/${name}`] = { version: "1.59.1", integrity: "sha512-fixture" };
    writeJSON(join(directory, "package.json"), { name, version: installedVersion, main: "index.cjs" });
  }
  writeJSON(join(root, "package-lock.json"), { packages });
  writeJSON(join(root, "node_modules/playwright-core/browsers.json"), {
    browsers: [{ name: "chromium", browserVersion: "147.0.7727.15" }],
  });
  const executable = join(root, "chromium");
  writeFileSync(join(root, "node_modules/playwright-core/index.cjs"),
    `exports.chromium = { executablePath: () => ${JSON.stringify(executable)} };`);
  if (!missingBrowser) {
    writeFileSync(executable, `#!/bin/sh\nprintf '%s\\n' 'Chromium ${browserVersion}'\n`);
    chmodSync(executable, 0o700);
  }
  return root;
}

test("accepts installed packages and the Playwright browser revision", (t) => {
  const root = fixture(t);
  assert.equal(verifyPackages(root), "1.59.1");
  assert.equal(verifyBrowser(root), "147.0.7727.15");
});

test("rejects installed packages that differ from the lock", (t) => {
  assert.throws(() => verifyPackages(fixture(t, { installedVersion: "1.60.0" })), /does not match package-lock/);
});

test("rejects a missing browser", (t) => {
  assert.throws(() => verifyBrowser(fixture(t, { missingBrowser: true })), /ENOENT/);
});

test("rejects a browser from a different Playwright revision", (t) => {
  assert.throws(() => verifyBrowser(fixture(t, { browserVersion: "149.0.7827.55" })), /does not match/);
});
