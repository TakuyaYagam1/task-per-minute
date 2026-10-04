import { pathToFileURL } from 'node:url';

export const repository = 'TakuyaYagam1/task-per-minute';
export const requiredJobs = [
  'resolve target',
  'backend checks / sql lint',
  'backend checks / lint',
  'backend checks / unit test',
  'backend checks / integration test',
  'backend checks / media test',
  'backend checks / build',
  'frontend verify / verify',
  'frontend verify / full-stack e2e',
  'build images / build backend image',
  'build images / build and verify frontend image',
];

const allowedFiles = new Set([
  'backend/go.mod', 'backend/go.sum', 'frontend/package.json', 'frontend/package-lock.json',
  'backend/Dockerfile', 'frontend/Dockerfile', 'test-data/importer/Dockerfile',
]);
const shaPattern = /^[a-f0-9]{40}$/;

function eligible(pr) {
  return pr.state === 'open' && !pr.draft && !pr.merged &&
    pr.user?.login === 'dependabot[bot]' && pr.user?.type === 'Bot' &&
    pr.base?.ref === 'dev' && pr.base?.repo?.full_name === repository &&
    pr.head?.repo?.full_name === repository && pr.head.ref.startsWith('dependabot/') &&
    shaPattern.test(pr.head.sha) && shaPattern.test(pr.base.sha);
}

function successfulRun(run, pr) {
  return run.path === '.github/workflows/pipeline.yml' && run.event === 'pull_request' &&
    run.status === 'completed' && run.conclusion === 'success' &&
    run.head_repository?.full_name === repository &&
    run.head_branch === pr.head.ref && run.head_sha === pr.head.sha;
}

export function completePipeline(jobs) {
  return jobs.length >= requiredJobs.length &&
    jobs.every(job => job.status === 'completed' && job.conclusion === 'success') &&
    requiredJobs.every(name => jobs.filter(job => job.name === name).length === 1);
}

export async function mergeDependencyUpdate(api, runId) {
  if (!/^[1-9][0-9]*$/.test(String(runId))) throw new Error('Invalid workflow run ID');
  const root = `/repos/${repository}`;
  const run = await api('GET', `${root}/actions/runs/${runId}`);
  if (run.path !== '.github/workflows/pipeline.yml' || run.event !== 'pull_request' ||
      run.status !== 'completed' || run.conclusion !== 'success' ||
      run.head_repository?.full_name !== repository || !run.head_branch?.startsWith('dependabot/')) {
    return 'Run is not a successful dependency pull request pipeline';
  }
  const query = new URLSearchParams({state: 'open', base: 'dev', head: `TakuyaYagam1:${run.head_branch}`, per_page: '100'});
  const pulls = await api('GET', `${root}/pulls?${query}`);
  if (pulls.length !== 1) return 'No unique open dependency pull request';
  const number = pulls[0].number;
  const prPath = `${root}/pulls/${number}`;
  const pr = await api('GET', prPath);
  if (!eligible(pr) || !successfulRun(run, pr)) return 'Pull request or revision is not eligible';

  const latestQuery = new URLSearchParams({event: 'pull_request', branch: pr.head.ref, head_sha: pr.head.sha, per_page: '100'});
  async function stillPassed() {
    const latest = await api('GET', `${root}/actions/workflows/${run.workflow_id}/runs?${latestQuery}`);
    const latestRun = latest.workflow_runs[0];
    if (latestRun?.id !== run.id || latestRun.run_attempt !== run.run_attempt || !successfulRun(latestRun, pr)) return false;
    const jobs = await api.pages(`${root}/actions/runs/${run.id}/attempts/${run.run_attempt}/jobs`, 'jobs');
    return completePipeline(jobs);
  }
  if (!await stillPassed()) return 'Latest pipeline is incomplete, stale, skipped or failed';
  const files = await api.pages(`${prPath}/files`);
  if (files.length === 0 || files.length !== pr.changed_files ||
      files.some(file => !allowedFiles.has(file.filename) || file.status !== 'modified')) {
    return 'Pull request changes files outside dependency manifests';
  }

  const basePath = `${root}/git/ref/heads/dev`;
  const base = await api('GET', basePath);
  if (!shaPattern.test(base.object.sha)) throw new Error('Invalid base revision');
  const comparison = await api('GET', `${root}/compare/${base.object.sha}...${pr.head.sha}`);
  if (comparison.status === 'behind' || comparison.status === 'diverged') {
    await api('PUT', `${prPath}/update-branch`, {expected_head_sha: pr.head.sha});
    return 'Updated dependency branch; a new full pipeline must pass before merge';
  }
  if (!['ahead', 'identical'].includes(comparison.status)) return 'Cannot verify base ancestry';

  await api('POST', `${prPath}/reviews`, {event: 'APPROVE', commit_id: pr.head.sha});
  const current = await api('GET', prPath);
  const currentBase = await api('GET', basePath);
  if (!eligible(current) || current.head.sha !== pr.head.sha ||
      currentBase.object.sha !== base.object.sha || !await stillPassed()) {
    return 'Pull request, base or checks changed after approval; merge deferred';
  }
  // The API enforces the expected head and repository rules; no admin bypass is requested.
  const result = await api('PUT', `${prPath}/merge`, {sha: pr.head.sha, merge_method: 'squash'});
  if (!result.merged) throw new Error('GitHub did not merge the pull request');
  return `Merged dependency pull request #${number} into dev`;
}

export function githubAPI(token) {
  if (!token) throw new Error('Actions secret TOKEN is not configured');
  const api = async (method, path, body) => {
    const response = await fetch(`https://api.github.com${path}`, {
      method, redirect: 'error', signal: AbortSignal.timeout(30_000),
      headers: {
        Accept: 'application/vnd.github+json', Authorization: `Bearer ${token}`,
        'X-GitHub-Api-Version': '2022-11-28', 'Content-Type': 'application/json',
      },
      ...(body === undefined ? {} : {body: JSON.stringify(body)}),
    });
    if (!response.ok) throw new Error(`GitHub API request failed with HTTP ${response.status}`);
    return response.json();
  };
  api.pages = async (path, field) => {
    const entries = [];
    for (let page = 1; page <= 30; page++) {
      const result = await api('GET', `${path}?per_page=100&page=${page}`);
      const batch = field ? result[field] : result;
      if (!Array.isArray(batch)) throw new Error('Invalid GitHub pagination response');
      entries.push(...batch);
      if (batch.length < 100) return entries;
    }
    throw new Error('GitHub pagination exceeded the reviewed limit');
  };
  return api;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GITHUB_REPOSITORY !== repository) throw new Error('Unexpected repository');
    console.log(await mergeDependencyUpdate(githubAPI(process.env.GH_TOKEN), process.env.RUN_ID));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
