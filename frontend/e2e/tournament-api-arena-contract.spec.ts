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
const waveId = '00000000-0000-4000-8000-000000000015';
const assignmentId = '00000000-0000-4000-8000-000000000017';
const attemptId = '00000000-0000-4000-8000-000000000018';
const participantId = '00000000-0000-4000-8000-000000000019';
const taskId = '00000000-0000-4000-8000-000000000020';
const taskSnapshotId = '00000000-0000-4000-8000-000000000021';
const receiptId = '00000000-0000-4000-8000-000000000022';
const readyWindowId = '00000000-0000-4000-8000-000000000023';
const snapshotPath = `/api/v1/tournaments/${tournamentId}/snapshot`;
const participantLobbyPath = `/api/v1/tournaments/${tournamentId}/participant/lobby`;
const participantAssignmentPath = `/api/v1/tournaments/${tournamentId}/participant/assignments/${assignmentId}`;
const participantSnapshotPath = `/api/v1/tournaments/${tournamentId}/participant/snapshot`;
const participantReadyPath = `/api/v1/tournaments/${tournamentId}/participant/waves/${waveId}/ready`;
const noShowPath = `/api/v1/admin/tournaments/${tournamentId}/waves/00000000-0000-4000-8000-000000000015/no-shows`;
const adminTasksPath = '/api/v1/admin/tasks';
const snapshotPattern = `**${snapshotPath}`;
const participantLobbyPattern = `**${participantLobbyPath}`;
const participantAssignmentPattern = `**${participantAssignmentPath}`;
const participantSnapshotPattern = `**${participantSnapshotPath}`;
const participantReadyPattern = `**${participantReadyPath}`;
const noShowPattern = `**${noShowPath}`;
const adminTasksPattern = `**${adminTasksPath}`;
const adminRefreshPattern = '**/api/v1/admin/refresh';

type PublicSnapshot = components['schemas']['PublicRecoverySnapshot'];
type NoShowRequest = components['schemas']['OperatorNoShowRequest'];
type ParticipantLobbyResponse = components['schemas']['ParticipantLobbyResponse'];
type ParticipantAssignmentResponse = components['schemas']['ParticipantAssignmentResponse'];
type ParticipantRecoverySnapshot = components['schemas']['ParticipantRecoverySnapshot'];
type ParticipantReadyEvent = components['schemas']['ReadinessEvent'];
type FixtureSuccess = {
  state: 'success';
  value: unknown;
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
        wins: 1,
        losses: 0,
        bye_count: 0,
        provisional_tie: false,
        qualification_status: 'pending',
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
        format: 'bo1',
        first_display_name: 'alice',
        second_display_name: 'bob',
        score: { first_participant_wins: 1, second_participant_wins: 0 },
        scheduled_at: null,
        state: 'active',
        winner_display_name: null,
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
      stage: 'swiss',
      round_number: 1,
      scheduled_at: null,
    },
  ],
  official_results: [],
  live_draft: null,
  swiss_rounds: [{ bye: null, round_number: 1, state: 'active' }],
  next_cursor: { projection_revision: 4, event_sequence: 7 },
});

const participantLobby = (): ParticipantLobbyResponse => ({
  attendance: 'checked_in',
  current_swiss_round: 1,
  participant_id: participantId,
  tournament_id: tournamentId,
  state: 'swiss',
  projection_revision: 4,
  required_action: 'ready',
  roster_locked: true,
  status: 'assigned',
  series: [{
    series_id: seriesId,
    state: 'active',
    format: 'bo3',
    opponent_display_name: 'Боб',
    wave_id: waveId,
  }],
  swiss_points: 3,
});

const participantAssignment = (): ParticipantAssignmentResponse => ({
  tournament_id: tournamentId,
  projection_revision: 4,
  assignment: {
    id: assignmentId,
    attempt_id: attemptId,
    active_snapshot: {
      snapshot_id: taskSnapshotId,
      task_id: taskId,
      version: 2,
      kind: 'normal',
      title: 'Проверка контракта',
      description: 'Стабильное описание задания',
      category: 'web',
      difficulty: 'medium',
      time_limit: 900,
      hints: ['Проверьте URL'],
      task_url: null,
      source_file_available: false,
    },
    context: {
      effective_deadline: null,
      game_id: '00000000-0000-4000-8000-000000000024',
      game_number: 1,
      game_state: 'paused',
      series_id: seriesId,
      series_score: { first_participant_wins: 1, second_participant_wins: 0 },
      slot_id: '00000000-0000-4000-8000-000000000025',
      stage: 'swiss',
      started_at: '2026-09-08T10:00:00Z',
      swiss_round: 1,
      wave_id: waveId,
    },
    undisclosed_reserve_count: 1,
    receipt: {
      id: receiptId,
      assignment_id: assignmentId,
      attempt_id: attemptId,
      participant_id: participantId,
      snapshot_id: taskSnapshotId,
      task_id: taskId,
      delivered_at: '2026-09-08T10:01:00Z',
    },
  },
});

const participantRecoverySnapshot = (): ParticipantRecoverySnapshot => ({
  tournament_id: tournamentId,
  projection_revision: 5,
  lobby: {
    ...participantLobby(),
    projection_revision: 5,
  },
  series: null,
  wave: null,
  draft: null,
  assignment: null,
  runtime: null,
  next_cursor: {
    projection_revision: 5,
    participant_view_revision: 3,
    event_sequence: 8,
  },
});

const participantReadyEvent = (commandId: string): ParticipantReadyEvent => ({
  command_id: commandId,
  wave_id: waveId,
  window_id: readyWindowId,
  participant_id: participantId,
  type: 'ready',
  occurred_at: '2026-09-08T10:02:00Z',
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
        scoreboard: [{
          displayName: 'alice',
          effectiveTimeMs: 42_000,
          wins: 1,
          losses: 0,
          byeCount: 0,
          provisionalTie: false,
          qualificationStatus: 'pending',
        }],
        bracket: [{ score: { firstWins: 1, secondWins: 0 } }],
        liveSeries: [{ currentGamePosition: 2 }],
        nextCursor: { projectionRevision: 4, eventSequence: 7 },
      },
    });
    expect(requests).toEqual([`${fixtureURL}${snapshotPath}`]);
  });

  test('preserves participant lobby and assignment URLs and identities', async ({ page }) => {
    const requests: string[] = [];
    await page.route(participantLobbyPattern, async (route) => {
      const request = route.request();
      requests.push(request.url());
      expect(request.method()).toBe('GET');
      await fulfillJSON(route, 200, participantLobby());
    });
    await page.route(participantAssignmentPattern, async (route) => {
      const request = route.request();
      requests.push(request.url());
      expect(request.method()).toBe('GET');
      await fulfillJSON(route, 200, participantAssignment());
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Получить лобби и назначение' }).click();
    const result = await readResult(page, 'success');

    expect(result).toMatchObject({
      state: 'success',
      value: {
        lobby: {
          tournament_id: tournamentId,
          projection_revision: 4,
          series: [{ series_id: seriesId, wave_id: waveId }],
        },
        assignment: {
          tournament_id: tournamentId,
          projection_revision: 4,
          assignment: {
            id: assignmentId,
            attempt_id: attemptId,
            active_snapshot: { snapshot_id: taskSnapshotId, task_id: taskId },
            receipt: {
              id: receiptId,
              assignment_id: assignmentId,
              attempt_id: attemptId,
              participant_id: participantId,
            },
          },
        },
      },
    });
    expect(requests).toEqual([
      `${fixtureURL}${participantLobbyPath}`,
      `${fixtureURL}${participantAssignmentPath}`,
    ]);
  });

  test('reuses one idempotency key for the same participant ready intent', async ({ page }) => {
    const playerCSRFToken = 'participant-ready-intent-csrf';
    const requests: { key: string | undefined; body: unknown }[] = [];
    await page.addInitScript((token) => {
      document.cookie = `tpm_player_csrf=${encodeURIComponent(token)}; Path=/`;
    }, playerCSRFToken);
    await page.route(participantReadyPattern, async (route) => {
      const request = route.request();
      requests.push({ key: request.headers()['idempotency-key'], body: request.postDataJSON() });
      expect(request.method()).toBe('POST');
      expect(request.headers()['x-csrf-token']).toBe(playerCSRFToken);
      await fulfillJSON(route, 200, participantReadyEvent(request.headers()['idempotency-key'] ?? ''));
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Повторить ready intent' }).click();
    const result = await readResult(page, 'success');

    expect(requests).toHaveLength(2);
    expect(requests[0].key).toBeTruthy();
    expect(requests[0].key).toBe(requests[1].key);
    expect(requests[0].body).toEqual({ expected_projection_revision: 4, ready: true });
    expect(requests[1].body).toEqual(requests[0].body);
    expect(result).toMatchObject({
      state: 'success',
      value: {
        first: { command_id: requests[0].key, type: 'ready' },
        second: { command_id: requests[1].key, type: 'ready' },
      },
    });
  });

  test('creates a different idempotency key for a new participant ready intent', async ({ page }) => {
    const playerCSRFToken = 'participant-new-intent-csrf';
    const keys: (string | undefined)[] = [];
    await page.addInitScript((token) => {
      document.cookie = `tpm_player_csrf=${encodeURIComponent(token)}; Path=/`;
    }, playerCSRFToken);
    await page.route(participantReadyPattern, async (route) => {
      const request = route.request();
      keys.push(request.headers()['idempotency-key']);
      expect(request.headers()['x-csrf-token']).toBe(playerCSRFToken);
      await fulfillJSON(route, 200, participantReadyEvent(request.headers()['idempotency-key'] ?? ''));
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Создать новый ready intent' }).click();
    await readResult(page, 'success');

    expect(keys).toHaveLength(2);
    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBeTruthy();
    expect(keys[0]).not.toBe(keys[1]);
  });

  test('recovers a participant conflict with one snapshot read and no mutation retry', async ({ page }) => {
    const playerCSRFToken = 'participant-conflict-csrf';
    let mutationCalls = 0;
    let snapshotCalls = 0;
    await page.addInitScript((token) => {
      document.cookie = `tpm_player_csrf=${encodeURIComponent(token)}; Path=/`;
    }, playerCSRFToken);
    await page.route(participantReadyPattern, async (route) => {
      mutationCalls += 1;
      expect(route.request().method()).toBe('POST');
      await fulfillJSON(route, 409, problemBody(409, 'Projection revision conflict'));
    });
    await page.route(participantSnapshotPattern, async (route) => {
      snapshotCalls += 1;
      expect(route.request().method()).toBe('GET');
      await fulfillJSON(route, 200, participantRecoverySnapshot());
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Проверить конфликт участника' }).click();
    const result = await readResult(page, 'success');

    expect(mutationCalls).toBe(1);
    expect(snapshotCalls).toBe(1);
    expect(result).toMatchObject({
      state: 'success',
      value: {
        status: 'conflict',
        recovered: true,
        snapshot: {
          tournament_id: tournamentId,
          projection_revision: 5,
          next_cursor: { projection_revision: 5, participant_view_revision: 3, event_sequence: 8 },
        },
      },
    });
  });

  test('returns participant rate limit without snapshot recovery or mutation retry', async ({ page }) => {
    const playerCSRFToken = 'participant-rate-limit-csrf';
    let mutationCalls = 0;
    let snapshotCalls = 0;
    await page.addInitScript((token) => {
      document.cookie = `tpm_player_csrf=${encodeURIComponent(token)}; Path=/`;
    }, playerCSRFToken);
    await page.route(participantReadyPattern, async (route) => {
      mutationCalls += 1;
      await route.fulfill({
        status: 429,
        headers: { 'content-type': 'application/problem+json', 'retry-after': '17' },
        body: JSON.stringify(problemBody(429, 'Participant rate limit')),
      });
    });
    await page.route(participantSnapshotPattern, async (route) => {
      snapshotCalls += 1;
      await fulfillJSON(route, 200, participantRecoverySnapshot());
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Проверить rate limit участника' }).click();
    const result = await readResult(page, 'success');

    expect(mutationCalls).toBe(1);
    expect(snapshotCalls).toBe(0);
    expect(result).toMatchObject({
      state: 'success',
      value: { status: 'rate_limited', retryAfter: '17' },
    });
  });

  test('turns malformed successful participant JSON into a contract error', async ({ page }) => {
    await page.route(participantLobbyPattern, async (route) => {
      await fulfillJSON(route, 200, {});
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Получить лобби участника' }).click();
    const result = await readResult(page, 'error');

    expect(result).toMatchObject({
      state: 'error',
      error: {
        name: 'ApiContractError',
        message: 'Invalid API response: participant lobby',
      },
    });
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

  test('sends player CSRF for participant Arena mutations', async ({ page }) => {
    const playerCSRFToken = 'participant-ready-csrf';
    const requests: { method: string; csrf: string | undefined; authorization: string | undefined }[] = [];

    await page.addInitScript((token) => {
      document.cookie = `tpm_player_csrf=${encodeURIComponent(token)}; Path=/`;
    }, playerCSRFToken);
    await page.route(participantReadyPattern, async (route) => {
      const request = route.request();
      requests.push({
        method: request.method(),
        csrf: request.headers()['x-csrf-token'],
        authorization: request.headers().authorization,
      });
      expect(request.postDataJSON()).toEqual({
        expected_projection_revision: 4,
        ready: true,
      });
      await fulfillJSON(route, 200, participantReadyEvent(request.headers()['idempotency-key'] ?? ''));
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Отметить готовность участника' }).click();
    const result = await readResult(page, 'success');

    expect(result).toEqual({ state: 'success', value: null });
    expect(requests).toEqual([{
      method: 'POST',
      csrf: playerCSRFToken,
      authorization: undefined,
    }]);
  });

  test('preserves admin 403 without starting a refresh', async ({ page }) => {
    let refreshCalls = 0;
    await page.addInitScript(() => {
      document.cookie = 'tpm_admin_refresh_csrf=operator-refresh-csrf; Path=/';
    });
    await page.route(adminRefreshPattern, async (route) => {
      refreshCalls += 1;
      await fulfillJSON(route, 200, { expires_in: 900 });
    });
    await page.route(adminTasksPattern, async (route) => {
      await fulfillJSON(route, 403, problemBody(403, 'Forbidden operator request'));
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Проверить admin 403' }).click();
    const result = await readResult(page, 'error');

    expect(result).toMatchObject({
      state: 'error',
      error: {
        name: 'ApiError',
        status: 403,
        kind: 'forbidden',
      },
    });
    expect(refreshCalls).toBe(0);
  });

  test('shares one admin refresh across concurrent 401 responses', async ({ page }) => {
    let refreshCalls = 0;
    let taskCalls = 0;
    await page.addInitScript(() => {
      document.cookie = 'tpm_admin_refresh_csrf=operator-refresh-csrf; Path=/';
    });
    await page.route(adminRefreshPattern, async (route) => {
      refreshCalls += 1;
      await fulfillJSON(route, 200, { expires_in: 900 });
    });
    await page.route(adminTasksPattern, async (route) => {
      taskCalls += 1;
      if (taskCalls <= 2) {
        await fulfillJSON(route, 401, problemBody(401, 'Expired operator session'));
        return;
      }
      await fulfillJSON(route, 200, []);
    });

    await openFixture(page);
    await page.getByRole('button', { name: 'Проверить параллельный admin refresh' }).click();
    const result = await readResult(page, 'success');

    expect(result).toEqual({ state: 'success', value: null });
    expect(refreshCalls).toBe(1);
    expect(taskCalls).toBe(4);
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
