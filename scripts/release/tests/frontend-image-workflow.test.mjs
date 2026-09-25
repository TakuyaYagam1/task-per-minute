import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { runInNewContext } from 'node:vm';

const root = new URL('../../../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = read('.github/workflows/reusable-build-images.yml');
const pipeline = read('.github/workflows/pipeline.yml');
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
  assert.equal(value(section(build.text, 'with', 8), 'push', 10), 'true');
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
  assert.equal(value(defaults, 'packages', 2), 'write');
  for (const match of text.matchAll(/^( *)permissions:$/gm)) {
    const indent = match[1].length;
    const tail = text.slice(match.index).split('\n').slice(1);
    const end = tail.findIndex((line) => line.trim() && line.search(/\S/) <= indent);
    const permissions = entries(tail.slice(0, end < 0 ? tail.length : end).join('\n'), indent + 2);
    assert.deepEqual([...permissions.keys()].sort(), ['contents', 'packages']);
    assert.equal(permissions.get('contents'), 'read');
    assert.ok(['read', 'write'].includes(permissions.get('packages')));
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

function checkFrontendBuild(text) {
  const frontend = job(text, 'frontend-image');
  const items = steps(frontend);
  const build = oneStep(items, (step) => run(step).includes('node scripts/release/build-frontend-image.mjs'), 'frontend wrapper');
  const env = section(build.text, 'env', 8);
  assert.equal(value(env, 'TARGET_SHA', 10), '${{ inputs.target_sha }}');
  assert.equal(value(env, 'IMAGE_REF', 10), '${{ inputs.frontend_image_ref }}');
  assert.match(run(build), /--revision\s+"\$TARGET_SHA"(?:\s|$)/);
  assert.match(run(build), /--push-reference\s+"\$IMAGE_REF"(?:\s|$)/);
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

function exposeIdentity(program, report) {
  const writes = [];
  let error;
  try {
    runInNewContext(program, {
      process: { env: { TARGET_SHA: revision, RUNNER_TEMP: '/synthetic', GITHUB_OUTPUT: '/synthetic/output' } },
      join,
      readFileSync(path, encoding) {
        assert.equal(path, '/synthetic/frontend-image/identity.json');
        assert.equal(encoding, 'utf8');
        return JSON.stringify(report);
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
  assert.equal(value(section(job(pipeline, 'build-images'), 'permissions', 4), 'packages', 6), 'write');
  assert.equal(value(section(job(pipeline, 'deploy-production'), 'permissions', 4), 'packages', 6), 'read');
});
test('input expressions enter commands through env and do not copy private context', () => {
  checkCommands(workflow);
  checkCommands(pipeline);
});
test('frontend CI uses the wrapper with quoted revision and explicit push reference', () => checkFrontendBuild(workflow));
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
  ['write-all permissions', 'permissions:\n  contents: read\n  packages: write', 'permissions: write-all', checkPermissions],
  ['extra privileged permission', 'permissions:\n  contents: read\n  packages: write', 'permissions:\n  contents: read\n  packages: write\n  id-token: write', checkPermissions],
  ['direct SHA interpolation', '--revision "$TARGET_SHA"', '--revision "${{ inputs.target_sha }}"', checkCommands],
  ['unquoted SHA', '--revision "$TARGET_SHA"', '--revision $TARGET_SHA', checkFrontendBuild],
  ['implicit frontend publication', '--push-reference "$IMAGE_REF"', '', checkFrontendBuild],
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
