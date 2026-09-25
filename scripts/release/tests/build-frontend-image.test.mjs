import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  assertContextUnchanged,
  buildFrontendImage,
  createBuildRequest,
  parseArguments,
  prepareContext,
} from '../build-frontend-image.mjs';

const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');

function fixture(t, extra = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-build-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const frontend = path.join(root, 'frontend');
  const environment = {
    PATH: process.env.PATH, HOME: root, LC_ALL: 'C',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null',
    GIT_AUTHOR_DATE: '2026-09-25T00:00:00Z', GIT_COMMITTER_DATE: '2026-09-25T00:00:00Z',
  };
  const git = (...args) => execFileSync('git', ['-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgSign=false', ...args], {
    cwd: root, env: environment, stdio: ['pipe', 'pipe', 'pipe'],
  }).toString().trim();
  const write = (name, content) => {
    fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true });
    fs.writeFileSync(path.join(root, name), content);
  };
  for (const [name, content] of Object.entries({
    '.gitignore': 'unrelated-output\n',
    'frontend/.dockerignore': 'Dockerfile\n.dockerignore\n.env*\ne2e\nnode_modules\n.next\n*.log\n',
    'frontend/Dockerfile': 'FROM scratch\nCOPY public /public\n',
    'frontend/package.json': '{"private":true}\n',
    'frontend/package-lock.json': '{"lockfileVersion":3}\n',
    'frontend/app/page.tsx': 'export default function Page() { return null; }\n',
    'frontend/public/icon.svg': '<svg/>\n',
    ...extra,
  })) write(name, content);
  git('init', '--quiet', '--initial-branch=main');
  git('config', 'user.name', 'Fixture');
  git('config', 'user.email', 'fixture@example.invalid');
  git('add', '--force', '--all');
  git('commit', '--quiet', '-m', 'Fixture');
  return { root, frontend, git, write, revision: git('rev-parse', 'HEAD'), output: path.join(root, 'output') };
}

function tarList(buffer) {
  return execFileSync('tar', ['--list', '--file=-'], {
    input: buffer, env: { PATH: process.env.PATH, LC_ALL: 'C' },
  }).toString().trim().split('\n');
}

test('preparation produces deterministic tar identity from the exact selected bytes', (t) => {
  const f = fixture(t);
  const first = prepareContext(f.frontend);
  assert.equal(first.revision, f.revision);
  assert.equal(first.sourceDirty, false);
  assert.equal(first.sourceDateEpoch, 1790294400);
  assert.equal(first.lockfileSha256, sha256(Buffer.from('{"lockfileVersion":3}\n')));
  assert.equal(first.contextSha256, sha256(first.tar));
  const paths = ['.dockerignore', 'Dockerfile', 'app/page.tsx', 'package-lock.json', 'package.json', 'public/icon.svg'];
  assert.deepEqual(tarList(first.tar), paths);
  assert.deepEqual(first.files.map((file) => file.path), paths);
  fs.utimesSync(path.join(f.frontend, 'app/page.tsx'), new Date(), new Date());
  const second = prepareContext(f.frontend);
  assert.deepEqual(second.tar, first.tar);
  const copy = first.tar;
  copy.fill(0);
  assert.equal(sha256(first.tar), first.contextSha256, 'callers cannot mutate the frozen archive');
});

test('excluded tracked, private and untracked paths are never read into the context', (t) => {
  const excluded = [
    '.env', '.env.example', 'nested/.env.production', 'e2e/test.ts', 'fixtures/input.json',
    '__fixtures__/input.json', 'lib/shared/ui/__fixture__/input.json', 'testdata/data.json', 'preview/page.html', 'node_modules/lib/index.js',
    '.next/server.js', 'build/out.js', 'dist/app.js', 'coverage/report.json', 'log/output.log',
    'secrets/token', 'credentials.json', 'private/data.json', 'PRD/document.md', '.npmrc',
    'cert.pem', 'tls.key', 'tls.crt', '.aws/config', '.docker/config.json', 'tsconfig.tsbuildinfo',
    'flag', 'flags/value.txt', 'per-file.flag',
  ];
  const f = fixture(t, Object.fromEntries(excluded.map((name) => [`frontend/${name}`, 'SYNTHETIC_EXCLUDED\n'])));
  for (const name of excluded) {
    fs.unlinkSync(path.join(f.frontend, name));
    fs.symlinkSync('/nonexistent-synthetic-target', path.join(f.frontend, name));
  }
  f.write('frontend/untracked.ts', 'SYNTHETIC_UNTRACKED');
  const context = prepareContext(f.frontend);
  assert.equal(context.sourceDirty, false);
  assert.equal(context.files.length, 6);
  assert.equal(context.tar.includes(Buffer.from('SYNTHETIC_')), false);
});

test('dockerignore rules exclude custom paths but cannot reinclude private inputs', (t) => {
  const f = fixture(t, {
    'frontend/.dockerignore': 'custom/**\n!custom/keep.txt\n!.env.example\n',
    'frontend/custom/drop.txt': 'DROP_ME',
    'frontend/custom/keep.txt': 'keep',
    'frontend/.env.example': 'EXCLUDED_EXAMPLE',
  });
  const names = tarList(prepareContext(f.frontend).tar);
  assert.equal(names.includes('custom/drop.txt'), false);
  assert.equal(names.includes('.env.example'), false);
  assert.equal(names.includes('custom/keep.txt'), true);
});

test('dirty worktree bytes are detected even when size and timestamps are restored', (t) => {
  const f = fixture(t);
  const source = path.join(f.frontend, 'public/icon.svg');
  const old = fs.statSync(source);
  fs.writeFileSync(source, '<SVG/>\n');
  fs.utimesSync(source, old.atime, old.mtime);
  assert.throws(() => prepareContext(f.frontend), /dirty/i);
  const dirty = prepareContext(f.frontend, { allowDirty: true });
  assert.equal(dirty.sourceDirty, true);
  assert.equal(dirty.files.find((file) => file.path === 'public/icon.svg').sha256, sha256('<SVG/>\n'));
});

test('dirty index is detected even when worktree contents match HEAD', (t) => {
  const f = fixture(t);
  f.write('frontend/public/icon.svg', '<SVG/>\n');
  f.git('add', '--', 'frontend/public/icon.svg');
  f.write('frontend/public/icon.svg', '<svg/>\n');
  assert.throws(() => prepareContext(f.frontend), /dirty/i);
  assert.equal(prepareContext(f.frontend, { allowDirty: true }).sourceDirty, true);
});

test('unrelated gitignore changes do not taint the selected frontend context', (t) => {
  const f = fixture(t);
  const first = prepareContext(f.frontend);
  f.write('.gitignore', 'changed-unrelated-output\n');
  assert.equal(prepareContext(f.frontend).sourceDirty, false);
  assert.deepEqual(prepareContext(f.frontend).tar, first.tar);
});

test('executable mode and tracked add/delete changes affect source identity', (t) => {
  const f = fixture(t);
  fs.chmodSync(path.join(f.frontend, 'app/page.tsx'), 0o755);
  assert.throws(() => prepareContext(f.frontend), /dirty/i);
  assert.equal(prepareContext(f.frontend, { allowDirty: true }).files.find((file) => file.path === 'app/page.tsx').mode, '100755');
  f.git('rm', '--', 'frontend/public/icon.svg');
  f.write('frontend/public/new.svg', '<svg/>');
  f.git('add', '--', 'frontend/public/new.svg');
  const names = tarList(prepareContext(f.frontend, { allowDirty: true }).tar);
  assert.equal(names.includes('public/icon.svg'), false);
  assert.equal(names.includes('public/new.svg'), true);
});

test('requested revision must be an exact lowercase HEAD SHA', (t) => {
  const f = fixture(t);
  for (const revision of ['HEAD', f.revision.slice(0, 12), '0'.repeat(40), f.revision.toUpperCase()]) {
    assert.throws(() => prepareContext(f.frontend, { revision }), /revision|HEAD/i);
  }
  assert.equal(prepareContext(f.frontend, { revision: f.revision }).revision, f.revision);
});

test('selected leaf and parent symlinks are rejected', (t) => {
  for (const target of ['public/icon.svg', 'public']) {
    const f = fixture(t);
    fs.rmSync(path.join(f.frontend, target), { recursive: true });
    fs.symlinkSync('/nonexistent-synthetic-target', path.join(f.frontend, target));
    assert.throws(() => prepareContext(f.frontend, { allowDirty: true }), /symlink|regular|directory/i);
  }
});

test('freeze rejects later source, mode and index changes', (t) => {
  for (const change of ['bytes', 'mode', 'index']) {
    const f = fixture(t);
    const context = prepareContext(f.frontend);
    if (change === 'bytes') f.write('frontend/public/icon.svg', '<changed/>');
    if (change === 'mode') fs.chmodSync(path.join(f.frontend, 'public/icon.svg'), 0o755);
    if (change === 'index') f.git('update-index', '--chmod=+x', 'frontend/public/icon.svg');
    assert.throws(() => assertContextUnchanged(context), /changed|freeze/i);
    assert.equal(sha256(context.tar), context.contextSha256);
  }
});

test('local build request has only fixed exporters, attestations and public arguments', (t) => {
  const f = fixture(t);
  const context = prepareContext(f.frontend);
  const request = createBuildRequest(context, { output: f.output }, { PRIVATE_TOKEN: 'SYNTHETIC_PRIVATE' });
  assert.equal(request.source_revision, f.revision);
  assert.equal(request.source_dirty, false);
  assert.deepEqual(request.build_args.slice(0, 2), ['buildx', 'build']);
  assert.equal(request.build_args.at(-1), '-');
  assert.ok(request.build_args.includes('linux/amd64'));
  assert.ok(request.build_args.includes('--provenance=mode=max,version=v0.2'));
  assert.ok(request.build_args.includes('type=sbom,generator=docker.io/docker/buildkit-syft-scanner@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9'));
  assert.ok(request.build_args.includes(`type=oci,dest=${f.output}/image,tar=false`));
  assert.ok(request.build_args.includes('BACKEND_URL=http://backend:8080'));
  assert.ok(request.build_args.includes('BACKEND_PORT=8080'));
  assert.ok(request.build_args.includes('FRONTEND_PORT=3000'));
  assert.ok(request.build_args.includes('NEXT_PUBLIC_API_URL='));
  assert.equal(request.build_args.some((arg) => arg.includes('type=registry') || arg === '--push'), false);
  assert.equal(JSON.stringify(request).includes('SYNTHETIC_PRIVATE'), false);
  assert.ok(request.validation_args.includes('--metadata'));
  assert.ok(request.validation_args.includes(`${f.output}/metadata.json`));
});

test('only public allowlisted URL values can enter build arguments', (t) => {
  const f = fixture(t);
  const context = prepareContext(f.frontend);
  const request = createBuildRequest(context, { output: f.output }, { NEXT_PUBLIC_API_URL: 'https://app.example.invalid/api' });
  assert.ok(request.build_args.includes('NEXT_PUBLIC_API_URL=https://app.example.invalid/api'));
  for (const value of ['https://user:password@example.invalid', 'https://example.invalid?token=synthetic', 'https://example.invalid#synthetic', 'http://example.invalid', 'https://example.invalid?', 'https://example.invalid#', ' https://example.invalid', 'https://example.invalid\\path', 'https:example.invalid', 'https://@example.invalid']) {
    assert.throws(() => createBuildRequest(context, { output: f.output }, { NEXT_PUBLIC_API_URL: value }), /public|URL/i);
  }
  for (const value of ['http://localhost:3000/api', 'http://127.0.0.1:3000', 'http://[::1]:3000']) {
    assert.ok(createBuildRequest(context, { output: f.output }, { NEXT_PUBLIC_API_URL: value }).build_args.includes(`NEXT_PUBLIC_API_URL=${value}`));
  }
  assert.ok(createBuildRequest(context, { output: f.output }, { BACKEND_URL: 'http://backend:9000/service' }).build_args.includes('BACKEND_URL=http://backend:9000/service'));
  for (const name of ['BACKEND_URL', 'NEXT_PUBLIC_ADMIN_API_URL', 'NEXT_PUBLIC_SOURCE_FILE_ORIGINS']) {
    for (const value of ['https://user:password@example.invalid', 'https://example.invalid?token=synthetic', 'https://example.invalid#synthetic']) {
      assert.throws(() => createBuildRequest(context, { output: f.output }, { [name]: value }), /public|URL/i);
    }
  }
});

test('public ports are decimal numbers in the supported range', (t) => {
  const f = fixture(t);
  const context = prepareContext(f.frontend);
  for (const name of ['BACKEND_PORT', 'FRONTEND_PORT']) {
    for (const value of ['1', '9000', '65535']) {
      assert.ok(createBuildRequest(context, { output: f.output }, { [name]: value }).build_args.includes(`${name}=${value}`));
    }
    for (const value of ['', '0', '65536', '-1', '3000.0', '03', '3e3', ' 3000', '3000\n']) {
      assert.throws(() => createBuildRequest(context, { output: f.output }, { [name]: value }), /port|argument/i);
    }
  }
});

test('publish requires explicit CI, clean source, exact GHCR target and configured Docker directory', (t) => {
  const f = fixture(t);
  const context = prepareContext(f.frontend);
  const config = path.join(f.root, 'docker-config');
  fs.mkdirSync(config);
  const pushReference = `ghcr.io/example/task-per-minute-frontend:${f.revision}`;
  const environment = { GITHUB_ACTIONS: 'true', DOCKER_CONFIG: config };
  for (const env of [{}, { GITHUB_ACTIONS: 'false', DOCKER_CONFIG: config }, { GITHUB_ACTIONS: 'true' }]) {
    assert.throws(() => createBuildRequest(context, { output: f.output, pushReference }, env), /publish|GITHUB_ACTIONS|DOCKER_CONFIG/i);
  }
  for (const ref of [`${pushReference}-extra`, 'ghcr.io/example/task-per-minute-frontend:latest', `docker.io/example/frontend:${f.revision}`]) {
    assert.throws(() => createBuildRequest(context, { output: f.output, pushReference: ref }, environment), /reference|publish/i);
  }
  f.write('frontend/public/icon.svg', '<changed/>');
  const dirty = prepareContext(f.frontend, { allowDirty: true });
  assert.throws(() => createBuildRequest(dirty, { output: f.output, pushReference }, environment), /clean|dirty/i);
  const request = createBuildRequest(context, { output: f.output, pushReference }, environment);
  assert.ok(request.build_args.includes(`type=registry,name=${pushReference},oci-mediatypes=true`));
  assert.ok(request.build_args.includes(`type=oci,dest=${f.output}/image,tar=false`));
});

test('remote Docker endpoints and exporter injection paths are rejected', (t) => {
  const f = fixture(t);
  const context = prepareContext(f.frontend);
  for (const host of ['ssh://example.invalid', 'tcp://127.0.0.1:2375', 'unix://remote/path', 'unix:relative']) {
    assert.throws(() => createBuildRequest(context, { output: f.output }, { DOCKER_HOST: host }), /local|unix|DOCKER_HOST/i);
  }
  for (const output of ['relative', `${f.output},type=registry`, `${f.output}\nother`]) {
    assert.throws(() => createBuildRequest(context, { output }), /output/i);
  }
});

function fakeBuild(f, calls, mutate = () => {}, endpoint = 'unix:///var/run/docker.sock', contextHost = 'unix:///var/run/docker.sock') {
  return (program, args, options) => {
    calls.push({ program, args, options });
    if (program === 'docker' && args[1] === 'version') return Buffer.from('github.com/docker/buildx v0.35.0 fixture');
    if (program === 'docker' && args[0] === 'buildx' && args[1] === 'inspect') {
      assert.deepEqual(args, ['buildx', 'inspect'], 'buildx inspect does not support --format');
      return Buffer.from(`Name: fixture-builder\nDriver: docker-container\n\nNodes:\nName: fixture-builder0\nEndpoint: ${endpoint}\nStatus: running\nBuildKit version: v0.32.2\nPlatforms: linux/amd64\n`);
    }
    if (program === 'docker' && args[0] === 'context' && args[1] === 'inspect') {
      assert.deepEqual(args.slice(3), ['--format', '{{json .Endpoints.docker.Host}}']);
      return Buffer.from(JSON.stringify(contextHost));
    }
    if (program === 'docker' && args[1] === 'build') {
      assert.ok(Buffer.isBuffer(options.input));
      const contextArg = args.find((arg) => arg.startsWith('CONTEXT_SHA256='));
      assert.equal(contextArg, `CONTEXT_SHA256=${sha256(options.input)}`);
      fs.mkdirSync(path.join(f.output, 'image'));
      fs.writeFileSync(path.join(f.output, 'image', 'index.json'), '{}');
      fs.writeFileSync(path.join(f.output, 'metadata.json'), '{}');
      mutate();
      return Buffer.alloc(0);
    }
    if (program === process.execPath && args[0].endsWith('validate-frontend-image.mjs')) {
      fs.writeFileSync(path.join(f.output, 'identity.json'), JSON.stringify({ status: 'pass', source_revision: f.revision, source_dirty: false }));
      return Buffer.alloc(0);
    }
    throw new Error('unexpected external command');
  };
}

test('prepare-only writes private request JSON without executing Docker or persisting sources', (t) => {
  const f = fixture(t);
  buildFrontendImage(f.frontend, { output: f.output, prepareOnly: true }, {
    environment: {}, run: () => assert.fail('prepare-only must not execute build commands'),
  });
  assert.deepEqual(fs.readdirSync(f.output), ['request.json']);
  assert.equal(fs.statSync(f.output).mode & 0o777, 0o700);
  assert.equal(fs.statSync(path.join(f.output, 'request.json')).mode & 0o777, 0o600);
  const request = JSON.parse(fs.readFileSync(path.join(f.output, 'request.json'), 'utf8'));
  assert.equal(request.source_revision, f.revision);
  assert.equal(JSON.stringify(request).includes('export default'), false);
});

test('build consumes frozen tar, scrubs inherited credentials and restricts output modes', (t) => {
  const f = fixture(t);
  fs.chmodSync(path.join(f.frontend, 'app/page.tsx'), 0o755);
  const calls = [];
  buildFrontendImage(f.frontend, { output: f.output, allowDirty: true }, {
    environment: { PATH: process.env.PATH, DOCKER_HOST: 'unix:///run/user/1000/podman/podman.sock', DOCKER_AUTH_CONFIG: 'SYNTHETIC_AUTH', AWS_SECRET_ACCESS_KEY: 'SYNTHETIC_SECRET', NODE_OPTIONS: '--bad-option' },
    run: fakeBuild(f, calls),
  });
  const build = calls.find((call) => call.args[1] === 'build');
  assert.ok(build.args.includes('SOURCE_DIRTY=true'));
  const listing = execFileSync('tar', ['--list', '--verbose', '--numeric-owner', '--file=-'], {
    input: build.options.input, env: { PATH: process.env.PATH, LC_ALL: 'C' },
  }).toString();
  assert.match(listing, /^-rwxr-xr-x\s+0\/0\s+.*app\/page\.tsx$/m);
  assert.match(listing, /^-rw-r--r--\s+0\/0\s+.*package-lock\.json$/m);
  const lockBytes = execFileSync('tar', ['--extract', '--to-stdout', '--file=-', 'package-lock.json'], {
    input: build.options.input, env: { PATH: process.env.PATH, LC_ALL: 'C' },
  });
  assert.ok(build.args.includes(`LOCKFILE_SHA256=${sha256(lockBytes)}`));
  assert.equal(build.options.env.DOCKER_HOST, 'unix:///run/user/1000/podman/podman.sock');
  assert.equal(build.options.env.DOCKER_CONFIG, path.join(f.output, 'docker-config'));
  assert.equal(build.options.env.DOCKER_AUTH_CONFIG, undefined);
  assert.equal(build.options.env.AWS_SECRET_ACCESS_KEY, undefined);
  assert.equal(build.options.env.NODE_OPTIONS, undefined);
  assert.deepEqual(build.args.slice(2, 4), ['--builder', 'fixture-builder']);
  for (const name of ['request.json', 'metadata.json', 'identity.json', 'image/index.json', 'build.log']) {
    assert.equal(fs.statSync(path.join(f.output, name)).mode & 0o777, 0o600);
  }
});

test('all builder nodes must be running and local with an unambiguous supported driver', (t) => {
  const safe = 'Name: fixture\nDriver: docker-container\nNodes:\nName: first\nEndpoint: unix:///var/run/docker.sock\nStatus: running\n';
  const inspections = [
    `${safe}Name: second\nEndpoint: ssh://example.invalid\nStatus: running\n`,
    safe.replace('Status: running', 'Status: stopped'),
    safe.replace('Driver: docker-container', 'Driver: remote'),
    safe.replace('Endpoint: unix:///var/run/docker.sock', 'Endpoint: unix:///var/run/docker.sock\nEndpoint: unix:///other.sock'),
    safe.replace('Nodes:', 'Other:'),
  ];
  for (const inspection of inspections) {
    const f = fixture(t);
    const calls = [];
    const run = fakeBuild(f, calls);
    assert.throws(() => buildFrontendImage(f.frontend, { output: f.output }, {
      environment: {}, run: (program, args, options) => args[1] === 'inspect' ? Buffer.from(inspection) : run(program, args, options),
    }), /local|running|ambiguous|nodes/i);
    assert.equal(calls.some((call) => call.args[1] === 'build'), false);
  }
});

test('named builder contexts must resolve locally and explicit Docker configuration is retained', (t) => {
  const f = fixture(t);
  const config = path.join(f.root, 'explicit-docker');
  fs.mkdirSync(config);
  const calls = [];
  buildFrontendImage(f.frontend, { output: f.output }, {
    environment: { DOCKER_CONFIG: config, DOCKER_HOST: 'unix:///run/user/1000/podman/podman.sock', BUILDX_BUILDER: 'untrusted-override' },
    run: fakeBuild(f, calls, () => {}, 'default'),
  });
  const build = calls.find((call) => call.args[1] === 'build');
  assert.equal(build.options.env.DOCKER_CONFIG, config);
  assert.equal(build.options.env.BUILDX_BUILDER, undefined);
  assert.equal(build.options.env.DOCKER_HOST, 'unix:///run/user/1000/podman/podman.sock');
  assert.ok(calls.some((call) => call.args[0] === 'context' && call.args[2] === 'default'));
  const remote = fixture(t);
  const remoteCalls = [];
  assert.throws(() => buildFrontendImage(remote.frontend, { output: remote.output }, {
    environment: {}, run: fakeBuild(remote, remoteCalls, () => {}, 'remote-context', 'ssh://example.invalid'),
  }), /local|unix|endpoint/i);
  assert.equal(remoteCalls.some((call) => call.args[1] === 'build'), false);
});

test('build failure diagnostics stay in a private log without exposing child output', (t) => {
  const f = fixture(t);
  const calls = [];
  const run = fakeBuild(f, calls);
  assert.throws(() => buildFrontendImage(f.frontend, { output: f.output }, {
    environment: {}, run: (program, args, options) => {
      if (args[1] === 'build') {
        assert.equal(typeof options.logFd, 'number');
        assert.equal(options.captureOutput, false);
        fs.writeSync(options.logFd, 'SYNTHETIC_BUILD_FAILURE\n');
        throw new Error('docker command failed (exit 1)');
      }
      return run(program, args, options);
    },
  }), /docker command failed/);
  const log = path.join(f.output, 'build.log');
  assert.equal(fs.readFileSync(log, 'utf8'), 'SYNTHETIC_BUILD_FAILURE\n');
  assert.equal(fs.statSync(log).mode & 0o777, 0o600);
  assert.equal(calls.some((call) => call.args[0].endsWith('validate-frontend-image.mjs')), false);
});

test('subprocess failure preserves stdout and stderr in build.log without a real Docker process', (t) => {
  const f = fixture(t);
  // This executable is a synthetic CLI fixture, not a container runtime.
  f.write('bin/docker', `#!${process.execPath}
const command = process.argv[3];
if (command === 'version') process.stdout.write('github.com/docker/buildx v0.35.0 fixture');
else if (command === 'inspect') process.stdout.write('Name: fixture\\nDriver: docker-container\\nNodes:\\nName: fixture0\\nEndpoint: unix:///var/run/docker.sock\\nStatus: running\\n');
else if (command === 'build') {
  process.stdin.resume();
  process.stdin.on('end', () => {
    process.stdout.write('SYNTHETIC_STDOUT\\n');
    process.stderr.write('SYNTHETIC_STDERR\\n');
    process.exitCode = 37;
  });
} else process.exitCode = 38;
`);
  fs.chmodSync(path.join(f.root, 'bin/docker'), 0o700);
  assert.throws(() => buildFrontendImage(f.frontend, { output: f.output }, {
    environment: { PATH: `${path.join(f.root, 'bin')}:${process.env.PATH}` },
  }), (error) => error.message === 'docker command failed (exit 37)');
  const log = path.join(f.output, 'build.log');
  const contents = fs.readFileSync(log, 'utf8');
  assert.match(contents, /SYNTHETIC_STDOUT/);
  assert.match(contents, /SYNTHETIC_STDERR/);
  assert.equal(fs.statSync(log).mode & 0o777, 0o600);
  assert.equal(fs.existsSync(path.join(f.output, 'identity.json')), false);
});

test('changes during build prevent validation and remote builders never reach build', (t) => {
  const f = fixture(t);
  const calls = [];
  assert.throws(() => buildFrontendImage(f.frontend, { output: f.output }, {
    environment: {}, run: fakeBuild(f, calls, () => f.write('frontend/public/icon.svg', '<changed/>')),
  }), /changed|freeze/i);
  assert.equal(calls.some((call) => call.args[0].endsWith('validate-frontend-image.mjs')), false);
  const other = fixture(t);
  const remoteCalls = [];
  assert.throws(() => buildFrontendImage(other.frontend, { output: other.output }, {
    environment: {}, run: fakeBuild(other, remoteCalls, () => {}, 'tcp://example.invalid:2375'),
  }), /local|unix|endpoint/i);
  assert.equal(remoteCalls.some((call) => call.args[1] === 'build'), false);
});

test('existing output is never overwritten and CLI rejects unknown or duplicate options', (t) => {
  const f = fixture(t);
  fs.mkdirSync(f.output);
  fs.writeFileSync(path.join(f.output, 'sentinel'), 'preserve');
  assert.throws(() => buildFrontendImage(f.frontend, { output: f.output, prepareOnly: true }), /exist|new/i);
  assert.equal(fs.readFileSync(path.join(f.output, 'sentinel'), 'utf8'), 'preserve');
  assert.throws(() => parseArguments(['--output', f.output, '--push']), /unknown/i);
  assert.throws(() => parseArguments(['--output', f.output, '--output', '/other']), /duplicate/i);
  assert.deepEqual(parseArguments(['--output', f.output, '--revision', f.revision, '--allow-dirty', '--prepare-only']), {
    output: f.output, revision: f.revision, allowDirty: true, prepareOnly: true,
  });
});
