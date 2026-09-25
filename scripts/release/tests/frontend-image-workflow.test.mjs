import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { runInNewContext } from 'node:vm';

const root = new URL('../../../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = read('.github/workflows/reusable-build-images.yml');
const pipeline = read('.github/workflows/pipeline.yml');
const legacyDeploy = read('.github/workflows/reusable-deploy-production.yml');
const dockerfile = read('frontend/Dockerfile');
const dockerignore = read('frontend/.dockerignore');
const baseDigest = 'sha256:9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2';
const baseName = 'node:24.21.0-alpine3.23';
const identityNames = ['SOURCE_REVISION', 'SOURCE_DIRTY', 'LOCKFILE_SHA256', 'CONTEXT_SHA256'];

// These are focused assertions for the checked-in workflow shape, not a YAML parser.
// They intentionally need no host npm install before the image build job runs.
function section(text, key, indent) {
  const lines = text.split('\n');
  const header = `${' '.repeat(indent)}${key}:`;
  const matches = lines.flatMap((line, index) => line.trimEnd() === header ? [index] : []);
  assert.equal(matches.length, 1, `expected one ${key} section at indent ${indent}`);
  const start = matches[0] + 1;
  let end = start;
  while (end < lines.length) {
    const line = lines[end];
    if (line.trim() && line.search(/\S/) <= indent) break;
    end++;
  }
  return lines.slice(start, end).join('\n');
}

function entries(text, indent) {
  const expression = new RegExp(`^ {${indent}}([a-zA-Z_][a-zA-Z0-9_-]*):(?: +(.*))?$`, 'gm');
  const values = new Map();
  for (const match of text.matchAll(expression)) {
    assert.ok(!values.has(match[1]), `duplicate mapping key ${match[1]}`);
    values.set(match[1], match[2] ?? '');
  }
  return values;
}

function value(text, key, indent) {
  const values = entries(text, indent);
  assert.ok(values.has(key), `missing ${key} at indent ${indent}`);
  return values.get(key);
}

function job(text, name) {
  return section(section(text, 'jobs', 0), name, 2);
}

function steps(text) {
  const starts = [...text.matchAll(/^      - name: (.+)$/gm)];
  assert.ok(starts.length > 0, 'expected named workflow steps');
  return starts.map((match, index) => ({
    name: match[1],
    text: text.slice(match.index, starts[index + 1]?.index ?? text.length),
  }));
}

function run(step) {
  const lines = step.text.split('\n');
  const index = lines.findIndex((line) => line.startsWith('        run: '));
  if (index < 0) return '';
  const first = lines[index].slice('        run: '.length);
  if (first !== '|' && first !== '>-') return first;
  const body = [];
  for (const line of lines.slice(index + 1)) {
    if (line.trim() && !line.startsWith('          ')) break;
    body.push(line.slice(10));
  }
  return body.join('\n').trimEnd();
}

function oneStep(items, predicate, label) {
  const selected = items.filter(predicate);
  assert.equal(selected.length, 1, `expected one ${label} step`);
  return selected[0];
}

function replaceOnce(text, before, after) {
  assert.equal(text.split(before).length, 2, `mutation target must occur once: ${before}`);
  return text.replace(before, after);
}

function checkJobs(text) {
  assert.deepEqual([...entries(section(text, 'jobs', 0), 2).keys()], ['backend-image', 'frontend-image']);
  const backend = job(text, 'backend-image');
  const frontend = job(text, 'frontend-image');
  assert.equal(value(section(backend, 'outputs', 4), 'digest', 6), '${{ steps.build.outputs.digest }}');
  assert.equal(value(section(frontend, 'outputs', 4), 'digest', 6), '${{ steps.identity.outputs.digest }}');
  assert.equal(value(section(frontend, 'outputs', 4), 'source_revision', 6), '${{ steps.identity.outputs.source_revision }}');
  const exports = section(section(section(text, 'on', 0), 'workflow_call', 2), 'outputs', 4);
  for (const [name, expected] of [
    ['backend_digest', '${{ jobs.backend-image.outputs.digest }}'],
    ['frontend_digest', '${{ jobs.frontend-image.outputs.digest }}'],
    ['frontend_source_revision', '${{ jobs.frontend-image.outputs.source_revision }}'],
  ]) assert.equal(value(section(exports, name, 6), 'value', 8), expected);
  const build = oneStep(steps(backend), (step) => step.text.includes('uses: docker/build-push-action@'), 'backend build');
  assert.equal(value(build.text, 'id', 8), 'build');
  assert.equal(value(section(build.text, 'with', 8), 'context', 10), 'backend');
  assert.equal(value(section(build.text, 'with', 8), 'file', 10), 'backend/Dockerfile');
  assert.equal(value(section(build.text, 'with', 8), 'push', 10), 'false');
  assert.doesNotMatch(frontend, /uses: docker\/build-push-action@/);
}

function checkActions(text) {
  for (const match of text.matchAll(/^\s+uses: ([^\n]+)$/gm)) {
    if (match[1].startsWith('./.github/workflows/')) continue;
    assert.match(match[1], /^[\w.-]+\/[\w./-]+@[a-f0-9]{40}(?: +#.*)?$/, 'remote actions must be commit-pinned');
  }
}

function checkPermissions(text) {
  assert.doesNotMatch(text, /^\s*permissions: +\S/m, 'permissions must be an explicit mapping');
  const defaults = section(text, 'permissions', 0);
  assert.equal(value(defaults, 'contents', 2), 'read');
  assert.ok(!entries(defaults, 2).has('packages') || value(defaults, 'packages', 2) === 'read');
  for (const match of text.matchAll(/^( *)permissions:$/gm)) {
    const indent = match[1].length;
    const tail = text.slice(match.index).split('\n').slice(1);
    const end = tail.findIndex((line) => line.trim() && line.search(/\S/) <= indent);
    const permissions = entries(tail.slice(0, end < 0 ? tail.length : end).join('\n'), indent + 2);
    assert.ok([...permissions.keys()].every((name) => ['contents', 'packages'].includes(name)));
    assert.equal(permissions.get('contents'), 'read');
    assert.ok(!permissions.has('packages') || permissions.get('packages') === 'read');
  }
}

function checkCommands(text) {
  const jobs = section(text, 'jobs', 0);
  for (const name of entries(jobs, 2).keys()) {
    const contents = section(jobs, name, 2);
    if (!/^    steps:$/m.test(contents)) continue;
    for (const step of steps(contents)) {
      const command = run(step);
      assert.doesNotMatch(command, /\$\{\{/, 'untrusted expressions must enter scripts through env');
      assert.doesNotMatch(command, /--env-file\b|(?:^|[\s"'/])\.env[\w.-]*(?=$|[\s"'/])/m, 'commands must not read or copy env files');
      assert.doesNotMatch(command, /(?:^|[\s"'/])(?:preview|PRD|private|secrets|credentials)\//m, 'commands must not use private context paths');
      assert.doesNotMatch(command, /\b(?:cp|rsync|tar)\b[^\n]*\b(?:preview|PRD|private|secrets|credentials)\b/, 'commands must not copy private directories');
    }
  }
}

function checkReadOnly(text) {
  checkPermissions(text);
  assert.doesNotMatch(text, /^\s*secrets:|\$\{\{\s*(?:secrets\.|github\.token)/m, 'ordinary CI must not inherit or pass credentials');
  const allowedActions = new Set([
    'actions/checkout', 'actions/setup-node', 'actions/setup-python', 'actions/upload-artifact',
    'docker/setup-buildx-action', 'docker/build-push-action',
  ]);
  const allowedWorkflows = new Set([
    './.github/workflows/reusable-backend-checks.yml', './.github/workflows/reusable-frontend-verify.yml',
    './.github/workflows/reusable-build-images.yml',
  ]);
  for (const match of text.matchAll(/^\s+uses: ([^\s]+).*$/gm)) {
    assert.ok(allowedActions.has(match[1].split('@')[0]) || allowedWorkflows.has(match[1]),
      'ordinary CI must not call login, signing, publication or deploy actions/workflows');
  }
  for (const match of text.matchAll(/^\s+push: (.+)$/gm)) assert.equal(match[1], 'false', 'image publication must be disabled');
  for (const name of entries(section(text, 'jobs', 0), 2).keys()) {
    const contents = job(text, name);
    if (!/^    steps:$/m.test(contents)) continue;
    for (const step of steps(contents)) {
      const command = run(step);
      assert.doesNotMatch(command, /--push(?:[\s=]|$)|--push-reference|type=registry|push=true/);
      assert.doesNotMatch(command, /\b(?:docker|podman|buildah|crane|skopeo)\b[^\n]*(?:\bpush\b|\blogin\b|\bcopy\b)/);
      assert.doesNotMatch(command, /\b(?:cosign|notation)\b[^\n]*\b(?:sign|attest)\b/);
      assert.doesNotMatch(command, /\b(?:ssh|scp|sftp)\b|\b(?:helm|kubectl)\b[^\n]*\b(?:upgrade|install|apply|rollout)\b/);
      assert.doesNotMatch(command, /scripts\/release\/(?:publish|sign|deploy)[\w.-]*|\bgh\s+(?:release|workflow)\s+(?:create|run)\b/);
    }
  }
}

function checkPipeline(text) {
  const events = section(text, 'on', 0);
  assert.deepEqual([...entries(events, 2).keys()], ['push', 'pull_request', 'workflow_dispatch']);
  for (const event of ['push', 'pull_request']) assert.match(section(events, event, 2), /^      - 'security\/\*\*'$/m);
  assert.deepEqual([...entries(section(text, 'jobs', 0), 2).keys()], ['resolve', 'backend-checks', 'frontend-verify', 'build-images']);
  const build = job(text, 'build-images');
  assert.equal(value(build, 'uses', 4), './.github/workflows/reusable-build-images.yml');
  assert.ok(!entries(build, 4).has('if'), 'build must run after successful checks on all pipeline events');
  assert.deepEqual(section(build, 'needs', 4).split('\n').filter((line) => line.trim()).map((line) => line.trim()),
    ['- resolve', '- backend-checks', '- frontend-verify']);
  checkReadOnly(text);
}

function shell(command, environment) {
  return execFileSync('bash', ['--noprofile', '--norc', '-c', command], {
    env: { PATH: process.env.PATH, LC_ALL: 'C', ...environment }, stdio: ['ignore', 'pipe', 'pipe'],
  }).toString();
}

function checkRevisionGuards(text) {
  for (const name of ['backend-image', 'frontend-image']) {
    const items = steps(job(text, name));
    const guard = oneStep(items, (step) => step.name === 'validate source revision', 'source revision guard');
    const checkout = oneStep(items, (step) => step.text.includes('uses: actions/checkout@'), 'checkout');
    assert.ok(items.indexOf(guard) < items.indexOf(checkout));
    assert.equal(value(section(guard.text, 'env', 8), 'TARGET_SHA', 10), '${{ inputs.target_sha }}');
    shell(run(guard), { TARGET_SHA: revision });
    for (const invalid of ['', 'HEAD', 'a'.repeat(39), 'a'.repeat(41), 'A'.repeat(40), `${revision}\n`, `${revision}\ninjected=true`]) {
      assert.throws(() => shell(run(guard), { TARGET_SHA: invalid }), (error) => error.status === 1);
    }
  }
}

function checkLegacyGuard(text) {
  const deploy = job(text, 'deploy');
  assert.equal(value(deploy, 'environment', 4), 'production');
  const guard = value(deploy, 'if', 4);
  assert.equal(guard, "${{ github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main' && github.ref_protected }}");
  for (const event_name of ['push', 'pull_request', 'workflow_dispatch']) {
    for (const ref of ['refs/heads/main', 'refs/heads/feature']) {
      for (const ref_protected of [true, false]) {
        assert.equal(runInNewContext(guard.slice(3, -2), { github: { event_name, ref, ref_protected } }, { timeout: 1000 }),
          event_name === 'workflow_dispatch' && ref === 'refs/heads/main' && ref_protected);
      }
    }
  }
}

function checkFrontendBuild(text) {
  const frontend = job(text, 'frontend-image');
  const items = steps(frontend);
  const build = oneStep(items, (step) => run(step).includes('node scripts/release/build-frontend-image.mjs'), 'frontend wrapper');
  const env = section(build.text, 'env', 8);
  assert.equal(value(env, 'TARGET_SHA', 10), '${{ inputs.target_sha }}');
  assert.ok(!entries(env, 10).has('IMAGE_REF'), 'build-only frontend must not receive a publication target');
  assert.match(run(build), /--revision\s+"\$TARGET_SHA"(?:\s|$)/);
  assert.doesNotMatch(run(build), /--push-reference|\$IMAGE_REF/);
  assert.match(run(build), /--output\s+"\$RUNNER_TEMP\/frontend-image"(?:\s|$)/);
  assert.doesNotMatch(run(build), /\|\||continue-on-error|--env-file/);
  assert.doesNotMatch(frontend, /\bnpm (?:ci|install)\b/, 'image contract tests cannot depend on host npm installs');
  for (const name of ['backend-image', 'frontend-image']) {
    const checkout = oneStep(steps(job(text, name)), (step) => step.text.includes('uses: actions/checkout@'), 'checkout');
    const options = section(checkout.text, 'with', 8);
    assert.equal(value(options, 'ref', 10), '${{ inputs.target_sha }}');
    assert.equal(value(options, 'persist-credentials', 10), 'false');
  }
  const identity = oneStep(items, (step) => entries(step.text, 8).get('id') === 'identity', 'identity');
  assert.ok(items.indexOf(build) < items.indexOf(identity), 'identity must follow the verified build');
  assert.equal(value(section(identity.text, 'env', 8), 'TARGET_SHA', 10), '${{ inputs.target_sha }}');
  const evidenceTests = oneStep(items, (step) => run(step).startsWith('node --test '), 'evidence tests');
  assert.ok(items.indexOf(evidenceTests) < items.indexOf(build));
  for (const name of ['build-frontend-image', 'validate-frontend-image', 'frontend-image-workflow']) {
    assert.ok(run(evidenceTests).includes(`scripts/release/tests/${name}.test.mjs`));
  }
}

function checkArtifact(text) {
  const items = steps(job(text, 'frontend-image'));
  const upload = oneStep(items, (step) => step.text.includes('uses: actions/upload-artifact@'), 'artifact retention');
  const identity = oneStep(items, (step) => entries(step.text, 8).get('id') === 'identity', 'identity');
  assert.ok(items.indexOf(identity) < items.indexOf(upload));
  const options = section(upload.text, 'with', 8);
  assert.equal(value(options, 'path', 10), '${{ runner.temp }}/frontend-image');
  assert.equal(value(options, 'if-no-files-found', 10), 'error');
  assert.match(value(options, 'retention-days', 10), /^[1-9][0-9]*$/);
}

function checkFrontendSecurity(text) {
  const items = steps(job(text, 'frontend-image'));
  const build = oneStep(items, (step) => run(step).includes('node scripts/release/build-frontend-image.mjs'), 'frontend build');
  const prepare = oneStep(items, (step) => step.name === 'prepare frontend vulnerability scanner', 'scanner preparation');
  const scan = oneStep(items, (step) => step.name === 'scan exact frontend image', 'frontend security gate');
  const identity = oneStep(items, (step) => entries(step.text, 8).get('id') === 'identity', 'verified identity');
  assert.ok(items.indexOf(build) < items.indexOf(prepare));
  assert.ok(items.indexOf(prepare) < items.indexOf(scan));
  assert.ok(items.indexOf(scan) < items.indexOf(identity), 'security gate must pass before identity is exposed');
  for (const step of [prepare, scan]) {
    assert.doesNotMatch(step.text, /continue-on-error|^\s*if:|\|\|/m, 'security checks cannot be skipped or ignored');
    assert.match(run(step), /^set -euo pipefail/m);
  }
  const provision = run(prepare);
  assert.match(provision, /https:\/\/github\.com\/aquasecurity\/trivy\/releases\/download\/v0\.72\.0\/trivy_0\.72\.0_Linux-64bit\.tar\.gz/);
  for (const pin of ['bbb64b9695866ce4a7a8f5c9592002c5961cab378577fa3f8a040df362b9b2ea', '0e69edd134a3c338baa1a6806920773615d682b18cbc6a0cba2a3b658ef9b63e']) assert.ok(provision.includes(pin));
  assert.equal((provision.match(/sha256sum --check --status/g) || []).length, 2);
  assert.match(provision, /--config \/dev\/null/);
  assert.match(provision, /--db-repository ghcr\.io\/aquasecurity\/trivy-db:2 --download-db-only/);
  assert.ok(provision.indexOf('sha256sum --check --status') < provision.indexOf('tar --extract'));
  assert.ok(provision.lastIndexOf('sha256sum --check --status') < provision.indexOf('image --config'));
  const gate = run(scan);
  assert.match(gate, /node scripts\/release\/scan-frontend-image\.mjs/);
  for (const flag of ['--artifact', '--image-digest', '--cache-dir', '--scanner', '--output']) assert.ok(gate.includes(flag));
  assert.match(gate, /--image-digest "\$IMAGE_DIGEST"/);
  assert.match(gate, /--output "\$RUNNER_TEMP\/frontend-image\/security"/);
  assert.doesNotMatch(gate, /--ignore|--skip|--severity/);
  const tests = oneStep(items, (step) => run(step).startsWith('node --test '), 'image tests');
  for (const name of ['scan-frontend-image', 'check-frontend-runtime']) assert.ok(run(tests).includes(`scripts/release/tests/${name}.test.mjs`));
}

test('frontend security gate precedes verified outputs with pinned tooling', () => checkFrontendSecurity(workflow));
test('frontend security gate cannot be made non-blocking', () => {
  const changed = replaceOnce(workflow, '      - name: scan exact frontend image\n', '      - name: scan exact frontend image\n        continue-on-error: true\n');
  assert.throws(() => checkFrontendSecurity(changed));
});
test('frontend security gate cannot be omitted', () => {
  const changed = replaceOnce(workflow, '      - name: scan exact frontend image\n', '      - name: unused check\n');
  assert.throws(() => checkFrontendSecurity(changed));
});
test('frontend scanner archive must retain its reviewed pin', () => {
  const changed = replaceOnce(workflow, 'bbb64b9695866ce4a7a8f5c9592002c5961cab378577fa3f8a040df362b9b2ea', '0'.repeat(64));
  assert.throws(() => checkFrontendSecurity(changed));
});
test('frontend security report must be retained with OCI evidence', () => {
  const changed = replaceOnce(workflow, '--output "$RUNNER_TEMP/frontend-image/security"', '--output "$RUNNER_TEMP/unretained"');
  assert.throws(() => checkFrontendSecurity(changed));
});

function identityProgram(text) {
  const step = oneStep(steps(job(text, 'frontend-image')), (item) => entries(item.text, 8).get('id') === 'identity', 'identity');
  const command = run(step);
  const match = command.match(/^node --input-type=module <<'NODE'\n([\s\S]+)\nNODE$/);
  assert.ok(match, 'identity must be a single checked Node command');
  const imports = [...match[1].matchAll(/^import .+;$/gm)].map((item) => item[0]);
  assert.deepEqual(imports, [
    "import { readFileSync, appendFileSync } from 'node:fs';",
    "import { join } from 'node:path';",
  ]);
  return match[1].replace(/^import .+;\n/gm, '');
}

const revision = 'a'.repeat(40);
const validReport = { status: 'pass', source_dirty: false, source_revision: revision, index_digest: `sha256:${'b'.repeat(64)}` };
const validSecurity = { ...validReport, counts: { HIGH: 0, CRITICAL: 0 } };

function exposeIdentity(program, report, security = validSecurity) {
  const writes = [];
  let error;
  try {
    runInNewContext(program, {
      process: { env: { TARGET_SHA: revision, RUNNER_TEMP: '/synthetic', GITHUB_OUTPUT: '/synthetic/output' } },
      join,
      readFileSync(path, encoding) {
        assert.equal(encoding, 'utf8');
        if (path === '/synthetic/frontend-image/identity.json') return JSON.stringify(report);
        assert.equal(path, '/synthetic/frontend-image/security/report.json');
        if (security === null) throw new Error('Security evidence missing');
        return JSON.stringify(security);
      },
      appendFileSync(path, contents) {
        assert.equal(path, '/synthetic/output');
        writes.push(contents);
      },
    }, { timeout: 1000 });
  } catch (caught) {
    error = caught;
  }
  return { writes, error };
}

function checkIdentity(text) {
  const program = identityProgram(text);
  const accepted = exposeIdentity(program, validReport);
  assert.equal(accepted.error, undefined);
  assert.deepEqual(accepted.writes, [`digest=${validReport.index_digest}\nsource_revision=${revision}\n`]);
  const invalid = [
    { ...validReport, status: 'fail' },
    { ...validReport, status: undefined },
    { ...validReport, source_dirty: true },
    { ...validReport, source_dirty: undefined },
    { ...validReport, source_dirty: 'false' },
    { ...validReport, source_revision: 'c'.repeat(40) },
    { ...validReport, source_revision: undefined },
    { ...validReport, index_digest: undefined },
    { ...validReport, index_digest: `sha256:${'b'.repeat(63)}` },
    { ...validReport, index_digest: `sha256:${'B'.repeat(64)}` },
    { ...validReport, index_digest: `sha512:${'b'.repeat(64)}` },
    { ...validReport, index_digest: `${validReport.index_digest}\n` },
  ];
  for (const report of invalid) {
    const result = exposeIdentity(program, report);
    assert.ok(result.error, 'invalid identity must fail before outputs');
    assert.deepEqual(result.writes, [], 'invalid identity must publish no outputs');
  }
  const staleRevision = 'c'.repeat(40);
  const stalePair = exposeIdentity(program,
    { ...validReport, source_revision: staleRevision },
    { ...validSecurity, source_revision: staleRevision });
  assert.ok(stalePair.error, 'matching reports from another revision cannot authorize this build');
  assert.deepEqual(stalePair.writes, []);
  for (const security of [
    null, {}, { ...validSecurity, status: 'fail' },
    { ...validSecurity, source_dirty: true },
    { ...validSecurity, source_revision: 'd'.repeat(40) },
    { ...validSecurity, index_digest: `sha256:${'e'.repeat(64)}` },
    { ...validSecurity, counts: undefined },
    { ...validSecurity, counts: { HIGH: 1, CRITICAL: 0 } },
    { ...validSecurity, counts: { HIGH: 0, CRITICAL: 1 } },
  ]) {
    const result = exposeIdentity(program, validReport, security);
    assert.ok(result.error, 'invalid security evidence must fail before outputs');
    assert.deepEqual(result.writes, []);
  }
}

function checkDockerfile(text) {
  assert.deepEqual([...text.matchAll(/^FROM (\S+) AS (\S+)$/gm)].map((match) => match.slice(1)), [
    [`${baseName}@${baseDigest}`, 'builder'],
    [`${baseName}@${baseDigest}`, 'runner'],
  ]);
  const runner = text.slice(text.indexOf(' AS runner\n'));
  const builder = text.slice(0, text.indexOf(' AS runner\n'));
  for (const name of identityNames) {
    assert.match(runner, new RegExp(`^ARG ${name}$`, 'm'));
    assert.match(builder, new RegExp(`^ARG ${name}$`, 'm'));
    assert.doesNotMatch(text, new RegExp(`^ENV ${name}(?:=| )`, 'm'));
  }
  const labels = text.replace(/\\\n\s*/g, ' ').split('\n').filter((line) => line.startsWith('LABEL ')).join(' ');
  for (const [name, expected] of [
    ['org.opencontainers.image.revision', '${SOURCE_REVISION}'],
    ['io.task-per-minute.source.dirty', '${SOURCE_DIRTY}'],
    ['io.task-per-minute.lockfile.sha256', '${LOCKFILE_SHA256}'],
    ['io.task-per-minute.context.sha256', '${CONTEXT_SHA256}'],
    ['org.opencontainers.image.base.digest', baseDigest],
    ['org.opencontainers.image.base.name', `docker.io/library/${baseName}`],
  ]) assert.ok(labels.includes(`${name}="${expected}"`), `missing identity label ${name}`);
  assert.equal((text.match(/^RUN npm ci(?: |$)/gm) ?? []).length, 1);
  assert.match(text, /^RUN npm ci --ignore-scripts --no-audit --no-fund$/m);
  assert.doesNotMatch(text, /^COPY .*?(?:\.env|preview\/|PRD\/|private\/|secrets\/|credentials\/|\.\.\/)/m);
}

function checkIgnore(text) {
  const rules = text.split('\n').map((line) => line.trim());
  for (const rule of ['.env*', '**/.env*', 'preview/', '**/preview/', '**/.next', '**/*.next', 'private/', '**/private/', 'secrets/', '**/secrets/', 'credentials/', '**/credentials/']) {
    assert.ok(rules.includes(rule), `missing build-context exclusion ${rule}`);
  }
  assert.ok(!rules.some((rule) => rule.startsWith('!') && /\.env|preview|private|secrets|credentials|\.next/.test(rule)), 'private context must not be re-included');
}

function checkRuntimePackageManagers(text) {
  const runnerStart = text.search(/^FROM \S+ AS runner$/m);
  assert.ok(runnerStart >= 0, 'runner stage must exist');
  const builder = text.slice(0, runnerStart);
  const runner = text.slice(runnerStart).replace(/\\\n\s*/g, ' ');
  const directories = ['/usr/local/lib/node_modules/npm', '/usr/local/lib/node_modules/corepack', '/opt/yarn-v1.22.22'];
  const links = ['/usr/local/bin/npm', '/usr/local/bin/npx', '/usr/local/bin/corepack', '/usr/local/bin/pnpm', '/usr/local/bin/pnpx', '/usr/local/bin/yarn', '/usr/local/bin/yarnpkg'];
  const cleanup = [...runner.matchAll(/^RUN (rm -rf -- .+)$/gm)];
  assert.equal(cleanup.length, 1, 'runner must remove the unused global package managers');
  const commands = cleanup[0][1].split(/\s+&&\s+/);
  assert.equal(commands.length, 2, 'package manager cleanup must use only two scoped removals');
  assert.deepEqual(commands[0].trim().split(/\s+/), ['rm', '-rf', '--', ...directories]);
  assert.deepEqual(commands[1].trim().split(/\s+/), ['rm', '-f', '--', ...links]);
  for (const target of [...directories, ...links]) {
    assert.ok(!builder.includes(target), `builder must retain ${target}`);
  }
  assert.match(builder, /^RUN npm ci --ignore-scripts --no-audit --no-fund$/m);
  assert.match(builder, /^RUN npm run build$/m);
  assert.doesNotMatch(runner, /\b(?:npm|npx|corepack|yarn|yarnpkg|pnpm|pnpx)\s+(?:install|ci|enable|prepare)\b/);
  assert.ok(runner.indexOf(cleanup[0][0]) < runner.indexOf('USER nextjs'));
}

function checkComposeFrontend(text) {
  const frontend = section(section(text, 'services', 0), 'frontend', 2);
  const health = section(frontend, 'healthcheck', 4);
  const match = health.match(/^      test:\s*(\[[\s\S]*?\])/m);
  assert.ok(match, 'frontend healthcheck must use an exec array');
  const command = JSON.parse(match[1].replace(/,\s*\]/g, ']'));
  assert.deepEqual(command.slice(0, 3), ['CMD', 'node', '-e']);
  assert.equal(command.length, 4);
  assert.match(command[3], /\/health/);
  assert.match(command[3], /AbortSignal\.timeout\(4000\)/);
  assert.doesNotMatch(health, /\bwget\b|\bapk\b|\bcurl\b/);
  assert.doesNotMatch(dockerfile, /\bapk add\b|\bwget\b/);
}

function builderProgram(text) {
  const match = text.match(/RUN node -e '([\s\S]*?)'\n/);
  assert.ok(match, 'builder identity validation must exist');
  assert.ok(match.index < text.indexOf('RUN npm ci '), 'identity must be checked before dependency install');
  return match[1].replace(/\\\n/g, '\n');
}

const syntheticLockfile = Buffer.from('{"lockfileVersion":3}\n');
const validBuild = {
  SOURCE_REVISION: revision,
  SOURCE_DIRTY: 'false',
  LOCKFILE_SHA256: createHash('sha256').update(syntheticLockfile).digest('hex'),
  CONTEXT_SHA256: 'c'.repeat(64),
};

function validateBuild(program, env) {
  return runInNewContext(program, {
    process: { env },
    require(name) {
      if (name === 'node:crypto') return { createHash };
      assert.equal(name, 'node:fs');
      return { readFileSync(path) { assert.equal(path, 'package-lock.json'); return syntheticLockfile; } };
    },
  }, { timeout: 1000 });
}

test('backend and frontend build jobs expose distinct digest authorities', () => checkJobs(workflow));
test('workflow actions are immutable and permissions remain least privilege', () => {
  for (const text of [workflow, pipeline]) {
    checkActions(text);
    checkPermissions(text);
  }
  assert.equal(value(section(job(pipeline, 'build-images'), 'permissions', 4), 'packages', 6), 'read');
});
test('input expressions enter commands through env and do not copy private context', () => {
  checkCommands(workflow);
  checkCommands(pipeline);
});
test('frontend CI uses the build-only wrapper with a quoted revision', () => checkFrontendBuild(workflow));
test('ordinary CI is read-only and builds after checks on push, PR and dispatch', () => {
  checkPipeline(pipeline);
  checkReadOnly(workflow);
});
test('both reusable builds reject noncanonical revisions before checkout', () => checkRevisionGuards(workflow));
test('legacy production deploy requires protected main and manual dispatch', () => checkLegacyGuard(legacyDeploy));
test('pipeline metadata rejects noncanonical revisions before writing outputs', (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'pipeline-metadata-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const resolver = oneStep(steps(job(pipeline, 'resolve')), (step) => entries(step.text, 8).get('id') === 'resolve', 'metadata resolver');
  const command = run(resolver);
  let index = 0;
  const execute = (DISPATCH_SHA, REF_SHA) => {
    const output = join(directory, `output-${index++}`);
    const env = {
      DISPATCH_SHA, REF_SHA, GITHUB_OUTPUT: output, REPOSITORY_OWNER: 'Example',
      REGISTRY: 'ghcr.io', BACKEND_IMAGE_NAME: 'task-per-minute-backend', FRONTEND_IMAGE_NAME: 'task-per-minute-frontend',
    };
    return { output, env };
  };
  for (const [dispatch, fallback, expected] of [['', revision, revision], ['b'.repeat(40), revision, 'b'.repeat(40)]]) {
    const { output, env } = execute(dispatch, fallback);
    shell(command, env);
    assert.match(readFileSync(output, 'utf8'), new RegExp(`^target_sha=${expected}$`, 'm'));
    assert.match(readFileSync(output, 'utf8'), new RegExp(`^frontend_image_ref=ghcr.io/example/task-per-minute-frontend:${expected}$`, 'm'));
  }
  for (const invalid of ['', 'HEAD', 'a'.repeat(39), 'a'.repeat(41), 'A'.repeat(40), `${revision}\n`, `${revision}\ninjected=true`]) {
    const { output, env } = execute(invalid, invalid);
    assert.throws(() => shell(command, env), (error) => error.status === 1);
    assert.equal(existsSync(output), false);
  }
});
test('frontend evidence is retained and missing artifacts fail', () => checkArtifact(workflow));
test('identity output rejects failed, dirty, stale, missing and malformed reports', () => checkIdentity(workflow));
test('Dockerfile pins both stages, preserves labels and disables npm lifecycle scripts', () => checkDockerfile(dockerfile));
test('CI Node runtime matches the approved image version', () => {
  assert.equal(value(section(workflow, 'env', 0), 'NODE_VERSION', 2), '24.21.0');
});
test('runtime removes only unused package managers while builder retains npm', () => checkRuntimePackageManagers(dockerfile));
test('runtime cleanup contract rejects a retained manager, broad deletion and builder cleanup', () => {
  checkRuntimePackageManagers(dockerfile);
  const target = '/usr/local/lib/node_modules/npm';
  assert.throws(() => checkRuntimePackageManagers(replaceOnce(dockerfile, target, '')));
  assert.throws(() => checkRuntimePackageManagers(replaceOnce(dockerfile, target, '/usr/local/lib/node_modules')));
  assert.throws(() => checkRuntimePackageManagers(dockerfile.replace('WORKDIR /app', `WORKDIR /app\nRUN rm -rf -- ${target}`)));
});
test('build context excludes env, preview, private data and nested Next output', () => checkIgnore(dockerignore));
for (const path of ['deployment/docker/docker-compose.yml', 'deployment/docker/docker-compose.local.yml']) {
  test(`${path} frontend healthcheck needs only Node`, () => checkComposeFrontend(read(path)));
}
test('Compose contract ignores backend wget but rejects frontend wget', () => {
  const fixture = 'services:\n  backend:\n    healthcheck:\n      test: ["CMD", "wget", "/health"]\n  frontend:\n    healthcheck:\n      test: ["CMD", "node", "-e", "fetch(\'/health\', {signal: AbortSignal.timeout(4000)})"]\n';
  checkComposeFrontend(fixture);
  assert.throws(() => checkComposeFrontend(replaceOnce(fixture, '"node", "-e"', '"wget", "--tries=1"')));
});

const workflowMutations = [
  ['merged image jobs', '  frontend-image:\n', '  build-images:\n', checkJobs],
  ['frontend digest from backend', 'value: ${{ jobs.frontend-image.outputs.digest }}', 'value: ${{ jobs.backend-image.outputs.digest }}', checkJobs],
  ['frontend output before validation', 'digest: ${{ steps.identity.outputs.digest }}', 'digest: ${{ steps.build.outputs.digest }}', checkJobs],
  ['unpinned upload action', 'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02', 'actions/upload-artifact@v4', checkActions],
  ['write-all permissions', 'permissions:\n  contents: read\n  packages: read', 'permissions: write-all', checkPermissions],
  ['extra privileged permission', 'permissions:\n  contents: read\n  packages: read', 'permissions:\n  contents: read\n  packages: read\n  id-token: write', checkPermissions],
  ['package write permission', '  packages: read', '  packages: write', checkPermissions],
  ['content write permission', '  contents: read', '  contents: write', checkPermissions],
  ['nested write permission', '    name: build backend image\n', '    name: build backend image\n    permissions:\n      contents: read\n      packages: write\n', checkPermissions],
  ['direct SHA interpolation', '--revision "$TARGET_SHA"', '--revision "${{ inputs.target_sha }}"', checkCommands],
  ['unquoted SHA', '--revision "$TARGET_SHA"', '--revision $TARGET_SHA', checkFrontendBuild],
  ['frontend publication flag', '--revision "$TARGET_SHA"', '--revision "$TARGET_SHA" --push-reference "$IMAGE_REF"', checkFrontendBuild],
  ['backend publication', '          push: false', '          push: true', checkReadOnly],
  ['env file input', '--revision "$TARGET_SHA"', '--env-file .env --revision "$TARGET_SHA"', checkCommands],
  ['private context copy', '--revision "$TARGET_SHA"', '--revision "$TARGET_SHA"\n          cp -r preview/ "$RUNNER_TEMP/frontend-image"', checkCommands],
  ['missing artifact tolerated', 'if-no-files-found: error', 'if-no-files-found: ignore', checkArtifact],
  ['zero artifact retention', 'retention-days: 7', 'retention-days: 0', checkArtifact],
  ['missing status guard', "report.status !== 'pass' || ", '', checkIdentity],
  ['missing dirty guard', 'report.source_dirty !== false || ', '', checkIdentity],
  ['missing revision guard', 'report.source_revision !== process.env.TARGET_SHA || ', '', checkIdentity],
];
for (const [name, before, after, check] of workflowMutations) {
  test(`contract rejects ${name}`, () => assert.throws(() => check(replaceOnce(workflow, before, after))));
}

for (const [name, step] of [
  ['registry login action', '      - name: login\n        uses: docker/login-action@4907a6ddec9925e35a0a9e82d7399ccc52663121\n'],
  ['registry login command', '      - name: login\n        run: docker login ghcr.io\n'],
  ['image push command', '      - name: publish\n        run: docker push ghcr.io/example/frontend:test\n'],
  ['registry exporter', '      - name: publish\n        run: docker buildx build --output type=registry,name=ghcr.io/example/frontend:test frontend\n'],
  ['image push flag', '      - name: publish\n        run: docker buildx build --push frontend\n'],
  ['image signing', '      - name: sign\n        run: cosign sign ghcr.io/example/frontend:test\n'],
  ['image attestation signing', '      - name: attest\n        run: cosign attest ghcr.io/example/frontend:test\n'],
  ['deploy command', '      - name: deploy\n        run: ssh example.invalid deploy\n'],
]) {
  test(`ordinary CI rejects ${name}`, () => {
    checkReadOnly(workflow);
    const changed = replaceOnce(workflow, '      - name: setup node\n', `${step}\n      - name: setup node\n`);
    assert.throws(() => checkReadOnly(changed));
  });
}

test('ordinary pipeline cannot call manual publication, signing or deploy workflows', () => {
  checkPipeline(pipeline);
  for (const name of ['publish-frontend', 'sign-frontend', 'reusable-deploy-production']) {
    const changed = `${pipeline}\n  external-write:\n    uses: ./.github/workflows/${name}.yml\n`;
    assert.throws(() => checkPipeline(changed));
    assert.throws(() => checkReadOnly(changed));
  }
});

test('ordinary pipeline rejects inherited secrets and PR build exclusion', () => {
  checkPipeline(pipeline);
  const target = '    uses: ./.github/workflows/reusable-build-images.yml\n';
  assert.throws(() => checkReadOnly(replaceOnce(pipeline, target, `${target}    secrets: inherit\n`)));
  assert.throws(() => checkPipeline(replaceOnce(pipeline, target, `${target}    if: github.event_name != 'pull_request'\n`)));
  assert.throws(() => checkPipeline(pipeline.replace("      - 'security/**'\n", '')));
});

test('legacy deployment guard cannot omit any manual protected-main restriction', () => {
  checkLegacyGuard(legacyDeploy);
  for (const restriction of ["github.event_name == 'workflow_dispatch' && ", "github.ref == 'refs/heads/main' && ", ' && github.ref_protected']) {
    assert.throws(() => checkLegacyGuard(replaceOnce(legacyDeploy, restriction, '')));
  }
});

test('Docker contract rejects a mutable base and removed identity label', () => {
  assert.throws(() => checkDockerfile(dockerfile.replace(`@${baseDigest}`, '')));
  assert.throws(() => checkDockerfile(replaceOnce(dockerfile, 'io.task-per-minute.context.sha256=', 'io.task-per-minute.context.unbound=')));
});
test('context contract rejects env re-inclusion and missing nested output exclusion', () => {
  assert.throws(() => checkIgnore(`${dockerignore}\n!.env.example\n`));
  assert.throws(() => checkIgnore(replaceOnce(dockerignore, '**/.next\n', '')));
});
test('builder validates clean and dirty identity against actual lockfile bytes', () => {
  const program = builderProgram(dockerfile);
  validateBuild(program, validBuild);
  validateBuild(program, { ...validBuild, SOURCE_DIRTY: 'true' });
  assert.throws(() => validateBuild(program, { ...validBuild, LOCKFILE_SHA256: '0'.repeat(64) }), /LOCKFILE_SHA256 does not match/);
});
test('builder allows only entirely absent identity for unverified developer builds', () => {
  const program = builderProgram(dockerfile);
  validateBuild(program, {});
  validateBuild(program, { SOURCE_DATE_EPOCH: '1700000000' });
  assert.throws(() => validateBuild(program, Object.fromEntries(identityNames.map((name) => [name, '']))));
});
test('builder rejects every partial identity set', () => {
  const program = builderProgram(dockerfile);
  for (let mask = 1; mask < 15; mask++) {
    const partial = Object.fromEntries(identityNames.filter((_, index) => mask & (1 << index)).map((name) => [name, validBuild[name]]));
    assert.throws(() => validateBuild(program, partial), /SOURCE_REVISION|SOURCE_DIRTY|LOCKFILE_SHA256|CONTEXT_SHA256/);
  }
});
test('builder rejects malformed identity values including explicit empty args', () => {
  const program = builderProgram(dockerfile);
  for (const name of identityNames) {
    assert.throws(() => validateBuild(program, { ...validBuild, [name]: '' }), new RegExp(name));
    assert.throws(() => validateBuild(program, { ...validBuild, [name]: `${validBuild[name]}\n` }), new RegExp(name));
  }
  assert.throws(() => validateBuild(program, { ...validBuild, SOURCE_DIRTY: '1' }), /SOURCE_DIRTY/);
});
