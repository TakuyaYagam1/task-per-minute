import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { parse } from 'yaml';

const root = fileURLToPath(new URL('../../', import.meta.url));
const workflow = parse(readFileSync(join(root, '.github/workflows/reusable-frontend-verify.yml'), 'utf8'));
const step = workflow.jobs['full-stack-e2e'].steps.find((entry) => entry.name === 'write disposable e2e env');

function validateConfig(t, missingKey) {
  const directory = mkdtempSync(join(tmpdir(), 'e2e-compose-config-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const envFile = join(directory, 'synthetic.env');
  const setup = spawnSync('bash', ['-c', step.run], {
    cwd: directory,
    encoding: 'utf8',
    timeout: 30_000,
    env: { PATH: process.env.PATH, HOME: directory, FULL_STACK_ENV_FILE: envFile },
  });
  assert.ifError(setup.error);
  assert.equal(setup.status, 0, setup.stderr);
  if (missingKey) {
    const content = readFileSync(envFile, 'utf8').split('\n')
      .filter((line) => !line.startsWith(`${missingKey}=`)).join('\n');
    writeFileSync(envFile, content, { mode: 0o600 });
  }
  return spawnSync('docker', [
    'compose', '-p', 'e2e-config-test', '--env-file', envFile,
    '-f', join(root, 'deployment/docker/docker-compose.local.yml'), 'config', '--quiet',
  ], {
    cwd: directory,
    encoding: 'utf8',
    timeout: 30_000,
    // Do not let workstation secrets fill gaps in the CI fixture.
    env: { PATH: process.env.PATH, HOME: directory },
  });
}

test('CI fixture resolves the full Compose configuration without workstation settings', (t) => {
  const result = validateConfig(t);
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
});

for (const key of ['INCIDENT_EXPORT_HMAC_KEY_ID', 'INCIDENT_EXPORT_HMAC_SECRET']) {
  test(`Compose rejects a missing ${key}`, (t) => {
    const result = validateConfig(t, key);
    assert.ifError(result.error);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(`${key} is required`));
  });
}
