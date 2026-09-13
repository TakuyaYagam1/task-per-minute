import { spawn, type ChildProcessByStdio } from 'node:child_process';
import { createServer } from 'node:net';
import { resolve } from 'node:path';
import { type Readable } from 'node:stream';

import { expect, test, type Page, type Route } from '@playwright/test';
import type { components } from '../lib/shared/api/schema';

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, 'e2e/fixtures/arena');
const nextBinary = resolve(frontendRoot, 'node_modules/.bin/next');
const fixtureHost = '127.0.0.1';
const readinessTimeoutMs = 60_000;
const tournamentId = '00000000-0000-4000-8000-000000000001';
const seriesId = '00000000-0000-4000-8000-000000000010';
const snapshotPath = `/api/v1/tournaments/${tournamentId}/snapshot`;
const noShowPath = `/api/v1/admin/tournaments/${tournamentId}/waves/00000000-0000-4000-8000-000000000015/no-shows`;
const snapshotPattern = `**${snapshotPath}`;
const noShowPattern = `**${noShowPath}`;

type PublicSnapshot = components['schemas']['PublicRecoverySnapshot'];
type NoShowRequest = components['schemas']['OperatorNoShowRequest'];
type FixtureSuccess = {
  state: 'success';
  value: Record<string, unknown> | null;
};
type FixtureError = {
  state: 'error';
  error: {
    name: string;
    message: string;
    status?: number;
    kind?: string;
    retryAfter?: string | null;
    problem?: { status: number; title: string };
  };
};
type FixtureResult = FixtureSuccess | FixtureError;

const isFixtureResult = (value: unknown): value is FixtureResult =>
  value !== null
  && value !== undefined
  && typeof value === 'object'
  && 'state' in value
  && (value.state === 'success' || value.state === 'error');

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = '';
let fixtureOutput = '';

const publicSnapshot = (): PublicSnapshot => ({
  tournament: {
    tournament_id: tournamentId,
    preset: 'tournament_v1',
    state: 'swiss',
    roster_size: 8,
    started_at: '2026-09-08T10:00:00Z',
    finished_at: null,
    projection_revision: 4,
  },
  scoreboard: {
    tournament_id: tournamentId,
    projection_revision: 4,
    entries: [
      {
        rank: 1,
        display_name: 'alice',
        points: 3,
        buchholz: 2,
        effective_time_ms: 42_000,
      },
    ],
  },
  bracket: {
    tournament_id: tournamentId,
    projection_revision: 4,
    matches: [
      {
        stage: 'semifinal',
        position: 1,
        first_display_name: 'alice',
        second_display_name: 'bob',
        score: { first_participant_wins: 1, second_participant_wins: 0 },
        state: 'active',
      },
    ],
  },
  live_series: [
    {
      series_id: seriesId,
      format: 'bo3',
      state: 'active',
      first_display_name: 'alice',
      second_display_name: 'bob',
      score: { first_wins: 1, second_wins: 0 },
      current_game_position: 2,
    },
  ],
  official_results: [],
  live_draft: null,
  next_cursor: { projection_revision: 4, event_sequence: 7 },
});

const noShowBody = (): NoShowRequest => ({
  confirmed: true,
  expected_authority_revision: 4,
  expected_series_state: 'active',
  expected_wave_revision_id: '00000000-0000-4000-8000-000000000011',
  expected_window_revision_id: '00000000-0000-4000-8000-000000000012',
  game_result_revision_ids: [],
  reason: 'confirmed no-show',
  score_revision_id: '00000000-0000-4000-8000-000000000013',
  series_id: seriesId,
  series_result_revision_id: '00000000-0000-4000-8000-000000000014',
  tournament_id: tournamentId,
  wave_id: '00000000-0000-4000-8000-000000000015',
  window_id: '00000000-0000-4000-8000-000000000016',
});

const wait = (durationMs: number) => new Promise<void>((resolvePromise) => {
  setTimeout(resolvePromise, durationMs);
});

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once('error', reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === 'string') {
      server.close();
      reject(new Error('Could not reserve a loopback port for the Arena fixture'));
      return;
    }

    const port = address.port;
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolvePort(port);
    });
  });
});

const appendFixtureOutput = (chunk: Buffer | string): void => {
  fixtureOutput = `${fixtureOutput}${chunk.toString()}`.slice(-6_000);
};

const isFixtureReady = async (): Promise<boolean> => {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 1_000);

  try {
    const response = await fetch(fixtureURL, { signal: controller.signal });
    return response.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
};

const startFixture = async (): Promise<void> => {
  const port = await reservePort();
  fixtureURL = `http://${fixtureHost}:${port}`;
  fixtureOutput = '';

  const fixtureChild = spawn(
    nextBinary,
    ['dev', '--hostname', fixtureHost, '--port', String(port)],
    {
      cwd: fixtureRoot,
      detached: true,
      env: {
        NEXT_TELEMETRY_DISABLED: '1',
        NODE_ENV: 'development',
        NEXT_PUBLIC_API_URL: '',
        NEXT_PUBLIC_ADMIN_API_URL: '',
        PATH: process.env.PATH ?? '/usr/bin:/bin',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  fixtureProcess = fixtureChild;
  fixtureChild.stdout.on('data', appendFixtureOutput);
  fixtureChild.stderr.on('data', appendFixtureOutput);

  const deadline = Date.now() + readinessTimeoutMs;
  while (Date.now() < deadline) {
    if (fixtureChild.exitCode !== null) {
      throw new Error(`The Arena fixture exited before readiness.\n${fixtureOutput}`);
    }
    if (await isFixtureReady()) {
      return;
    }
    await wait(250);
  }

  throw new Error(`The Arena fixture did not become ready.\n${fixtureOutput}`);
};

const stopFixture = async (): Promise<void> => {
  const processToStop = fixtureProcess;
  fixtureProcess = undefined;
  if (!processToStop || processToStop.pid === undefined) {
    return;
  }

  const exited = new Promise<void>((resolveExit) => {
    if (processToStop.exitCode !== null) {
      resolveExit();
      return;
    }
    processToStop.once('exit', () => resolveExit());
  });

  try {
    process.kill(-processToStop.pid, 'SIGTERM');
  } catch {
    if (processToStop.exitCode === null) {
      processToStop.kill('SIGTERM');
    }
  }

  await Promise.race([exited, wait(5_000)]);
  if (processToStop.exitCode === null) {
    try {
      process.kill(-processToStop.pid, 'SIGKILL');
    } catch {
      processToStop.kill('SIGKILL');
    }
  }
};

const openFixture = async (page: Page): Promise<void> => {
  await page.goto(fixtureURL, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: 'Контракт API Arena' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Получить снимок' })).toBeEnabled();
  await expect(page.getByLabel('Результат вызова API')).toContainText('Ожидание');
};

const readResult = async (page: Page, state: FixtureResult['state']): Promise<FixtureResult> => {
  const result = page.getByLabel('Результат вызова API');
  await expect(result).toContainText(`"state":"${state}"`);
  const text = await result.textContent();
  if (!text) {
    throw new Error('Arena fixture returned an empty result');
  }
  const parsed: unknown = JSON.parse(text);
  if (!isFixtureResult(parsed)) {
    throw new Error('Arena fixture returned an unknown result shape');
  }
  return parsed;
};

const fulfillJSON = async (route: Route, status: number, body: unknown): Promise<void> => {
  await route.fulfill({
    status,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
};

const problemBody = (status: number, title = `Failure ${status}`) => ({
  type: 'about:blank',
  title,
  detail: `request failed with ${status}`,
  status,
  request_id: `request-${status}`,
});

test.describe('Arena browser API contract', () => {
  test.beforeAll(async () => {
    await startFixture();
  });

  test.afterAll(async () => {
    await stopFixture();
  });

  test('maps a valid Arena response into the public view model in Chromium', async ({ page }) => {
    const requests: string[] = [];
    await page.route(snapshotPattern, async (route) => {
      const request = route.request();
      requests.push(request.url());
      expect(request.method()).toBe('GET');
      await fulfillJSON(route, 200, publicSnapshot());
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Получить снимок' }).click();
    const result = await readResult(page, 'success');

    expect(result).toMatchObject({
      state: 'success',
      value: {
        tournament: {
          tournamentId,
          state: 'swiss',
          rosterSize: 8,
          projectionRevision: 4,
        },
        scoreboard: [{ displayName: 'alice', effectiveTimeMs: 42_000 }],
        bracket: [{ score: { firstWins: 1, secondWins: 0 } }],
        liveSeries: [{ currentGamePosition: 2 }],
        nextCursor: { projectionRevision: 4, eventSequence: 7 },
      },
    });
    expect(requests).toEqual([`${fixtureURL}${snapshotPath}`]);
  });

  for (const [status, kind] of [
    [401, 'unauthorized'],
    [403, 'forbidden'],
    [404, 'not_found'],
    [409, 'conflict'],
    [422, 'validation'],
    [429, 'rate_limited'],
  ] as const) {
    test(`preserves ${status} as a typed API error in Chromium`, async ({ page }) => {
      await page.route(snapshotPattern, async (route) => {
        await route.fulfill({
          status,
          headers: {
            'content-type': 'application/problem+json',
            ...(status === 429 ? { 'retry-after': '7' } : {}),
          },
          body: JSON.stringify(problemBody(status)),
        });
      });

      await openFixture(page);
      await page.getByRole('button', { name: 'Получить снимок' }).click();
      const result = await readResult(page, 'error');

      expect(result).toMatchObject({
        state: 'error',
        error: {
          name: 'ApiError',
          status,
          kind,
          problem: { status, title: `Failure ${status}` },
        },
      });
      if (status === 429) {
        expect(result).toMatchObject({ error: { retryAfter: '7' } });
      }
    });
  }

  test('normalizes a browser transport failure as a transport API error', async ({ page }) => {
    await page.route(snapshotPattern, async (route) => {
      await route.abort('failed');
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Получить снимок' }).click();
    const result = await readResult(page, 'error');

    expect(result).toMatchObject({
      state: 'error',
      error: { name: 'ApiError', status: 0, kind: 'transport' },
    });
  });

  test('accepts an operator 204 response without attempting JSON parsing', async ({ page }) => {
    const requests: { method: string; body: string | null; idempotencyKey: string | null }[] = [];
    const expectedBody = noShowBody();
    await page.route(noShowPattern, async (route) => {
      const request = route.request();
      requests.push({
        method: request.method(),
        body: request.postData(),
        idempotencyKey: request.headers()['idempotency-key'] ?? null,
      });
      expect(request.method()).toBe('POST');
      expect(request.headers()['content-type']).toContain('application/json');
      expect(request.postDataJSON()).toEqual(expectedBody);
      await route.fulfill({ status: 204 });
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Выполнить no-show' }).click();
    const result = await readResult(page, 'success');

    expect(result).toEqual({ state: 'success', value: null });
    expect(requests).toHaveLength(1);
    expect(requests[0]).toMatchObject({
      method: 'POST',
      idempotencyKey: 'contract-no-show',
    });
  });

  test('turns malformed successful JSON into a contract error in the browser', async ({ page }) => {
    await page.route(snapshotPattern, async (route) => {
      await route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json' },
        body: '{',
      });
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Получить снимок' }).click();
    const result = await readResult(page, 'error');

    expect(result).toMatchObject({
      state: 'error',
      error: {
        name: 'ApiContractError',
        message: 'Invalid API response: public tournament snapshot',
      },
    });
  });
});
