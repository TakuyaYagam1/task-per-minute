import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import ProgressReporter from "./e2e-progress.mjs";

const source = join(dirname(fileURLToPath(import.meta.url)), "run-e2e.mjs");
const sensitiveValue = "synthetic-private-assertion-value";

test("progress is emitted before completion without assertion values or private paths", (t) => {
  let output = "";
  t.mock.method(process.stderr, "write", (chunk) => { output += chunk; return true; });
  const reporter = new ProgressReporter();
  const currentTest = {
    location: { file: join(dirname(source), "../e2e/logout.spec.ts"), line: 12 },
    title: sensitiveValue,
  };
  reporter.onBegin({}, { allTests: () => [currentTest] });
  reporter.onTestBegin(currentTest, { workerIndex: 0 });
  assert.match(output, /e2e\/logout.spec.ts:12 started/);
  assert.doesNotMatch(output, /1\/1/);
  reporter.onTestEnd(currentTest, {
    status: "timedOut", duration: 60000, errors: [{ message: sensitiveValue }],
  });
  assert.match(output, /1\/1: e2e\/logout.spec.ts:12 timedOut \(60000ms\)/);
  reporter.onTestBegin({ location: { file: "/private/account-data", line: 1 } }, { workerIndex: 1 });
  assert.match(output, /unknown test started/);
  assert.equal(output.includes(sensitiveValue), false);
  assert.equal(output.includes("/private/account-data"), false);
});

function runFixture(t, { status = "expected", resultStatus = "passed", exitCode = 0, discovered = 1, errors = [] } = {}) {
  const root = mkdtempSync(join(tmpdir(), "e2e-runner-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const frontend = join(root, "frontend");
  mkdirSync(join(frontend, "scripts"), { recursive: true });
  mkdirSync(join(frontend, "node_modules/.bin"), { recursive: true });
  mkdirSync(join(root, "scripts/release"), { recursive: true });
  copyFileSync(source, join(frontend, "scripts/run-e2e.mjs"));
  writeFileSync(join(root, "scripts/release/verify-security-tools.sh"), "exit 0\n");
  const report = {
    config: { rootDir: join(frontend, "e2e") },
    suites: [{ suites: [{ specs: [{
      file: "logout.spec.ts",
      line: 12,
      column: 1,
      title: "logout returns to the login form",
      tests: [{
        status,
        expectedStatus: "passed",
        results: [{
          status: resultStatus,
          errorLocation: { file: join(frontend, "e2e/logout.spec.ts"), line: 20, column: 5 },
          errors: [{
            message: sensitiveValue,
            snippet: sensitiveValue,
            stack: sensitiveValue,
          }],
          stdout: [sensitiveValue],
          attachments: [{ name: "trace", body: sensitiveValue }],
        }],
      }],
    }] }] }],
    errors,
  };
  const playwright = join(frontend, "node_modules/.bin/playwright");
  writeFileSync(playwright, `#!/usr/bin/env node
const fs = require('node:fs');
if (process.argv.includes('--list')) {
  process.stdout.write(JSON.stringify({ suites: [{ specs: [{ tests: Array(${discovered}).fill({}) }] }] }));
} else {
  fs.writeFileSync(process.env.PLAYWRIGHT_JSON_OUTPUT_NAME, ${JSON.stringify(JSON.stringify(report))});
  process.exitCode = ${exitCode};
}
`);
  chmodSync(playwright, 0o700);
  const summaryPath = join(root, "evidence/e2e-summary.json");
  const result = spawnSync(process.execPath, [join(frontend, "scripts/run-e2e.mjs")], {
    cwd: frontend,
    env: { PATH: process.env.PATH, TMPDIR: root, E2E_SUMMARY_FILE: summaryPath },
    encoding: "utf8",
  });
  assert.equal(result.error, undefined);
  return { result, summaryPath };
}

test("failed execution identifies the test and assertion location without disclosing values", (t) => {
  const { result, summaryPath } = runFixture(t, { status: "unexpected", resultStatus: "failed", exitCode: 1 });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /frontend e2e failure:/);
  assert.match(result.stderr, /logout returns to the login form/);
  assert.match(result.stderr, /"line":20/);
  const summary = readFileSync(summaryPath, "utf8");
  assert.equal(JSON.parse(summary).tests[0].attempts[0].status, "failed");
  assert.equal(JSON.parse(summary).tests[0].file, "e2e/logout.spec.ts");
  assert.equal(statSync(summaryPath).mode & 0o077, 0);
  for (const output of [result.stdout, result.stderr, summary]) {
    assert.equal(output.includes(sensitiveValue), false);
  }
});

test("passing execution saves its summary and succeeds", (t) => {
  const { result, summaryPath } = runFixture(t);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /execution passed: 1 test/);
  assert.equal(JSON.parse(readFileSync(summaryPath, "utf8")).tests[0].status, "expected");
});

for (const [name, fixture, diagnostic] of [
  ["skipped test", { status: "skipped", resultStatus: "skipped" }, /skipped 1 test/],
  ["flaky test", { status: "flaky" }, /unexpected or flaky/],
  ["discovery mismatch", { discovered: 2 }, /differs from discovery count/],
]) {
  test(`diagnostics do not allow a ${name} to pass`, (t) => {
    const { result } = runFixture(t, fixture);
    assert.equal(result.status, 1);
    assert.match(result.stderr, diagnostic);
  });
}

test("global failures expose only counts and project source locations", (t) => {
  const { result, summaryPath } = runFixture(t, {
    exitCode: 1,
    errors: [{ message: sensitiveValue, location: { file: "/private/account-data", line: 1 } }],
  });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /runner errors: 1/);
  assert.deepEqual(JSON.parse(readFileSync(summaryPath, "utf8")).error_locations, []);
  assert.equal(result.stderr.includes(sensitiveValue), false);
  assert.equal(result.stderr.includes("/private/account-data"), false);
});
