#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(scriptDir, "../..");
const frontendRoot = resolve(
  process.env.DEPENDENCY_AUDIT_FRONTEND_ROOT || resolve(repositoryRoot, "frontend"),
);
const userConfig = resolve(frontendRoot, "config/npm-empty-userconfig");
const exceptionPath = process.env.DEPENDENCY_AUDIT_EXCEPTIONS ||
  resolve(frontendRoot, "config/npm-audit-exceptions.json");
const npmCommand = process.env.NPM_BIN || "npm";
let expectedDependencyTotal;
try {
  const lockfile = JSON.parse(readFileSync(resolve(frontendRoot, "package-lock.json"), "utf8"));
  expectedDependencyTotal = Object.keys(lockfile.packages || {}).filter((name) => name !== "").length;
} catch (error) {
  fail(`cannot read package-lock.json: ${error.message}`);
}
if (!Number.isInteger(expectedDependencyTotal) || expectedDependencyTotal < 1) {
  fail("package-lock.json has no resolved dependency entries");
}

function fail(message) {
  process.stderr.write(`dependency advisory validation: ${message}\n`);
  process.exit(1);
}

function audit(name, argumentsList) {
  const command = process.env.NPM_NODE && process.env.NPM_CLI ? process.env.NPM_NODE : npmCommand;
  const commandArguments = process.env.NPM_NODE && process.env.NPM_CLI
    ? [process.env.NPM_CLI, "audit", ...argumentsList, "--json"]
    : ["audit", ...argumentsList, "--json"];
  const result = spawnSync(command, commandArguments, {
    cwd: frontendRoot,
    encoding: "utf8",
    env: {
      HOME: "/nonexistent",
      LANG: "C",
      LC_ALL: "C",
      NO_COLOR: "1",
      PATH: process.env.AUDIT_RUNTIME_PATH || "/run/current-system/sw/bin:/usr/bin:/bin",
      npm_config_ignore_scripts: "true",
      npm_config_userconfig: userConfig,
      npm_config_registry: "https://registry.npmjs.org/",
    },
  });
  if (result.error) fail(`cannot execute npm audit: ${result.error.message}`);
  if (result.status !== 0 && result.status !== 1) {
    fail(`npm audit failed with status ${result.status}: ${result.stderr.trim()}`);
  }
  if (process.env.DEPENDENCY_AUDIT_REPORT_DIR) {
    writeFileSync(
      resolve(process.env.DEPENDENCY_AUDIT_REPORT_DIR, `${name}.json`),
      result.stdout,
      { mode: 0o600 },
    );
  }
  try {
    const report = JSON.parse(result.stdout);
    if (!report || typeof report !== "object" || Array.isArray(report)) fail("npm audit returned a non-object report");
    if (report.error) fail("npm audit returned an error object");
    if (report.auditReportVersion !== 2 || !report.metadata || typeof report.metadata !== "object") {
      fail("npm audit report is incomplete");
    }
    if (!report.metadata.dependencies || typeof report.metadata.dependencies !== "object") {
      fail("npm audit dependency scope is missing");
    }
    if (!Number.isInteger(report.metadata.dependencies.total)) {
      fail("npm audit dependency total is missing");
    }
    for (const field of ["prod", "dev", "optional", "peer", "peerOptional", "total"]) {
      if (!Number.isInteger(report.metadata.dependencies[field]) || report.metadata.dependencies[field] < 0) {
        fail(`npm audit dependency scope field is missing: ${field}`);
      }
    }
    if (!report.vulnerabilities || typeof report.vulnerabilities !== "object" || Array.isArray(report.vulnerabilities)) {
      fail("npm audit vulnerability scope is missing");
    }
    for (const field of ["info", "low", "moderate", "high", "critical", "total"]) {
      if (!Number.isInteger(report.metadata.vulnerabilities?.[field]) || report.metadata.vulnerabilities[field] < 0) {
        fail(`npm audit vulnerability scope field is missing: ${field}`);
      }
    }
    const severityCounts = { info: 0, low: 0, moderate: 0, high: 0, critical: 0 };
    for (const [packageName, vulnerability] of Object.entries(report.vulnerabilities)) {
      if (!vulnerability || typeof vulnerability !== "object" || !Object.hasOwn(severityCounts, vulnerability.severity)) {
        fail("npm audit vulnerability entry is malformed: " + packageName);
      }
      if (!Array.isArray(vulnerability.via) || vulnerability.via.length === 0) {
        fail("npm audit vulnerability entry has no causes: " + packageName);
      }
      severityCounts[vulnerability.severity] += 1;
    }
    for (const field of Object.keys(severityCounts)) {
      if (report.metadata.vulnerabilities[field] !== severityCounts[field]) {
        fail("npm audit vulnerability counter disagrees with findings: " + field);
      }
    }
    if (report.metadata.vulnerabilities.total !== Object.keys(report.vulnerabilities).length) {
      fail("npm audit vulnerability total disagrees with findings");
    }
    if (report.metadata.dependencies.total !== expectedDependencyTotal) {
      fail(`npm audit dependency total does not cover package-lock.json: ${report.metadata.dependencies.total} != ${expectedDependencyTotal}`);
    }
    return report;
  } catch {
    fail("npm audit returned malformed JSON");
  }
}

function parseDate(value, field) {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    fail(`invalid ${field}`);
  }
  const parsed = new Date(`${value}T00:00:00Z`);
  if (!Number.isFinite(parsed.getTime())) fail(`invalid ${field}`);
  return parsed;
}

function loadExceptions() {
  let document;
  try {
    document = JSON.parse(readFileSync(exceptionPath, "utf8"));
  } catch (error) {
    fail(`cannot read exception file: ${error.message}`);
  }
  if (document.schema_version !== 1 || !Array.isArray(document.exceptions)) {
    fail("exception file must use schema_version 1 and an exceptions array");
  }
  const today = new Date();
  today.setUTCHours(0, 0, 0, 0);
  const keys = new Set();
  return document.exceptions.map((entry, index) => {
    const label = `exceptions[${index}]`;
    if (!Number.isInteger(entry.advisory_id) || entry.advisory_id < 1) fail(`${label}.advisory_id is invalid`);
    if (typeof entry.package !== "string" || entry.package.trim() !== entry.package || entry.package === "") fail(`${label}.package is invalid`);
    if (entry.scope !== "development") fail(`${label}.scope must be development`);
    for (const field of ["owner", "reviewed_by", "reason"]) {
      if (typeof entry[field] !== "string" || entry[field].trim() !== entry[field] || entry[field].length < 3) {
        fail(`${label}.${field} is invalid`);
      }
    }
    const reviewed = parseDate(entry.reviewed_on, `${label}.reviewed_on`);
    const expires = parseDate(entry.expires_on, `${label}.expires_on`);
    if (reviewed > today) fail(`${label}.reviewed_on is in the future`);
    if (expires < today) fail(`${label} is expired`);
    if ((expires - reviewed) / 86400000 > 90) fail(`${label} exceeds the 90-day review window`);
    const key = `${entry.package}\u0000${entry.advisory_id}`;
    if (keys.has(key)) fail(`${label} duplicates another exception`);
    keys.add(key);
    return { ...entry, key, used: false };
  });
}

function totalFindings(report) {
  const total = report?.metadata?.vulnerabilities?.total;
  if (!Number.isInteger(total) || total < 0) fail("npm audit metadata is missing");
  return total;
}

const exceptions = loadExceptions();
const production = audit("production", ["--omit=dev", "--package-lock-only", "--include=prod"]);
const productionTotal = totalFindings(production);

const complete = audit("complete", ["--package-lock-only", "--include=dev"]);
const completeTotal = totalFindings(complete);
const advisories = [];
for (const [packageName, vulnerability] of Object.entries(complete.vulnerabilities || {})) {
  for (const cause of vulnerability.via || []) {
    if (typeof cause === "object" && Number.isInteger(cause.source)) {
      advisories.push({ package: packageName, advisoryId: cause.source });
    }
  }
}

const unique = new Map(advisories.map((item) => [`${item.package}\u0000${item.advisoryId}`, item]));
for (const finding of unique.values()) {
  const exception = exceptions.find((entry) => entry.key === `${finding.package}\u0000${finding.advisoryId}`);
  if (!exception) fail(`unreviewed development finding ${finding.advisoryId} in ${finding.package}`);
  exception.used = true;
}
if (completeTotal !== 0 && unique.size === 0) fail("npm audit findings have no reviewable advisory identities");
for (const exception of exceptions) {
  if (!exception.used) fail(`stale exception ${exception.advisory_id} in ${exception.package}`);
}
if (productionTotal !== 0) fail(`${productionTotal} production findings are not exception eligible`);

process.stdout.write(`dependency advisory validation passed: ${completeTotal} findings, ${exceptions.length} reviewed exceptions\n`);
