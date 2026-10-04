import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { completePipeline, githubAPI, mergeDependencyUpdate, repository, requiredJobs } from './dependabot-merge.mjs';

const head = 'a'.repeat(40);
const base = 'b'.repeat(40);
const other = 'c'.repeat(40);
const read = path => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8');

function fixture() {
  const pr = {
    number: 7, state: 'open', draft: false, merged: false, changed_files: 2,
    user: {login: 'dependabot[bot]', type: 'Bot'},
    head: {ref: 'dependabot/multi/dependencies', sha: head, repo: {full_name: repository}},
    base: {ref: 'dev', sha: base, repo: {full_name: repository}},
  };
  const run = {
    id: 19, workflow_id: 23, run_attempt: 1, path: '.github/workflows/pipeline.yml',
    event: 'pull_request', status: 'completed', conclusion: 'success',
    head_repository: {full_name: repository}, head_sha: head, head_branch: pr.head.ref,
  };
  const state = {
    pr, run, latest: structuredClone(run), current: null, currentBase: base,
    jobs: requiredJobs.map(name => ({name, status: 'completed', conclusion: 'success'})),
    files: ['frontend/package.json', 'frontend/package-lock.json'].map(filename => ({filename, status: 'modified'})),
    comparison: 'ahead', calls: [], approved: false,
  };
  const api = async (method, path, body) => {
    state.calls.push({method, path, body});
    if (method === 'POST') { state.approved = true; return {}; }
    if (method === 'PUT') return {merged: true};
    if (path.includes('/actions/runs/')) return state.run;
    if (path.includes('/actions/workflows/')) return {workflow_runs: [state.latest]};
    if (path.includes('/pulls?')) return [state.pr];
    if (path.endsWith('/pulls/7')) return state.approved && state.current ? state.current : state.pr;
    if (path.includes('/git/ref/')) return {object: {sha: state.approved ? state.currentBase : base}};
    if (path.includes('/compare/')) return {status: state.comparison};
    throw new Error(`Unexpected API path ${path}`);
  };
  api.pages = async path => path.endsWith('/jobs') ? state.jobs : state.files;
  state.api = api;
  return state;
}

const writes = state => state.calls.filter(call => call.method !== 'GET');

test('approves the tested commit and merges it into dev only after every pipeline job passes', async () => {
  const state = fixture();
  assert.match(await mergeDependencyUpdate(state.api, 19), /Merged dependency pull request #7 into dev/);
  assert.deepEqual(writes(state).map(call => call.body), [
    {event: 'APPROVE', commit_id: head}, {sha: head, merge_method: 'squash'},
  ]);
});

for (const [name, change] of [
  ['human author', s => { s.pr.user.login = 'maintainer'; }],
  ['spoofed bot login', s => { s.pr.user.type = 'User'; }],
  ['fork', s => { s.pr.head.repo.full_name = 'other/fork'; }],
  ['main target', s => { s.pr.base.ref = 'main'; }],
  ['closed PR', s => { s.pr.state = 'closed'; }],
  ['draft PR', s => { s.pr.draft = true; }],
  ['new head', s => { s.pr.head.sha = other; }],
  ['push run', s => { s.run.event = 'push'; }],
  ['different workflow', s => { s.run.path = '.github/workflows/other.yml'; }],
  ['failed run', s => { s.run.conclusion = 'failure'; }],
  ['newer pending run', s => { s.latest.id++; s.latest.status = 'in_progress'; }],
  ['newer failed run', s => { s.latest.id++; s.latest.conclusion = 'failure'; }],
  ['rerun in progress', s => { s.latest.run_attempt++; s.latest.status = 'in_progress'; }],
  ['missing build', s => { s.jobs.pop(); }],
  ['skipped integration job', s => { s.jobs[4].conclusion = 'skipped'; }],
  ['failed extra job', s => { s.jobs.push({name: 'extra', status: 'completed', conclusion: 'failure'}); }],
  ['workflow change', s => { s.files[0].filename = '.github/workflows/pipeline.yml'; }],
  ['helper change', s => { s.files[0].filename = '.github/scripts/dependabot-merge.mjs'; }],
  ['renamed manifest', s => { s.files[0].status = 'renamed'; }],
  ['incomplete file list', s => { s.pr.changed_files++; }],
]) {
  test(`refuses ${name} without approval or merge`, async () => {
    const state = fixture();
    change(state);
    await mergeDependencyUpdate(state.api, 19);
    assert.deepEqual(writes(state), []);
  });
}

test('updates a stale branch and requires a new pipeline, without approving it', async () => {
  const state = fixture();
  state.comparison = 'diverged';
  assert.match(await mergeDependencyUpdate(state.api, 19), /new full pipeline/);
  assert.deepEqual(writes(state), [{method: 'PUT', path: `/repos/${repository}/pulls/7/update-branch`, body: {expected_head_sha: head}}]);
});

for (const [name, change] of [
  ['head', s => { s.current = structuredClone(s.pr); s.current.head.sha = other; }],
  ['base branch', s => { s.current = structuredClone(s.pr); s.current.base.ref = 'main'; }],
  ['base revision', s => { s.currentBase = other; }],
]) {
  test(`does not merge if ${name} changes after approval`, async () => {
    const state = fixture();
    change(state);
    assert.match(await mergeDependencyUpdate(state.api, 19), /merge deferred/);
    assert.equal(writes(state).length, 1);
    assert.equal(writes(state)[0].method, 'POST');
  });
}

test('rechecks CI after approving the commit', async () => {
  const state = fixture();
  const original = state.api;
  const api = async (...args) => {
    const result = await original(...args);
    if (args[0] === 'POST') state.latest.conclusion = 'failure';
    return result;
  };
  api.pages = original.pages;
  assert.match(await mergeDependencyUpdate(api, 19), /merge deferred/);
  assert.equal(writes(state).length, 1);
});

test('rejects invalid run IDs and missing credentials', async () => {
  for (const id of ['', '0', '../pulls', '19\n', '19; echo secret']) {
    await assert.rejects(mergeDependencyUpdate(fixture().api, id), /Invalid workflow run ID/);
  }
  assert.throws(() => githubAPI(''), /TOKEN/);
  assert.equal(completePipeline([]), false);
});

test('merge workflow never executes PR code or consumes CI artifacts with its token', () => {
  const source = read('.github/workflows/dependabot-merge.yml');
  assert.match(source, /workflow_run:\n\s+workflows: \[pipeline\]\n\s+types: \[completed\]/);
  assert.match(source, /ref: \$\{\{ github.sha \}\}/);
  assert.match(source, /persist-credentials: false/);
  assert.match(source, /GH_TOKEN: \$\{\{ secrets.TOKEN \}\}/);
  assert.doesNotMatch(source, /pull_request_target:|download-artifact|secrets: inherit|--admin/);
  assert.deepEqual([...source.matchAll(/^\s+run: (.+)$/gm)].map(match => match[1]), ['node .github/scripts/dependabot-merge.mjs']);
  assert.deepEqual([...source.matchAll(/^\s+uses: (.+)$/gm)].map(match => match[1]), [
    'actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2',
  ]);
});

test('every reusable CI job is included in the merge gate', () => {
  const expected = ['resolve target'];
  for (const [file, prefix] of [
    ['reusable-backend-checks.yml', 'backend checks'],
    ['reusable-frontend-verify.yml', 'frontend verify'],
    ['reusable-build-images.yml', 'build images'],
  ]) {
    for (const match of read(`.github/workflows/${file}`).matchAll(/^    name: (.+)$/gm)) {
      expected.push(`${prefix} / ${match[1]}`);
    }
  }
  assert.deepEqual([...requiredJobs].sort(), expected.sort());
});
