import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';

import {
  expect,
  test as base,
  type APIRequestContext,
  type APIResponse,
  type BrowserContext,
  type Page,
  type Response as BrowserResponse,
} from '@playwright/test';

const frontendURL = (process.env.E2E_FRONTEND_URL || 'http://127.0.0.1:3000').replace(/\/+$/, '');
const backendURL = (process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const adminPassword = process.env.E2E_ADMIN_PASSWORD || '';

type AdminSession = {
  expires_in: number;
  access_csrf_token: string;
  refresh_csrf_token: string;
};

type AdminTask = {
  id: string;
  kind: 'normal' | 'golden';
  title: string;
  version: number;
};

type AdminTournament = {
  content_revision: number;
  id: string;
  name: string;
  public_id: string;
  revision: number;
  state: string;
};

type FullStackPlayer = {
  id: string;
  username: string;
};

type FullStackRosterParticipant = {
  attendance: 'invited' | 'registered' | 'checked_in' | 'withdrawn';
  id: string;
  player_id: string;
  roster_id: string;
  seed: number;
  tournament_id: string;
};

type FullStackRosterParticipantInput = Pick<
  FullStackRosterParticipant,
  'attendance' | 'player_id' | 'seed'
>;

type FullStackRoster = {
  execution_started: boolean;
  id: string;
  locked: boolean;
  participants: FullStackRosterParticipant[];
  revision: number;
  tournament_id: string;
};

type FullStackPreflightReport = {
  checks?: Array<{
    code: string;
    evidence?: string[];
    passed: boolean;
  }>;
  id: string;
  passed: boolean;
  tournament_id: string;
};

type FullStackSwissRound = {
  bye?: { participant_id: string } | null;
  locked: boolean;
  pairings: Array<{
    first_participant_id: string;
    repeated?: boolean;
    second_participant_id: string;
  }>;
  revision: number;
  roster_participant_ids: string[];
  round_number: number;
  tournament_id: string;
};

type FullStackWave = {
  id: string;
  members: Array<{
    participant_id: string;
    ready: boolean;
    series_id?: string | null;
  }>;
  state: string;
};

type FullStackOperatorSnapshot = {
  next_cursor: {
    projection_revision: number;
  };
  waves: FullStackWave[];
};

type FullStackPlayoffSeries = {
  first_participant_id: string;
  format: 'bo1' | 'bo3';
  id: string;
  second_participant_id: string;
  score: {
    first_participant_wins: number;
    second_participant_wins: number;
  };
  slots: Array<{
    attempts: Array<{ id: string; state: string }>;
    position: number;
  }>;
  state: string;
  winner_id: string | null;
};

type FullStackPublicCurrentGame = {
  category: FullStackTaskInput['category'];
  effective_deadline: string | null;
  finished_at: string | null;
  first_connection_status: 'connected' | 'disconnected' | 'unknown';
  position: number;
  result_reason: string | null;
  second_connection_status: 'connected' | 'disconnected' | 'unknown';
  started_at: string | null;
  state: string;
  winner_display_name: string | null;
};

type FullStackPublicDraftAction = {
  action: 'ban' | 'pick';
  actor_display_name: string;
  automatic: boolean;
  category: FullStackTaskInput['category'];
  occurred_at: string;
  turn: number;
};

type FullStackPublicDraft = {
  actions: FullStackPublicDraftAction[];
  auto_action_pending: boolean;
  current_action: 'ban' | 'pick' | null;
  current_actor_display_name: string | null;
  current_turn: number | null;
  first_actor_display_name: string;
  format: 'bo1' | 'bo3';
  pool: FullStackTaskInput['category'][];
  projection_revision: number;
  selected_categories: FullStackTaskInput['category'][];
  series_id: string;
  state: string;
  tournament_id: string;
  turn_deadline: string | null;
};

type FullStackPublicOfficialResult = {
  recorded_at: string;
  revision_id: string;
  score: {
    first_wins: number;
    second_wins: number;
  };
  series_id: string;
  state: string;
  winner_display_name?: string;
};

type FullStackDraft = {
  actions: Array<{
    automatic: boolean;
    category: FullStackTaskInput['category'];
  }>;
  current_action: 'ban' | 'pick' | null;
  current_actor_id: string | null;
  id: string;
  legal_categories: FullStackTaskInput['category'][];
  revision: number;
  series_id: string;
  state: string;
  turn: number;
  turn_deadline: string | null;
};

type FullStackParticipantSnapshot = {
  projection_revision: number;
  tournament_id?: string;
  draft?: FullStackDraft | null;
  lobby?: {
    participant_id?: string;
    current_swiss_round?: number | null;
    status?: string;
    required_action?: string;
  };
  assignment?: {
    id: string;
    attempt_id: string;
    context: {
      wave_id: string;
      series_id: string;
      slot_id: string;
      game_id: string;
      stage: 'swiss' | 'semifinal' | 'final';
      swiss_round: number | null;
      game_number: number;
      series_score: {
        first_participant_wins: number;
        second_participant_wins: number;
      };
      game_state: string;
      started_at: string | null;
      effective_deadline: string | null;
    };
    active_snapshot: {
      category: FullStackTaskInput['category'];
      task_id: string;
      snapshot_id: string;
      version: number;
      title: string;
      time_limit: number;
    };
    receipt: {
      assignment_id: string;
      attempt_id: string;
      participant_id: string;
      snapshot_id: string;
      task_id: string;
      delivered_at: string;
    };
  } | null;
  series?: {
    id: string;
    state: string;
    slots: Array<{
      id: string;
      position: number;
      attempts: Array<{ id: string; state: string }>;
    }>;
  } | null;
  wave?: {
    id: string;
    state: string;
    members: Array<{ participant_id: string; series_id?: string | null }>;
  } | null;
};

type FullStackPublicSnapshot = {
  bracket: {
    matches: Array<{
      first_display_name: string | null;
      second_display_name: string | null;
      score: {
        first_participant_wins: number;
        second_participant_wins: number;
      };
      stage: 'semifinal' | 'final';
      state: string;
      position: number;
      winner_display_name: string | null;
    }>;
  };
  live_series: Array<{
    first_display_name: string;
    second_display_name: string;
    format: 'bo1' | 'bo3';
    round_number: number | null;
    score: {
      first_wins: number;
      second_wins: number;
    };
    series_id: string;
    stage: string;
    state: string;
    current_game: FullStackPublicCurrentGame | null;
    current_game_position?: number;
    scheduled_at: string | null;
  }>;
  official_results: FullStackPublicOfficialResult[];
  live_draft: FullStackPublicDraft | null;
  swiss_rounds: Array<{
    bye: {
      display_name: string;
      points_awarded: number;
    } | null;
    round_number: number;
    state: string;
  }>;
  next_cursor: {
    event_sequence: number;
    projection_revision: number;
  };
  tournament: {
    state: string;
    tournament_id: string;
  };
};

type FullStackConfigurationMutation = {
  affected_artifacts: Array<{
    id: string;
    successor_revision_id: string;
  }>;
};

type UploadSourceResponse = {
  source_file_url: string;
};

type BrowserAuthStorage = {
  sessionToken: string | null;
  localSessionToken: string | null;
  playerCSRFToken: string | null;
  adminAccessCSRFToken: string | null;
  adminRefreshCSRFToken: string | null;
};

type FullStackTaskInput = {
  title: string;
  description: string;
  kind?: 'normal' | 'golden';
  category: 'web' | 'crypto' | 'forensics' | 'reverse' | 'pwn';
  difficulty: 'easy';
  time_limit: number;
  flag: string;
  hints: [string, string, string];
  task_url?: string | null;
};

type FullStackTournamentInput = {
  content_revision: number;
  name: string;
  planned_roster_size: number;
  public_id: string;
  preset: 'tournament_v1';
  expected_revision: number;
};

type AdminCSRFSession = Pick<AdminSession, 'access_csrf_token'>;

type OwnedFixture = {
  taskIDs: Set<string>;
  tournamentIDs: Set<string>;
  contexts: BrowserContext[];
};

const test = base.extend<{ ownedFixture: OwnedFixture }>({
  ownedFixture: [async ({ request }, use, testInfo) => {
    const fixture: OwnedFixture = {
      taskIDs: new Set(),
      tournamentIDs: new Set(),
      contexts: [],
    };
    try {
      await use(fixture);
    } finally {
      await cleanupOwnedTournamentFixture(
        request,
        fixture,
        testInfo.status !== testInfo.expectedStatus,
      );
    }
  }, { timeout: 120_000 }],
});

const uniqueName = (prefix: string) =>
  `${prefix}-${Date.now().toString(36)}-${Math.random().toString(16).slice(2, 8)}`;

const ensureFullStackEnabled = async (request: APIRequestContext): Promise<void> => {
  test.skip(process.env.E2E_FULL_STACK !== '1', 'set E2E_FULL_STACK=1 to run local compose e2e');
  test.skip(!adminPassword, 'E2E_ADMIN_PASSWORD is required for local compose e2e');

  const waitForHealthy = async (url: string, label: string): Promise<void> => {
    await expect.poll(
      async () => {
        try {
          const response = await request.get(url, { timeout: 10_000 });
          return response.ok();
        } catch {
          return false;
        }
      },
      { message: `${label} failed at ${url}`, timeout: 15_000, intervals: [100, 250, 500, 1_000] },
    ).toBe(true);
  };

  await waitForHealthy(`${backendURL}/health`, 'backend health');
  await waitForHealthy(`${frontendURL}/`, 'frontend health');
};

const adminLogin = async (request: APIRequestContext): Promise<AdminSession> => {
  const response = await request.post(`${backendURL}/api/v1/admin/login`, {
    data: { password: adminPassword },
  });
  expect(response.ok(), `admin login failed with ${response.status()}`).toBeTruthy();
  const accessCSRFToken = response.headers()['x-csrf-token'];
  const refreshCSRFToken = response.headers()['x-admin-refresh-csrf-token'];
  expect(accessCSRFToken, 'admin login did not return access CSRF token').toBeTruthy();
  expect(refreshCSRFToken, 'admin login did not return refresh CSRF token').toBeTruthy();
  const session = (await response.json()) as { expires_in: number };
  expect(Object.keys(session)).toEqual(['expires_in']);
  expect(session.expires_in).toBeGreaterThanOrEqual(0);
  const cookieNames = (await request.storageState()).cookies.map((cookie) => cookie.name);
  expect(cookieNames).toContain('tpm_admin_access');
  expect(cookieNames).toContain('tpm_admin_refresh');
  return {
    expires_in: session.expires_in,
    access_csrf_token: accessCSRFToken,
    refresh_csrf_token: refreshCSRFToken,
  };
};

const loginThroughAdminUI = async (page: Page): Promise<void> => {
  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill(adminPassword);
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByRole('button', { name: 'Задачи' })).toBeVisible({ timeout: 15_000 });
  await page.getByRole('button', { name: 'Задачи' }).click();

  const cookies = await page.context().cookies();
  expect(cookies.some((cookie) => cookie.name === 'tpm_admin_access')).toBe(true);
  expect(cookies.some((cookie) => cookie.name === 'tpm_admin_refresh')).toBe(true);
};

const fillAdminTaskForm = async (page: Page, input: FullStackTaskInput): Promise<void> => {
  const form = page.locator('form').filter({ has: page.getByPlaceholder('Введите название...') }).first();
  await form.getByPlaceholder('Введите название...').fill(input.title);
  await form.getByPlaceholder('Опишите задачу...').fill(input.description);
  await form.getByLabel('Категория').selectOption(input.category);
  await form.getByLabel('Сложность').selectOption(input.difficulty);
  await form.getByLabel('Пул задания').selectOption(input.kind ?? 'normal');
  await form.getByPlaceholder('60').fill(String(input.time_limit));
  await form.getByPlaceholder('flag{...}').fill(input.flag);
  await form.getByPlaceholder('https://example.com/task').fill(input.task_url ?? '');
  await form.getByPlaceholder('Подсказка 1').fill(input.hints[0]);
  await form.getByPlaceholder('Подсказка 2').fill(input.hints[1]);
  await form.getByPlaceholder('Подсказка 3').fill(input.hints[2]);
};

const createTaskViaApi = async (
  fixture: OwnedFixture,
  request: APIRequestContext,
  session: AdminCSRFSession,
  input: FullStackTaskInput,
): Promise<AdminTask> => {
  const response = await request.post(`${backendURL}/api/v1/admin/tasks`, {
    headers: {
      'X-CSRF-Token': session.access_csrf_token,
    },
    data: input,
  });
  expect(response.status(), `task create failed with ${response.status()}`).toBe(201);
  const task = (await response.json()) as AdminTask;
  expect(task.id, 'task create did not return an id').toMatch(/^[0-9a-f-]{36}$/i);
  fixture.taskIDs.add(task.id);
  return task;
};

const getTournamentContentRevision = async (
  request: APIRequestContext,
): Promise<number> => {
  return (await getTournamentContentSelection(request)).content_revision;
};

type FullStackContentSelection = {
  content_revision: number;
  golden_pool_revision_id: string;
  normal_pool_revision_id: string;
  publication_id: string;
};

const getTournamentContentSelection = async (
  request: APIRequestContext,
): Promise<FullStackContentSelection> => {
  const contentURL = `${backendURL}/api/v1/admin/tournament-content`;
  const response = await request.get(contentURL);
  expect(response.ok(), `tournament content GET failed at ${contentURL} with ${response.status()}`).toBeTruthy();
  const content = (await response.json()) as Partial<FullStackContentSelection>;
  const contentRevision = content.content_revision;
  expect(contentRevision, 'tournament content did not return content_revision').toEqual(
    expect.any(Number),
  );
  if (typeof contentRevision !== 'number') {
    throw new Error(`tournament content returned an invalid content_revision at ${contentURL}`);
  }
  expect(content.publication_id).toMatch(/^[0-9a-f-]{36}$/i);
  expect(content.normal_pool_revision_id).toMatch(/^[0-9a-f-]{36}$/i);
  expect(content.golden_pool_revision_id).toMatch(/^[0-9a-f-]{36}$/i);
  return content as FullStackContentSelection;
};

const createTournamentViaApi = async (
  fixture: OwnedFixture,
  request: APIRequestContext,
  session: AdminCSRFSession,
  input: FullStackTournamentInput,
): Promise<AdminTournament> => {
  const idempotencyKey = randomUUID();
  const response = await request.post(`${backendURL}/api/v1/admin/tournaments`, {
    headers: {
      'X-CSRF-Token': session.access_csrf_token,
      'Idempotency-Key': idempotencyKey,
      Origin: frontendURL,
    },
    data: input,
  });
  expect(response.status(), `tournament create failed with ${response.status()}`).toBe(201);
  const tournament = (await response.json()) as AdminTournament;
  expect(tournament.id, 'tournament create did not return an id').toMatch(
    /^[0-9a-f-]{36}$/i,
  );
  fixture.tournamentIDs.add(tournament.id);
  expect(tournament.name).toBe(input.name);
  expect(tournament.public_id).toBe(input.public_id);
  expect(tournament.state).toBe('draft');
  return tournament;
};

const getRosterViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  expectedRosterID?: string,
): Promise<FullStackRoster> => {
  const response = await request.get(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/roster`, {
    headers: { Origin: frontendURL },
  });
  expect(response.ok(), `roster GET failed with ${response.status()}`).toBeTruthy();
  const roster = (await response.json()) as FullStackRoster;
  expect(roster.id, 'roster response did not return a roster id').toMatch(
    /^[0-9a-f-]{36}$/i,
  );
  expect(roster.tournament_id, 'roster response returned the wrong tournament id').toBe(
    tournamentID,
  );
  if (expectedRosterID) {
    expect(roster.id, 'roster id changed between server responses').toBe(expectedRosterID);
  }
  for (const participant of roster.participants) {
    expect(participant.id, 'roster participant did not return an id').toMatch(
      /^[0-9a-f-]{36}$/i,
    );
    expect(participant.tournament_id, 'roster participant returned the wrong tournament id').toBe(
      tournamentID,
    );
    expect(participant.roster_id, 'roster participant returned the wrong roster id').toBe(
      roster.id,
    );
  }
  return roster;
};

const sortRosterParticipantsBySeed = (
  participants: FullStackRoster['participants'],
): FullStackRoster['participants'] => [...participants].sort((first, second) => first.seed - second.seed);

type OperatorSnapshotEnvelope = {
  next_cursor?: { projection_revision?: unknown };
  tournament?: { id?: unknown; state?: unknown };
};

const readOperatorSnapshotViaApi = async <T = OperatorSnapshotEnvelope>(
  request: APIRequestContext,
  tournamentID: string,
): Promise<T> => {
  const snapshotURL = `${backendURL}/api/v1/admin/tournaments/${tournamentID}/snapshot`;
  const maxAttempts = 4;
  const totalTimeoutMs = 75_000;
  const deadline = Date.now() + totalTimeoutMs;
  let response: APIResponse | null = null;

  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    const remainingMs = deadline - Date.now();
    if (remainingMs <= 0) {
      break;
    }

    response = await request.get(snapshotURL, {
      headers: { Origin: frontendURL },
      timeout: Math.max(1, Math.min(15_000, remainingMs)),
    });
    if (response.status() !== 429 || attempt + 1 >= maxAttempts) {
      break;
    }

    const retryAfter = response.headers()['retry-after']?.trim() ?? '';
    let retryDelayMs: number | null = null;
    if (/^\d+$/.test(retryAfter)) {
      const retryAfterSeconds = Number(retryAfter);
      retryDelayMs = Number.isFinite(retryAfterSeconds)
        ? retryAfterSeconds * 1_000
        : Number.MAX_SAFE_INTEGER;
    } else if (retryAfter !== '') {
      const retryAt = Date.parse(retryAfter);
      if (Number.isFinite(retryAt)) {
        retryDelayMs = Math.max(0, retryAt - Date.now());
      }
    }

    const waitBudgetMs = deadline - Date.now();
    const waitMs = Math.min(
      waitBudgetMs,
      (retryDelayMs ?? transientRetryDelayMs(attempt)) + 100,
    );
    if (waitMs <= 0) {
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, waitMs));
  }

  if (!response) {
    throw new Error(`operator snapshot GET exceeded ${totalTimeoutMs}ms for ${tournamentID}`);
  }
  expect(response.status(), `operator snapshot GET failed with ${response.status()}`).toBe(200);
  const snapshot = (await response.json()) as T;
  const envelope = snapshot as T & OperatorSnapshotEnvelope;
  expect(envelope.tournament?.id, 'operator snapshot returned the wrong tournament id').toBe(
    tournamentID,
  );
  return snapshot;
};

const getOperatorProjectionRevisionViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
): Promise<number> => {
  const snapshot = await readOperatorSnapshotViaApi<OperatorSnapshotEnvelope>(request, tournamentID);
  const projectionRevision = snapshot.next_cursor?.projection_revision;
  expect(projectionRevision, 'operator snapshot did not return projection_revision').toEqual(
    expect.any(Number),
  );
  if (
    typeof projectionRevision !== 'number' ||
    !Number.isSafeInteger(projectionRevision) ||
    projectionRevision < 1
  ) {
    throw new Error(`operator snapshot returned an invalid projection_revision for ${tournamentID}`);
  }
  return projectionRevision;
};

const cancelOwnedTournamentFixtureViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
): Promise<void> => {
  const snapshot = await readOperatorSnapshotViaApi<OperatorSnapshotEnvelope>(request, tournamentID);
  if (!snapshot.tournament) {
    throw new Error('owned tournament cleanup snapshot returned no tournament');
  }
  if (snapshot.tournament.state === 'cancelled' || snapshot.tournament.state === 'completed') {
    return;
  }

  const projectionRevision = snapshot.next_cursor?.projection_revision;
  if (
    typeof projectionRevision !== 'number' ||
    !Number.isSafeInteger(projectionRevision) ||
    projectionRevision < 1
  ) {
    throw new Error('owned tournament cleanup snapshot returned an invalid projection revision');
  }
  const csrfToken = (await request.storageState()).cookies.find(
    (cookie) => cookie.name === 'tpm_admin_access_csrf',
  )?.value;
  if (!csrfToken) {
    throw new Error('owned tournament cleanup could not find the current admin CSRF cookie');
  }

  const cancellationResponse = await request.post(
    `${backendURL}/api/v1/admin/tournaments/${tournamentID}/actions`,
    {
      headers: {
        'X-CSRF-Token': csrfToken,
        'Idempotency-Key': randomUUID(),
        Origin: frontendURL,
      },
      data: {
        action: 'cancel',
        confirmed: true,
        expected_projection_revision: projectionRevision,
        reason: 'Full-stack E2E fixture cleanup',
      },
      timeout: 15_000,
    },
  ).catch(() => {
    throw new Error('owned tournament cleanup cancellation request failed');
  });
  if (cancellationResponse.status() !== 200) {
    throw new Error(`owned tournament cleanup cancellation failed with status ${cancellationResponse.status()}`);
  }

  const cancelledTournament = (await cancellationResponse.json().catch(() => {
    throw new Error('owned tournament cleanup cancellation returned invalid JSON');
  })) as {
    id?: unknown;
    state?: unknown;
  };
  if (cancelledTournament.id !== tournamentID || cancelledTournament.state !== 'cancelled') {
    throw new Error('owned tournament cleanup did not cancel the owned tournament');
  }
};

const disableOwnedTaskFixtureViaApi = async (
  request: APIRequestContext,
  taskID: string,
): Promise<void> => {
  const csrfToken = (await request.storageState()).cookies.find(
    (cookie) => cookie.name === 'tpm_admin_access_csrf',
  )?.value;
  if (!csrfToken) {
    throw new Error('owned task cleanup could not find the current admin CSRF cookie');
  }
  const response = await request.put(`${backendURL}/api/v1/admin/tasks/${taskID}`, {
    headers: { 'X-CSRF-Token': csrfToken, Origin: frontendURL },
    // Partial update preserves the task data and its historical source asset.
    data: { enabled: false },
    timeout: 15_000,
  }).catch(() => {
    throw new Error(`owned task ${taskID} cleanup update request failed`);
  });
  if (response.status() !== 200) {
    throw new Error(`owned task ${taskID} cleanup update failed with status ${response.status()}`);
  }
  const task = (await response.json().catch(() => {
    throw new Error(`owned task ${taskID} cleanup update returned invalid JSON`);
  })) as { id?: unknown; enabled?: unknown };
  if (task.id !== taskID || task.enabled !== false) {
    throw new Error(`owned task ${taskID} cleanup did not disable the owned task`);
  }
};

const cleanupOwnedTournamentFixture = async (
  request: APIRequestContext,
  fixture: OwnedFixture,
  testFailed: boolean,
): Promise<void> => {
  const failures: string[] = [];
  if (fixture.tournamentIDs.size > 0 || fixture.taskIDs.size > 0) {
    let authenticated = false;
    try {
      await adminLogin(request);
      authenticated = true;
    } catch {
      failures.push('owned fixture cleanup admin login failed');
    }
    if (authenticated) {
      for (const tournamentID of fixture.tournamentIDs) {
        try {
          await cancelOwnedTournamentFixtureViaApi(request, tournamentID);
        } catch (error) {
          failures.push(error instanceof Error ? error.message : 'owned tournament cleanup failed');
        }
      }
      // Do not change the catalog while an owned tournament may still be active.
      if (failures.length === 0) {
        for (const taskID of fixture.taskIDs) {
          try {
            await disableOwnedTaskFixtureViaApi(request, taskID);
          } catch (error) {
            failures.push(error instanceof Error ? error.message : 'owned task cleanup failed');
          }
        }
      }
    }
  }

  const contextClosures = await Promise.allSettled(fixture.contexts.map((context) => context.close()));
  const failedClosures = contextClosures.filter((result) => result.status === 'rejected').length;
  if (failedClosures > 0) {
    failures.push(`${failedClosures} fixture context(s) failed to close`);
  }

  if (failures.length === 0) {
    return;
  }

  const message = `Full-stack fixture cleanup failed: ${failures.join('; ')}`;
  if (testFailed) {
    test.info().annotations.push({ type: 'fixture-cleanup', description: message });
    return;
  }
  throw new Error(message);
};

const readParticipantSnapshotViaApi = async (
  context: BrowserContext,
  tournamentID: string,
  timeoutMs?: number,
): Promise<FullStackParticipantSnapshot> => {
  const response = await context.request.get(
    `${backendURL}/api/v1/tournaments/${tournamentID}/participant/snapshot`,
    { headers: { Origin: frontendURL }, timeout: timeoutMs },
  );
  expect(response.status(), `participant snapshot failed with ${response.status()}`).toBe(200);
  const snapshot = (await response.json()) as FullStackParticipantSnapshot;
  expect(snapshot.projection_revision).toEqual(expect.any(Number));
  return snapshot;
};

const assertParticipantSnapshotPollSchema = (
  snapshot: FullStackParticipantSnapshot,
  participantID: string,
): void => {
  if (
    !Number.isSafeInteger(snapshot.projection_revision) ||
    snapshot.projection_revision < 1
  ) {
    throw new Error(
      `participant ${participantID} snapshot returned an invalid projection_revision`,
    );
  }
  if (snapshot.series !== undefined && snapshot.series !== null) {
    if (typeof snapshot.series.id !== 'string' || snapshot.series.id.length === 0) {
      throw new Error(`participant ${participantID} snapshot returned an invalid series id`);
    }
    if (!Array.isArray(snapshot.series.slots)) {
      throw new Error(`participant ${participantID} snapshot returned invalid series slots`);
    }
  }
  if (snapshot.assignment !== undefined && snapshot.assignment !== null) {
    if (
      typeof snapshot.assignment.context?.series_id !== 'string' ||
      snapshot.assignment.context.series_id.length === 0
    ) {
      throw new Error(
        `participant ${participantID} snapshot returned an invalid assignment series id`,
      );
    }
  }
};

const readParticipantSnapshotUntilSeries = async (
  context: BrowserContext,
  tournamentID: string,
  participantID: string,
  expectedProjectionRevision: number,
  expectedSeriesID: string,
): Promise<FullStackParticipantSnapshot> => {
  const timeoutMs = 30_000;
  const pollIntervalMs = 250;
  const deadline = Date.now() + timeoutMs;
  let lastSnapshot: FullStackParticipantSnapshot | null = null;

  while (Date.now() < deadline) {
    const remainingMs = Math.max(1, deadline - Date.now());
    const snapshot = await readParticipantSnapshotViaApi(
      context,
      tournamentID,
      remainingMs,
    );
    assertParticipantSnapshotPollSchema(snapshot, participantID);
    lastSnapshot = snapshot;

    if (
      snapshot.projection_revision >= expectedProjectionRevision &&
      snapshot.series?.id === expectedSeriesID
    ) {
      return snapshot;
    }

    await new Promise((resolve) => setTimeout(resolve, Math.min(pollIntervalMs, remainingMs)));
  }

  throw new Error(
    `participant ${participantID} snapshot did not expose series ${expectedSeriesID} ` +
      `with projection_revision >= ${expectedProjectionRevision} within ${timeoutMs}ms ` +
      `(last projection_revision=${lastSnapshot?.projection_revision ?? 'unknown'}, ` +
      `series=${lastSnapshot?.series?.id ?? 'none'}, ` +
      `assignment_series=${lastSnapshot?.assignment?.context.series_id ?? 'none'})`,
  );
};

const readPublicSnapshotViaApi = async (
  context: BrowserContext,
  tournamentID: string,
): Promise<FullStackPublicSnapshot> => {
  const response = await context.request.get(
    `${backendURL}/api/v1/tournaments/${tournamentID}/snapshot`,
    { headers: { Origin: frontendURL } },
  );
  expect(response.status(), `public snapshot failed with ${response.status()}`).toBe(200);
  const snapshot = (await response.json()) as FullStackPublicSnapshot;
  expect(snapshot.tournament.tournament_id).toBe(tournamentID);
  return snapshot;
};

const collectJSONKeys = (value: unknown, keys: Set<string>): void => {
  if (Array.isArray(value)) {
    for (const item of value) {
      collectJSONKeys(item, keys);
    }
    return;
  }
  if (typeof value !== 'object' || value === null) {
    return;
  }
  for (const [key, child] of Object.entries(value)) {
    keys.add(key);
    collectJSONKeys(child, keys);
  }
};

const assertPublicProjectionRedacted = (payload: unknown): void => {
  const keys = new Set<string>();
  collectJSONKeys(payload, keys);
  const forbiddenKeys = [
    'assignment',
    'assignment_id',
    'audit',
    'audit_links',
    'actor_id',
    'command',
    'command_id',
    'decision_evidence',
    'decision_inputs',
    'decision_result',
    'decision_seed',
    'evidence',
    'flag',
    'first_participant_id',
    'game_id',
    'hint',
    'hints',
    'operator_action',
    'operator_actions',
    'participant_id',
    'participant_ids',
    'player_id',
    'second_participant_id',
    'slot_id',
    'snapshot_id',
    'source_projection_revision_id',
    'source_file_url',
    'seed',
    'service_epoch',
    'task',
    'task_id',
    'task_snapshot',
    'task_url',
    'presence_epoch',
    'reconnect_epoch',
    'winner_id',
  ];
  for (const key of forbiddenKeys) {
    expect(keys.has(key), `public projection leaked key ${key}`).toBe(false);
  }
};

const transientRetryDelayMs = (attempt: number): number => Math.min(500 * 2 ** attempt, 4_000);

const waitForTransientRetry = async (attempt: number): Promise<void> => {
  await new Promise((resolve) => setTimeout(resolve, transientRetryDelayMs(attempt)));
};

const applyOpenRegistrationViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
): Promise<APIResponse> => {
  const projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
  return request.post(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/actions`, {
    headers: {
      'X-CSRF-Token': csrfToken,
      'Idempotency-Key': randomUUID(),
      Origin: frontendURL,
    },
    data: {
      expected_projection_revision: projectionRevision,
      action: 'open_registration',
      confirmed: true,
    },
  });
};

const applyStartSwissViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
): Promise<APIResponse> => {
  let projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
  let response: APIResponse | null = null;

  for (let attempt = 0; attempt < 60; attempt += 1) {
    response = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/actions`, {
      headers: {
        'X-CSRF-Token': csrfToken,
        'Idempotency-Key': randomUUID(),
        Origin: frontendURL,
      },
      data: {
        expected_projection_revision: projectionRevision,
        action: 'start_swiss',
        confirmed: true,
      },
    });
    const status = response.status();
    if (status !== 409 && status !== 429) {
      return response;
    }

    if (status === 409) {
      const conflict = (await response.json()) as { current_revision?: unknown };
      if (
        typeof conflict.current_revision === 'number' &&
        Number.isSafeInteger(conflict.current_revision) &&
        conflict.current_revision >= 1
      ) {
        projectionRevision = conflict.current_revision;
      } else {
        projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
      }
    } else {
      if (attempt < 59) {
        await waitForTransientRetry(attempt);
        projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
      }
      continue;
    }

    if (attempt < 59) {
      await waitForTransientRetry(attempt);
    }
  }

  if (!response) {
    throw new Error('start Swiss did not issue a request');
  }
  return response;
};

const applyStartPlayoffsViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
): Promise<APIResponse> => {
  let projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
  let response: APIResponse | null = null;

  for (let attempt = 0; attempt < 3; attempt += 1) {
    response = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/actions`, {
      headers: {
        'X-CSRF-Token': csrfToken,
        'Idempotency-Key': randomUUID(),
        Origin: frontendURL,
      },
      data: {
        expected_projection_revision: projectionRevision,
        action: 'start_playoffs',
        confirmed: true,
      },
    });
    if (response.status() !== 409) {
      return response;
    }

    const conflict = (await response.json()) as { current_revision?: unknown };
    if (
      typeof conflict.current_revision !== 'number' ||
      !Number.isSafeInteger(conflict.current_revision) ||
      conflict.current_revision < 1
    ) {
      return response;
    }
    projectionRevision = conflict.current_revision;
  }

  if (!response) {
    throw new Error('start playoffs did not issue a request');
  }
  return response;
};

const applyStartGoldenViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
): Promise<APIResponse> => {
  let projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
  let response: APIResponse | null = null;

  for (let attempt = 0; attempt < 3; attempt += 1) {
    response = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/actions`, {
      headers: {
        'X-CSRF-Token': csrfToken,
        'Idempotency-Key': randomUUID(),
        Origin: frontendURL,
      },
      data: {
        expected_projection_revision: projectionRevision,
        action: 'start_golden',
        confirmed: true,
      },
    });
    if (response.status() !== 409) {
      return response;
    }

    const conflict = (await response.json()) as { current_revision?: unknown };
    if (
      typeof conflict.current_revision !== 'number' ||
      !Number.isSafeInteger(conflict.current_revision) ||
      conflict.current_revision < 1
    ) {
      return response;
    }
    projectionRevision = conflict.current_revision;
  }

  if (!response) {
    throw new Error('start Golden did not issue a request');
  }
  return response;
};

const runRosterPreflightViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
): Promise<APIResponse> => {
  const projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
  return request.post(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/roster/preflight`, {
    headers: {
      'X-CSRF-Token': csrfToken,
      'Idempotency-Key': randomUUID(),
      Origin: frontendURL,
    },
    data: { expected_projection_revision: projectionRevision },
  });
};

const lockRosterViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
  preflightRevisionID: string,
  checkedInPlayerIDs: string[],
): Promise<APIResponse> => {
  const projectionRevision = await getOperatorProjectionRevisionViaApi(request, tournamentID);
  return request.post(`${backendURL}/api/v1/admin/tournaments/${tournamentID}/roster/lock`, {
    headers: {
      'X-CSRF-Token': csrfToken,
      'Idempotency-Key': randomUUID(),
      Origin: frontendURL,
    },
    data: {
      expected_projection_revision: projectionRevision,
      preflight_revision_id: preflightRevisionID,
      checked_in_player_ids: checkedInPlayerIDs,
    },
  });
};

const readRosterResponse = async (
  response: APIResponse | BrowserResponse,
  tournamentID: string,
  expectedRosterID?: string,
): Promise<FullStackRoster> => {
  expect(response.status(), `roster response failed with ${response.status()}`).toBe(200);
  const roster = (await response.json()) as FullStackRoster;
  expect(roster.id, 'roster replace did not return a roster id').toMatch(
    /^[0-9a-f-]{36}$/i,
  );
  expect(roster.tournament_id, 'roster replace returned the wrong tournament id').toBe(
    tournamentID,
  );
  if (expectedRosterID) {
    expect(roster.id, 'roster replace changed the roster id').toBe(expectedRosterID);
  }
  for (const participant of roster.participants) {
    expect(participant.id, 'roster replace participant did not return an id').toMatch(
      /^[0-9a-f-]{36}$/i,
    );
    expect(participant.tournament_id, 'roster replace participant returned the wrong tournament id').toBe(
      tournamentID,
    );
    expect(participant.roster_id, 'roster replace participant returned the wrong roster id').toBe(
      roster.id,
    );
  }
  return roster;
};

const replaceRosterViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
  csrfToken: string,
  expectedProjectionRevision: number,
  participants: FullStackRosterParticipantInput[],
): Promise<APIResponse> => request.put(
  `${backendURL}/api/v1/admin/tournaments/${tournamentID}/roster`,
  {
    headers: {
      'X-CSRF-Token': csrfToken,
      'Idempotency-Key': randomUUID(),
      Origin: frontendURL,
    },
    data: {
      expected_projection_revision: expectedProjectionRevision,
      participants,
    },
  },
);

const uploadSourceViaApi = async (
  request: APIRequestContext,
  session: AdminSession,
  taskID: string,
  payload: Buffer,
): Promise<UploadSourceResponse> => {
  const response = await request.post(`${backendURL}/api/v1/admin/tasks/${taskID}/source`, {
    headers: {
      'X-CSRF-Token': session.access_csrf_token,
    },
    multipart: {
      file: {
        name: 'source.zip',
        mimeType: 'application/zip',
        buffer: payload,
      },
    },
  });
  expect(response.ok(), `source upload failed with ${response.status()}`).toBeTruthy();
  return (await response.json()) as UploadSourceResponse;
};

const joinAsPlayer = async (page: Page, username: string): Promise<void> => {
  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill(username);
  const joinResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/v1/players/join' &&
      response.request().method() === 'POST',
    { timeout: 15_000 },
  );
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  const joinResponse = await joinResponsePromise;
  const retryAfter = await joinResponse.headerValue('retry-after');
  const retryAfterSeconds = /^\d+$/.test(retryAfter ?? '') ? retryAfter : 'absent or non-numeric';
  expect(
    joinResponse.status(),
    `player join failed with ${joinResponse.status()}; Retry-After seconds: ${retryAfterSeconds}`,
  ).toBe(200);
  await expect(page.getByText('Игрок готов')).toBeVisible({ timeout: 15_000 });
};

const cookieHeaderForPage = async (page: Page): Promise<string> => {
  const cookies = await page.context().cookies();
  return cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join('; ');
};

const expectNoSensitiveAuthStorage = async (page: Page): Promise<void> => {
  const storage = await page.evaluate((): BrowserAuthStorage => ({
    sessionToken: window.sessionStorage.getItem('session_token'),
    localSessionToken: window.localStorage.getItem('session_token'),
    playerCSRFToken: window.sessionStorage.getItem('player_csrf_token'),
    adminAccessCSRFToken: window.sessionStorage.getItem('admin_access_csrf_token'),
    adminRefreshCSRFToken: window.sessionStorage.getItem('admin_refresh_csrf_token'),
  }));
  expect(storage.sessionToken, 'player session token must stay out of sessionStorage').toBeNull();
  expect(storage.localSessionToken, 'player session token must stay out of localStorage').toBeNull();
  expect(storage.playerCSRFToken, 'player CSRF token must stay out of sessionStorage').toBeNull();
  expect(storage.adminAccessCSRFToken, 'admin access CSRF token must stay out of sessionStorage').toBeNull();
  expect(storage.adminRefreshCSRFToken, 'admin refresh CSRF token must stay out of sessionStorage').toBeNull();
};

const readServerCountdownSeconds = async (page: Page): Promise<number> => {
  const countdown = await page.getByTestId('server-countdown').textContent();
  const parts = (countdown ?? '0:00').split(':').map(Number);
  if (parts.length !== 2 || parts.some((part) => !Number.isFinite(part))) {
    throw new Error(`Unexpected server countdown: ${countdown ?? '<empty>'}`);
  }
  return (parts[0] ?? 0) * 60 + (parts[1] ?? 0);
};

const readBroadcastCountdownSeconds = async (page: Page): Promise<number> => {
  const countdown = page.getByTestId('broadcast-game-countdown');
  await expect(countdown).toHaveAttribute('data-countdown-status', 'running');
  const text = await countdown.textContent();
  const parts = (text ?? '0:00').split(':').map(Number);
  if (parts.length !== 2 || parts.some((part) => !Number.isFinite(part))) {
    throw new Error(`Unexpected broadcast countdown: ${text ?? '<empty>'}`);
  }
  return (parts[0] ?? 0) * 60 + (parts[1] ?? 0);
};

test.describe('local compose full stack e2e', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeEach(async ({ request }) => {
    await ensureFullStackEnabled(request);
  });

  test('player session is cookie-backed and logout invalidates it', async ({ page, request }) => {
    test.setTimeout(60_000);

    const username = uniqueName('player-auth');
    await joinAsPlayer(page, username);
    await expectNoSensitiveAuthStorage(page);

    const cookieHeader = await cookieHeaderForPage(page);
    expect(cookieHeader, 'player join must issue a player session cookie').toContain('tpm_player_session=');
    expect(cookieHeader, 'player join must issue a player csrf cookie').toContain('tpm_player_csrf=');

    const meResponse = await request.get(`${backendURL}/api/v1/players/me`, {
      headers: {
        Cookie: cookieHeader,
        Origin: frontendURL,
      },
    });
    expect(meResponse.ok(), `players/me failed with ${meResponse.status()}`).toBeTruthy();
    const me = (await meResponse.json()) as { player: { username: string } };
    expect(me.player.username).toBe(username);

    await page.getByRole('button', { name: /Сменить игрока/ }).click();
    await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible({ timeout: 15_000 });
    await expectNoSensitiveAuthStorage(page);

    const staleMeResponse = await request.get(`${backendURL}/api/v1/players/me`, {
      headers: {
        Cookie: cookieHeader,
        Origin: frontendURL,
      },
    });
    expect(staleMeResponse.status(), 'old player session cookie must be invalid after logout').toBe(401);
  });

  test('admin UI creates and deletes a task through the real backend', async ({ page, request, ownedFixture }) => {
    test.setTimeout(90_000);

    const title = uniqueName('fullstack-admin');
    const taskInput: FullStackTaskInput = {
      title,
      description: 'Full stack admin task created through the real UI.',
      category: 'web',
      difficulty: 'easy',
      time_limit: 90,
      flag: `flag{${title.replaceAll('-', '_')}}`,
      hints: ['first hint', 'second hint', 'third hint'],
      task_url: 'https://example.com/full-stack-admin',
    };
    await loginThroughAdminUI(page);
    await adminLogin(request);
    await page.getByRole('button', { name: 'Задачи' }).click();
    await page.getByRole('button', { name: 'Создать задачу', exact: true }).click();
    await expect(page.getByPlaceholder('Введите название...')).toBeVisible();
    await fillAdminTaskForm(page, taskInput);
    const createResponsePromise = page.waitForResponse(
      (response) => new URL(response.url()).pathname === '/api/v1/admin/tasks' &&
        response.request().method() === 'POST',
    );
    await page.getByRole('dialog', { name: 'Создать задачу', exact: true })
      .getByRole('button', { name: 'Создать задачу', exact: true }).click();
    const createResponse = await createResponsePromise;
    expect(createResponse.status()).toBe(201);
    const createdTask = (await createResponse.json()) as AdminTask;
    expect(createdTask.id).toMatch(/^[0-9a-f-]{36}$/i);
    ownedFixture.taskIDs.add(createdTask.id);
    await expect(page.getByText(title)).toBeVisible({ timeout: 15_000 });

    const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`);
    expect(listResponse.ok(), `admin task list failed with ${listResponse.status()}`).toBeTruthy();
    const tasks = (await listResponse.json()) as AdminTask[];
    expect(tasks.some((task) => task.title === title)).toBe(true);

    page.once('dialog', async (dialog) => dialog.accept());
    const deleteResponsePromise = page.waitForResponse(
      (response) => new URL(response.url()).pathname === `/api/v1/admin/tasks/${createdTask.id}` &&
        response.request().method() === 'DELETE',
    );
    await page
      .locator('[class*="taskItem"]')
      .filter({ hasText: title })
      .locator('[title="Удалить задачу"]')
      .click();
    expect((await deleteResponsePromise).status()).toBe(204);
    ownedFixture.taskIDs.delete(createdTask.id);
    await expect(page.getByText(title)).toBeHidden({ timeout: 15_000 });

    await page.goto('/leaderboard');
    await expect(page.getByRole('heading', { name: 'Общий рейтинг' })).toBeVisible();
  });

  test('admin UI creates a tournament from the current content revision', async ({ page, ownedFixture }) => {
    test.setTimeout(90_000);

    const tournamentName = uniqueName('fullstack-admin-tournament');
    await loginThroughAdminUI(page);
    const adminRequest = page.context().request;
    const adminAccessCSRF = (await page.context().cookies()).find(
      (cookie) => cookie.name === 'tpm_admin_access_csrf',
    );
    const adminAccessCSRFToken = adminAccessCSRF?.value ?? '';
    expect(adminAccessCSRFToken, 'admin login did not issue an access CSRF cookie').toBeTruthy();
    const normalTaskName = uniqueName('admin-tournament-normal');
    const goldenTaskName = uniqueName('admin-tournament-golden');
    await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
      title: normalTaskName,
      description: 'Healthy normal task for the admin tournament flow.',
      kind: 'normal',
      category: 'web',
      difficulty: 'easy',
      time_limit: 90,
      flag: `flag{${normalTaskName.replaceAll('-', '_')}}`,
      hints: ['normal hint one', 'normal hint two', 'normal hint three'],
      task_url: 'https://example.com/admin-tournament-normal',
    });
    await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
      title: goldenTaskName,
      description: 'Healthy golden task for the admin tournament flow.',
      kind: 'golden',
      category: 'web',
      difficulty: 'easy',
      time_limit: 90,
      flag: `flag{${goldenTaskName.replaceAll('-', '_')}}`,
      hints: ['golden hint one', 'golden hint two', 'golden hint three'],
      task_url: 'https://example.com/admin-tournament-golden',
    });
    const contentRevision = await getTournamentContentRevision(adminRequest);

    await page.getByRole('button', { name: 'Соревнования' }).click();
    await expect(page.getByRole('heading', { name: 'Новое соревнование' })).toBeVisible();
    const reloadPublication = page.getByRole('button', { name: 'Обновить публикацию' });
    if (await reloadPublication.isVisible().catch(() => false)) {
      await reloadPublication.click();
    }
    await expect(
      page.getByRole('region', { name: 'Новое соревнование' }).getByText(`Ревизия ${contentRevision}`),
    ).toBeVisible();
    await page.getByLabel('Название соревнования').fill(tournamentName);
    await page.getByLabel('Плановый размер состава').selectOption('4');

    const createResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/v1/admin/tournaments' &&
        response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Создать соревнование' }).click();

    const response = await createResponse;
    expect(response.status()).toBe(201);

    const tournament = (await response.json()) as AdminTournament;
    expect(tournament.id).toMatch(/^[0-9a-f-]{36}$/i);
    ownedFixture.tournamentIDs.add(tournament.id);
    expect(tournament.content_revision).toBe(contentRevision);
    expect(tournament.name).toBe(tournamentName);
    expect(tournament.state).toBe('draft');
    const isTournamentOverviewURL = (url: URL): boolean =>
      url.origin === new URL(frontendURL).origin
      && url.pathname === '/admin'
      && url.searchParams.size === 3
      && url.searchParams.get('section') === 'tournaments'
      && url.searchParams.get('tournament') === tournament.id
      && url.searchParams.get('view') === 'overview';
    await expect(page).toHaveURL(isTournamentOverviewURL);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page).toHaveURL(isTournamentOverviewURL);
    await expect(page.getByRole('heading').filter({ hasText: tournamentName })).toBeVisible({
      timeout: 15_000,
    });

    const listResponse = await adminRequest.get(`${backendURL}/api/v1/admin/tournaments`);
    expect(listResponse.ok(), `tournament list failed with ${listResponse.status()}`).toBeTruthy();
    const list = (await listResponse.json()) as { items: AdminTournament[] };
    expect(list.items.some((item) => item.id === tournament.id && item.name === tournamentName)).toBe(true);
  });

  test('FE-028 admin UI creates, uploads, downloads, and updates a task through the relocated catalog', async ({ page, request, ownedFixture }) => {
    test.setTimeout(120_000);

    const title = uniqueName('fullstack-fe028');
    const updatedTitle = `${title}-updated`;
    const supportTitle = uniqueName('fullstack-fe028-normal');
    const taskInput: FullStackTaskInput = {
      title,
      description: 'FE-028 task created through the tournament admin catalog.',
      kind: 'golden',
      category: 'forensics',
      difficulty: 'easy',
      time_limit: 120,
      flag: `flag{${title.replaceAll('-', '_')}}`,
      hints: ['fe028 hint one', 'fe028 hint two', 'fe028 hint three'],
      task_url: null,
    };
    await loginThroughAdminUI(page);
    const adminSession = await adminLogin(request);
    await createTaskViaApi(ownedFixture, request, adminSession, {
      title: supportTitle,
      description: 'Normal pool companion for the FE-028 publication check.',
      kind: 'normal',
      category: 'web',
      difficulty: 'easy',
      time_limit: 120,
      flag: `flag{${supportTitle.replaceAll('-', '_')}}`,
      hints: ['normal hint one', 'normal hint two', 'normal hint three'],
      task_url: 'https://example.com/fe028-normal',
    });
    await page.getByRole('button', { name: 'Задачи' }).click();
    await page.getByRole('button', { name: 'Создать задачу', exact: true }).click();
    await expect(page.getByPlaceholder('Введите название...')).toBeVisible({ timeout: 15_000 });

    await fillAdminTaskForm(page, taskInput);
    await page.locator('input[type="file"]').setInputFiles({
      name: 'fe028-source.zip',
      mimeType: 'application/zip',
      buffer: Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x66, 0x65, 0x30, 0x32, 0x38]),
    });

    const createResponsePromise = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/v1/admin/tasks' &&
        response.request().method() === 'POST',
    );
    const uploadResponsePromise = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith('/source') &&
        response.request().method() === 'POST',
    );
    await page.getByRole('dialog', { name: 'Создать задачу', exact: true })
      .getByRole('button', { name: 'Создать задачу', exact: true }).click();

    const createResponse = await createResponsePromise;
    expect(createResponse.status()).toBe(201);
    const createdTask = (await createResponse.json()) as AdminTask;
    expect(createdTask.id).toMatch(/^[0-9a-f-]{36}$/i);
    ownedFixture.taskIDs.add(createdTask.id);
    expect(createdTask.title).toBe(title);
    expect(createdTask.kind).toBe('golden');
    expect(createdTask.version).toBe(1);

    const uploadResponse = await uploadResponsePromise;
    expect(uploadResponse.status()).toBe(200);
    const upload = (await uploadResponse.json()) as UploadSourceResponse;
    expect(upload.source_file_url).toMatch(/^https?:\/\//);
    await expect(page.getByText('Исходники загружены', { exact: true })).toBeVisible({ timeout: 15_000 });
    const downloadLink = page.getByRole('link', { name: /Скачать.*ZIP/ });
    await expect(downloadLink).toHaveAttribute('href', upload.source_file_url);

    const download = await page.context().request.get(upload.source_file_url, { timeout: 15_000 });
    expect(download.ok(), `source download failed with ${download.status()}`).toBeTruthy();
    expect((await download.body()).subarray(0, 2).toString()).toBe('PK');

    await expect(page.getByText(title, { exact: true })).toBeVisible();
    await adminLogin(request);
    const afterUploadTasksResponse = await request.get(`${backendURL}/api/v1/admin/tasks`);
    expect(
      afterUploadTasksResponse.ok(),
      `admin task list after upload failed with ${afterUploadTasksResponse.status()}`,
    ).toBeTruthy();
    const afterUploadTask = ((await afterUploadTasksResponse.json()) as AdminTask[])
      .find((task) => task.id === createdTask.id);
    expect(afterUploadTask).toBeDefined();
    if (!afterUploadTask) {
      throw new Error('created task disappeared after source upload');
    }
    expect(afterUploadTask.kind).toBe('golden');
    expect(afterUploadTask.version).toBeGreaterThan(createdTask.version);
    const createdTaskCard = page.getByRole('heading', { name: title, exact: true })
      .locator('xpath=ancestor::article[1]');
    await expect(createdTaskCard.getByText(`Версия: ${afterUploadTask.version}`, { exact: true })).toBeVisible();
    const afterUploadContent = await getTournamentContentSelection(request);

    await page.getByRole('button', { name: `Редактировать задачу ${title}` }).click();
    await expect(page.getByLabel('Пул задания')).toHaveValue('golden');
    await page.getByPlaceholder('Введите название...').fill(updatedTitle);
    const updateResponsePromise = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/v1/admin/tasks/${createdTask.id}` &&
        response.request().method() === 'PUT',
    );
    await page.getByRole('button', { name: /Сохранить задачу/ }).click();
    const updateResponse = await updateResponsePromise;
    expect(updateResponse.status()).toBe(200);
    await expect(page.getByText('Задача успешно обновлена!')).toBeVisible();
    await expect(page.getByText(updatedTitle, { exact: true })).toBeVisible();

    const refreshedTasks = await request.get(`${backendURL}/api/v1/admin/tasks`);
    expect(refreshedTasks.ok(), `admin task list failed with ${refreshedTasks.status()}`).toBeTruthy();
    const taskList = (await refreshedTasks.json()) as AdminTask[];
    const updatedTask = taskList.find((task) => task.id === createdTask.id);
    expect(updatedTask?.title).toBe(updatedTitle);
    expect(updatedTask?.kind).toBe('golden');
    expect(updatedTask?.version).toBeGreaterThan(afterUploadTask.version);
    if (!updatedTask) {
      throw new Error('updated task disappeared from admin catalog');
    }
    const updatedTaskCard = page.getByRole('heading', { name: updatedTitle, exact: true })
      .locator('xpath=ancestor::article[1]');
    await expect(updatedTaskCard.getByText(`Версия: ${updatedTask.version}`, { exact: true })).toBeVisible();

    const afterUpdateContent = await getTournamentContentSelection(request);
    expect(afterUpdateContent.content_revision).toBeGreaterThan(afterUploadContent.content_revision);
    expect(afterUpdateContent.publication_id).not.toBe(afterUploadContent.publication_id);
    expect(afterUpdateContent.normal_pool_revision_id).not.toBe(afterUploadContent.normal_pool_revision_id);
    expect(afterUpdateContent.golden_pool_revision_id).not.toBe(afterUploadContent.golden_pool_revision_id);
  });

  test('FE-017, FE-023, and FE-027 through FE-033 compose roster, Wave, reconnect, and operator control', async ({ page, browser, ownedFixture }) => {
    test.setTimeout(480_000);
    page.setDefaultTimeout(15_000);

    const tournamentName = uniqueName('fullstack-roster');
    const tournamentPublicID = uniqueName('roster-public');
    const playerNames = [
      uniqueName('roster-player-a'),
      uniqueName('roster-player-b'),
      uniqueName('roster-player-c'),
      uniqueName('roster-player-d'),
      uniqueName('roster-player-e'),
    ];
    const playerContexts: BrowserContext[] = [];

    try {
      await loginThroughAdminUI(page);
      const adminRequest = page.context().request;
      const adminAccessCSRF = (await page.context().cookies()).find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      );
      let adminAccessCSRFToken = adminAccessCSRF?.value ?? '';
      expect(adminAccessCSRFToken, 'admin login did not issue an access CSRF cookie').toBeTruthy();

      await page.goto('/arena', { waitUntil: 'domcontentloaded' });

      const normalTaskGroups: Array<{
        category: FullStackTaskInput['category'];
        count: number;
      }> = [
        { category: 'web', count: 27 },
        { category: 'crypto', count: 27 },
        { category: 'reverse', count: 27 },
        { category: 'forensics', count: 3 },
        { category: 'pwn', count: 3 },
      ];
      const normalFlagsByTitle = new Map<string, string>();
      for (const group of normalTaskGroups) {
        for (let index = 0; index < group.count; index += 1) {
          const normalTaskName = uniqueName(`roster-${group.category}-${index + 1}`);
          const normalTaskFlag = `flag{${normalTaskName.replaceAll('-', '_')}}`;
          normalFlagsByTitle.set(normalTaskName, normalTaskFlag);
          await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
            title: normalTaskName,
            description: 'Healthy normal task for the roster flow.',
            kind: 'normal',
            category: group.category,
            difficulty: 'easy',
            time_limit: 180,
            flag: normalTaskFlag,
            hints: ['normal hint one', 'normal hint two', 'normal hint three'],
            task_url: 'https://example.com/roster-normal',
          });
        }
      }
      for (let index = 0; index < 6; index += 1) {
        const goldenTaskName = uniqueName(`roster-golden-${index + 1}`);
        await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
          title: goldenTaskName,
          description: 'Healthy golden task for the roster flow.',
          kind: 'golden',
          category: 'web',
          difficulty: 'easy',
          time_limit: 180,
          flag: `flag{${goldenTaskName.replaceAll('-', '_')}}`,
          hints: ['golden hint one', 'golden hint two', 'golden hint three'],
          task_url: 'https://example.com/roster-golden',
        });
      }

      const contentRevision = await getTournamentContentRevision(adminRequest);
      const tournament = await createTournamentViaApi(
        ownedFixture,
        adminRequest,
        { access_csrf_token: adminAccessCSRFToken },
        {
          name: tournamentName,
          content_revision: contentRevision,
          planned_roster_size: 4,
          public_id: tournamentPublicID,
          preset: 'tournament_v1',
          expected_revision: 0,
        },
      );
      const players: FullStackPlayer[] = [];
      for (const username of playerNames) {
        const context = await browser.newContext({ baseURL: frontendURL });
        playerContexts.push(context);
        const playerPage = await context.newPage();
        await joinAsPlayer(playerPage, username);
        const meResponse = await context.request.get(`${backendURL}/api/v1/players/me`, {
          headers: {
            Cookie: await cookieHeaderForPage(playerPage),
            Origin: frontendURL,
          },
        });
        expect(meResponse.ok(), `players/me failed with ${meResponse.status()}`).toBeTruthy();
        const me = (await meResponse.json()) as { player: FullStackPlayer };
        expect(me.player.username).toBe(username);
        players.push(me.player);
      }

      await page.goto('/admin', { waitUntil: 'domcontentloaded' });
      await expect(page.getByRole('button', { name: 'Соревнования' })).toBeVisible({ timeout: 15_000 });
      await page.getByRole('button', { name: 'Соревнования' }).click();
      const tournamentListRefresh = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/v1/admin/tournaments' &&
          response.request().method() === 'GET',
      );
      await page
        .getByRole('region', { name: 'Соревнования' })
        .getByRole('button', { name: 'Обновить список' })
        .click();
      const refreshedTournamentListResponse = await tournamentListRefresh;
      expect(refreshedTournamentListResponse.status()).toBe(200);
      const loadedTournamentList = (await refreshedTournamentListResponse.json()) as {
        items: Array<{ id: string; name: string }>;
      };
      expect(
        loadedTournamentList.items.some(
          (item) => item.id === tournament.id && item.name === tournamentName,
        ),
      ).toBe(true);
      const tournamentRow = page.getByRole('row').filter({ hasText: tournamentName });
      const openTournamentButton = tournamentRow.getByRole('button', { name: 'Открыть', exact: true });
      await expect(openTournamentButton).toBeVisible({ timeout: 15_000 });
      const playersResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/v1/admin/players' &&
          response.request().method() === 'GET',
      );
      const rosterResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === `/api/v1/admin/tournaments/${tournament.id}/roster` &&
          response.request().method() === 'GET',
      );
      await openTournamentButton.click();
      await page.getByRole('button', { name: 'Участники', exact: true }).click();
      expect((await playersResponse).status()).toBe(200);
      const initialRoster = await readRosterResponse(await rosterResponse, tournament.id);

      const rosterRegion = page.getByRole('region', { name: 'Состав соревнования' });
      await expect(rosterRegion).toBeVisible();
      expect(initialRoster.participants).toHaveLength(0);
      await expect(rosterRegion.getByText('Состав пуст')).toBeVisible();
      await expect(rosterRegion.getByRole('button', { name: 'Добавить участника' })).toBeEnabled();
      await rosterRegion.getByRole('button', { name: 'Добавить участника' }).click({ clickCount: 4 });
      await expect(rosterRegion.getByRole('group')).toHaveCount(4);

      for (const [index, player] of players.slice(0, 4).entries()) {
        const group = rosterRegion.getByRole('group').nth(index);
        await group.getByRole('combobox', { name: 'Игрок' }).fill(player.username);
        await group.getByRole('option', { name: player.username, exact: true }).click();
        await group.getByRole('spinbutton', { name: 'Позиция' }).fill(String(index + 1));
      }

      const firstTournamentRefresh = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/v1/admin/tournaments' &&
          response.request().method() === 'GET',
      );
      const firstSave = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === `/api/v1/admin/tournaments/${tournament.id}/roster` &&
          response.request().method() === 'PUT',
      );
      await rosterRegion.getByRole('button', { name: 'Сохранить состав' }).click();
      const savedRosterResponse = await readRosterResponse(await firstSave, tournament.id);
      const refreshedTournaments = await firstTournamentRefresh;
      expect(refreshedTournaments.status()).toBe(200);
      const refreshedTournamentList = (await refreshedTournaments.json()) as {
        items: Array<{ id: string; revision: number }>;
      };
      const refreshedTournament = refreshedTournamentList.items.find(
        (item) => item.id === tournament.id,
      );
      expect(refreshedTournament?.revision).toBeGreaterThanOrEqual(tournament.revision);

      const savedRoster = await getRosterViaApi(adminRequest, tournament.id);
      expect(savedRoster.id).toBe(savedRosterResponse.id);
      const savedParticipants = sortRosterParticipantsBySeed(savedRoster.participants);
      expect(savedParticipants).toHaveLength(4);
      expect(new Set(savedParticipants.map((item) => item.player_id)).size).toBe(4);
      expect(savedParticipants.map((item) => item.player_id).sort()).toEqual(
        players.slice(0, 4).map((player) => player.id).sort(),
      );
      expect(savedParticipants.map((item) => item.seed)).toEqual([1, 2, 3, 4]);
      expect(savedParticipants.map((item) => item.attendance)).toEqual([
        'invited',
        'invited',
        'invited',
        'invited',
      ]);
      const savedSeedTwoParticipant = savedParticipants.find((item) => item.seed === 2);
      expect(savedSeedTwoParticipant).toBeDefined();
      if (!savedSeedTwoParticipant) {
        throw new Error('saved roster did not return a participant at seed 2');
      }

      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.getByRole('button', { name: 'Соревнования' })).toBeVisible({ timeout: 15_000 });
      const refreshedAdminAccessCSRF = (await page.context().cookies()).find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      );
      adminAccessCSRFToken = refreshedAdminAccessCSRF?.value ?? '';
      expect(adminAccessCSRFToken, 'admin reload did not restore the access CSRF cookie').toBeTruthy();
      await page.getByRole('button', { name: 'Соревнования' }).click();
      await page.getByRole('button', { name: 'Участники', exact: true }).click();
      await expect(rosterRegion).toBeVisible({ timeout: 15_000 });
      await expect(rosterRegion.getByRole('group')).toHaveCount(4);
      for (const [index, participant] of savedParticipants.entries()) {
        const group = rosterRegion.getByRole('group').nth(index);
        const savedPlayer = players.find((player) => player.id === participant.player_id);
        expect(savedPlayer).toBeDefined();
        if (!savedPlayer) {
          throw new Error(`saved roster player ${participant.player_id} was not found in fixture players`);
        }
        await expect(group.getByRole('combobox', { name: 'Игрок' })).toHaveValue(
          savedPlayer.username,
        );
        await expect(group.getByRole('spinbutton', { name: 'Позиция' })).toHaveValue(
          String(index + 1),
        );
      }

      const replacementGroup = rosterRegion.getByRole('group').nth(1);
      await replacementGroup.getByRole('combobox', { name: 'Игрок' }).fill(players[4].username);
      await replacementGroup.getByRole('option', { name: players[4].username, exact: true }).click();

      const secondSave = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === `/api/v1/admin/tournaments/${tournament.id}/roster` &&
          response.request().method() === 'PUT',
      );
      await rosterRegion.getByRole('button', { name: 'Сохранить состав' }).click();
      const replacedRosterResponse = await readRosterResponse(
        await secondSave,
        tournament.id,
        savedRoster.id,
      );

      const replacedRoster = await getRosterViaApi(adminRequest, tournament.id, savedRoster.id);
      expect(replacedRoster.id).toBe(replacedRosterResponse.id);
      const replacedParticipants = sortRosterParticipantsBySeed(replacedRoster.participants);
      expect(replacedParticipants).toHaveLength(4);
      expect(new Set(replacedParticipants.map((item) => item.player_id)).size).toBe(4);
      expect(replacedParticipants.map((item) => item.player_id)).toEqual(
        savedParticipants.map((item) =>
          item.seed === savedSeedTwoParticipant.seed ? players[4].id : item.player_id,
        ),
      );
      expect(replacedParticipants.map((item) => item.seed)).toEqual([1, 2, 3, 4]);
      expect(replacedParticipants.map((item) => item.attendance)).toEqual([
        'invited',
        'invited',
        'invited',
        'invited',
      ]);

      const openRegistrationResponse = await applyOpenRegistrationViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      expect(openRegistrationResponse.status()).toBe(200);
      const openedTournament = (await openRegistrationResponse.json()) as AdminTournament;
      expect(openedTournament.id).toBe(tournament.id);
      expect(openedTournament.state).toBe('registration');

      const checkedInSourceParticipants: FullStackRosterParticipantInput[] =
        replacedParticipants.map((participant) => ({
          player_id: participant.player_id,
          seed: participant.seed,
          attendance: 'checked_in',
        }));
      const checkedInSourceProjectionRevision = await getOperatorProjectionRevisionViaApi(
        adminRequest,
        tournament.id,
      );
      const checkedInSourceResponse = await replaceRosterViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
        checkedInSourceProjectionRevision,
        checkedInSourceParticipants,
      );
      expect(checkedInSourceResponse.status()).toBe(200);
      const checkedInSourceRoster = await readRosterResponse(
        checkedInSourceResponse,
        tournament.id,
        replacedRoster.id,
      );
      expect(checkedInSourceRoster.locked).toBe(false);
      const checkedInSourceRosterParticipants = sortRosterParticipantsBySeed(
        checkedInSourceRoster.participants,
      );
      expect(checkedInSourceRosterParticipants.map((item) => item.player_id)).toEqual(
        replacedParticipants.map((item) => item.player_id),
      );
      expect(checkedInSourceRosterParticipants.map((item) => item.seed)).toEqual([1, 2, 3, 4]);
      expect(checkedInSourceRosterParticipants.map((item) => item.attendance)).toEqual([
        'checked_in',
        'checked_in',
        'checked_in',
        'checked_in',
      ]);

      const preflightRosterResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === `/api/v1/admin/tournaments/${tournament.id}/roster` &&
          response.request().method() === 'GET',
      );
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.getByRole('button', { name: 'Соревнования' })).toBeVisible({ timeout: 15_000 });
      const preflightAdminAccessCSRF = (await page.context().cookies()).find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      );
      adminAccessCSRFToken = preflightAdminAccessCSRF?.value ?? '';
      expect(
        adminAccessCSRFToken,
        'admin reload before FE-029 did not restore the access CSRF cookie',
      ).toBeTruthy();
      await page.getByRole('button', { name: 'Соревнования' }).click();
      await page.getByRole('button', { name: 'Участники', exact: true }).click();
      await expect(rosterRegion).toBeVisible({ timeout: 15_000 });
      expect((await preflightRosterResponse).status()).toBe(200);
      await expect(rosterRegion.getByText('Готов', { exact: true })).toHaveCount(4);

      const browserPreflightResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/roster/preflight` &&
          response.request().method() === 'POST',
      );
      await rosterRegion.getByRole('button', { name: 'Запустить проверку' }).click();
      const browserPreflight = await browserPreflightResponse;
      expect(browserPreflight.status()).toBe(200);
      const browserPreflightReport = (await browserPreflight.json()) as FullStackPreflightReport;
      expect(
        browserPreflightReport.passed,
        `browser roster preflight failed: ${JSON.stringify(
          browserPreflightReport.checks?.filter((check) => !check.passed) ?? [],
        )}`,
      ).toBe(true);
      await expect(rosterRegion.getByText('Проверка пройдена', { exact: true })).toBeVisible();
      await expect(
        rosterRegion.getByRole('button', { name: 'Зафиксировать состав' }),
      ).toBeEnabled();

      const browserLockResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/roster/lock` &&
          response.request().method() === 'POST',
      );
      await rosterRegion.getByRole('button', { name: 'Зафиксировать состав' }).click();
      const browserLockedRoster = await readRosterResponse(
        await browserLockResponse,
        tournament.id,
        checkedInSourceRoster.id,
      );
      expect(browserLockedRoster.locked).toBe(true);
      expect(browserLockedRoster.execution_started).toBe(false);
      await rosterRegion.getByText('Изменить зафиксированный состав', { exact: true }).click();
      await expect(
        rosterRegion.getByRole('button', { name: 'Открыть состав для изменений' }),
      ).toBeVisible();

      await rosterRegion
        .getByLabel('Причина изменения')
        .fill('Проверка управляемого возврата перед стартом');
      await rosterRegion.getByLabel('Подтверждаю открытие состава для изменений').check();
      const browserUnlockResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/roster/unlock` &&
          response.request().method() === 'POST',
      );
      await rosterRegion.getByRole('button', { name: 'Открыть состав для изменений' }).click();
      const browserUnlockedRoster = await readRosterResponse(
        await browserUnlockResponse,
        tournament.id,
        checkedInSourceRoster.id,
      );
      expect(browserUnlockedRoster.locked).toBe(false);
      await expect(rosterRegion.getByText('Можно редактировать', { exact: true })).toBeVisible();

      const preflightResponse = await runRosterPreflightViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      expect(preflightResponse.status()).toBe(200);
      const preflightReport = (await preflightResponse.json()) as FullStackPreflightReport;
      expect(preflightReport.id).toMatch(/^[0-9a-f-]{36}$/i);
      expect(preflightReport.tournament_id).toBe(tournament.id);
      expect(preflightReport.passed).toBe(true);

      const lockedSourceResponse = await lockRosterViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
        preflightReport.id,
        checkedInSourceRosterParticipants.map((item) => item.player_id),
      );
      expect(lockedSourceResponse.status()).toBe(200);
      const lockedSourceRoster = await readRosterResponse(
        lockedSourceResponse,
        tournament.id,
        checkedInSourceRoster.id,
      );
      expect(lockedSourceRoster.locked).toBe(true);
      expect(lockedSourceRoster.execution_started).toBe(false);

      const lockedProjectionRevision = await getOperatorProjectionRevisionViaApi(
        adminRequest,
        tournament.id,
      );
      const lockedParticipants = sortRosterParticipantsBySeed(lockedSourceRoster.participants);
      expect(lockedParticipants).toHaveLength(4);
      const lockedReplacementParticipants: FullStackRosterParticipantInput[] =
        lockedParticipants.map((participant, index) => ({
          player_id: participant.player_id,
          seed: index === 0 ? 2 : index === 1 ? 1 : participant.seed,
          attendance: participant.attendance,
        }));
      expect(lockedReplacementParticipants.map((participant) => participant.seed)).toEqual([
        2,
        1,
        3,
        4,
      ]);
      expect(new Set(lockedReplacementParticipants.map((participant) => participant.player_id)).size).toBe(4);

      const lockedReplacementResponse = await replaceRosterViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
        lockedProjectionRevision,
        lockedReplacementParticipants,
      );
      expect(lockedReplacementResponse.status()).toBe(409);

      const rosterAfterLockedReplacement = await getRosterViaApi(
        adminRequest,
        tournament.id,
        lockedSourceRoster.id,
      );
      expect(rosterAfterLockedReplacement).toEqual(lockedSourceRoster);
      expect(rosterAfterLockedReplacement.id).toBe(lockedSourceRoster.id);
      expect(rosterAfterLockedReplacement.revision).toBe(lockedSourceRoster.revision);
      expect(rosterAfterLockedReplacement.participants).toEqual(lockedSourceRoster.participants);
      expect(rosterAfterLockedReplacement.locked).toBe(true);
      expect(rosterAfterLockedReplacement.execution_started).toBe(false);

      const secondTournament = await createTournamentViaApi(
        ownedFixture,
        adminRequest,
        { access_csrf_token: adminAccessCSRFToken },
        {
          name: `${tournamentName}-conflict`,
          content_revision: contentRevision,
          planned_roster_size: 4,
          public_id: `${tournamentPublicID}-conflict`,
          preset: 'tournament_v1',
          expected_revision: 0,
        },
      );
      const secondRoster = await getRosterViaApi(adminRequest, secondTournament.id);
      expect(secondRoster.participants).toHaveLength(0);
      const firstRosterBeforeConflict = await getRosterViaApi(
        adminRequest,
        tournament.id,
        replacedRoster.id,
      );
      const firstParticipantsBeforeConflict = sortRosterParticipantsBySeed(
        firstRosterBeforeConflict.participants,
      );
      const reservedParticipant =
        firstParticipantsBeforeConflict.find((item) => item.seed === 1) ??
        firstParticipantsBeforeConflict[0];
      expect(reservedParticipant).toBeDefined();
      if (!reservedParticipant) {
        throw new Error('replaced roster did not return a reserved participant');
      }
      expect(players.map((player) => player.id)).toContain(reservedParticipant.player_id);
      const conflictPlayerIDs = [
        reservedParticipant.player_id,
        ...players
          .map((player) => player.id)
          .filter((playerID) => playerID !== reservedParticipant.player_id)
          .slice(0, 3),
      ];
      expect(conflictPlayerIDs).toHaveLength(4);
      expect(new Set(conflictPlayerIDs).size).toBe(4);
      const secondRosterBeforeConflict = await getRosterViaApi(
        adminRequest,
        secondTournament.id,
        secondRoster.id,
      );
      const conflictParticipants: FullStackRosterParticipantInput[] = conflictPlayerIDs.map(
        (playerID, index) => ({
          player_id: playerID,
          seed: index + 1,
          attendance: 'registered',
        }),
      );
      expect(conflictParticipants.map((item) => item.seed)).toEqual([1, 2, 3, 4]);
      expect(new Set(conflictParticipants.map((item) => item.player_id)).size).toBe(4);
      const conflictProjectionRevision = await getOperatorProjectionRevisionViaApi(
        adminRequest,
        secondTournament.id,
      );
      const conflict = await replaceRosterViaApi(
        adminRequest,
        secondTournament.id,
        adminAccessCSRFToken,
        conflictProjectionRevision,
        conflictParticipants,
      );
      expect(conflict.status(), 'a player reserved in another roster must return 409').toBe(409);
      const firstRosterAfterConflict = await getRosterViaApi(
        adminRequest,
        tournament.id,
        firstRosterBeforeConflict.id,
      );
      const secondRosterAfterConflict = await getRosterViaApi(
        adminRequest,
        secondTournament.id,
        secondRosterBeforeConflict.id,
      );
      expect(firstRosterAfterConflict).toEqual(firstRosterBeforeConflict);
      expect(secondRosterAfterConflict).toEqual(secondRosterBeforeConflict);

      const startSwissResponse = await applyStartSwissViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      const startSwissFailure =
        startSwissResponse.status() === 200 ? '' : startSwissResponse.statusText();
      expect(
        startSwissResponse.status(),
        `start Swiss action must succeed${startSwissFailure ? `: ${startSwissFailure}` : ''}`,
      ).toBe(200);
      const swissTournament = (await startSwissResponse.json()) as AdminTournament;
      expect(swissTournament.state).toBe('swiss');

      await page.getByRole('button', { name: 'Сетка и серии', exact: true }).click();
      const pairingRegion = page.getByRole('region', { name: 'Пары квалификации' });
      await expect(pairingRegion).toBeVisible({ timeout: 15_000 });
      const pairingReload = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/configuration` &&
          response.request().method() === 'GET',
      );
      await pairingRegion.getByRole('button', { name: 'Обновить состояние' }).click();
      expect((await pairingReload).status()).toBe(200);

      const pairingResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/pairings` &&
          response.request().method() === 'POST',
      );
      await pairingRegion.getByRole('button', { name: 'Сформировать пары' }).click();
      const configuredPairingResponse = await pairingResponse;
      expect(
        configuredPairingResponse.status(),
        `Swiss pairing failed with ${configuredPairingResponse.status()}`,
      ).toBe(200);
      const configuredRound = (await configuredPairingResponse.json()) as FullStackSwissRound;
      expect(configuredRound.tournament_id).toBe(tournament.id);
      expect(configuredRound.round_number).toBe(1);
      expect(configuredRound.revision).toBeGreaterThan(0);
      expect(configuredRound.locked).toBe(false);
      expect(configuredRound.pairings).toHaveLength(2);
      expect(configuredRound.pairings.every((pairing) => pairing.repeated !== true)).toBe(true);
      const pairedParticipantIDs = configuredRound.pairings.flatMap((pairing) => [
        pairing.first_participant_id,
        pairing.second_participant_id,
      ]);
      expect(new Set(pairedParticipantIDs).size).toBe(4);
      expect([...pairedParticipantIDs].sort()).toEqual(
        [...configuredRound.roster_participant_ids].sort(),
      );
      await expect(pairingRegion.getByText('План раунда 1')).toBeVisible();
      await expect(pairingRegion.getByText('Сохранен', { exact: true })).toBeVisible();
      await expect(pairingRegion.getByText('Повтор', { exact: true })).toHaveCount(0);

      const seriesRegion = page.getByRole('region', { name: 'Конфигурация серий' });
      await expect(seriesRegion).toBeVisible();
      const seriesConfigurationReload = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/configuration` &&
          response.request().method() === 'GET',
      );
      await seriesRegion.getByRole('button', { name: 'Обновить конфигурацию' }).click();
      expect((await seriesConfigurationReload).status()).toBe(200);

      const firstSeries = seriesRegion.getByRole('article').first();
      await expect(firstSeries).toBeVisible();
      const firstSeriesID = await firstSeries.getAttribute('data-series-id');
      expect(firstSeriesID).toMatch(/^[0-9a-f-]{36}$/i);
      await expect(firstSeries.getByLabel('Режим серии 1')).toHaveValue('random');
      await firstSeries.getByLabel('Режим серии 1').selectOption('admin');
      await firstSeries.getByLabel('Категория серии 1').selectOption('crypto');

      const seriesUpdateResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/series/${firstSeriesID}/configuration` &&
          response.request().method() === 'PATCH',
      );
      const seriesRefreshResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/configuration` &&
          response.request().method() === 'GET',
      );
      await firstSeries.getByRole('button', { name: 'Сохранить серию 1' }).click();
      const savedSeriesResponse = await seriesUpdateResponse;
      expect(savedSeriesResponse.status()).toBe(200);
      const seriesMutation = (await savedSeriesResponse.json()) as FullStackConfigurationMutation;
      const successorSeriesID = seriesMutation.affected_artifacts.find(
        (artifact) => artifact.id === firstSeriesID,
      )?.successor_revision_id;
      expect(successorSeriesID).toMatch(/^[0-9a-f-]{36}$/i);
      expect((await seriesRefreshResponse).status()).toBe(200);
      const successorSeries = seriesRegion.locator(
        `[data-series-id="${successorSeriesID}"]`,
      );
      await expect(successorSeries).toBeVisible();
      await expect(successorSeries.getByLabel(/Режим серии/)).toHaveValue('admin');
      await expect(successorSeries.getByLabel(/Категория серии/)).toHaveValue('crypto');

      await page.getByRole('button', { name: 'Проведение', exact: true }).click();
      const waveRegion = page.getByRole('region', { name: 'Волны и матчи' });
      await expect(waveRegion).toBeVisible();
      const refreshedWaveSnapshotResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/snapshot` &&
          response.request().method() === 'GET',
      );
      await waveRegion.getByRole('button', { name: 'Обновить матчи' }).click();
      const refreshedWaveSnapshotHTTP = await refreshedWaveSnapshotResponse;
      expect(refreshedWaveSnapshotHTTP.status()).toBe(200);
      const refreshedWaveSnapshot = (await refreshedWaveSnapshotHTTP.json()) as FullStackOperatorSnapshot;
      expect(refreshedWaveSnapshot.waves).toHaveLength(1);
      const plannedWave = refreshedWaveSnapshot.waves[0];
      if (!plannedWave) {
        throw new Error('Swiss pairing did not create a Wave');
      }
      await expect(waveRegion.getByTestId('operator-wave')).toHaveCount(1);
      await expect(waveRegion.getByTestId('operator-match')).toHaveCount(2);

      const openWaveResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/waves/${plannedWave.id}/actions` &&
          response.request().method() === 'POST',
      );
      await waveRegion.getByRole('button', { name: 'Открыть готовность' }).click();
      const openedWaveHTTP = await openWaveResponse;
      expect(
        openedWaveHTTP.status(),
        `open Wave failed with ${openedWaveHTTP.status()} ${openedWaveHTTP.statusText()}`,
      ).toBe(200);

      const firstWaveSeriesID = plannedWave.members.find((member) => member.series_id)?.series_id;
      expect(firstWaveSeriesID, 'started Wave did not expose a Series for its first match').toMatch(
        /^[0-9a-f-]{36}$/i,
      );
      const firstWaveSeriesMembers = plannedWave.members.filter(
        (member) => member.series_id === firstWaveSeriesID,
      );
      expect(firstWaveSeriesMembers, 'started Wave did not expose both members of a Series').toHaveLength(2);

      const contextForParticipant = (participantID: string): BrowserContext => {
        const rosterParticipant = checkedInSourceRosterParticipants.find(
          (participant) => participant.id === participantID,
        );
        expect(rosterParticipant, `missing roster participant ${participantID}`).toBeDefined();
        if (!rosterParticipant) {
          throw new Error(`missing roster participant ${participantID}`);
        }
        const playerIndex = players.findIndex((player) => player.id === rosterParticipant.player_id);
        const context = playerContexts[playerIndex];
        if (!context) {
          throw new Error(`missing browser context for player ${rosterParticipant.player_id}`);
        }
        return context;
      };

      for (const member of firstWaveSeriesMembers) {
        const waitingSnapshot = await readParticipantSnapshotViaApi(
          contextForParticipant(member.participant_id),
          tournament.id,
        );
        expect(waitingSnapshot.tournament_id).toBe(tournament.id);
        expect(waitingSnapshot.assignment, 'waiting participant snapshot must not expose an assignment').toBeNull();
        expect(JSON.stringify(waitingSnapshot)).not.toContain('active_snapshot');
        expect(JSON.stringify(waitingSnapshot)).not.toContain('effective_deadline');
      }

      let readinessParticipantPage: Page | null = null;
      for (const participant of checkedInSourceRosterParticipants) {
        const playerIndex = players.findIndex((player) => player.id === participant.player_id);
        const playerContext = playerContexts[playerIndex];
        if (!playerContext) {
          throw new Error(`missing browser context for player ${participant.player_id}`);
        }
        const playerCSRF = (await playerContext.cookies()).find(
          (cookie) => cookie.name === 'tpm_player_csrf',
        )?.value;
        expect(playerCSRF, `player ${participant.player_id} did not receive a CSRF cookie`).toBeTruthy();
        const participantSnapshotResponse = await playerContext.request.get(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/snapshot`,
          { headers: { Origin: frontendURL } },
        );
        expect(
          participantSnapshotResponse.status(),
          `participant snapshot failed with ${participantSnapshotResponse.status()}`,
        ).toBe(200);
        const participantSnapshot =
          (await participantSnapshotResponse.json()) as FullStackParticipantSnapshot;
        if (playerIndex === 0) {
          const participantPage = await playerContext.newPage();
          readinessParticipantPage = participantPage;
          await participantPage.goto(
            `${frontendURL}/arena/participant/${tournament.id}`,
          );
          const participantPanel = participantPage.getByTestId('participant-player-panel');
          await expect(participantPanel).toHaveAttribute('data-state', 'assigned');
          await expect(participantPanel.getByText('Подтверждена', { exact: true })).toBeVisible();
          await expect(participantPanel.getByText('Раунд 1', { exact: true })).toBeVisible();
          await expect(participantPage.getByTestId('participant-ready-button')).toBeEnabled();

          const participantReadyResponse = participantPage.waitForResponse(
            (response) =>
              new URL(response.url()).pathname ===
                `/api/v1/tournaments/${tournament.id}/participant/waves/${plannedWave.id}/ready` &&
              response.request().method() === 'POST',
          );
          await participantPage.getByTestId('participant-ready-button').click();
          const participantReadyHTTP = await participantReadyResponse;
          expect(
            participantReadyHTTP.status(),
            `participant UI readiness failed with ${participantReadyHTTP.status()} ${participantReadyHTTP.statusText()}`,
          ).toBe(200);
          await expect(
            participantPage.getByText('Готовность подтверждена сервером.', { exact: true }),
          ).toBeVisible();
          continue;
        }
        const readyResponse = await playerContext.request.post(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${plannedWave.id}/ready`,
          {
            headers: {
              'X-CSRF-Token': playerCSRF ?? '',
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              expected_projection_revision: participantSnapshot.projection_revision,
              ready: true,
            },
          },
        );
        expect(
          readyResponse.status(),
          `participant readiness failed with ${readyResponse.status()} ${readyResponse.statusText()}`,
        ).toBe(200);
      }

      const readyWaveSnapshotResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/snapshot` &&
          response.request().method() === 'GET',
      );
      await waveRegion.getByRole('button', { name: 'Обновить матчи' }).click();
      expect((await readyWaveSnapshotResponse).status()).toBe(200);
      await expect(waveRegion.getByRole('button', { name: 'Начать волну' })).toBeEnabled();
      await expect(waveRegion.getByTestId('wave-ready-countdown')).toHaveCount(2);

      const startWaveResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/waves/${plannedWave.id}/actions` &&
          response.request().method() === 'POST',
      );
      await waveRegion.getByRole('button', { name: 'Начать волну' }).click();
      const startedWaveHTTP = await startWaveResponse;
      expect(
        startedWaveHTTP.status(),
        `start Wave failed with ${startedWaveHTTP.status()} ${startedWaveHTTP.statusText()}`,
      ).toBe(200);
      await expect(waveRegion.getByTestId('operator-match')).toHaveCount(2);
      await expect(waveRegion.getByTestId('operator-match').getByText('Идет', { exact: true }))
        .toHaveCount(2);

      const deliveredPairSnapshots = await Promise.all(
        firstWaveSeriesMembers.map((member) =>
          readParticipantSnapshotViaApi(contextForParticipant(member.participant_id), tournament.id),
        ),
      );
      const deliveredPairAssignments = deliveredPairSnapshots.map((snapshot, index) => {
        expect(snapshot.assignment, `participant ${index + 1} did not receive a delivered assignment`).not.toBeNull();
        if (!snapshot.assignment) {
          throw new Error(`participant ${index + 1} did not receive a delivered assignment`);
        }
        return snapshot.assignment;
      });
      const firstAssignment = deliveredPairAssignments[0];
      const secondAssignment = deliveredPairAssignments[1];
      const firstDeliveredSnapshot = deliveredPairSnapshots[0];
      const secondDeliveredSnapshot = deliveredPairSnapshots[1];
      const firstWaveMember = firstWaveSeriesMembers[0];
      const secondWaveMember = firstWaveSeriesMembers[1];
      if (!firstAssignment || !secondAssignment || !firstDeliveredSnapshot || !secondDeliveredSnapshot ||
        !firstWaveMember || !secondWaveMember) {
        throw new Error('FE-018 did not receive both participant assignments');
      }

      expect(firstAssignment.active_snapshot.task_id).toBe(secondAssignment.active_snapshot.task_id);
      expect(firstAssignment.active_snapshot.snapshot_id).toBe(secondAssignment.active_snapshot.snapshot_id);
      expect(firstAssignment.active_snapshot.version).toBe(secondAssignment.active_snapshot.version);
      expect(firstAssignment.context).toEqual(secondAssignment.context);
      expect(firstAssignment.context.wave_id).toBe(plannedWave.id);
      expect(firstAssignment.context.series_id).toBe(firstWaveSeriesID);
      expect(secondAssignment.context.series_id).toBe(firstWaveSeriesID);
      expect(firstAssignment.context.slot_id).toMatch(/^[0-9a-f-]{36}$/i);
      expect(firstAssignment.context.game_id).toMatch(/^[0-9a-f-]{36}$/i);
      expect(firstAssignment.context.stage).toBe('swiss');
      expect(firstAssignment.context.swiss_round).toBe(1);
      expect(firstAssignment.context.game_number).toBe(1);
      expect(firstAssignment.context.series_score).toEqual({
        first_participant_wins: 0,
        second_participant_wins: 0,
      });
      expect(firstAssignment.context.game_state).toBe('active');
      expect(firstAssignment.context.started_at).toBeTruthy();
      expect(firstAssignment.context.effective_deadline).toBeTruthy();
      expect(firstAssignment.active_snapshot.time_limit).toBe(180);
      if (!firstAssignment.context.started_at || !firstAssignment.context.effective_deadline) {
        throw new Error('FE-018 assignment did not return authoritative game timing');
      }
      expect(
        Date.parse(firstAssignment.context.effective_deadline) -
          Date.parse(firstAssignment.context.started_at),
      ).toBe(180_000);
      expect(firstAssignment.receipt.task_id).toBe(firstAssignment.active_snapshot.task_id);
      expect(firstAssignment.receipt.snapshot_id).toBe(firstAssignment.active_snapshot.snapshot_id);
      expect(secondAssignment.receipt.task_id).toBe(secondAssignment.active_snapshot.task_id);
      expect(secondAssignment.receipt.snapshot_id).toBe(secondAssignment.active_snapshot.snapshot_id);
      expect(firstDeliveredSnapshot.wave?.id).toBe(plannedWave.id);
      expect(secondDeliveredSnapshot.wave?.id).toBe(plannedWave.id);
      expect(firstDeliveredSnapshot.wave?.members).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            participant_id: firstWaveMember.participant_id,
            series_id: firstWaveMember.series_id,
          }),
        ]),
      );
      expect(secondDeliveredSnapshot.wave?.members).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            participant_id: secondWaveMember.participant_id,
            series_id: secondWaveMember.series_id,
          }),
        ]),
      );
      for (const snapshot of deliveredPairSnapshots) {
        const series = snapshot.series;
        expect(series?.id).toBe(firstAssignment.context.series_id);
        const slot = series?.slots.find((candidate) => candidate.id === firstAssignment.context.slot_id);
        expect(slot?.position).toBe(firstAssignment.context.game_number);
        expect(slot?.attempts).toEqual(
          expect.arrayContaining([
            expect.objectContaining({ id: firstAssignment.context.game_id, state: 'active' }),
          ]),
        );
      }

      let firstParticipantPage = await contextForParticipant(firstWaveMember.participant_id).newPage();
      const secondParticipantPage = await contextForParticipant(secondWaveMember.participant_id).newPage();
      const participantPath = `${frontendURL}/arena/participant/${tournament.id}`;
      await Promise.all([
        firstParticipantPage.goto(participantPath, { waitUntil: 'domcontentloaded' }),
        secondParticipantPage.goto(participantPath, { waitUntil: 'domcontentloaded' }),
      ]);
      for (const activeParticipantPage of [firstParticipantPage, secondParticipantPage]) {
        await expect(activeParticipantPage.getByTestId('participant-player-panel')).toHaveAttribute(
          'data-state',
          /assigned|ready/,
        );
        await expect(activeParticipantPage.getByTestId('server-countdown')).toBeVisible({
          timeout: 15_000,
        });
      }
      // Once the in-game sockets are active, release the pre-start readiness
      // page. Its lease can drain without creating a false zero-connection gap.
      if (readinessParticipantPage) {
        await readinessParticipantPage.close();
        readinessParticipantPage = null;
      }
      await expect(
        secondParticipantPage.getByTestId('participant-runtime-presence').locator('li'),
      ).toHaveCount(2, { timeout: 15_000 });

      const countdownBeforeReload = await readServerCountdownSeconds(firstParticipantPage);
      expect(countdownBeforeReload).toBeGreaterThan(0);
      await firstParticipantPage.waitForTimeout(1_000);
      await firstParticipantPage.reload({ waitUntil: 'domcontentloaded' });
      await expect(firstParticipantPage.getByTestId('server-countdown')).toBeVisible({
        timeout: 15_000,
      });
      const countdownAfterReload = await readServerCountdownSeconds(firstParticipantPage);
      expect(countdownAfterReload).toBeGreaterThan(0);
      expect(countdownAfterReload).toBeLessThanOrEqual(countdownBeforeReload + 1);
      expect(countdownAfterReload).toBeLessThanOrEqual(180);

      await firstParticipantPage.close();
      const disconnectStatus = secondParticipantPage.getByTestId('participant-runtime-status');
      await expect(disconnectStatus).toHaveAttribute('data-paused', 'true', { timeout: 15_000 });
      await expect(disconnectStatus).toHaveAttribute('data-pause-reason', 'disconnect');
      await expect(secondParticipantPage.getByTestId('server-countdown')).toHaveCount(0);
      await expect(secondParticipantPage.getByTestId('participant-submit-button')).toBeDisabled();
      await expect(
        secondParticipantPage.getByTestId(
          `participant-runtime-presence-${firstWaveMember.participant_id}`,
        ),
      ).toHaveAttribute('data-state', 'disconnected');
      await expect(
        secondParticipantPage.getByTestId('participant-runtime-reconnect').locator('li'),
      ).toHaveCount(1);

      firstParticipantPage = await contextForParticipant(firstWaveMember.participant_id).newPage();
      await firstParticipantPage.goto(participantPath, { waitUntil: 'domcontentloaded' });
      for (const activeParticipantPage of [firstParticipantPage, secondParticipantPage]) {
        await expect(activeParticipantPage.getByTestId('participant-runtime-status'))
          .toHaveAttribute('data-paused', 'false', { timeout: 15_000 });
        await expect(activeParticipantPage.getByTestId('server-countdown')).toBeVisible();
        await expect(activeParticipantPage.getByTestId('participant-submit-button')).toBeEnabled();
      }

      const operatorPath = `/arena/operator/${tournament.id}`;
      await page.goto(operatorPath, { waitUntil: 'domcontentloaded' });
      await expect(page.getByText('Снимок подтвержден', { exact: true })).toBeVisible({
        timeout: 15_000,
      });
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page).toHaveURL(new URL(operatorPath, frontendURL).toString());
      await expect(page.getByText('Снимок подтвержден', { exact: true })).toBeVisible({
        timeout: 15_000,
      });
      await expectNoSensitiveAuthStorage(page);

      const restoredCookies = await page.context().cookies();
      const restoredAccessCSRF = restoredCookies.find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      );
      expect(restoredAccessCSRF?.path).toBe('/');
      expect(restoredAccessCSRF?.httpOnly).toBe(false);
      const restoredAccessSession = restoredCookies.find(
        (cookie) => cookie.name === 'tpm_admin_access',
      );
      expect(restoredAccessSession?.path).toBe('/api/v1/admin');
      expect(restoredAccessSession?.httpOnly).toBe(true);

      const operatorWaveActionPath =
        `/api/v1/admin/tournaments/${tournament.id}/waves/${plannedWave.id}/actions`;
      const actionSelect = page.getByLabel('Команда оператора');
      await actionSelect.selectOption('pause');
      await page.getByLabel('Причина').fill('Проверка CSRF после reload operator Arena');
      await page
        .getByLabel('Подтверждаю, что команда соответствует актуальным данным соревнования.')
        .check();
      const pauseResponsePromise = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === operatorWaveActionPath &&
          response.request().method() === 'POST',
      );
      await page.getByRole('button', { name: 'Выполнить: Пауза соревнования' }).click();
      const pauseResponse = await pauseResponsePromise;
      expect(
        pauseResponse.request().headers()['x-csrf-token'],
        'pause after reload must send the restored admin access CSRF token',
      ).toBeTruthy();
      expect(
        pauseResponse.status(),
        `pause after reload failed with ${pauseResponse.status()} ${pauseResponse.statusText()}`,
      ).toBe(200);
      await expect(page.getByRole('region', { name: 'Управление соревнованием' }))
        .toContainText('Техническая пауза');
      for (const activeParticipantPage of [firstParticipantPage, secondParticipantPage]) {
        const runtimeStatus = activeParticipantPage.getByTestId('participant-runtime-status');
        await expect(runtimeStatus).toHaveAttribute('data-paused', 'true', { timeout: 15_000 });
        await expect(runtimeStatus).toHaveAttribute('data-pause-reason', 'operator');
        await expect(activeParticipantPage.getByTestId('server-countdown')).toHaveCount(0);
        await expect(activeParticipantPage.getByTestId('participant-submit-button')).toBeDisabled();
      }

      await firstParticipantPage.close();
      await expect(secondParticipantPage.getByTestId('participant-runtime-status'))
        .toHaveAttribute('data-pause-reason', 'operator', { timeout: 15_000 });
      firstParticipantPage = await contextForParticipant(firstWaveMember.participant_id).newPage();
      await firstParticipantPage.goto(participantPath, { waitUntil: 'domcontentloaded' });
      await expect(firstParticipantPage.getByTestId('participant-runtime-status'))
        .toHaveAttribute('data-pause-reason', 'operator', { timeout: 15_000 });
      await expect(firstParticipantPage.getByTestId('server-countdown')).toHaveCount(0);

      await actionSelect.selectOption('resume');
      await page.getByLabel('Причина').fill('Проверка resume с восстановленным CSRF contract');
      await page
        .getByLabel('Подтверждаю, что команда соответствует актуальным данным соревнования.')
        .check();
      const resumeResponsePromise = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === operatorWaveActionPath &&
          response.request().method() === 'POST',
      );
      await page.getByRole('button', { name: 'Выполнить: Возобновить соревнование' }).click();
      const resumeResponse = await resumeResponsePromise;
      expect(
        resumeResponse.request().headers()['x-csrf-token'],
        'resume after reload must reuse the restored admin access CSRF token',
      ).toBeTruthy();
      expect(
        resumeResponse.status(),
        `resume after reload failed with ${resumeResponse.status()} ${resumeResponse.statusText()}`,
      ).toBe(200);
      await expect(page.getByRole('region', { name: 'Управление соревнованием' }))
        .toContainText('Квалификация');
      for (const activeParticipantPage of [firstParticipantPage, secondParticipantPage]) {
        await expect(activeParticipantPage.getByTestId('participant-runtime-status'))
          .toHaveAttribute('data-paused', 'false', { timeout: 15_000 });
        await expect(activeParticipantPage.getByTestId('server-countdown')).toBeVisible();
        await expect(activeParticipantPage.getByTestId('participant-submit-button')).toBeEnabled();
      }

      const correctFlag = normalFlagsByTitle.get(firstAssignment.active_snapshot.title);
      expect(correctFlag, 'assigned normal task must retain its acceptance flag').toBeTruthy();
      if (!correctFlag) {
        throw new Error('assigned normal task did not retain its acceptance flag');
      }
      const submissionResponsePromise = firstParticipantPage.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/tournaments/${tournament.id}/participant/series/${firstAssignment.context.series_id}/games/${firstAssignment.context.game_id}/submissions` &&
          response.request().method() === 'POST',
      );
      await firstParticipantPage.getByTestId('participant-answer-input').fill(correctFlag);
      await firstParticipantPage.getByTestId('participant-submit-button').click();
      const submissionResponse = await submissionResponsePromise;
      expect(
        submissionResponse.status(),
        `participant correct submission failed with ${submissionResponse.status()} ${submissionResponse.statusText()}`,
      ).toBe(200);
      const reloadTerminalParticipant = async (participantPage: Page): Promise<void> => {
        const lobbyResponsePromise = participantPage.waitForResponse(
          (response) =>
            new URL(response.url()).pathname ===
              `/api/v1/tournaments/${tournament.id}/participant/lobby` &&
            response.request().method() === 'GET',
        );
        const snapshotResponsePromise = participantPage.waitForResponse(
          (response) =>
            new URL(response.url()).pathname ===
              `/api/v1/tournaments/${tournament.id}/participant/snapshot` &&
            response.request().method() === 'GET',
        );
        await participantPage.reload({ waitUntil: 'domcontentloaded' });
        const lobbyResponse = await lobbyResponsePromise;
        expect(
          lobbyResponse.status(),
          `terminal participant lobby failed with ${lobbyResponse.status()} ${lobbyResponse.statusText()}`,
        ).toBe(200);
        const snapshotResponse = await snapshotResponsePromise;
        expect(
          snapshotResponse.status(),
          `terminal participant snapshot failed with ${snapshotResponse.status()} ${snapshotResponse.statusText()}`,
        ).toBe(200);
      };
      await Promise.all([
        reloadTerminalParticipant(firstParticipantPage),
        reloadTerminalParticipant(secondParticipantPage),
      ]);
      for (const activeParticipantPage of [firstParticipantPage, secondParticipantPage]) {
        const seriesResult = activeParticipantPage.getByTestId('participant-series-result');
        await expect(seriesResult).toHaveAttribute('data-series-state', 'completed', {
          timeout: 15_000,
        });
        await expect(seriesResult.getByTestId('participant-series-score')).toHaveText(/^(1:0|0:1)$/);
        await expect(seriesResult).toContainText('Ревизия результата');
        await expect(seriesResult.getByTestId('participant-series-game-1-1')).toContainText('Решено');
      }

      const staleStartResponse = await adminRequest.post(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/waves/${plannedWave.id}/actions`,
        {
          headers: {
            'X-CSRF-Token': adminAccessCSRFToken,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            action: 'start',
            confirmed: true,
            expected_projection_revision: refreshedWaveSnapshot.next_cursor.projection_revision,
            reason: 'Проверка отказа для устаревшей ревизии',
          },
        },
      );
      expect(staleStartResponse.status(), 'stale Wave start must be rejected').toBe(409);
    } finally {
      ownedFixture.contexts.push(...playerContexts);
    }
  });

  test('FE-036, FE-037, and FE-050 real backend correction audit cancellation and privacy', async ({ page, browser, ownedFixture }) => {
    test.setTimeout(600_000);
    page.setDefaultTimeout(15_000);

    type CorrectionSnapshotSeries = {
      first_participant_id: string;
      id: string;
      second_participant_id: string;
      score: {
        first_participant_wins: number;
        second_participant_wins: number;
      };
      slots: Array<{
        attempts: Array<{
          id: string;
          result_revision_id: string | null;
          state: string;
        }>;
        position: number;
      }>;
    };
    type CorrectionSnapshot = FullStackOperatorSnapshot & {
      series: CorrectionSnapshotSeries[];
      tournament: { id: string; state: string };
    };

    const tournamentName = uniqueName('correction');
    const tournamentPublicID = uniqueName('correction-public');
    const playerContexts: BrowserContext[] = [];
    let publicContext: BrowserContext | null = null;
    let spectatorPage: Page | null = null;

    try {
      await loginThroughAdminUI(page);
      const adminRequest = page.context().request;
      const adminAccessCSRFToken = (await page.context().cookies()).find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      )?.value ?? '';
      expect(adminAccessCSRFToken, 'admin login did not issue an access CSRF cookie').toBeTruthy();

      const normalTaskGroups: Array<{
        category: FullStackTaskInput['category'];
        count: number;
      }> = [
        { category: 'web', count: 27 },
        { category: 'crypto', count: 27 },
        { category: 'reverse', count: 27 },
        { category: 'forensics', count: 3 },
        { category: 'pwn', count: 3 },
      ];
      const normalFlagsByTitle = new Map<string, string>();
      const privateTaskURL = 'https://example.com/private/correction-normal.zip?expires=1893456000&signature=FE050-private-canary';
      for (const group of normalTaskGroups) {
        for (let index = 0; index < group.count; index += 1) {
          const title = uniqueName(`correction-${group.category}-${index + 1}`);
          const flag = `flag{${title.replaceAll('-', '_')}}`;
          normalFlagsByTitle.set(title, flag);
          await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
            title,
            description: 'Normal task for the FE-036 correction flow.',
            kind: 'normal',
            category: group.category,
            difficulty: 'easy',
            time_limit: 180,
            flag,
            hints: ['correction hint one', 'correction hint two', 'correction hint three'],
            task_url: privateTaskURL,
          });
        }
      }
      for (let index = 0; index < 6; index += 1) {
        const title = uniqueName(`correction-golden-${index + 1}`);
        await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
          title,
          description: 'Golden task for the FE-036 correction flow.',
          kind: 'golden',
          category: 'web',
          difficulty: 'easy',
          time_limit: 180,
          flag: `flag{${title.replaceAll('-', '_')}}`,
          hints: ['golden hint one', 'golden hint two', 'golden hint three'],
          task_url: 'https://example.com/correction-golden',
        });
      }

      const contentRevision = await getTournamentContentRevision(adminRequest);
      const tournament = await createTournamentViaApi(
        ownedFixture,
        adminRequest,
        { access_csrf_token: adminAccessCSRFToken },
        {
          name: tournamentName,
          content_revision: contentRevision,
          planned_roster_size: 4,
          public_id: tournamentPublicID,
          preset: 'tournament_v1',
          expected_revision: 0,
        },
      );

      const players: FullStackPlayer[] = [];
      for (let index = 0; index < 4; index += 1) {
        const context = await browser.newContext({ baseURL: frontendURL });
        playerContexts.push(context);
        const playerPage = await context.newPage();
        const username = uniqueName(`correction-player-${index + 1}`);
        await joinAsPlayer(playerPage, username);
        const meResponse = await context.request.get(`${backendURL}/api/v1/players/me`, {
          headers: { Origin: frontendURL },
        });
        expect(meResponse.status(), `players/me failed with ${meResponse.status()}`).toBe(200);
        const me = (await meResponse.json()) as { player: FullStackPlayer };
        expect(me.player.username).toBe(username);
        players.push(me.player);
        await playerPage.close();
      }

      const roster = await getRosterViaApi(adminRequest, tournament.id);
      const invitedParticipants = players.map((player, index) => ({
        attendance: 'invited' as const,
        player_id: player.id,
        seed: index + 1,
      }));
      const invitedRosterResponse = await replaceRosterViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
        await getOperatorProjectionRevisionViaApi(adminRequest, tournament.id),
        invitedParticipants,
      );
      const invitedRoster = await readRosterResponse(invitedRosterResponse, tournament.id, roster.id);
      expect(invitedRoster.participants).toHaveLength(4);

      const openRegistration = await applyOpenRegistrationViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      expect(openRegistration.status(), `open registration failed with ${openRegistration.status()}`).toBe(200);

      const checkedInRosterResponse = await replaceRosterViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
        await getOperatorProjectionRevisionViaApi(adminRequest, tournament.id),
        invitedParticipants.map((participant) => ({ ...participant, attendance: 'checked_in' as const })),
      );
      const checkedInRoster = await readRosterResponse(
        checkedInRosterResponse,
        tournament.id,
        roster.id,
      );
      const checkedInParticipants = sortRosterParticipantsBySeed(checkedInRoster.participants);
      expect(checkedInParticipants.map((participant) => participant.attendance)).toEqual([
        'checked_in',
        'checked_in',
        'checked_in',
        'checked_in',
      ]);

      const rosterPreflightResponse = await runRosterPreflightViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      expect(rosterPreflightResponse.status(), `roster preflight failed with ${rosterPreflightResponse.status()}`).toBe(200);
      const preflight = (await rosterPreflightResponse.json()) as FullStackPreflightReport;
      const lockResponse = await lockRosterViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
        preflight.id,
        checkedInParticipants.map((participant) => participant.player_id),
      );
      expect(lockResponse.status(), `roster lock failed with ${lockResponse.status()}`).toBe(200);

      const startSwiss = await applyStartSwissViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      expect(startSwiss.status(), `start Swiss failed with ${startSwiss.status()} ${startSwiss.statusText()}`).toBe(200);

      const contextForParticipant = (participantID: string): BrowserContext => {
        const rosterParticipant = checkedInParticipants.find((participant) => participant.id === participantID);
        expect(rosterParticipant, `missing roster participant ${participantID}`).toBeDefined();
        if (!rosterParticipant) {
          throw new Error(`missing roster participant ${participantID}`);
        }
        const playerIndex = players.findIndex((player) => player.id === rosterParticipant.player_id);
        const context = playerContexts[playerIndex];
        if (!context) {
          throw new Error(`missing context for player ${rosterParticipant.player_id}`);
        }
        return context;
      };

      const waveAction = async (
        waveID: string,
        action: 'open_ready_window' | 'start' | 'complete',
        knownProjectionRevision?: number,
      ): Promise<FullStackWave> => {
        let expectedRevision = knownProjectionRevision ?? await getOperatorProjectionRevisionViaApi(
          adminRequest,
          tournament.id,
        );
        let response: APIResponse | null = null;
        for (let attempt = 0; attempt < 60; attempt += 1) {
          response = await adminRequest.post(
            `${backendURL}/api/v1/admin/tournaments/${tournament.id}/waves/${waveID}/actions`,
            {
              headers: {
                'X-CSRF-Token': adminAccessCSRFToken,
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: {
                action,
                confirmed: true,
                expected_projection_revision: expectedRevision,
              },
            },
          );
          if (response.status() === 200) {
            return (await response.json()) as FullStackWave;
          }

          const status = response.status();
          expect([409, 429], `${action} Wave returned unexpected status ${status}`).toContain(status);
          if (status === 409) {
            const conflict = (await response.json()) as { current_revision?: unknown };
            if (
              typeof conflict.current_revision === 'number' &&
              Number.isSafeInteger(conflict.current_revision) &&
              conflict.current_revision >= 1
            ) {
              expectedRevision = conflict.current_revision;
            } else {
              expectedRevision = await getOperatorProjectionRevisionViaApi(adminRequest, tournament.id);
            }
          } else {
            if (attempt < 59) {
              await waitForTransientRetry(attempt);
              expectedRevision = await getOperatorProjectionRevisionViaApi(adminRequest, tournament.id);
            }
            continue;
          }

          if (attempt < 59) {
            await waitForTransientRetry(attempt);
          }
        }

        throw new Error(`${action} Wave did not converge after retries: ${response?.status() ?? 'no response'}`);
      };

      const settleWave = async (
        wave: FullStackWave,
      ): Promise<{ gameID: string; seriesID: string } | null> => {
        const membersBySeries = new Map<string, FullStackWave['members']>();
        for (const member of wave.members) {
          if (!member.series_id) {
            continue;
          }
          const members = membersBySeries.get(member.series_id) ?? [];
          members.push(member);
          membersBySeries.set(member.series_id, members);
        }

        let latestResult: { gameID: string; seriesID: string } | null = null;
        for (const [seriesID, members] of membersBySeries) {
          let seriesResult: { gameID: string; seriesID: string } | null = null;
          for (const member of members) {
            const context = contextForParticipant(member.participant_id);
            const participant = await readParticipantSnapshotViaApi(context, tournament.id);
            if (!participant.assignment || participant.assignment.context.series_id !== seriesID) {
              continue;
            }
            const flag = normalFlagsByTitle.get(participant.assignment.active_snapshot.title);
            expect(flag, `missing flag for ${participant.assignment.active_snapshot.title}`).toBeTruthy();
            const csrfToken = (await context.cookies()).find(
              (cookie) => cookie.name === 'tpm_player_csrf',
            )?.value;
            expect(csrfToken, `participant ${member.participant_id} did not retain CSRF`).toBeTruthy();
            const submission = await context.request.post(
              `${backendURL}/api/v1/tournaments/${tournament.id}/participant/series/${seriesID}/games/${participant.assignment.context.game_id}/submissions`,
              {
                headers: {
                  'X-CSRF-Token': csrfToken ?? '',
                  'Idempotency-Key': randomUUID(),
                  Origin: frontendURL,
                },
                data: {
                  expected_projection_revision: participant.projection_revision,
                  submitted_flag: flag,
                },
              },
            );
            expect(submission.status(), `submission failed with ${submission.status()} ${submission.statusText()}`).toBe(200);
            seriesResult = {
              gameID: participant.assignment.context.game_id,
              seriesID,
            };
            break;
          }
          expect(seriesResult, `series ${seriesID} did not receive a submission`).not.toBeNull();
          if (seriesResult) {
            latestResult = seriesResult;
          }
        }

        const snapshot = await readOperatorSnapshotViaApi<FullStackOperatorSnapshot>(
          adminRequest,
          tournament.id,
        );
        const currentWave = snapshot.waves.find((candidate) => candidate.id === wave.id);
        expect(currentWave, `snapshot lost Wave ${wave.id}`).toBeDefined();
        if (currentWave?.state !== 'completed') {
          await waveAction(wave.id, 'complete');
        }
        return latestResult;
      };

      const prepareRound = async (
        roundNumber: number,
        knownWaveIDs: ReadonlySet<string>,
      ): Promise<FullStackWave> => {
        const pairing = await adminRequest.post(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/pairings`,
          {
            headers: {
              'X-CSRF-Token': adminAccessCSRFToken,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              categories: ['web', 'crypto', 'forensics'],
              category_mode: 'random',
              expected_projection_revision: await getOperatorProjectionRevisionViaApi(
                adminRequest,
                tournament.id,
              ),
              pairing_mode: 'automatic',
              round_number: roundNumber,
            },
          },
        );
        expect(pairing.status(), `Swiss round ${roundNumber} pairing failed with ${pairing.status()} ${pairing.statusText()}`).toBe(200);

        const snapshot = await readOperatorSnapshotViaApi<FullStackOperatorSnapshot>(
          adminRequest,
          tournament.id,
        );
        const wave = snapshot.waves.find((candidate) => !knownWaveIDs.has(candidate.id));
        expect(wave, `Swiss round ${roundNumber} did not create a Wave`).toBeDefined();
        if (!wave) {
          throw new Error(`Swiss round ${roundNumber} did not create a Wave`);
        }
        const waveProjectionRevision = snapshot.next_cursor.projection_revision;
        await waveAction(wave.id, 'open_ready_window', waveProjectionRevision);
        for (const member of wave.members) {
          const context = contextForParticipant(member.participant_id);
          const participant = await readParticipantSnapshotViaApi(context, tournament.id);
          const csrfToken = (await context.cookies()).find(
            (cookie) => cookie.name === 'tpm_player_csrf',
          )?.value;
          expect(csrfToken, `participant ${member.participant_id} did not retain CSRF`).toBeTruthy();
          const readiness = await context.request.post(
            `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${wave.id}/ready`,
            {
              headers: {
                'X-CSRF-Token': csrfToken ?? '',
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: {
                expected_projection_revision: participant.projection_revision,
                ready: true,
              },
            },
          );
          expect(readiness.status(), `round ${roundNumber} readiness failed with ${readiness.status()} ${readiness.statusText()}`).toBe(200);
        }
        await waveAction(wave.id, 'start', waveProjectionRevision);
        return wave;
      };

      const knownWaveIDs = new Set<string>();
      let correctionTarget: { gameID: string; seriesID: string } | null = null;
      for (const roundNumber of [1, 2, 3]) {
        const wave = await prepareRound(roundNumber, knownWaveIDs);
        knownWaveIDs.add(wave.id);
        const result = await settleWave(wave);
        expect(result, `Swiss round ${roundNumber} did not produce a result`).not.toBeNull();
        if (roundNumber === 3) {
          correctionTarget = result;
        }
      }
      expect(correctionTarget).not.toBeNull();
      if (!correctionTarget) {
        throw new Error('Swiss rounds did not produce a correction target');
      }

      const startPlayoffs = await applyStartPlayoffsViaApi(
        adminRequest,
        tournament.id,
        adminAccessCSRFToken,
      );
      expect(startPlayoffs.status(), `start playoffs failed with ${startPlayoffs.status()} ${startPlayoffs.statusText()}`).toBe(200);

      const operatorSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      const targetSeries = operatorSnapshot.series.find((series) => series.id === correctionTarget.seriesID);
      const targetGame = targetSeries?.slots.flatMap((slot) => slot.attempts).find(
        (attempt) => attempt.id === correctionTarget?.gameID,
      );
      expect(targetSeries, 'correction target Series missing from operator snapshot').toBeDefined();
      expect(targetGame?.result_revision_id, 'correction target has no source result revision').toBeTruthy();
      expect(operatorSnapshot.tournament.state).toBe('playoffs');
      if (!targetSeries || !targetGame?.result_revision_id) {
        throw new Error('correction target has no source result revision or Series');
      }
      const sourceProjectionRevision = operatorSnapshot.next_cursor.projection_revision;
      const sourceSeriesScore = targetSeries.score;
      const sourcePairing = [targetSeries.first_participant_id, targetSeries.second_participant_id];

      await page.goto(`/arena/operator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
      await expect(page.getByRole('heading', { name: 'Коррекция результата' })).toBeVisible();
      await page.getByLabel('Официальный результат').selectOption(
        `${correctionTarget.seriesID}:${correctionTarget.gameID}`,
      );
      await page.getByLabel('Объяснение').fill('Исправление подтверждено протоколом full-stack проверки');
      await page
        .getByLabel('Подтверждаю коррекцию результата и атомарную перестройку зависимых проекций.')
        .check();
      const preflightResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname.endsWith('/corrections/preflight') &&
          response.request().method() === 'POST',
      );
      const commitResponse = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname.endsWith('/corrections') &&
          !new URL(response.url()).pathname.endsWith('/corrections/preflight') &&
          response.request().method() === 'POST',
      );
      await page.getByRole('button', { name: 'Подтвердить коррекцию' }).click();
      const preflightHTTP = await preflightResponse;
      const commitHTTP = await commitResponse;
      expect(preflightHTTP.status(), `correction preflight failed with ${preflightHTTP.status()} ${preflightHTTP.statusText()}`).toBe(200);
      expect(commitHTTP.status(), `correction commit failed with ${commitHTTP.status()} ${commitHTTP.statusText()}`).toBe(200);
      const preflightKey = preflightHTTP.request().headers()['idempotency-key'];
      const commitKey = commitHTTP.request().headers()['idempotency-key'];
      expect(preflightKey).toBeTruthy();
      expect(commitKey).toBe(preflightKey);
      const preparedCorrection = (await preflightHTTP.json()) as Record<string, unknown>;
      expect(preparedCorrection.source_result_revision).toBe(targetGame.result_revision_id);
      expect(preparedCorrection.projection_intents).toEqual(expect.arrayContaining([expect.any(Object)]));
      expect(Array.isArray(preparedCorrection.unlock_intents)).toBe(true);
      await expect(page.getByText('Новая проекция подтверждена.')).toBeVisible();

      const correctedOperatorSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      expect(correctedOperatorSnapshot.next_cursor.projection_revision)
        .toBeGreaterThan(sourceProjectionRevision);
      const correctedSeries = correctedOperatorSnapshot.series.find(
        (series) => series.id === correctionTarget.seriesID,
      );
      const correctedGame = correctedSeries?.slots.flatMap((slot) => slot.attempts).find(
        (attempt) => attempt.id === correctionTarget?.gameID,
      );
      expect(correctedSeries, 'corrected Series missing from operator projection').toBeDefined();
      expect(correctedGame?.result_revision_id)
        .toBeTruthy();
      expect(correctedGame?.result_revision_id).not.toBe(targetGame.result_revision_id);
      expect(correctedSeries?.score).not.toEqual(sourceSeriesScore);
      expect(correctedSeries && [correctedSeries.first_participant_id, correctedSeries.second_participant_id])
        .toEqual(sourcePairing);
      expect(
        correctedOperatorSnapshot.waves.some((wave) =>
          wave.members.some((member) => member.series_id === correctionTarget?.seriesID)),
      ).toBe(true);

      const targetParticipantIDs = new Set([
        targetSeries.first_participant_id,
        targetSeries.second_participant_id,
      ]);
      const targetParticipantIDsInOrder = [
        targetSeries.first_participant_id,
        targetSeries.second_participant_id,
      ];
      const repeatedCorrectionDraft = {
        confirmed: preparedCorrection.confirmed,
        explanation: preparedCorrection.explanation,
        fields: preparedCorrection.fields,
        patch: preparedCorrection.patch,
        reason: preparedCorrection.reason,
        source_result_revision: preparedCorrection.source_result_revision,
        expected_projection_revision: correctedOperatorSnapshot.next_cursor.projection_revision,
      };
      const repeatedCorrectionResponse = await adminRequest.post(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/series/${correctionTarget.seriesID}/games/${correctionTarget.gameID}/corrections/preflight`,
        {
          headers: {
            'X-CSRF-Token': adminAccessCSRFToken,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: repeatedCorrectionDraft,
        },
      );
      expect(repeatedCorrectionResponse.status()).toBe(409);
      const repeatedCorrectionProblem = (await repeatedCorrectionResponse.json()) as {
        code?: string;
        current_revision?: number;
        expected_revision?: number;
      };
      expect(repeatedCorrectionProblem.code).toBe('stale_result');
      expect(repeatedCorrectionProblem.expected_revision)
        .toBe(correctedOperatorSnapshot.next_cursor.projection_revision);
      expect(repeatedCorrectionProblem.current_revision)
        .toBe(correctedOperatorSnapshot.next_cursor.projection_revision);
      const afterRejectedCorrectionSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      expect(afterRejectedCorrectionSnapshot).toEqual(correctedOperatorSnapshot);

      let dependentCutoffCode: 'cutoff_wave_started' | 'cutoff_golden_direct_allocated';
      let startedDependentSnapshot: CorrectionSnapshot;
      const dependentWaves = correctedOperatorSnapshot.waves.filter((wave) =>
        wave.state === 'planned' && wave.members.some((member) => targetParticipantIDs.has(member.participant_id)),
      );
      expect(
        dependentWaves.length,
        'correction did not materialize planned dependent playoff Waves for target participants',
      ).toBeGreaterThan(0);
      if (dependentWaves.length === 0) {
        throw new Error('correction did not materialize a planned dependent playoff Wave for target participants');
      }
      const dependentWaveIDs = dependentWaves.map((wave) => wave.id);
      if (correctedOperatorSnapshot.tournament.state === 'golden') {
        type CorrectionGoldenGroup = {
          attempt_id: string;
          group_id: string;
          members: Array<{ participant_id: string }>;
          ready_window_id: string;
          runtime_revision: number;
          state: string;
        };
        type CorrectionGoldenOperatorState = { groups: CorrectionGoldenGroup[] };
        type CorrectionGoldenParticipantState = {
          attempt_id: string;
          ready_window_id: string;
          runtime_revision: number;
        };

        const openGoldenResponse = await adminRequest.post(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden/open`,
          {
            headers: {
              'X-CSRF-Token': adminAccessCSRFToken,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              expected_projection_revision: await getOperatorProjectionRevisionViaApi(adminRequest, tournament.id),
              expected_runtime_revision: 0,
            },
          },
        );
        expect(openGoldenResponse.status(), `open dependent Golden failed with ${openGoldenResponse.status()} ${openGoldenResponse.statusText()}`).toBe(200);
        let goldenState = (await openGoldenResponse.json()) as CorrectionGoldenOperatorState;
        expect(goldenState.groups.length, `Golden open returned no runtime groups for ${tournament.id}`).toBeGreaterThan(0);
        const dependentGoldenGroup = goldenState.groups.find((group) =>
          group.members.some((member) => targetParticipantIDs.has(member.participant_id)),
        ) ?? goldenState.groups[0];
        expect(dependentGoldenGroup).toBeDefined();
        if (!dependentGoldenGroup) {
          throw new Error('Golden correction did not materialize a dependent group');
        }
        const openedGoldenGroup = goldenState.groups.find((group) =>
          group.group_id === dependentGoldenGroup.group_id,
        );
        if (!openedGoldenGroup) {
          throw new Error('dependent Golden group disappeared after open');
        }
        if (openedGoldenGroup.state !== 'active') {
          for (const member of openedGoldenGroup.members) {
            const context = contextForParticipant(member.participant_id);
            const participantResponse = await context.request.get(
              `${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden`,
              { headers: { Origin: frontendURL } },
            );
            expect(participantResponse.status()).toBe(200);
            const participant = (await participantResponse.json()) as CorrectionGoldenParticipantState;
            const csrfToken = (await context.cookies()).find(
              (cookie) => cookie.name === 'tpm_player_csrf',
            )?.value;
            expect(csrfToken).toBeTruthy();
            const readiness = await context.request.post(
              `${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden/ready`,
              {
                headers: {
                  'X-CSRF-Token': csrfToken ?? '',
                  'Idempotency-Key': randomUUID(),
                  Origin: frontendURL,
                },
                data: {
                  attempt_id: participant.attempt_id,
                  expected_runtime_revision: participant.runtime_revision,
                  ready: true,
                  ready_window_id: participant.ready_window_id,
                },
              },
            );
            expect(readiness.status(), `dependent Golden readiness failed with ${readiness.status()} ${readiness.statusText()}`).toBe(200);
          }
        }
        const readyGoldenResponse = await adminRequest.get(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`,
          { headers: { Origin: frontendURL } },
        );
        expect(readyGoldenResponse.status()).toBe(200);
        const readyGoldenState = (await readyGoldenResponse.json()) as CorrectionGoldenOperatorState;
        const readyGoldenGroup = readyGoldenState.groups.find(
          (group) => group.group_id === dependentGoldenGroup.group_id,
        );
        expect(['active', 'ready']).toContain(readyGoldenGroup?.state);
        if (!readyGoldenGroup) {
          throw new Error('dependent Golden group disappeared after readiness');
        }
        if (readyGoldenGroup.state !== 'active') {
          const startGoldenResponse = await adminRequest.post(
            `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden/attempts/${readyGoldenGroup.attempt_id}/start`,
            {
              headers: {
                'X-CSRF-Token': adminAccessCSRFToken,
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: {
                expected_runtime_revision: readyGoldenGroup.runtime_revision,
                ready_window_id: readyGoldenGroup.ready_window_id,
              },
            },
          );
          expect(startGoldenResponse.status(), `start dependent Golden failed with ${startGoldenResponse.status()} ${startGoldenResponse.statusText()}`).toBe(200);
        }
        dependentCutoffCode = 'cutoff_golden_direct_allocated';
      } else {
        const dependentProjectionRevision = correctedOperatorSnapshot.next_cursor.projection_revision;
        for (const dependentWave of dependentWaves) {
          await waveAction(dependentWave.id, 'open_ready_window', dependentProjectionRevision);
          for (const member of dependentWave.members) {
            const context = contextForParticipant(member.participant_id);
            const participant = await readParticipantSnapshotViaApi(context, tournament.id);
            const csrfToken = (await context.cookies()).find(
              (cookie) => cookie.name === 'tpm_player_csrf',
            )?.value;
            expect(csrfToken, `dependent Wave participant ${member.participant_id} did not retain CSRF`).toBeTruthy();
            const readiness = await context.request.post(
              `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${dependentWave.id}/ready`,
              {
                headers: {
                  'X-CSRF-Token': csrfToken ?? '',
                  'Idempotency-Key': randomUUID(),
                  Origin: frontendURL,
                },
                data: {
                  expected_projection_revision: participant.projection_revision,
                  ready: true,
                },
              },
            );
            expect(readiness.status(), `dependent Wave readiness failed with ${readiness.status()} ${readiness.statusText()}`).toBe(200);
          }
          await waveAction(dependentWave.id, 'start', dependentProjectionRevision);
        }
        dependentCutoffCode = 'cutoff_wave_started';
      }

      startedDependentSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      expect(startedDependentSnapshot.next_cursor.projection_revision)
        .toBeGreaterThanOrEqual(correctedOperatorSnapshot.next_cursor.projection_revision);

      const lateCorrectionKey = randomUUID();
      const latePreflightResponse = await adminRequest.post(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/series/${correctionTarget.seriesID}/games/${correctionTarget.gameID}/corrections/preflight`,
        {
          headers: {
            'X-CSRF-Token': adminAccessCSRFToken,
            'Idempotency-Key': lateCorrectionKey,
            Origin: frontendURL,
          },
          data: {
            ...repeatedCorrectionDraft,
            source_result_revision: correctedGame?.result_revision_id,
            expected_projection_revision: startedDependentSnapshot.next_cursor.projection_revision,
          },
        },
      );
      expect(latePreflightResponse.status()).toBe(409);
      const dependentCorrectionProblem = (await latePreflightResponse.json()) as {
        code?: string;
        current_revision?: number;
        expected_revision?: number;
      };
      expect(dependentCorrectionProblem.code).toBe(dependentCutoffCode);
      expect(dependentCorrectionProblem.expected_revision)
        .toBe(startedDependentSnapshot.next_cursor.projection_revision);
      expect(dependentCorrectionProblem.current_revision)
        .toBe(startedDependentSnapshot.next_cursor.projection_revision);
      const afterDependentCorrectionSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      expect(afterDependentCorrectionSnapshot).toEqual(startedDependentSnapshot);

      const expectedDependentWaves = dependentWaveIDs.map((dependentWaveID) => {
        const expectedDependentWave = startedDependentSnapshot.waves.find(
          (wave) => wave.id === dependentWaveID,
        );
        expect(
          expectedDependentWave,
          `started snapshot is missing chosen dependent playoff Wave ${dependentWaveID}`,
        ).toBeDefined();
        if (!expectedDependentWave) {
          throw new Error(`started snapshot is missing chosen dependent playoff Wave ${dependentWaveID}`);
        }
        return expectedDependentWave;
      });
      const expectedParticipantSeries = new Map<string, string>();
      const targetParticipantWaveIDs = new Map<string, string>();
      for (const expectedDependentWave of expectedDependentWaves) {
        for (const member of expectedDependentWave.members) {
          if (!targetParticipantIDs.has(member.participant_id)) {
            continue;
          }
          const previousWaveID = targetParticipantWaveIDs.get(member.participant_id);
          if (previousWaveID) {
            throw new Error(
              `target participant ${member.participant_id} appears in dependent playoff Waves ${previousWaveID} and ${expectedDependentWave.id}`,
            );
          }
          targetParticipantWaveIDs.set(member.participant_id, expectedDependentWave.id);
          if (typeof member.series_id !== 'string') {
            throw new Error(
              `chosen dependent playoff Wave ${expectedDependentWave.id} returned a non-string series for target participant ${member.participant_id}`,
            );
          }
          if (member.series_id === correctionTarget.seriesID) {
            throw new Error(
              `chosen dependent playoff Wave ${expectedDependentWave.id} returned the old series ${correctionTarget.seriesID} for target participant ${member.participant_id}`,
            );
          }
          expectedParticipantSeries.set(member.participant_id, member.series_id);
        }
      }
      for (const participantID of targetParticipantIDsInOrder) {
        if (!targetParticipantWaveIDs.has(participantID)) {
          throw new Error(
            `chosen dependent playoff Waves ${dependentWaveIDs.join(', ')} are missing target participant ${participantID}`,
          );
        }
        expect(expectedParticipantSeries.get(participantID), `missing dependent series for participant ${participantID}`).toEqual(expect.any(String));
      }

      const correctedParticipantSnapshots = await Promise.all(
        targetParticipantIDsInOrder.map((participantID) => {
          const expectedSeriesID = expectedParticipantSeries.get(participantID);
          expect(expectedSeriesID, `missing dependent series for participant ${participantID}`).toEqual(
            expect.any(String),
          );
          if (!expectedSeriesID) {
            throw new Error(`missing dependent series for participant ${participantID}`);
          }
          return readParticipantSnapshotUntilSeries(
            contextForParticipant(participantID),
            tournament.id,
            participantID,
            startedDependentSnapshot.next_cursor.projection_revision,
            expectedSeriesID,
          );
        }),
      );
      for (const [index, participantSnapshot] of correctedParticipantSnapshots.entries()) {
        const participantID = targetParticipantIDsInOrder[index];
        expect(participantSnapshot.projection_revision)
          .toBeGreaterThanOrEqual(startedDependentSnapshot.next_cursor.projection_revision);
        expect(participantSnapshot.series).not.toBeNull();
        expect(participantSnapshot.series?.id)
          .toBe(expectedParticipantSeries.get(participantID));
        if (dependentCutoffCode === 'cutoff_wave_started') {
          expect(
            participantSnapshot.assignment,
            `participant ${participantID} did not receive an assignment for started normal Wave`,
          ).not.toBeNull();
          expect(participantSnapshot.assignment?.context.series_id)
            .toBe(participantSnapshot.series?.id);
        } else {
          expect(
            participantSnapshot.assignment,
            `participant ${participantID} must not receive an assignment while the dependent playoff Wave is planned in the Golden branch`,
          ).toBeNull();
        }
        expect(participantSnapshot.series?.slots.length).toBeGreaterThan(0);
      }

      const correctionTaskTitles = [...normalFlagsByTitle.keys()];
      const cancellationReason = `FE-050 cancellation ${uniqueName('audit-reason')}`;
      const privateCanaries = [
        ...correctionTaskTitles,
        ...normalFlagsByTitle.values(),
        privateTaskURL,
        'correction hint one',
        'correction hint two',
        'correction hint three',
        cancellationReason,
      ];
      publicContext = await browser.newContext({ baseURL: frontendURL });
      const publicFrames: string[] = [];
      const publicRequests: Array<{ headers: Record<string, string>; url: string }> = [];
      spectatorPage = await publicContext.newPage();
      spectatorPage.on('request', (requestEvent) => {
        if (requestEvent.url().includes(`/api/v1/tournaments/${tournament.id}/`)) {
          publicRequests.push({ headers: requestEvent.headers(), url: requestEvent.url() });
        }
      });
      spectatorPage.on('websocket', (socket) => {
        if (!new URL(socket.url()).pathname.endsWith(`/api/v1/tournaments/${tournament.id}/realtime`)) {
          return;
        }
        socket.on('framereceived', (frame) => {
          publicFrames.push(
            typeof frame.payload === 'string' ? frame.payload : frame.payload.toString('utf8'),
          );
        });
      });
      await spectatorPage.goto(`/arena/spectator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
      const correctedPublicSnapshot = await readPublicSnapshotViaApi(publicContext, tournament.id);
      expect(correctedPublicSnapshot.next_cursor.projection_revision)
        .toBeGreaterThanOrEqual(correctedOperatorSnapshot.next_cursor.projection_revision);
      const correctedPublicResult = correctedPublicSnapshot.official_results.find(
        (result) => result.series_id === correctionTarget.seriesID,
      );
      expect(correctedPublicResult).toBeDefined();
      expect(correctedPublicResult?.score).toEqual({
        first_wins: correctedSeries?.score.first_participant_wins,
        second_wins: correctedSeries?.score.second_participant_wins,
      });
      assertPublicProjectionRedacted(correctedPublicSnapshot);
      const assertPublicWireRedacted = (): void => {
        for (const frame of publicFrames) {
          let payload: unknown;
          try {
            payload = JSON.parse(frame) as unknown;
          } catch {
            continue;
          }
          assertPublicProjectionRedacted(payload);
        }
        const publicWire = publicFrames.join('\\n');
        for (const privateCanary of privateCanaries) {
          expect(publicWire).not.toContain(privateCanary);
        }
      };
      for (const privateCanary of privateCanaries) {
        expect(JSON.stringify(correctedPublicSnapshot)).not.toContain(privateCanary);
      }
      await expect.poll(() => publicFrames.length).toBeGreaterThan(0);
      assertPublicWireRedacted();

      await page.goto('/admin', { waitUntil: 'domcontentloaded' });
      await expect(page.getByRole('button', { name: 'Соревнования' })).toBeVisible();
      await page.getByRole('button', { name: 'Соревнования' }).click();
      const tournamentRow = page.getByRole('row').filter({ hasText: tournamentName });
      await expect(tournamentRow).toBeVisible();
      const auditResponsePromise = page.waitForResponse(
        (response) => {
          const url = new URL(response.url());
          return url.pathname === '/api/v1/admin/tournament-audit' &&
            url.searchParams.get('tournament_id') === tournament.id;
        },
      );
      await tournamentRow.getByRole('button', { name: 'Открыть', exact: true }).click();
      await page
        .getByRole('navigation', { name: 'Разделы соревнования' })
        .getByRole('button', { name: 'Журнал', exact: true })
        .click();
      const auditResponse = await auditResponsePromise;
      expect(
        auditResponse.status(),
        `audit list failed with ${auditResponse.status()} ${auditResponse.statusText()}`,
      ).toBe(200);
      const auditRegion = page.getByRole('region', { name: 'Аудит и incident bundle' });
      await expect(auditRegion.getByRole('list', { name: 'События аудита' })).toBeVisible();
      await expect(auditRegion.getByText('Текущая revision').first()).toBeVisible();
      await expect(auditRegion.getByText('Заменена').first()).toBeVisible();
      await expect(auditRegion.getByText(targetGame.result_revision_id, { exact: true }).first())
        .toBeVisible();

      const currentAdminAccessCSRFToken = (await page.context().cookies()).find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      )?.value ?? adminAccessCSRFToken;
      const revisionBeforeCancellation = await getOperatorProjectionRevisionViaApi(
        adminRequest,
        tournament.id,
      );
      const cancellationResponse = await adminRequest.post(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/actions`,
        {
          headers: {
            'X-CSRF-Token': currentAdminAccessCSRFToken,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            action: 'cancel',
            confirmed: true,
            expected_projection_revision: revisionBeforeCancellation,
            reason: cancellationReason,
          },
        },
      );
      const cancellationStatus = cancellationResponse.status();
      let cancellationFailure = `tournament cancellation failed with status=${cancellationStatus} statusText=${cancellationResponse.statusText()}`;
      if (cancellationStatus !== 200) {
        let cancellationBody = '<unavailable>';
        try {
          cancellationBody =
            (await cancellationResponse.text())
              .replace(/[\u0000-\u001f\u007f]/g, ' ')
              .replace(/\s+/g, ' ')
              .trim()
              .slice(0, 512) || '<empty>';
        } catch {
          // Keep the bounded diagnostic if the failed response body is unavailable.
        }
        cancellationFailure += ` body=${JSON.stringify(cancellationBody)}`;
      }
      expect(cancellationStatus, cancellationFailure).toBe(200);
      const cancelledTournament = (await cancellationResponse.json()) as { revision: number; state: string };
      expect(cancelledTournament.state).toBe('cancelled');
      expect(cancelledTournament.revision).toBeGreaterThan(0);

      const cancelledProjectionRevision = await getOperatorProjectionRevisionViaApi(
        adminRequest,
        tournament.id,
      );
      expect(cancelledProjectionRevision).toBeGreaterThanOrEqual(revisionBeforeCancellation);
      const cancelledOperatorSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      expect(cancelledOperatorSnapshot.tournament.state).toBe('cancelled');
      const blockedCommand = await adminRequest.post(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/actions`,
        {
          headers: {
            'X-CSRF-Token': currentAdminAccessCSRFToken,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            action: 'pause',
            confirmed: true,
            expected_projection_revision: cancelledProjectionRevision,
            reason: 'FE-050 command after cancellation must be rejected',
          },
        },
      );
      expect(blockedCommand.status()).toBe(409);
      const blockedProblem = (await blockedCommand.json()) as {
        current_revision?: number;
        current_state?: string;
        expected_revision?: number;
      };
      expect(blockedProblem).toMatchObject({
        current_revision: cancelledProjectionRevision,
        current_state: 'cancelled',
        expected_revision: cancelledProjectionRevision,
      });
      const afterBlockedCommandSnapshot = await readOperatorSnapshotViaApi<CorrectionSnapshot>(
        adminRequest,
        tournament.id,
      );
      expect(afterBlockedCommandSnapshot).toEqual(cancelledOperatorSnapshot);

      const cancelledAuditResponse = await adminRequest.get(
        `${backendURL}/api/v1/admin/tournament-audit?tournament_id=${tournament.id}`,
        { headers: { Origin: frontendURL } },
      );
      expect(cancelledAuditResponse.status()).toBe(200);
      const cancelledAudit = (await cancelledAuditResponse.json()) as {
        events: unknown[];
        cancellation?: {
          reason: string;
          resulting_revision: number;
          source_projection_revision: number;
          source_projection_revision_id: string;
          source_revision: number;
        };
      };
      expect(cancelledAudit.events.length).toBeGreaterThan(0);
      expect(cancelledAudit.cancellation).toMatchObject({
        reason: cancellationReason,
        source_revision: cancelledTournament.revision - 1,
        resulting_revision: cancelledTournament.revision,
        source_projection_revision: revisionBeforeCancellation,
      });
      expect(cancelledAudit.cancellation?.source_projection_revision_id).toMatch(
        /^[0-9a-f-]{36}$/,
      );

      const participantPage = await playerContexts[0]!.newPage();
      await participantPage.goto(`/arena/participant/${tournament.id}`, { waitUntil: 'domcontentloaded' });
      await expect(participantPage.getByTestId('participant-player-panel')).toBeVisible();
      await expect(participantPage.getByTestId('participant-player-panel')).toContainText('Отменено');
      await expect(participantPage.getByTestId('participant-submit-button')).toHaveCount(0);

      if (!publicContext || !spectatorPage) {
        throw new Error('public context was not initialized');
      }
      const broadcast = spectatorPage.getByTestId('tournament-broadcast');
      await spectatorPage.reload({ waitUntil: 'domcontentloaded' });
      await expect(broadcast.getByTestId('broadcast-phase-title')).toHaveText('Соревнование отменено');
      const publicSnapshot = await readPublicSnapshotViaApi(publicContext, tournament.id);
      const publicSerialized = JSON.stringify(publicSnapshot);
      assertPublicProjectionRedacted(publicSnapshot);
      for (const privateCanary of privateCanaries) {
        expect(publicSerialized).not.toContain(privateCanary);
      }
      await expect.poll(() => publicFrames.length).toBeGreaterThan(0);
      assertPublicWireRedacted();
      for (const publicRequest of publicRequests) {
        expect(publicRequest.headers.authorization).toBeUndefined();
        expect(publicRequest.headers['x-csrf-token']).toBeUndefined();
        expect(publicRequest.headers.cookie ?? '').not.toContain('tpm_');
      }
      await participantPage.close();

      const incidentResponsePromise = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/admin/tournaments/${tournament.id}/incident-export`,
      );
      const incidentDownloadPromise = page.waitForEvent('download');
      await auditRegion.getByRole('button', { name: 'Скачать incident bundle' }).click();
      const incidentResponse = await incidentResponsePromise;
      expect(
        incidentResponse.status(),
        `incident export failed with ${incidentResponse.status()} ${incidentResponse.statusText()}`,
      ).toBe(200);
      const incidentDownload = await incidentDownloadPromise;
      const incidentPath = await incidentDownload.path();
      expect(incidentPath, 'incident download did not produce a file').toBeTruthy();
      const incidentBundle = JSON.parse(
        await readFile(incidentPath ?? '', 'utf8'),
      ) as {
        canonical_content: string;
        generated_at: string;
        projection_revision: number;
        sha256: string;
        tournament_id: string;
      };
      expect(incidentBundle.tournament_id).toBe(tournament.id);
      expect(incidentBundle.projection_revision).toBeGreaterThan(0);
      expect(Date.parse(incidentBundle.generated_at)).not.toBeNaN();
      expect(incidentBundle.sha256).toMatch(/^[0-9a-f]{64}$/);
      expect(incidentBundle.canonical_content).toBeTruthy();
      const incidentCanonicalContent = Buffer.from(incidentBundle.canonical_content, 'base64').toString('utf8');
      expect(incidentCanonicalContent).toContain(cancellationReason);
      expect(incidentCanonicalContent).toContain(`"source_revision":${cancelledTournament.revision - 1}`);
      expect(incidentCanonicalContent).toContain(`"resulting_revision":${cancelledTournament.revision}`);
      for (const privateCanary of privateCanaries.filter((canary) => canary !== cancellationReason)) {
        expect(incidentCanonicalContent).not.toContain(privateCanary);
      }
      await expect(page.getByText(incidentBundle.canonical_content, { exact: false })).toHaveCount(0);

      const playerIncident = await playerContexts[0]?.request.get(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/incident-export`,
        { headers: { Origin: frontendURL } },
      );
      expect(playerIncident, 'player context was not available for authorization check').toBeDefined();
      expect([401, 403]).toContain(playerIncident?.status());

      const anonymousContext = await browser.newContext({ baseURL: frontendURL });
      try {
        const anonymousIncident = await anonymousContext.request.get(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/incident-export`,
          { headers: { Origin: frontendURL } },
        );
        expect([401, 403]).toContain(anonymousIncident.status());
      } finally {
        ownedFixture.contexts.push(anonymousContext);
      }
    } finally {
      ownedFixture.contexts.push(...playerContexts);
      if (publicContext) {
        ownedFixture.contexts.push(publicContext);
      }
    }
  });

  test('FE-049 real backend records no-solve without a draw or stale readiness', async ({ browser, request, page, ownedFixture }) => {
    test.setTimeout(600_000);
    page.setDefaultTimeout(15_000);

    type ReplaySnapshot = FullStackOperatorSnapshot & {
      recovery_controls: Array<{
        assignment_id: string;
        attempts: Array<{
          attempt_no: number;
          id: string;
          result_reason: string | null;
          state: string;
        }>;
        expected_authority_revision: number;
        kind: string;
        old_wave_id: string;
        replay: { available: boolean; expected_closure_revision_id: string } | null;
        series_id: string;
        slot_id: string;
      }>;
      series: Array<{
        id: string;
        slots: Array<{ attempts: Array<{ id: string; result_reason: string | null; state: string }>; id: string }>;
        state: string;
      }>;
      tournament: { id: string; state: string };
    };

    const tournamentName = uniqueName('fe049-replay');
    const tournamentPublicID = uniqueName('fe049-public');
    const playerContexts: BrowserContext[] = [];

    try {
      await loginThroughAdminUI(page);
      const adminSession = await adminLogin(request);
      const normalTaskGroups: Array<{ category: FullStackTaskInput['category']; count: number }> = [
        { category: 'web', count: 27 },
        { category: 'crypto', count: 27 },
        { category: 'reverse', count: 27 },
        { category: 'forensics', count: 3 },
        { category: 'pwn', count: 3 },
      ];
      for (const group of normalTaskGroups) {
        for (let index = 0; index < group.count; index += 1) {
          const title = uniqueName(`fe049-${group.category}-${index + 1}`);
          await createTaskViaApi(ownedFixture, request, adminSession, {
            title,
            description: 'Normal task for the FE-049 no-solve replay flow.',
            kind: 'normal',
            category: group.category,
            difficulty: 'easy',
            time_limit: 180,
            flag: `flag{${title.replaceAll('-', '_')}}`,
            hints: ['FE-049 hint one', 'FE-049 hint two', 'FE-049 hint three'],
            task_url: 'https://example.com/fe049-normal',
          });
        }
      }
      for (let index = 0; index < 6; index += 1) {
        const title = uniqueName(`fe049-golden-${index + 1}`);
        await createTaskViaApi(ownedFixture, request, adminSession, {
          title,
          description: 'Golden task reserved for the FE-049 tournament preset.',
          kind: 'golden',
          category: 'web',
          difficulty: 'easy',
          time_limit: 180,
          flag: `flag{${title.replaceAll('-', '_')}}`,
          hints: ['FE-049 Golden hint one', 'FE-049 Golden hint two', 'FE-049 Golden hint three'],
          task_url: 'https://example.com/fe049-golden',
        });
      }

      const tournament = await createTournamentViaApi(ownedFixture, request, adminSession, {
        name: tournamentName,
        content_revision: await getTournamentContentRevision(request),
        planned_roster_size: 4,
        public_id: tournamentPublicID,
        preset: 'tournament_v1',
        expected_revision: 0,
      });
      const players: FullStackPlayer[] = [];
      for (let index = 0; index < 4; index += 1) {
        const context = await browser.newContext({ baseURL: frontendURL });
        playerContexts.push(context);
        const playerPage = await context.newPage();
        const username = uniqueName(`fe049-player-${index + 1}`);
        await joinAsPlayer(playerPage, username);
        const meResponse = await context.request.get(`${backendURL}/api/v1/players/me`, {
          headers: { Origin: frontendURL },
        });
        expect(meResponse.status()).toBe(200);
        players.push(((await meResponse.json()) as { player: FullStackPlayer }).player);
        await playerPage.close();
      }

      const roster = await getRosterViaApi(request, tournament.id);
      const rosterInputs = players.map((player, index) => ({
        attendance: 'invited' as const,
        player_id: player.id,
        seed: index + 1,
      }));
      const invited = await replaceRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        await getOperatorProjectionRevisionViaApi(request, tournament.id),
        rosterInputs,
      );
      expect((await readRosterResponse(invited, tournament.id, roster.id)).participants).toHaveLength(4);
      expect((await applyOpenRegistrationViaApi(request, tournament.id, adminSession.access_csrf_token)).status()).toBe(200);
      const checkedIn = await replaceRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        await getOperatorProjectionRevisionViaApi(request, tournament.id),
        rosterInputs.map((participant) => ({ ...participant, attendance: 'checked_in' as const })),
      );
      const checkedInRoster = await readRosterResponse(checkedIn, tournament.id, roster.id);
      const checkedInParticipants = sortRosterParticipantsBySeed(checkedInRoster.participants);
      const preflight = await runRosterPreflightViaApi(request, tournament.id, adminSession.access_csrf_token);
      expect(preflight.status()).toBe(200);
      const preflightReport = (await preflight.json()) as FullStackPreflightReport;
      expect(preflightReport.passed, JSON.stringify(preflightReport.checks ?? [])).toBe(true);
      expect((await lockRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        preflightReport.id,
        checkedInParticipants.map((participant) => participant.player_id),
      )).status()).toBe(200);
      expect((await applyStartSwissViaApi(request, tournament.id, adminSession.access_csrf_token)).status()).toBe(200);

      const operatorAction = async (
        waveID: string,
        action: 'open_ready_window' | 'start' | 'complete',
        knownProjectionRevision?: number,
      ) => {
        const response = await request.post(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/waves/${waveID}/actions`,
          {
            headers: {
              'X-CSRF-Token': adminSession.access_csrf_token,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              action,
              confirmed: true,
              expected_projection_revision: knownProjectionRevision ?? await getOperatorProjectionRevisionViaApi(
                request,
                tournament.id,
              ),
            },
          },
        );
        expect(response.status(), `${action} failed with ${response.status()}`).toBe(200);
        return (await response.json()) as FullStackWave;
      };
      const pairing = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/pairings`, {
        headers: {
          'X-CSRF-Token': adminSession.access_csrf_token,
          'Idempotency-Key': randomUUID(),
          Origin: frontendURL,
        },
        data: {
          categories: ['web', 'crypto', 'forensics'],
          category_mode: 'random',
          expected_projection_revision: await getOperatorProjectionRevisionViaApi(request, tournament.id),
          pairing_mode: 'automatic',
          round_number: 1,
        },
      });
      expect(pairing.status()).toBe(200);
      const operatorBeforeReady = await readOperatorSnapshotViaApi<ReplaySnapshot>(
        request,
        tournament.id,
      );
      const wave = operatorBeforeReady.waves.at(-1);
      expect(wave).toBeDefined();
      if (!wave) throw new Error('FE-049 did not materialize a Swiss wave');
      const waveProjectionRevision = operatorBeforeReady.next_cursor.projection_revision;
      await operatorAction(wave.id, 'open_ready_window', waveProjectionRevision);

      const contextForParticipant = (participantID: string): BrowserContext => {
        const rosterParticipant = checkedInParticipants.find((participant) => participant.id === participantID);
        expect(rosterParticipant).toBeDefined();
        if (!rosterParticipant) throw new Error(`missing participant ${participantID}`);
        const playerIndex = players.findIndex((player) => player.id === rosterParticipant.player_id);
        const context = playerContexts[playerIndex];
        if (!context) throw new Error(`missing context for ${rosterParticipant.player_id}`);
        return context;
      };
      for (const member of wave.members) {
        const context = contextForParticipant(member.participant_id);
        const snapshot = await readParticipantSnapshotViaApi(context, tournament.id);
        const csrfToken = (await context.cookies()).find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
        expect(csrfToken).toBeTruthy();
        const ready = await context.request.post(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${wave.id}/ready`,
          {
            headers: { 'X-CSRF-Token': csrfToken ?? '', 'Idempotency-Key': randomUUID(), Origin: frontendURL },
            data: { expected_projection_revision: snapshot.projection_revision, ready: true },
          },
        );
        expect(ready.status()).toBe(200);
      }
      await operatorAction(wave.id, 'start', waveProjectionRevision);
      const waveSeriesIDs = [...new Set(wave.members.map((member) => member.series_id).filter((id): id is string => id !== null && id !== undefined))];
      expect(waveSeriesIDs.length).toBeGreaterThan(0);
      const targetSeriesID = waveSeriesIDs[0];
      if (!targetSeriesID) throw new Error('FE-049 target Series was not created');
      const targetMember = wave.members.find((member) => member.series_id === targetSeriesID);
      expect(targetMember).toBeDefined();
      if (!targetMember) throw new Error('FE-049 target Series has no member');
      const targetParticipant = await readParticipantSnapshotViaApi(
        contextForParticipant(targetMember.participant_id),
        tournament.id,
      );
      expect(targetParticipant.assignment?.context.effective_deadline).toBeTruthy();
      expect(targetParticipant.assignment?.active_snapshot.time_limit).toBe(180);
      if (!targetParticipant.assignment?.context.started_at || !targetParticipant.assignment.context.effective_deadline) {
        throw new Error('FE-049 target game did not expose an authoritative deadline');
      }
      expect(
        Date.parse(targetParticipant.assignment.context.effective_deadline) -
          Date.parse(targetParticipant.assignment.context.started_at),
      ).toBe(180_000);
      const oldGameID = targetParticipant.assignment.context.game_id;

      const participantPage = await contextForParticipant(targetMember.participant_id).newPage();
      await participantPage.goto(`/arena/participant/${tournament.id}`, { waitUntil: 'domcontentloaded' });
      await expect(participantPage.getByTestId('participant-player-panel')).toBeVisible();
      await expect(participantPage.getByTestId('participant-submit-button')).toBeEnabled();

      let terminalSnapshot: ReplaySnapshot | null = null;
      await expect.poll(async () => {
        const response = await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/snapshot`, {
          headers: { Origin: frontendURL },
        });
        if (response.status() !== 200) return false;
        const current = (await response.json()) as ReplaySnapshot;
        const targetSeries = current.series.find((series) => series.id === targetSeriesID);
        const targetAttempt = targetSeries?.slots.flatMap((slot) => slot.attempts).find((attempt) => attempt.id === oldGameID);
        if (targetSeries?.state === 'replay_required' && targetAttempt?.state === 'void' && targetAttempt.result_reason === 'no_solve') {
          terminalSnapshot = current;
          return true;
        }
        return false;
      }, { timeout: 240_000, intervals: [1_000, 2_000, 5_000] }).toBe(true);
      const resolvedTerminalSnapshot = terminalSnapshot as ReplaySnapshot | null;
      expect(resolvedTerminalSnapshot).not.toBeNull();
      if (!resolvedTerminalSnapshot) throw new Error('FE-049 no-solve terminal state was not observed');
      const terminalSeries = resolvedTerminalSnapshot.series.find((series) => series.id === targetSeriesID);
      const terminalAttempts = terminalSeries?.slots.flatMap((slot) => slot.attempts) ?? [];
      expect(terminalAttempts).toHaveLength(1);
      expect(terminalAttempts[0]).toEqual(expect.objectContaining({ id: oldGameID, state: 'void', result_reason: 'no_solve' }));
      expect(resolvedTerminalSnapshot.recovery_controls).toEqual([]);
      expect(resolvedTerminalSnapshot.tournament.state).toBe('swiss');

      const participantAfterTimeout = await readParticipantSnapshotViaApi(
        contextForParticipant(targetMember.participant_id),
        tournament.id,
      );
      expect(participantAfterTimeout.assignment?.context.game_id).toBe(oldGameID);
      expect(participantAfterTimeout.assignment?.context.game_state).toBe('void');
      expect(participantAfterTimeout.assignment?.context.series_score).toEqual({
        first_participant_wins: 0,
        second_participant_wins: 0,
      });
      expect(participantAfterTimeout.assignment?.active_snapshot).toEqual(
        expect.objectContaining({ task_id: expect.any(String), version: expect.any(Number) }),
      );
      expect(participantAfterTimeout.series).toEqual(expect.objectContaining({ id: targetSeriesID, state: 'replay_required' }));

      const spectatorContext = await browser.newContext({ baseURL: frontendURL });
      try {
        const spectatorPage = await spectatorContext.newPage();
        await participantPage.reload({ waitUntil: 'domcontentloaded' });
        await expect(participantPage.getByTestId('participant-runtime-status')).toBeVisible();
        await expect(participantPage.getByTestId('participant-runtime-status'))
          .toHaveAttribute('data-game-state', 'void');
        await expect(participantPage.getByText(/Подсказки/i)).toHaveCount(0);
        await expect(participantPage.getByTestId('participant-submit-button')).toBeDisabled();
        await page.goto(`/arena/operator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
        await expect(page.getByText('Снимок подтвержден', { exact: true })).toBeVisible();
        await page.reload({ waitUntil: 'domcontentloaded' });
        await expect(page.getByText('Снимок подтвержден', { exact: true })).toBeVisible();
        await spectatorPage.goto(`/arena/spectator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
        await expect(spectatorPage.getByTestId('tournament-broadcast')).toBeVisible();
        const publicSnapshot = await readPublicSnapshotViaApi(spectatorContext, tournament.id);
        const publicSeries = publicSnapshot.live_series.find((series) => series.series_id === targetSeriesID);
        expect(publicSeries?.state).toBe('replay_required');
      } finally {
        ownedFixture.contexts.push(spectatorContext);
      }
    } finally {
      ownedFixture.contexts.push(...playerContexts);
    }
  });

  (['solves', 'deadline'] as const).forEach((completion) => test(`FE-048 real backend resolves boundary Golden no-show before playoff (${completion})`, async ({ browser, request, page, ownedFixture }) => {
    test.setTimeout(900_000);

    type GoldenGroup = {
      attempt_id: string;
      deadline: string | null;
      group_id: string;
      group_revision_id: string;
      members: Array<{ participant_id: string; position: number | null; ready: boolean; submitted: boolean }>;
      position_from: number;
      position_to: number;
      ready_window_id: string;
      runtime_revision: number;
      started_at: string | null;
      state: string;
    };
    type GoldenOperatorState = { groups: GoldenGroup[]; observed_at: string };
    type GoldenParticipantState = {
      attempt_id: string;
      group_id: string;
      ready_window_id: string;
      runtime_revision: number;
      state: string;
      submitted: boolean;
      position: number | null;
      task: {
        assignment_id: string;
        category: FullStackTaskInput['category'];
        description: string;
        source_file_available: boolean;
        snapshot_id: string;
        task_id: string;
        task_url: string | null;
        time_limit_seconds: 180;
        title: string;
        version: number;
      } | null;
    };

    const tournamentName = uniqueName('fe048-golden');
    const tournamentPublicID = uniqueName('fe048-public');
    const playerNames = Array.from({ length: 6 }, (_, index) => uniqueName(`fe048-player-${index + 1}`));
    const playerContexts: BrowserContext[] = [];

    try {
      await loginThroughAdminUI(page);
      const adminSession = await adminLogin(request);
      const normalFlagsByTitle = new Map<string, string>();
      const goldenFlagsByTitle = new Map<string, string>();
      const normalTaskGroups: Array<{
        category: FullStackTaskInput['category'];
        count: number;
      }> = [
        { category: 'web', count: 27 },
        { category: 'crypto', count: 27 },
        { category: 'reverse', count: 27 },
        { category: 'forensics', count: 3 },
        { category: 'pwn', count: 3 },
      ];

      for (const group of normalTaskGroups) {
        for (let index = 0; index < group.count; index += 1) {
          const title = uniqueName(`fe048-${group.category}-${index + 1}`);
          const flag = `flag{${title.replaceAll('-', '_')}}`;
          normalFlagsByTitle.set(title, flag);
          await createTaskViaApi(ownedFixture, request, adminSession, {
            title,
            description: 'Normal task for the FE-048 Golden boundary flow.',
            kind: 'normal',
            category: group.category,
            difficulty: 'easy',
            time_limit: 180,
            flag,
            hints: ['FE-048 normal hint one', 'FE-048 normal hint two', 'FE-048 normal hint three'],
            task_url: 'https://example.com/fe048-normal',
          });
        }
      }
      for (let index = 0; index < 6; index += 1) {
        const title = uniqueName(`fe048-golden-${index + 1}`);
        const flag = `flag{${title.replaceAll('-', '_')}}`;
        goldenFlagsByTitle.set(title, flag);
        const createdGoldenTask = await createTaskViaApi(ownedFixture, request, adminSession, {
          title,
          description: 'Golden task for the FE-048 boundary flow.',
          kind: 'golden',
          category: 'web',
          difficulty: 'easy',
          time_limit: 180,
          flag,
          hints: ['FE-048 golden hint one', 'FE-048 golden hint two', 'FE-048 golden hint three'],
          task_url: 'https://example.com/fe048-golden',
        });
        const uploadedGoldenSource = await uploadSourceViaApi(
          request,
          adminSession,
          createdGoldenTask.id,
          Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x66, 0x65, 0x30, 0x34, 0x38, index]),
        );
        expect(uploadedGoldenSource.source_file_url).toMatch(/^https?:\/\//);
      }

      const tournament = await createTournamentViaApi(
        ownedFixture,
        request,
        adminSession,
        {
          name: tournamentName,
          content_revision: await getTournamentContentRevision(request),
          planned_roster_size: 6,
          public_id: tournamentPublicID,
          preset: 'tournament_v1',
          expected_revision: 0,
        },
      );
      const players: FullStackPlayer[] = [];
      for (const username of playerNames) {
        const context = await browser.newContext({ baseURL: frontendURL });
        playerContexts.push(context);
        const playerPage = await context.newPage();
        await joinAsPlayer(playerPage, username);
        const meResponse = await context.request.get(`${backendURL}/api/v1/players/me`, {
          headers: { Origin: frontendURL },
        });
        expect(meResponse.status()).toBe(200);
        const me = (await meResponse.json()) as { player: FullStackPlayer };
        expect(me.player.username).toBe(username);
        players.push(me.player);
        await playerPage.close();
      }

      const roster = await getRosterViaApi(request, tournament.id);
      const rosterInputs = players.map((player, index) => ({
        attendance: 'invited' as const,
        player_id: player.id,
        seed: index + 1,
      }));
      const invitedResponse = await replaceRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        await getOperatorProjectionRevisionViaApi(request, tournament.id),
        rosterInputs,
      );
      expect((await readRosterResponse(invitedResponse, tournament.id, roster.id)).participants).toHaveLength(6);
      expect((await applyOpenRegistrationViaApi(request, tournament.id, adminSession.access_csrf_token)).status()).toBe(200);
      const checkedInResponse = await replaceRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        await getOperatorProjectionRevisionViaApi(request, tournament.id),
        rosterInputs.map((participant) => ({ ...participant, attendance: 'checked_in' as const })),
      );
      const checkedInRoster = await readRosterResponse(checkedInResponse, tournament.id, roster.id);
      const checkedInParticipants = sortRosterParticipantsBySeed(checkedInRoster.participants);
      expect(checkedInParticipants).toHaveLength(6);

      const preflightResponse = await runRosterPreflightViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
      );
      expect(preflightResponse.status()).toBe(200);
      const preflight = (await preflightResponse.json()) as FullStackPreflightReport;
      expect(preflight.passed, JSON.stringify(preflight.checks ?? [])).toBe(true);
      const lockedResponse = await lockRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        preflight.id,
        checkedInParticipants.map((participant) => participant.player_id),
      );
      expect(lockedResponse.status()).toBe(200);
      let startSwissResponse: APIResponse | null = null;
      for (let attempt = 0; attempt < 60; attempt += 1) {
        startSwissResponse = await applyStartSwissViaApi(request, tournament.id, adminSession.access_csrf_token);
        if (startSwissResponse.status() === 200) break;
        const status = startSwissResponse.status();
        expect([409, 429], `start Swiss returned unexpected status ${status}`).toContain(status);
        await waitForTransientRetry(attempt);
      }
      if (!startSwissResponse) throw new Error('start Swiss did not issue a request');
      expect(startSwissResponse.status(), `start Swiss failed with ${startSwissResponse.status()}`).toBe(200);

      const contextForParticipant = (participantID: string): BrowserContext => {
        const rosterParticipant = checkedInParticipants.find((participant) => participant.id === participantID);
        expect(rosterParticipant, `missing roster participant ${participantID}`).toBeDefined();
        if (!rosterParticipant) throw new Error(`missing roster participant ${participantID}`);
        const playerIndex = players.findIndex((player) => player.id === rosterParticipant.player_id);
        const context = playerContexts[playerIndex];
        if (!context) throw new Error(`missing browser context for ${rosterParticipant.player_id}`);
        return context;
      };

      const waveAction = async (
        waveID: string,
        action: 'open_ready_window' | 'start' | 'complete',
        knownProjectionRevision?: number,
      ) => {
        let expectedRevision = knownProjectionRevision ?? await getOperatorProjectionRevisionViaApi(
          request,
          tournament.id,
        );
        let response: APIResponse | null = null;
        for (let attempt = 0; attempt < 60; attempt += 1) {
          response = await request.post(
            `${backendURL}/api/v1/admin/tournaments/${tournament.id}/waves/${waveID}/actions`,
            {
              headers: {
                'X-CSRF-Token': adminSession.access_csrf_token,
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: { action, confirmed: true, expected_projection_revision: expectedRevision },
            },
          );
          if (response.status() === 200) return (await response.json()) as FullStackWave;
          const status = response.status();
          expect([409, 429], `${action} Swiss Wave returned unexpected status ${status}`).toContain(status);
          if (status === 409) {
            const conflict = (await response.json()) as { current_revision?: unknown };
            if (typeof conflict.current_revision === 'number' && Number.isSafeInteger(conflict.current_revision)) {
              expectedRevision = conflict.current_revision;
            } else {
              expectedRevision = await getOperatorProjectionRevisionViaApi(request, tournament.id);
            }
          } else {
            await waitForTransientRetry(attempt);
            expectedRevision = await getOperatorProjectionRevisionViaApi(request, tournament.id);
            continue;
          }
          await waitForTransientRetry(attempt);
        }
        throw new Error(`${action} Swiss Wave did not converge after retries: ${response?.status() ?? 'no response'}`);
      };

      const submitSwissResult = async (
        winnerID: string,
        loserID: string,
        seriesID: string,
      ): Promise<void> => {
        for (const participantID of [loserID, winnerID]) {
          const context = contextForParticipant(participantID);
          const snapshot = await readParticipantSnapshotViaApi(context, tournament.id);
          expect(snapshot.assignment?.context.series_id).toBe(seriesID);
          if (!snapshot.assignment) throw new Error(`participant ${participantID} did not receive assignment`);
          const flag = normalFlagsByTitle.get(snapshot.assignment.active_snapshot.title);
          expect(flag).toBeTruthy();
          const csrfToken = (await context.cookies()).find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
          expect(csrfToken).toBeTruthy();
          const response = await context.request.post(
            `${backendURL}/api/v1/tournaments/${tournament.id}/participant/series/${seriesID}/games/${snapshot.assignment.context.game_id}/submissions`,
            {
              headers: { 'X-CSRF-Token': csrfToken ?? '', 'Idempotency-Key': randomUUID(), Origin: frontendURL },
              data: {
                expected_projection_revision: snapshot.projection_revision,
                submitted_flag: participantID === winnerID ? flag : `incorrect-${randomUUID()}`,
              },
            },
          );
          expect(response.status(), `Swiss submission failed with ${response.status()}`).toBe(200);
        }
      };

      const knownWaveIDs = new Set<string>();
      const swissPoints = new Map(checkedInParticipants.map((participant) => [participant.id, 0]));
      const participantOrder = new Map(
        [...checkedInParticipants]
          .map((participant) => participant.id)
          .sort()
          .map((participantID, index) => [participantID, index] as const),
      );
      const targetSwissPoints = new Map(
        checkedInParticipants.map((participant, index) => [
          participant.id,
          index === 0 ? 3 : index <= 3 ? 2 : 0,
        ] as const),
      );
      const manualRoundPairings = [
        [[0, 1], [2, 4], [3, 5]],
        [[0, 2], [1, 5], [3, 4]],
        [[0, 3], [1, 4], [2, 5]],
      ];
      for (const roundNumber of [1, 2, 3]) {
        const pairingPlan = manualRoundPairings[roundNumber - 1];
        expect(pairingPlan).toBeDefined();
        if (!pairingPlan) throw new Error(`missing manual pairing plan for round ${roundNumber}`);
        const pairing = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/pairings`, {
          headers: {
            'X-CSRF-Token': adminSession.access_csrf_token,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            categories: ['web', 'crypto', 'forensics'],
            category_mode: 'random',
            expected_projection_revision: await getOperatorProjectionRevisionViaApi(request, tournament.id),
            pairing_mode: 'manual',
            manual_pairings: pairingPlan.map(([firstIndex, secondIndex]) => ({
              first_participant_id: checkedInParticipants[firstIndex]?.id,
              second_participant_id: checkedInParticipants[secondIndex]?.id,
            })),
            round_number: roundNumber,
          },
        });
        expect(pairing.status()).toBe(200);
        const configured = (await pairing.json()) as FullStackSwissRound;
        expect(configured.pairings).toHaveLength(3);
        const snapshot = await readOperatorSnapshotViaApi<FullStackOperatorSnapshot & { series?: FullStackPlayoffSeries[] }>(
          request,
          tournament.id,
        );
        const wave = snapshot.waves.find((candidate) => !knownWaveIDs.has(candidate.id));
        expect(wave).toBeDefined();
        if (!wave) throw new Error(`round ${roundNumber} Wave was not materialized`);
        knownWaveIDs.add(wave.id);
        const waveProjectionRevision = snapshot.next_cursor.projection_revision;
        await waveAction(wave.id, 'open_ready_window', waveProjectionRevision);
        for (const member of wave.members) {
          const context = contextForParticipant(member.participant_id);
          const participant = await readParticipantSnapshotViaApi(context, tournament.id);
          const csrfToken = (await context.cookies()).find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
          expect(csrfToken).toBeTruthy();
          const ready = await context.request.post(
            `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${wave.id}/ready`,
            {
              headers: { 'X-CSRF-Token': csrfToken ?? '', 'Idempotency-Key': randomUUID(), Origin: frontendURL },
              data: { expected_projection_revision: participant.projection_revision, ready: true },
            },
          );
          expect(ready.status()).toBe(200);
        }
        await waveAction(wave.id, 'start', waveProjectionRevision);
        const membersBySeries = new Map<string, FullStackWave['members']>();
        for (const member of wave.members) {
          if (!member.series_id) continue;
          const members = membersBySeries.get(member.series_id) ?? [];
          members.push(member);
          membersBySeries.set(member.series_id, members);
        }
        for (const [seriesID, members] of membersBySeries) {
          expect(members).toHaveLength(2);
          const first = members[0];
          const second = members[1];
          if (!first || !second) throw new Error(`series ${seriesID} lost a member`);
          const firstSnapshot = await readParticipantSnapshotViaApi(contextForParticipant(first.participant_id), tournament.id);
          const secondSnapshot = await readParticipantSnapshotViaApi(contextForParticipant(second.participant_id), tournament.id);
          expect(firstSnapshot.assignment?.active_snapshot.task_id).toBe(secondSnapshot.assignment?.active_snapshot.task_id);
          expect(firstSnapshot.assignment?.active_snapshot.snapshot_id).toBe(secondSnapshot.assignment?.active_snapshot.snapshot_id);
          expect(firstSnapshot.assignment?.active_snapshot.time_limit).toBe(180);
          expect(secondSnapshot.assignment?.active_snapshot.time_limit).toBe(180);
          const firstOrder = participantOrder.get(first.participant_id);
          const secondOrder = participantOrder.get(second.participant_id);
          expect(firstOrder).toBeDefined();
          expect(secondOrder).toBeDefined();
          if (firstOrder === undefined || secondOrder === undefined) {
            throw new Error(`Swiss participant order is missing for ${seriesID}`);
          }
          const firstPoints = swissPoints.get(first.participant_id) ?? 0;
          const secondPoints = swissPoints.get(second.participant_id) ?? 0;
          const firstTargetPoints = targetSwissPoints.get(first.participant_id) ?? 0;
          const secondTargetPoints = targetSwissPoints.get(second.participant_id) ?? 0;
          const firstRemainingWins = firstTargetPoints - firstPoints;
          const secondRemainingWins = secondTargetPoints - secondPoints;
          const winner = firstRemainingWins > 0 && secondRemainingWins <= 0
            ? first
            : secondRemainingWins > 0 && firstRemainingWins <= 0
              ? second
              : firstTargetPoints > secondTargetPoints
                ? first
                : secondTargetPoints > firstTargetPoints
                  ? second
                  : firstRemainingWins > secondRemainingWins || (
                      firstRemainingWins === secondRemainingWins && firstOrder < secondOrder
                    )
                    ? first
                    : second;
          const loser = winner.participant_id === first.participant_id ? second : first;
          await submitSwissResult(winner.participant_id, loser.participant_id, seriesID);
          swissPoints.set(winner.participant_id, (swissPoints.get(winner.participant_id) ?? 0) + 1);
        }
        await expect.poll(async () => {
          const response = await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/snapshot`, {
            headers: { Origin: frontendURL },
          });
          if (response.status() !== 200) return false;
          const current = (await response.json()) as FullStackOperatorSnapshot & { series?: FullStackPlayoffSeries[] };
          return current.series?.filter((series) => membersBySeries.has(series.id)).every((series) => (
            series.state === 'completed' && series.winner_id &&
            series.score.first_participant_wins + series.score.second_participant_wins === 1
          )) ?? false;
        }, { timeout: 20_000 }).toBe(true);
        const currentWave = (await readOperatorSnapshotViaApi<FullStackOperatorSnapshot>(
          request,
          tournament.id,
        )).waves.find((candidate) => candidate.id === wave.id);
        if (currentWave?.state !== 'completed') await waveAction(wave.id, 'complete');
      }
      expect(checkedInParticipants.map((participant) => swissPoints.get(participant.id) ?? 0)).toEqual([
        3, 2, 2, 2, 0, 0,
      ]);

      let startGoldenResponse: APIResponse | null = null;
      const startGoldenMaxAttempts = 12;
      for (let attempt = 0; attempt < startGoldenMaxAttempts; attempt += 1) {
        startGoldenResponse = await applyStartGoldenViaApi(
          request,
          tournament.id,
          adminSession.access_csrf_token,
        );
        const status = startGoldenResponse.status();
        if (status === 200) break;
        expect([409, 429], `start Golden returned unexpected status ${status}`).toContain(status);
        if (attempt < startGoldenMaxAttempts - 1) {
          await waitForTransientRetry(attempt);
        }
      }
      if (!startGoldenResponse) throw new Error('start Golden did not issue a request');
      expect(startGoldenResponse.status(), `start Golden failed with ${startGoldenResponse.status()}`).toBe(200);
      const openGolden = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden/open`, {
        headers: {
          'X-CSRF-Token': adminSession.access_csrf_token,
          'Idempotency-Key': randomUUID(),
          Origin: frontendURL,
        },
        data: {
          expected_projection_revision: await getOperatorProjectionRevisionViaApi(request, tournament.id),
          expected_runtime_revision: 0,
        },
      });
      expect(openGolden.status(), `open Golden failed with ${openGolden.status()}`).toBe(200);
      const opened = (await openGolden.json()) as GoldenOperatorState;
      expect(opened.groups, 'a non-impacting tie must not create extra Golden groups').toHaveLength(1);
      expect(opened.groups).toEqual(expect.arrayContaining([
        expect.objectContaining({ position_from: 2, position_to: 4 }),
      ]));
      const noShowSource = opened.groups[0];
      expect(noShowSource).toBeDefined();
      if (!noShowSource) throw new Error('Golden no-show source was not opened');
      const maxSwissPoints = Math.max(...swissPoints.values());
      const swissLeaders = checkedInParticipants.filter((participant) => swissPoints.get(participant.id) === maxSwissPoints);
      expect(swissLeaders).toHaveLength(1);
      const swissLeader = swissLeaders[0];
      if (!swissLeader) throw new Error('Swiss leader was not resolved');
      const goldenMemberIDs = new Set(noShowSource.members.map((member) => member.participant_id));
      expect(goldenMemberIDs.has(swissLeader.id)).toBe(false);
      const goldenPoints = noShowSource.members.map((member) => swissPoints.get(member.participant_id) ?? 0);
      expect(new Set(goldenPoints).size).toBe(1);
      const lowerTiePoints = checkedInParticipants
        .filter((participant) => participant.id !== swissLeader.id && !goldenMemberIDs.has(participant.id))
        .map((participant) => swissPoints.get(participant.id) ?? 0);
      expect(lowerTiePoints.length).toBeGreaterThanOrEqual(2);
      expect(new Set(lowerTiePoints).size).toBeLessThan(lowerTiePoints.length);
      expect(Math.max(...lowerTiePoints)).toBeLessThan(goldenPoints[0] ?? 0);
      const survivors = noShowSource.members.slice(0, 2);
      const noShowParticipant = noShowSource.members[2];
      expect(survivors).toHaveLength(2);
      expect(noShowParticipant).toBeDefined();
      if (!noShowParticipant) throw new Error('Golden no-show source has no no-show participant');

      const readGoldenParticipant = async (participantID: string): Promise<GoldenParticipantState> => {
        const response = await contextForParticipant(participantID).request.get(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden`,
          { headers: { Origin: frontendURL } },
        );
        expect(response.status()).toBe(200);
        return (await response.json()) as GoldenParticipantState;
      };
      for (const group of opened.groups) {
        for (const member of group.members) {
          const participant = await readGoldenParticipant(member.participant_id);
          expect(participant.attempt_id).toBe(group.attempt_id);
          expect(participant.task).toBeNull();
          const context = contextForParticipant(member.participant_id);
          const csrfToken = (await context.cookies()).find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
          if (!survivors.some((candidate) => candidate.participant_id === member.participant_id)) continue;
          const ready = await context.request.post(`${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden/ready`, {
            headers: { 'X-CSRF-Token': csrfToken ?? '', 'Idempotency-Key': randomUUID(), Origin: frontendURL },
            data: {
              attempt_id: participant.attempt_id,
              expected_runtime_revision: participant.runtime_revision,
              ready: true,
              ready_window_id: participant.ready_window_id,
            },
          });
          expect(ready.status()).toBe(200);
        }
      }

      const activeGroups: GoldenGroup[] = [];
      await expect.poll(async () => {
        const response = await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`, {
          headers: { Origin: frontendURL },
        });
        if (response.status() !== 200) return false;
        const current = (await response.json()) as GoldenOperatorState;
        const group = current.groups.find((candidate) => candidate.group_id === noShowSource.group_id);
        return group?.state === 'ready' && group.runtime_revision > noShowSource.runtime_revision;
      }, { timeout: 60_000, intervals: [1_000, 2_000, 5_000] }).toBe(true);

      for (const group of opened.groups) {
        let expectedAttemptID = group.attempt_id;
        let expectedRuntimeRevision = group.runtime_revision;
        let expectedReadyWindowID = group.ready_window_id;
        let startedGroup: GoldenGroup | undefined;
        for (let attempt = 0; attempt < 60; attempt += 1) {
          const response = await request.post(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden/attempts/${expectedAttemptID}/start`, {
            headers: {
              'X-CSRF-Token': adminSession.access_csrf_token,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: { expected_runtime_revision: expectedRuntimeRevision, ready_window_id: expectedReadyWindowID },
          });
          if (response.status() === 200) {
            const current = (await response.json()) as GoldenOperatorState;
            startedGroup = current.groups.find((candidate) => candidate.group_id === group.group_id);
            break;
          }
          const status = response.status();
          expect([409, 429], `start Golden attempt returned unexpected status ${status}`).toContain(status);
          if (status === 429) {
            await waitForTransientRetry(attempt);
          }
          const current = (await (await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`, {
            headers: { Origin: frontendURL },
          })).json()) as GoldenOperatorState;
          const refreshed = current.groups.find((candidate) => candidate.group_id === group.group_id);
          expect(refreshed).toBeDefined();
          if (!refreshed) throw new Error(`Golden group ${group.group_id} disappeared`);
          expectedAttemptID = refreshed.attempt_id;
          expectedRuntimeRevision = refreshed.runtime_revision;
          expectedReadyWindowID = refreshed.ready_window_id;
          if (status === 409) await waitForTransientRetry(attempt);
        }
        expect(startedGroup).toBeDefined();
        if (!startedGroup) throw new Error(`started Golden group ${group.group_id} disappeared`);
        activeGroups.push(startedGroup);
      }

      const submitGolden = async (participantID: string): Promise<GoldenParticipantState> => {
        const context = contextForParticipant(participantID);
        const participant = await readGoldenParticipant(participantID);
        expect(participant.task).not.toBeNull();
        if (!participant.task) throw new Error(`Golden participant ${participantID} has no task`);
        expect(participant.task.time_limit_seconds).toBe(180);
        expect(participant.task.source_file_available).toBe(true);
        const flag = goldenFlagsByTitle.get(participant.task.title);
        expect(flag).toBeTruthy();
        const csrfToken = (await context.cookies()).find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
        const response = await context.request.post(`${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden/submissions`, {
          headers: { 'X-CSRF-Token': csrfToken ?? '', 'Idempotency-Key': randomUUID(), Origin: frontendURL },
          data: {
            attempt_id: participant.attempt_id,
            expected_runtime_revision: participant.runtime_revision,
            ready_window_id: participant.ready_window_id,
            submitted_flag: flag,
            },
        });
        expect(response.status(), `Golden submission failed with ${response.status()}`).toBe(200);
        return (await response.json()) as GoldenParticipantState;
      };

      const activeSource = activeGroups[0];
      expect(activeSource).toBeDefined();
      if (!activeSource) throw new Error('Golden no-show source was not started');
      const activeSurvivors = await Promise.all(survivors.map((member) => readGoldenParticipant(member.participant_id)));
      expect(activeSurvivors.every((participant) => participant.task !== null)).toBe(true);
      const survivorTasks = activeSurvivors.map((participant) => participant.task);
      expect(survivorTasks[0]?.task_id).toBe(survivorTasks[1]?.task_id);
      expect(survivorTasks[0]?.snapshot_id).toBe(survivorTasks[1]?.snapshot_id);
      expect(survivorTasks[0]?.assignment_id).toBe(survivorTasks[1]?.assignment_id);
      expect(survivorTasks[0]?.version).toBe(survivorTasks[1]?.version);
      expect(survivorTasks[0]?.title).toBe(survivorTasks[1]?.title);
      expect(survivorTasks[0]?.description).toBe(survivorTasks[1]?.description);
      expect(survivorTasks[0]?.task_url).toBe(survivorTasks[1]?.task_url);
      expect(survivorTasks[0]?.source_file_available).toBe(survivorTasks[1]?.source_file_available);
      expect(survivorTasks[0]?.source_file_available).toBe(true);
      const sourceBodies = await Promise.all(activeSurvivors.map(async (participant, index) => {
        if (!participant.task) throw new Error('Golden survivor task disappeared before source download');
        const member = survivors[index];
        if (!member) throw new Error(`Golden survivor ${index} disappeared before source download`);
        const participantContext = contextForParticipant(member.participant_id);
        const sourceAccess = await participantContext.request.get(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/assignments/${participant.task.assignment_id}/source-file`,
          { headers: { Origin: frontendURL } },
        );
        expect(sourceAccess.status()).toBe(200);
        const source = (await sourceAccess.json()) as { source_file_url: string; expires_at: string };
        expect(source.source_file_url).toMatch(/^https?:\/\//);
        expect(source.expires_at).toEqual(expect.any(String));
        const download = await participantContext.request.get(source.source_file_url);
        expect(download.ok(), `Golden source download failed with ${download.status()}`).toBeTruthy();
        const body = await download.body();
        expect(body.subarray(0, 2).toString()).toBe('PK');
        return body;
      }));
      expect(sourceBodies[0]?.equals(sourceBodies[1] ?? Buffer.alloc(0))).toBe(true);
      expect((await readGoldenParticipant(noShowParticipant.participant_id)).task).toBeNull();
      const solvingSurvivors = completion === 'solves' ? survivors : survivors.slice(0, 1);
      for (const member of solvingSurvivors) {
        const result = await submitGolden(member.participant_id);
        expect(result.submitted).toBe(true);
      }
      const lastSurvivor = survivors[1];
      if (!lastSurvivor) throw new Error('Golden source lost its second survivor');
      let deadlineAt = 0;
      if (completion === 'deadline') {
        expect(activeSource.started_at).not.toBeNull();
        expect(activeSource.deadline).not.toBeNull();
        if (!activeSource.started_at || !activeSource.deadline) throw new Error('active Golden source has no deadline');
        deadlineAt = Date.parse(activeSource.deadline);
        expect(deadlineAt - Date.parse(activeSource.started_at)).toBe(180_000);
        const beforeDeadlineResponse = await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`, {
          headers: { Origin: frontendURL },
        });
        expect(beforeDeadlineResponse.status()).toBe(200);
        const beforeDeadline = (await beforeDeadlineResponse.json()) as GoldenOperatorState;
        expect(Date.parse(beforeDeadline.observed_at)).toBeLessThan(deadlineAt);
        const pendingGroup = beforeDeadline.groups.find((group) => group.group_id === activeSource.group_id);
        expect(pendingGroup?.state).toBe('active');
        expect(pendingGroup?.attempt_id).toBe(activeSource.attempt_id);
        const pending = await readGoldenParticipant(lastSurvivor.participant_id);
        expect(pending.state).toBe('active');
        expect(pending.attempt_id).toBe(activeSource.attempt_id);
        expect(pending.submitted).toBe(false);
        expect(pending.position).toBeNull();
      }
      await expect.poll(async () => {
        const response = await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`, {
          headers: { Origin: frontendURL },
        });
        if (response.status() !== 200) return false;
        const current = (await response.json()) as GoldenOperatorState;
        const group = current.groups.find((candidate) => candidate.group_id === activeSource.group_id);
        if (completion === 'deadline' && group) {
          expect(group.attempt_id).toBe(activeSource.attempt_id);
          const pending = group.members.find((member) => member.participant_id === lastSurvivor.participant_id);
          expect(pending?.submitted).toBe(false);
          if (group.state === 'completed') {
            expect(Date.parse(current.observed_at)).toBeGreaterThanOrEqual(deadlineAt);
          } else {
            expect(group.state).toBe('active');
            expect(pending?.position).toBeNull();
          }
        }
        return group?.state === 'completed';
      }, { timeout: completion === 'deadline' ? 210_000 : 20_000, intervals: [1_000, 2_000, 5_000] }).toBe(true);

      const completedState = (await (await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`, {
        headers: { Origin: frontendURL },
      })).json()) as GoldenOperatorState;
      const completedSource = completedState.groups.find((group) => group.group_id === activeSource.group_id);
      expect(completedSource?.state).toBe('completed');
      expect(completedSource?.attempt_id).toBe(activeSource.attempt_id);
      const committedPositions = new Map(completedSource?.members.map((member) => [member.participant_id, member.position]) ?? []);
      expect([...committedPositions.values()].sort((left, right) => (left ?? 0) - (right ?? 0))).toEqual([2, 3, 4]);
      expect(committedPositions.get(noShowParticipant.participant_id)).toBe(4);
      expect(survivors.map((survivor) => committedPositions.get(survivor.participant_id))).toEqual([2, 3]);
      if (completion === 'deadline') {
        const terminalMember = completedSource?.members.find((member) => member.participant_id === lastSurvivor.participant_id);
        expect(terminalMember?.submitted).toBe(false);
        expect(terminalMember?.position).toBe(3);
        const terminalParticipant = await readGoldenParticipant(lastSurvivor.participant_id);
        expect(terminalParticipant.state).toBe('completed');
        expect(terminalParticipant.attempt_id).toBe(activeSource.attempt_id);
        expect(terminalParticipant.submitted).toBe(false);
        expect(terminalParticipant.position).toBe(3);

        const terminalPage = await contextForParticipant(lastSurvivor.participant_id).newPage();
        try {
          await terminalPage.goto(`/arena/participant/${tournament.id}`, { waitUntil: 'domcontentloaded' });
          const participantPanel = terminalPage.getByTestId('participant-golden-panel');
          await expect(participantPanel).toHaveAttribute('data-attempt-id', activeSource.attempt_id);
          await expect(participantPanel).toHaveAttribute('data-golden-state', 'completed');
          await expect(participantPanel.getByTestId('participant-golden-position').locator('strong')).toHaveText('3');
          await expect(participantPanel.getByText('Решение принято', { exact: true })).toHaveCount(0);
        } finally {
          await terminalPage.close();
        }

        await page.goto('/admin', { waitUntil: 'domcontentloaded' });
        await page.getByRole('button', { name: 'Соревнования', exact: true }).click();
        const tournamentRow = page.getByRole('row').filter({
          hasText: tournamentName,
        });
        await tournamentRow.getByRole('button', { name: 'Открыть', exact: true }).click();
        await page.getByRole('button', { name: 'Проведение', exact: true }).click();
        const goldenPanel = page.getByTestId('operator-golden-playoff-control-panel');
        await expect(goldenPanel).toBeVisible();
        const operatorGroup = goldenPanel.locator(
          `[data-testid="golden-group"][data-group-id="${activeSource.group_id}"]`,
        );
        await expect(operatorGroup).toBeVisible();
        const terminalRow = operatorGroup.getByTestId('golden-member').filter({
          has: page.getByText(lastSurvivor.participant_id, { exact: true }),
        });
        await expect(terminalRow).toHaveCount(1);
        await expect(terminalRow.getByText('Позиция 3', { exact: true })).toBeVisible();
        await expect(terminalRow.getByText('Не отправил', { exact: true })).toBeVisible();
        await expect(terminalRow.getByText('Отправил', { exact: true })).toHaveCount(0);
      }

      const completedGolden = (await (await request.get(`${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`, {
        headers: { Origin: frontendURL },
      })).json()) as GoldenOperatorState;
      expect(completedGolden.groups).toHaveLength(opened.groups.length);
      expect(completedGolden.groups.every((group) => group.members.every((member) => member.position !== null))).toBe(true);
      const playoffResponse = await applyStartPlayoffsViaApi(request, tournament.id, adminSession.access_csrf_token);
      expect(playoffResponse.status(), `start playoffs failed with ${playoffResponse.status()}`).toBe(200);

      const spectatorContext = await browser.newContext({ baseURL: frontendURL });
      try {
        const publicSnapshot = await readPublicSnapshotViaApi(spectatorContext, tournament.id);
        expect(publicSnapshot.bracket.matches).toHaveLength(3);
        expect(publicSnapshot.bracket.matches.filter((match) => match.stage === 'semifinal')).toHaveLength(2);
        expect(publicSnapshot.bracket.matches.find((match) => match.stage === 'final')).toBeDefined();
        const topFourPlayerIDs = new Set([
          swissLeader.player_id,
          ...completedGolden.groups.flatMap((group) => group.members
            .filter((member) => member.position !== null && member.position <= 4)
            .map((member) => checkedInParticipants.find((participant) => participant.id === member.participant_id)?.player_id)
            .filter((playerID): playerID is string => playerID !== undefined)),
        ]);
        const topFourNames = new Set([...topFourPlayerIDs]
          .map((playerID) => players.find((player) => player.id === playerID)?.username)
          .filter((username): username is string => username !== undefined));
        const bracketNames = new Set(publicSnapshot.bracket.matches.flatMap((match) => [
          match.first_display_name,
          match.second_display_name,
        ].filter((name): name is string => name !== null)));
        expect([...bracketNames].sort()).toEqual([...topFourNames].sort());
        const spectatorPage = await spectatorContext.newPage();
        await spectatorPage.goto(`/arena/spectator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
        await expect(spectatorPage.getByTestId('tournament-broadcast')).toBeVisible();
        await expect(spectatorPage.getByRole('tab', { name: 'Плей-офф' })).toBeVisible();
      } finally {
        ownedFixture.contexts.push(spectatorContext);
      }
    } finally {
      ownedFixture.contexts.push(...playerContexts);
    }
  }));

  test('FE-047 real backend completes a 16-player Swiss and BO3 tournament', async ({ browser, request, page, ownedFixture }) => {
    test.setTimeout(900_000);

    type FullStackPlayoffSnapshot = FullStackOperatorSnapshot & {
      series: FullStackPlayoffSeries[];
      tournament: { id: string; state: string };
    };

    const tournamentName = uniqueName('fe040-spectator');
    const tournamentPublicID = uniqueName('fe040-public');
    const playerNames = Array.from({ length: 16 }, (_, index) =>
      uniqueName(`fe040-player-${index + 1}`));
    const playerContexts: BrowserContext[] = [];
    let spectatorContext: BrowserContext | undefined;
    let spectatorPage: Page | undefined;

    try {
      await loginThroughAdminUI(page);
      const adminSession = await adminLogin(request);
      const normalFlagsByTitle = new Map<string, string>();
      const goldenFlagsByTitle = new Map<string, string>();
      const normalTimeLimitSeconds = 180;
      const reserveChainLength = 3;
      const swissRounds = playerNames.length <= 8 ? 3 : 4;
      const sharedCategoryCount =
        (playerNames.length / 2 * swissRounds + 3) * reserveChainLength;
      const normalTaskGroups: Array<{
        category: FullStackTaskInput['category'];
        count: number;
      }> = [
        { category: 'web', count: sharedCategoryCount },
        { category: 'crypto', count: sharedCategoryCount },
        { category: 'reverse', count: sharedCategoryCount },
        { category: 'forensics', count: reserveChainLength },
        { category: 'pwn', count: reserveChainLength },
      ];

      for (const group of normalTaskGroups) {
        for (let index = 0; index < group.count; index += 1) {
          const title = uniqueName(`fe040-${group.category}-${index + 1}`);
          const flag = `flag{${title.replaceAll('-', '_')}}`;
          normalFlagsByTitle.set(title, flag);
          await createTaskViaApi(ownedFixture, request, adminSession, {
            title,
            description: 'Normal task for the real backend spectator flow.',
            kind: 'normal',
            category: group.category,
            difficulty: 'easy',
            time_limit: normalTimeLimitSeconds,
            flag,
            hints: ['spectator hint one', 'spectator hint two', 'spectator hint three'],
            task_url: 'https://example.com/fe040-normal',
          });
        }
      }
      const goldenTaskCount = playerNames.length / 2 * reserveChainLength;
      for (let index = 0; index < goldenTaskCount; index += 1) {
        const title = uniqueName(`fe040-golden-${index + 1}`);
        const flag = `flag{${title.replaceAll('-', '_')}}`;
        goldenFlagsByTitle.set(title, flag);
        await createTaskViaApi(ownedFixture, request, adminSession, {
          title,
          description: 'Golden task for the real backend spectator flow.',
          kind: 'golden',
          category: 'web',
          difficulty: 'easy',
          time_limit: 180,
          flag,
          hints: ['golden hint one', 'golden hint two', 'golden hint three'],
          task_url: 'https://example.com/fe040-golden',
        });
      }

      const contentRevision = await getTournamentContentRevision(request);
      const tournament = await createTournamentViaApi(
        ownedFixture,
        request,
        adminSession,
        {
          name: tournamentName,
          content_revision: contentRevision,
          planned_roster_size: 16,
          public_id: tournamentPublicID,
          preset: 'tournament_v1',
          expected_revision: 0,
        },
      );

      const players: FullStackPlayer[] = [];
      for (const username of playerNames) {
        const context = await browser.newContext({ baseURL: frontendURL });
        playerContexts.push(context);
        const playerPage = await context.newPage();
        await joinAsPlayer(playerPage, username);
        const meResponse = await context.request.get(`${backendURL}/api/v1/players/me`, {
          headers: { Origin: frontendURL },
        });
        expect(meResponse.status(), `players/me failed with ${meResponse.status()}`).toBe(200);
        const me = (await meResponse.json()) as { player: FullStackPlayer };
        expect(me.player.username).toBe(username);
        players.push(me.player);
        await playerPage.close();
      }

      const roster = await getRosterViaApi(request, tournament.id);
      const invitedParticipants: FullStackRosterParticipantInput[] = players.map(
        (player, index) => ({
          attendance: 'invited',
          player_id: player.id,
          seed: index + 1,
        }),
      );
      const invitedRosterResponse = await replaceRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        await getOperatorProjectionRevisionViaApi(request, tournament.id),
        invitedParticipants,
      );
      const invitedRoster = await readRosterResponse(invitedRosterResponse, tournament.id, roster.id);
      expect(invitedRoster.participants).toHaveLength(16);

      const openRegistration = await applyOpenRegistrationViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
      );
      expect(openRegistration.status(), `open registration failed with ${openRegistration.status()}`).toBe(200);

      const checkedInParticipants = invitedParticipants.map((participant) => ({
        ...participant,
        attendance: 'checked_in' as const,
      }));
      const checkedInRosterResponse = await replaceRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        await getOperatorProjectionRevisionViaApi(request, tournament.id),
        checkedInParticipants,
      );
      const checkedInRoster = await readRosterResponse(
        checkedInRosterResponse,
        tournament.id,
        roster.id,
      );
      const checkedInRosterParticipants = sortRosterParticipantsBySeed(checkedInRoster.participants);
      expect(checkedInRosterParticipants).toHaveLength(16);
      expect(checkedInRosterParticipants.every((participant) => participant.attendance === 'checked_in')).toBe(true);

      let preflight: FullStackPreflightReport | null = null;
      for (let attempt = 0; attempt < 12 && !preflight?.passed; attempt += 1) {
        const preflightResponse = await runRosterPreflightViaApi(
          request,
          tournament.id,
          adminSession.access_csrf_token,
        );
        expect(
          preflightResponse.status(),
          `roster preflight failed with ${preflightResponse.status()}`,
        ).toBe(200);
        preflight = (await preflightResponse.json()) as FullStackPreflightReport;
        if (!preflight.passed) {
          await new Promise((resolve) => setTimeout(resolve, 250));
        }
      }
      expect(preflight, 'roster preflight did not return a report').not.toBeNull();
      if (!preflight) {
        throw new Error('roster preflight did not return a report');
      }
      const failedPreflightChecks = (preflight.checks ?? [])
        .filter((check) => !check.passed)
        .map((check) => ({ code: check.code, evidence: check.evidence ?? [] }));
      expect(
        preflight.passed,
        `roster preflight failed: ${JSON.stringify(failedPreflightChecks)}`,
      ).toBe(true);

      const lockResponse = await lockRosterViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
        preflight.id,
        checkedInRosterParticipants.map((participant) => participant.player_id),
      );
      expect(lockResponse.status(), `roster lock failed with ${lockResponse.status()}`).toBe(200);
      const lockedRoster = await readRosterResponse(lockResponse, tournament.id, roster.id);
      expect(lockedRoster.locked).toBe(true);

      const startSwiss = await applyStartSwissViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
      );
      expect(startSwiss.status(), `start Swiss failed with ${startSwiss.status()}`).toBe(200);

      const contextForParticipant = (participantID: string): BrowserContext => {
        const rosterParticipant = checkedInRosterParticipants.find(
          (participant) => participant.id === participantID,
        );
        expect(rosterParticipant, `missing roster participant ${participantID}`).toBeDefined();
        if (!rosterParticipant) {
          throw new Error(`missing roster participant ${participantID}`);
        }
        const playerIndex = players.findIndex((player) => player.id === rosterParticipant.player_id);
        const context = playerContexts[playerIndex];
        if (!context) {
          throw new Error(`missing browser context for player ${rosterParticipant.player_id}`);
        }
        return context;
      };

      const qualifierParticipantIDs = new Set(
        checkedInRosterParticipants.slice(0, 4).map((participant) => participant.id),
      );
      const swissPoints = new Map(
        checkedInRosterParticipants.map((participant) => [participant.id, 0]),
      );
      const participantSeed = new Map(
        checkedInRosterParticipants.map((participant) => [participant.id, participant.seed]),
      );
      const opponentPairs = new Set<string>();

      type WaveAction = 'open_ready_window' | 'start' | 'complete';
      const waveAction = async (
        waveID: string,
        action: WaveAction,
        knownProjectionRevision?: number,
      ): Promise<FullStackWave> => {
        const response = await request.post(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/waves/${waveID}/actions`,
          {
            headers: {
              'X-CSRF-Token': adminSession.access_csrf_token,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              action,
              confirmed: true,
              expected_projection_revision: knownProjectionRevision ?? await getOperatorProjectionRevisionViaApi(
                request,
                tournament.id,
              ),
            },
          },
        );
        expect(response.status(), `${action} Wave failed with ${response.status()}`).toBe(200);
        return (await response.json()) as FullStackWave;
      };

      const prepareSwissRound = async (
        roundNumber: number,
        knownWaveIDs: ReadonlySet<string>,
      ): Promise<FullStackWave> => {
        const pairing = await request.post(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/pairings`,
          {
            headers: {
              'X-CSRF-Token': adminSession.access_csrf_token,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              categories: ['web', 'crypto', 'forensics'],
              category_mode: 'random',
              expected_projection_revision: await getOperatorProjectionRevisionViaApi(
                request,
                tournament.id,
              ),
              pairing_mode: 'automatic',
              round_number: roundNumber,
            },
          },
        );
        expect(pairing.status(), `Swiss round ${roundNumber} pairing failed with ${pairing.status()}`).toBe(200);
        const configuredRound = (await pairing.json()) as FullStackSwissRound;
        expect(configuredRound.round_number).toBe(roundNumber);
        expect(configuredRound.pairings).toHaveLength(8);
        expect(new Set(configuredRound.pairings.flatMap((pair) => [
          pair.first_participant_id,
          pair.second_participant_id,
        ])).size).toBe(16);
        for (const pair of configuredRound.pairings) {
          const pairKey = [pair.first_participant_id, pair.second_participant_id].sort().join(':');
          expect(opponentPairs.has(pairKey), `Swiss repeated opponents in round ${roundNumber}`).toBe(false);
          opponentPairs.add(pairKey);
        }
        if (configuredRound.bye) {
          swissPoints.set(
            configuredRound.bye.participant_id,
            (swissPoints.get(configuredRound.bye.participant_id) ?? 0) + 1,
          );
        }

        const snapshot = await readOperatorSnapshotViaApi<FullStackOperatorSnapshot>(
          request,
          tournament.id,
        );
        const wave = snapshot.waves.find((candidate) => !knownWaveIDs.has(candidate.id));
        expect(wave, `Swiss round ${roundNumber} did not create a Wave`).toBeDefined();
        if (!wave) {
          throw new Error(`Swiss round ${roundNumber} did not create a Wave`);
        }

        const waveProjectionRevision = snapshot.next_cursor.projection_revision;
        await waveAction(wave.id, 'open_ready_window', waveProjectionRevision);
        for (const member of wave.members.filter((candidate) => candidate.series_id !== null)) {
          const context = contextForParticipant(member.participant_id);
          const participant = await readParticipantSnapshotViaApi(context, tournament.id);
          const csrfToken = (await context.cookies()).find(
            (cookie) => cookie.name === 'tpm_player_csrf',
          )?.value;
          expect(csrfToken, `participant ${member.participant_id} did not retain CSRF`).toBeTruthy();
          const readiness = await context.request.post(
            `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${wave.id}/ready`,
            {
              headers: {
                'X-CSRF-Token': csrfToken ?? '',
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: {
                expected_projection_revision: participant.projection_revision,
                ready: true,
              },
            },
          );
          expect(readiness.status(), `round ${roundNumber} readiness failed with ${readiness.status()}`).toBe(200);
        }
        return waveAction(wave.id, 'start', waveProjectionRevision);
      };

      const submitCorrectAssignment = async (
        context: BrowserContext,
        expectedSeriesID: string,
      ): Promise<void> => {
        const participant = await readParticipantSnapshotViaApi(context, tournament.id);
        const assignment = participant.assignment;
        expect(assignment, `participant did not receive assignment for ${expectedSeriesID}`).not.toBeNull();
        if (!assignment) {
          throw new Error(`participant did not receive assignment for ${expectedSeriesID}`);
        }
        expect(assignment.context.series_id).toBe(expectedSeriesID);
        const flag = normalFlagsByTitle.get(assignment.active_snapshot.title);
        expect(flag, `missing flag for ${assignment.active_snapshot.title}`).toBeTruthy();
        if (!flag) {
          throw new Error(`missing flag for ${assignment.active_snapshot.title}`);
        }
        const csrfToken = (await context.cookies()).find(
          (cookie) => cookie.name === 'tpm_player_csrf',
        )?.value;
        expect(csrfToken, `participant did not retain CSRF for ${expectedSeriesID}`).toBeTruthy();
        const submission = await context.request.post(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/series/${expectedSeriesID}/games/${assignment.context.game_id}/submissions`,
          {
            headers: {
              'X-CSRF-Token': csrfToken ?? '',
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              expected_projection_revision: participant.projection_revision,
              submitted_flag: flag,
            },
          },
        );
        expect(submission.status(), `submission failed with ${submission.status()}`).toBe(200);
      };

      const settleWave = async (wave: FullStackWave): Promise<void> => {
        const membersBySeries = new Map<string, FullStackWave['members']>();
        for (const member of wave.members) {
          if (!member.series_id) {
            continue;
          }
          const members = membersBySeries.get(member.series_id) ?? [];
          members.push(member);
          membersBySeries.set(member.series_id, members);
        }

        for (const [seriesID, members] of membersBySeries) {
          expect(members, `Swiss series ${seriesID} must contain exactly two participants`).toHaveLength(2);
          const pairAssignments = await Promise.all(
            members.map(async (member) => {
              const participant = await readParticipantSnapshotViaApi(
                contextForParticipant(member.participant_id),
                tournament.id,
              );
              expect(participant.assignment, `participant ${member.participant_id} did not receive a Swiss assignment`).not.toBeNull();
              if (!participant.assignment) {
                throw new Error(`participant ${member.participant_id} did not receive a Swiss assignment`);
              }
              return participant.assignment;
            }),
          );
          const firstAssignment = pairAssignments[0];
          const secondAssignment = pairAssignments[1];
          if (!firstAssignment || !secondAssignment) {
            throw new Error(`Swiss series ${seriesID} did not return both assignments`);
          }
          expect(firstAssignment.active_snapshot.task_id).toBe(secondAssignment.active_snapshot.task_id);
          expect(firstAssignment.active_snapshot.snapshot_id).toBe(secondAssignment.active_snapshot.snapshot_id);
          expect(firstAssignment.active_snapshot.version).toBe(secondAssignment.active_snapshot.version);
          expect(firstAssignment.active_snapshot.time_limit).toBe(normalTimeLimitSeconds);
          expect(secondAssignment.active_snapshot.time_limit).toBe(normalTimeLimitSeconds);
          expect(firstAssignment.context.stage).toBe('swiss');
          expect(firstAssignment.context.series_id).toBe(seriesID);
          expect(secondAssignment.context.series_id).toBe(seriesID);
          expect(firstAssignment.context.started_at).toBeTruthy();
          expect(firstAssignment.context.effective_deadline).toBeTruthy();
          expect(secondAssignment.context.started_at).toBeTruthy();
          expect(secondAssignment.context.effective_deadline).toBeTruthy();
          expect(
            Date.parse(firstAssignment.context.effective_deadline ?? '') -
              Date.parse(firstAssignment.context.started_at ?? ''),
          ).toBe(180_000);
          expect(Date.parse(firstAssignment.context.effective_deadline ?? '')).toBeGreaterThan(Date.now());

          const orderedMembers = [...members].sort((first, second) => {
            const firstQualifies = qualifierParticipantIDs.has(first.participant_id);
            const secondQualifies = qualifierParticipantIDs.has(second.participant_id);
            if (firstQualifies !== secondQualifies) {
              return firstQualifies ? -1 : 1;
            }
            const firstPoints = swissPoints.get(first.participant_id) ?? 0;
            const secondPoints = swissPoints.get(second.participant_id) ?? 0;
            if (firstPoints !== secondPoints) {
              return firstQualifies ? firstPoints - secondPoints : secondPoints - firstPoints;
            }
            return (participantSeed.get(first.participant_id) ?? 0) -
              (participantSeed.get(second.participant_id) ?? 0);
          });
          const winner = orderedMembers[0];
          expect(winner, `series ${seriesID} did not expose a winner candidate`).toBeDefined();
          if (!winner) {
            throw new Error(`series ${seriesID} did not expose a winner candidate`);
          }
          await submitCorrectAssignment(contextForParticipant(winner.participant_id), seriesID);
          swissPoints.set(
            winner.participant_id,
            (swissPoints.get(winner.participant_id) ?? 0) + 1,
          );
        }

        const snapshot = await readOperatorSnapshotViaApi<FullStackOperatorSnapshot>(
          request,
          tournament.id,
        );
        const currentWave = snapshot.waves.find((candidate) => candidate.id === wave.id);
        expect(currentWave, `snapshot lost Wave ${wave.id}`).toBeDefined();
        if (currentWave && currentWave.state !== 'completed') {
          await waveAction(wave.id, 'complete');
        }
        await expect.poll(async () => {
          const completedResponse = await request.get(
            `${backendURL}/api/v1/admin/tournaments/${tournament.id}/snapshot`,
            { headers: { Origin: frontendURL } },
          );
          if (completedResponse.status() !== 200) {
            return false;
          }
          const completedSnapshot = (await completedResponse.json()) as FullStackOperatorSnapshot & {
            series?: FullStackPlayoffSeries[];
          };
          const completedSeries = completedSnapshot.series?.filter((series) =>
            membersBySeries.has(series.id),
          ) ?? [];
          return completedSeries.length === membersBySeries.size && completedSeries.every((series) => {
            expect(series.winner_id, `Swiss series ${series.id} did not publish a server winner`).toBeTruthy();
            expect(series.score.first_participant_wins + series.score.second_participant_wins).toBe(1);
            expect(
              [series.score.first_participant_wins, series.score.second_participant_wins].sort(),
            ).toEqual([0, 1]);
            return series.state === 'completed';
          });
        }, { timeout: 20_000 }).toBe(true);
      };

      const assertFE041PublicPayloadSafe = (payload: unknown): void => {
        assertPublicProjectionRedacted(payload);
        const serialized = JSON.stringify(payload);
        const privateValues = [
          ...checkedInRosterParticipants.flatMap((participant) => [participant.id, participant.player_id]),
          ...players.map((player) => player.id),
          ...normalFlagsByTitle.keys(),
          ...normalFlagsByTitle.values(),
          ...goldenFlagsByTitle.keys(),
          ...goldenFlagsByTitle.values(),
          'spectator hint one',
          'spectator hint two',
          'spectator hint three',
          'golden hint one',
          'golden hint two',
          'golden hint three',
        ];
        for (const privateValue of privateValues) {
          expect(serialized).not.toContain(privateValue);
        }
      };

      const assertFE041LivePublicBroadcast = async (wave: FullStackWave): Promise<void> => {
        const liveSeriesIDs = [...new Set(
          wave.members.flatMap((member) => member.series_id ? [member.series_id] : []),
        )];
        expect(liveSeriesIDs, 'the active Swiss Wave must expose eight simultaneous Series').toHaveLength(8);

        const publicContext = await browser.newContext({ baseURL: frontendURL });
        spectatorContext = publicContext;
        let serverDate = '';
        await expect.poll(async () => {
          const response = await publicContext.request.get(
            `${backendURL}/api/v1/tournaments/${tournament.id}/snapshot`,
            { headers: { Origin: frontendURL } },
          );
          if (response.status() !== 200) {
            return false;
          }
          serverDate = response.headers()['date'] ?? '';
          const candidate = (await response.json()) as FullStackPublicSnapshot;
          return candidate.live_series.filter((series) => (
            liveSeriesIDs.includes(series.series_id) &&
            series.state === 'active' &&
            series.current_game?.state === 'active'
          )).length === liveSeriesIDs.length;
        }, { timeout: 15_000 }).toBe(true);
        const snapshotResponse = await publicContext.request.get(
          `${backendURL}/api/v1/tournaments/${tournament.id}/snapshot`,
          { headers: { Origin: frontendURL } },
        );
        expect(snapshotResponse.status()).toBe(200);
        serverDate = snapshotResponse.headers()['date'] ?? serverDate;
        const publicSnapshot = (await snapshotResponse.json()) as FullStackPublicSnapshot;
        expect(serverDate, 'public recovery must provide an HTTP Date authority').toBeTruthy();
        assertFE041PublicPayloadSafe(publicSnapshot);
        expect(publicSnapshot.tournament.tournament_id).toBe(tournament.id);

        const liveSeries = publicSnapshot.live_series.filter((series) => liveSeriesIDs.includes(series.series_id));
        expect(liveSeries).toHaveLength(liveSeriesIDs.length);
        expect(new Set(liveSeries.map((series) => series.series_id)).size).toBe(liveSeriesIDs.length);
        for (const series of liveSeries) {
          const currentGame = series.current_game;
          expect(currentGame, `Series ${series.series_id} did not expose its current Game`).not.toBeNull();
          if (!currentGame) {
            throw new Error(`Series ${series.series_id} did not expose its current Game`);
          }
          expect(series.state).toBe('active');
          expect(currentGame.state).toBe('active');
          expect(currentGame.started_at).toBeTruthy();
          expect(currentGame.effective_deadline).toBeTruthy();

          const activeMember = wave.members.find((member) => member.series_id === series.series_id);
          expect(activeMember, `Series ${series.series_id} did not expose an active participant`).toBeDefined();
          if (!activeMember) {
            throw new Error(`Series ${series.series_id} did not expose an active participant`);
          }
          const participant = await readParticipantSnapshotViaApi(
            contextForParticipant(activeMember.participant_id),
            tournament.id,
          );
          expect(participant.assignment?.context.series_id).toBe(series.series_id);
          expect(participant.assignment?.active_snapshot.category).toBe(currentGame.category);
          expect(participant.assignment?.active_snapshot.time_limit).toBe(normalTimeLimitSeconds);
          expect(participant.assignment?.context.game_state).toBe(currentGame.state);
          expect(participant.assignment?.context.effective_deadline).toBe(currentGame.effective_deadline);
          if (!currentGame.started_at || !currentGame.effective_deadline || !participant.assignment) {
            throw new Error(`Series ${series.series_id} did not expose authoritative game timing`);
          }
          expect(
            Date.parse(currentGame.effective_deadline) - Date.parse(currentGame.started_at),
          ).toBe(180_000);
        }

        const publicRequests: Array<{ headers: Record<string, string>; url: string }> = [];
        const publicFrames: string[] = [];
        let pageServerDate = '';
        const publicPage = await publicContext.newPage();
        spectatorPage = publicPage;
        publicPage.setDefaultTimeout(15_000);
        publicPage.on('request', (requestEvent) => {
          if (requestEvent.url().includes(`/api/v1/tournaments/${tournament.id}/`)) {
            publicRequests.push({ headers: requestEvent.headers(), url: requestEvent.url() });
          }
        });
        publicPage.on('response', (response) => {
          const url = new URL(response.url());
          if (
            pageServerDate === '' &&
            response.request().method() === 'GET' &&
            url.pathname === `/api/v1/tournaments/${tournament.id}/snapshot` &&
            response.status() === 200
          ) {
            pageServerDate = response.headers().date ?? '';
          }
        });
        publicPage.on('websocket', (socket) => {
          if (!new URL(socket.url()).pathname.endsWith(`/api/v1/tournaments/${tournament.id}/realtime`)) {
            return;
          }
          socket.on('framereceived', (frame) => {
            publicFrames.push(
              typeof frame.payload === 'string' ? frame.payload : frame.payload.toString('utf8'),
            );
          });
        });
        await publicPage.clock.install({ time: new Date(serverDate).getTime() });
        await publicPage.goto(`/arena/spectator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
        const broadcast = publicPage.getByTestId('tournament-broadcast');
        await expect(broadcast).toBeVisible();
        await expect.poll(() => publicRequests.length).toBeGreaterThan(0);
        await expect.poll(() => pageServerDate).not.toBe('');
        for (const publicRequest of publicRequests) {
          expect(publicRequest.headers.authorization).toBeUndefined();
          expect(publicRequest.headers['x-csrf-token']).toBeUndefined();
          expect(publicRequest.headers.cookie ?? '').not.toContain('tpm_');
        }
        expect(
          (await publicContext.cookies()).some((cookie) => cookie.name.startsWith('tpm_')),
        ).toBe(false);
        await expectNoSensitiveAuthStorage(publicPage);

        const seriesButtons = broadcast.locator('button[data-match-key^="series:"]');
        await expect(seriesButtons).toHaveCount(liveSeries.length);
        const firstSeries = liveSeries[0];
        const secondSeries = liveSeries[1];
        if (!firstSeries || !secondSeries || !firstSeries.current_game || !secondSeries.current_game) {
          throw new Error('public live Series fixture did not contain two current Games');
        }
        await seriesButtons.nth(0).click();
        const selectedSeries = broadcast.getByTestId('broadcast-selected-series');
        const selectedGame = broadcast.getByTestId('broadcast-selected-game');
        await expect(selectedGame).toHaveAttribute('data-series-id', firstSeries.series_id);
        await expect(selectedGame).toHaveAttribute(
          'data-deadline',
          firstSeries.current_game.effective_deadline ?? '',
        );
        await expect(selectedSeries).toContainText(
          `${firstSeries.first_display_name} - ${firstSeries.second_display_name}`,
        );
        await expect(selectedGame.getByRole('heading', { name: `Игра ${firstSeries.current_game.position}` }))
          .toBeVisible();
        await expect(selectedGame).toContainText('Идет');
        await expect(selectedGame.getByTestId('broadcast-game-category'))
          .toHaveText(firstSeries.current_game.category);
        await expect(selectedGame.getByTestId('broadcast-game-deadline').locator('time'))
          .toHaveAttribute('datetime', firstSeries.current_game.effective_deadline ?? '');
        const countdownBefore = await readBroadcastCountdownSeconds(publicPage);
        const expectedCountdown = Math.ceil(Math.max(
          0,
          Date.parse(firstSeries.current_game.effective_deadline ?? '') - Date.parse(pageServerDate),
        ) / 1_000);
        expect(countdownBefore).toBeLessThanOrEqual(expectedCountdown);
        expect(countdownBefore).toBeGreaterThanOrEqual(expectedCountdown - 3);
        expect(countdownBefore).toBeGreaterThan(0);
        await publicPage.clock.fastForward(1_000);
        await expect.poll(() => readBroadcastCountdownSeconds(publicPage)).toBeLessThan(countdownBefore);

        await seriesButtons.nth(1).click();
        await expect(selectedGame).toHaveAttribute('data-series-id', secondSeries.series_id);
        await expect(selectedGame).toHaveAttribute(
          'data-deadline',
          secondSeries.current_game.effective_deadline ?? '',
        );
        await expect(selectedSeries).toContainText(
          `${secondSeries.first_display_name} - ${secondSeries.second_display_name}`,
        );
        await expect(selectedGame.getByRole('heading', { name: `Игра ${secondSeries.current_game.position}` }))
          .toBeVisible();
        await expect(selectedGame).toContainText('Идет');
        await expect(selectedGame.getByTestId('broadcast-game-category'))
          .toHaveText(secondSeries.current_game.category);
        await expect(selectedGame.getByTestId('broadcast-game-deadline').locator('time'))
          .toHaveAttribute('datetime', secondSeries.current_game.effective_deadline ?? '');
        const secondCountdown = await readBroadcastCountdownSeconds(publicPage);
        const expectedSecondCountdown = Math.max(0, Math.ceil(Math.max(
          0,
          Date.parse(secondSeries.current_game.effective_deadline ?? '') - Date.parse(pageServerDate),
        ) / 1_000) - 1);
        expect(secondCountdown).toBeLessThanOrEqual(expectedSecondCountdown + 1);
        expect(secondCountdown).toBeGreaterThanOrEqual(expectedSecondCountdown - 2);
        await expect.poll(() => new URL(publicPage.url()).searchParams.get('match'))
          .toBe(`series:${secondSeries.series_id}`);

        const fullscreenButton = broadcast.getByTestId('broadcast-fullscreen-toggle');
        await expect(fullscreenButton).toBeVisible();
        const fullscreenSupported = await fullscreenButton.isEnabled();
        if (fullscreenSupported) {
          await fullscreenButton.click();
          await expect.poll(() => publicPage.evaluate(() => document.fullscreenElement !== null)).toBe(true);
          await fullscreenButton.click();
          await expect.poll(() => publicPage.evaluate(() => document.fullscreenElement === null)).toBe(true);
        } else {
          await expect(fullscreenButton).toBeDisabled();
        }

        await expect.poll(() => publicFrames.length).toBeGreaterThan(0);
        for (const frame of publicFrames) {
          const payload = JSON.parse(frame) as unknown;
          assertFE041PublicPayloadSafe(payload);
        }
        await expect(broadcast).not.toContainText('flag{');
        await expect(broadcast).not.toContainText('подсказ');
        await expect(
          broadcast.locator('[data-task-id], [data-participant-id], [data-operator-action]'),
        ).toHaveCount(0);
      };

      const knownSwissWaveIDs = new Set<string>();
      for (const roundNumber of [1, 2, 3, 4]) {
        const wave = await prepareSwissRound(roundNumber, knownSwissWaveIDs);
        knownSwissWaveIDs.add(wave.id);
        if (roundNumber === 1) {
          await assertFE041LivePublicBroadcast(wave);
        }
        await settleWave(wave);
        if (roundNumber === 1) {
          const completedSeriesID = wave.members.find((member) => member.series_id)?.series_id;
          expect(completedSeriesID, 'the first live Wave did not expose a completed Series candidate').toBeTruthy();
          const publicContext = spectatorContext;
          if (!completedSeriesID || !publicContext) {
            throw new Error('the first live Wave did not expose a completed Series candidate');
          }
          await expect.poll(async () => {
            const snapshot = await readPublicSnapshotViaApi(publicContext, tournament.id);
            return snapshot.official_results.some((result) => (
              result.series_id === completedSeriesID && result.state === 'completed'
            ));
          }, { timeout: 15_000 }).toBe(true);
        }
      }

      type GoldenGroup = {
        attempt_id: string;
        group_id: string;
        members: Array<{ participant_id: string }>;
        ready_window_id: string;
        runtime_revision: number;
        state: string;
      };
      type GoldenOperatorState = { groups: GoldenGroup[] };
      type GoldenParticipantState = {
        attempt_id: string;
        ready_window_id: string;
        runtime_revision: number;
        state: string;
        task: { title: string } | null;
      };

      const startGolden = await applyStartGoldenViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
      );
      expect(startGolden.status(), `start Golden failed with ${startGolden.status()} ${startGolden.statusText()}`).toBe(200);

      const openGolden = await request.post(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden/open`,
        {
          headers: {
            'X-CSRF-Token': adminSession.access_csrf_token,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            expected_projection_revision: await getOperatorProjectionRevisionViaApi(
              request,
              tournament.id,
            ),
            expected_runtime_revision: 0,
          },
        },
      );
      expect(openGolden.status(), `open Golden failed with ${openGolden.status()} ${openGolden.statusText()}`).toBe(200);
      const openedGolden = (await openGolden.json()) as GoldenOperatorState;
      expect(openedGolden.groups.length, 'Golden must resolve the Swiss ordering ties').toBeGreaterThan(0);

      const readGoldenParticipant = async (
        participantID: string,
      ): Promise<GoldenParticipantState> => {
        const context = contextForParticipant(participantID);
        const response = await context.request.get(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden`,
          { headers: { Origin: frontendURL } },
        );
        expect(response.status(), `read Golden participant failed with ${response.status()}`).toBe(200);
        return (await response.json()) as GoldenParticipantState;
      };

      for (const group of openedGolden.groups) {
        for (const member of group.members) {
          const context = contextForParticipant(member.participant_id);
          const participant = await readGoldenParticipant(member.participant_id);
          const csrfToken = (await context.cookies()).find(
            (cookie) => cookie.name === 'tpm_player_csrf',
          )?.value;
          expect(csrfToken, `Golden participant ${member.participant_id} lost CSRF`).toBeTruthy();
          const ready = await context.request.post(
            `${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden/ready`,
            {
              headers: {
                'X-CSRF-Token': csrfToken ?? '',
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: {
                attempt_id: participant.attempt_id,
                expected_runtime_revision: participant.runtime_revision,
                ready: true,
                ready_window_id: participant.ready_window_id,
              },
            },
          );
          expect(ready.status(), `Golden readiness failed with ${ready.status()} ${ready.statusText()}`).toBe(200);
        }
      }

      for (const expectedGroup of openedGolden.groups) {
        const operatorResponse = await request.get(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`,
          { headers: { Origin: frontendURL } },
        );
        expect(operatorResponse.status()).toBe(200);
        const operator = (await operatorResponse.json()) as GoldenOperatorState;
        const group = operator.groups.find((candidate) => candidate.group_id === expectedGroup.group_id);
        expect(group, `Golden group ${expectedGroup.group_id} disappeared`).toBeDefined();
        if (!group) {
          throw new Error(`Golden group ${expectedGroup.group_id} disappeared`);
        }
        expect(group.state).toBe('ready');
        const startAttempt = await request.post(
          `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden/attempts/${group.attempt_id}/start`,
          {
            headers: {
              'X-CSRF-Token': adminSession.access_csrf_token,
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              expected_runtime_revision: group.runtime_revision,
              ready_window_id: group.ready_window_id,
            },
          },
        );
        expect(startAttempt.status(), `start Golden attempt failed with ${startAttempt.status()} ${startAttempt.statusText()}`).toBe(200);

        for (const member of group.members) {
          const context = contextForParticipant(member.participant_id);
          const participant = await readGoldenParticipant(member.participant_id);
          expect(participant.task, `Golden task missing for ${member.participant_id}`).not.toBeNull();
          const flag = participant.task ? goldenFlagsByTitle.get(participant.task.title) : undefined;
          expect(flag, `Golden flag missing for ${participant.task?.title ?? 'unknown task'}`).toBeTruthy();
          const csrfToken = (await context.cookies()).find(
            (cookie) => cookie.name === 'tpm_player_csrf',
          )?.value;
          expect(csrfToken, `Golden participant ${member.participant_id} lost CSRF`).toBeTruthy();
          const submission = await context.request.post(
            `${backendURL}/api/v1/tournaments/${tournament.id}/participant/golden/submissions`,
            {
              headers: {
                'X-CSRF-Token': csrfToken ?? '',
                'Idempotency-Key': randomUUID(),
                Origin: frontendURL,
              },
              data: {
                attempt_id: participant.attempt_id,
                expected_runtime_revision: participant.runtime_revision,
                ready_window_id: participant.ready_window_id,
                submitted_flag: flag ?? '',
              },
            },
          );
          expect(submission.status(), `Golden submission failed with ${submission.status()} ${submission.statusText()}`).toBe(200);
        }
      }

      const completedGoldenResponse = await request.get(
        `${backendURL}/api/v1/admin/tournaments/${tournament.id}/golden`,
        { headers: { Origin: frontendURL } },
      );
      expect(completedGoldenResponse.status()).toBe(200);
      const completedGolden = (await completedGoldenResponse.json()) as GoldenOperatorState;
      expect(completedGolden.groups.every((group) => group.state === 'completed')).toBe(true);


      const startPlayoffs = await applyStartPlayoffsViaApi(
        request,
        tournament.id,
        adminSession.access_csrf_token,
      );
      expect(
        startPlayoffs.status(),
        `start playoffs failed with ${startPlayoffs.status()} ${startPlayoffs.statusText()}`,
      ).toBe(200);

      const readPlayoffSnapshot = async (): Promise<FullStackPlayoffSnapshot> => {
        const snapshot = await readOperatorSnapshotViaApi<FullStackPlayoffSnapshot>(
          request,
          tournament.id,
        );
        expect(snapshot.series).toBeDefined();
        expect(snapshot.tournament.id).toBe(tournament.id);
        return snapshot;
      };

      const waveForSeries = (
        snapshot: FullStackPlayoffSnapshot,
        seriesID: string,
      ): FullStackWave | undefined => {
        let completedWave: FullStackWave | undefined;
        for (const wave of snapshot.waves) {
          if (!wave.members.some((member) => member.series_id === seriesID)) {
            continue;
          }
          if (wave.state !== 'completed') {
            return wave;
          }
          completedWave = wave;
        }
        return completedWave;
      };

      const runPlayoffSeries = async (
        seriesID: string,
        semifinalWinner: 'first' | 'second',
      ): Promise<void> => {
        for (let iteration = 0; iteration < 8; iteration += 1) {
          let snapshot = await readPlayoffSnapshot();
          const series = snapshot.series.find((candidate) => candidate.id === seriesID);
          expect(series, `playoff series ${seriesID} disappeared`).toBeDefined();
          if (!series) {
            throw new Error(`playoff series ${seriesID} disappeared`);
          }
          if (series.state === 'completed') {
            expect(series.winner_id).toBeTruthy();
            return;
          }

          let wave = waveForSeries(snapshot, seriesID);
          expect(wave, `playoff series ${seriesID} did not expose a Wave`).toBeDefined();
          if (!wave) {
            throw new Error(`playoff series ${seriesID} did not expose a Wave`);
          }
          const waveProjectionRevision = snapshot.next_cursor.projection_revision;
          if (wave.state === 'planned') {
            wave = await waveAction(wave.id, 'open_ready_window', waveProjectionRevision);
          }
          if (wave.state === 'ready_window_open' || wave.state === 'ready') {
            for (const member of wave.members.filter((candidate) => candidate.series_id === seriesID)) {
              const context = contextForParticipant(member.participant_id);
              const participant = await readParticipantSnapshotViaApi(context, tournament.id);
              const csrfToken = (await context.cookies()).find(
                (cookie) => cookie.name === 'tpm_player_csrf',
              )?.value;
              expect(csrfToken, `playoff participant ${member.participant_id} did not retain CSRF`).toBeTruthy();
              const readiness = await context.request.post(
                `${backendURL}/api/v1/tournaments/${tournament.id}/participant/waves/${wave.id}/ready`,
                {
                  headers: {
                    'X-CSRF-Token': csrfToken ?? '',
                    'Idempotency-Key': randomUUID(),
                    Origin: frontendURL,
                  },
                  data: {
                    expected_projection_revision: participant.projection_revision,
                    ready: true,
                  },
                },
              );
              expect(readiness.status(), `playoff readiness failed with ${readiness.status()}`).toBe(200);
            }
            wave = await waveAction(wave.id, 'start', waveProjectionRevision);
          }
          expect(wave.state).toBe('active');

          const activeSeries = snapshot.series.find((candidate) => candidate.id === seriesID) ?? series;
          let winnerParticipantID = activeSeries.first_participant_id;
          if (activeSeries.format === 'bo1') {
            winnerParticipantID = semifinalWinner === 'first'
              ? activeSeries.first_participant_id
              : activeSeries.second_participant_id;
          } else {
            const nextGameNumber =
              activeSeries.score.first_participant_wins + activeSeries.score.second_participant_wins + 1;
            winnerParticipantID = nextGameNumber === 2
              ? activeSeries.second_participant_id
              : activeSeries.first_participant_id;
          }
          const winnerMember = wave.members.find(
            (member) => member.series_id === seriesID && member.participant_id === winnerParticipantID,
          );
          expect(winnerMember, `playoff winner ${winnerParticipantID} was not in Wave ${wave.id}`).toBeDefined();
          if (!winnerMember) {
            throw new Error(`playoff winner ${winnerParticipantID} was not in Wave ${wave.id}`);
          }
          await submitCorrectAssignment(contextForParticipant(winnerParticipantID), seriesID);

          snapshot = await readPlayoffSnapshot();
          if (snapshot.tournament.state === 'completed') {
            const completedSeries = snapshot.series.find(
              (candidate) => candidate.id === seriesID,
            );
            expect(completedSeries?.state).toBe('completed');
            expect(completedSeries?.winner_id).toBeTruthy();
            return;
          }
          const currentWave = snapshot.waves.find((candidate) => candidate.id === wave?.id);
          if (currentWave && currentWave.state !== 'completed') {
            await waveAction(currentWave.id, 'complete');
          }
        }
        throw new Error(`playoff series ${seriesID} did not settle`);
      };

      let playoffSnapshot = await readPlayoffSnapshot();
      const semifinalSeries = playoffSnapshot.series.filter(
        (series) => series.format === 'bo1' && series.state !== 'completed' && series.winner_id === null,
      );
      expect(semifinalSeries).toHaveLength(2);
      for (const [index, semifinal] of semifinalSeries.entries()) {
        await runPlayoffSeries(semifinal.id, index === 0 ? 'first' : 'second');
      }

      let finalDraft: FullStackDraft | null = null;
      for (let attempt = 0; attempt < 20 && !finalDraft; attempt += 1) {
        for (const context of playerContexts) {
          const participant = await readParticipantSnapshotViaApi(context, tournament.id);
          if (participant.draft?.state === 'active') {
            finalDraft = participant.draft;
            break;
          }
        }
        if (!finalDraft) {
          await new Promise((resolve) => setTimeout(resolve, 250));
        }
      }
      expect(finalDraft, 'final BO3 draft did not become active').not.toBeNull();
      if (!finalDraft) {
        throw new Error('final BO3 draft did not become active');
      }
      const activeFinalDraft = finalDraft;

      const draftContext = spectatorContext;
      const draftPage = spectatorPage;
      expect(draftContext, 'FE-041 spectator context was not created during the live Wave').toBeDefined();
      expect(draftPage, 'FE-041 spectator page was not created during the live Wave').toBeDefined();
      if (!draftContext || !draftPage) {
        throw new Error('FE-041 spectator page was not created during the live Wave');
      }
      await draftPage.goto(
        `/arena/spectator/${tournament.id}?match=series:${activeFinalDraft.series_id}`,
        { waitUntil: 'domcontentloaded' },
      );
      const draftSnapshotHolder: {
        receivedAt?: number;
        requestedAt?: number;
        value?: FullStackPublicSnapshot;
      } = {};
      await expect.poll(async () => {
        const requestedAt = Date.now();
        const candidate = await readPublicSnapshotViaApi(draftContext, tournament.id);
        draftSnapshotHolder.requestedAt = requestedAt;
        draftSnapshotHolder.receivedAt = Date.now();
        draftSnapshotHolder.value = candidate;
        const draft = candidate.live_draft;
        return draft?.series_id === activeFinalDraft.series_id && draft.state === 'active';
      }, { timeout: 15_000 }).toBe(true);
      const draftPublicSnapshot = draftSnapshotHolder.value;
      expect(draftPublicSnapshot, 'public recovery did not return a draft snapshot').toBeDefined();
      if (!draftPublicSnapshot) {
        throw new Error('public recovery did not return a draft snapshot');
      }
      const initialPublicDraft = draftPublicSnapshot.live_draft;
      expect(initialPublicDraft, 'public recovery did not expose the active BO3 draft').not.toBeNull();
      if (!initialPublicDraft) {
        throw new Error('public recovery did not expose the active BO3 draft');
      }
      let publicDraft: FullStackPublicDraft = initialPublicDraft;
      assertFE041PublicPayloadSafe(publicDraft);
      expect(publicDraft.series_id).toBe(activeFinalDraft.series_id);
      expect(publicDraft.tournament_id).toBe(tournament.id);
      expect(publicDraft.format).toBe('bo3');
      expect(publicDraft.state).toBe('active');
      expect(publicDraft.first_actor_display_name).toBeTruthy();
      expect(publicDraft.current_turn).toBeGreaterThanOrEqual(activeFinalDraft.turn);
      expect(publicDraft.current_action).toBeTruthy();
      expect(publicDraft.current_actor_display_name).toBeTruthy();
      expect(publicDraft.turn_deadline).toBeTruthy();
      for (const action of publicDraft.actions) {
        expect(action.automatic).toEqual(expect.any(Boolean));
      }
      if (!publicDraft.turn_deadline) {
        throw new Error('public draft did not expose its server-authoritative turn deadline');
      }
      const draftDeadline = Date.parse(publicDraft.turn_deadline);
      expect(Number.isFinite(draftDeadline)).toBe(true);
      if (publicDraft.auto_action_pending) {
        expect(draftDeadline).toBeLessThanOrEqual(draftSnapshotHolder.receivedAt ?? Date.now());
      } else {
        expect(draftDeadline).toBeGreaterThan(draftSnapshotHolder.requestedAt ?? 0);
      }

      await draftPage.goto(
        `/arena/spectator/${tournament.id}?match=series:${publicDraft.series_id}`,
        { waitUntil: 'domcontentloaded' },
      );
      const draftBroadcast = draftPage.getByTestId('tournament-broadcast');
      const draftPanel = draftBroadcast.getByTestId('broadcast-draft');
      await expect(draftPanel).toBeVisible();
      await expect(draftPanel).toContainText('Идет');
      let synchronizedPublicDraft: FullStackPublicDraft | undefined;
      await expect.poll(async () => {
        const candidate = (await readPublicSnapshotViaApi(draftContext, tournament.id)).live_draft;
        if (
          candidate === null ||
          candidate.series_id !== publicDraft.series_id ||
          candidate.state !== 'active' ||
          candidate.turn_deadline === null ||
          !Number.isFinite(Date.parse(candidate.turn_deadline))
        ) {
          return false;
        }
        const [panelSeriesID, panelDeadline] = await Promise.all([
          draftPanel.getAttribute('data-series-id'),
          draftPanel.getAttribute('data-deadline'),
        ]);
        if (panelSeriesID !== candidate.series_id || panelDeadline !== candidate.turn_deadline) {
          return false;
        }
        synchronizedPublicDraft = candidate;
        return true;
      }, {
        message: 'broadcast draft did not converge on the latest server deadline',
        timeout: 15_000,
        intervals: [100, 250, 500, 1_000],
      }).toBe(true);
      if (!synchronizedPublicDraft) {
        throw new Error('broadcast draft did not expose a synchronized server deadline');
      }
      publicDraft = synchronizedPublicDraft;
      if (!publicDraft.turn_deadline) {
        throw new Error('broadcast draft did not expose a synchronized server deadline');
      }
      const synchronizedTurnDeadline = publicDraft.turn_deadline;
      await expect(draftPanel).toHaveAttribute('data-series-id', publicDraft.series_id);
      await expect(draftPanel).toHaveAttribute(
        'data-deadline',
        /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/,
      );
      await expect(draftPanel).toHaveAttribute('data-deadline', synchronizedTurnDeadline);
      await expect(draftPanel.getByTestId('broadcast-draft-first-actor'))
        .toHaveText(publicDraft.first_actor_display_name);
      await expect(draftPanel.getByTestId('broadcast-draft-current-actor'))
        .toHaveText(publicDraft.current_actor_display_name ?? 'Не объявлен');
      await expect(draftPanel.getByTestId('broadcast-draft-turn'))
        .toHaveText(String(publicDraft.current_turn ?? '-'));
      const currentActionLabel = publicDraft.current_action === 'ban'
        ? 'Бан'
        : publicDraft.current_action === 'pick'
          ? 'Выбор'
          : publicDraft.current_action ?? 'Не объявлено';
      await expect(draftPanel.getByTestId('broadcast-draft-current-action'))
        .toHaveText(currentActionLabel);
      await expect(draftPanel.getByTestId('broadcast-draft-deadline')).not.toHaveText('Не объявлен');
      await expect(draftPanel.getByTestId('broadcast-draft-deadline').locator('time'))
        .toHaveAttribute('datetime', synchronizedTurnDeadline);
      for (const action of publicDraft.actions.filter((item) => item.automatic)) {
        await expect(draftPanel.getByTestId(`broadcast-draft-action-${action.turn}`))
          .toHaveAttribute('data-automatic', 'true');
      }

      while (finalDraft.state === 'active') {
        let actorContext: BrowserContext | undefined;
        let participant: FullStackParticipantSnapshot | undefined;
        let currentDraft: FullStackDraft | null | undefined;
        for (let refresh = 0; refresh < 3; refresh += 1) {
          expect(finalDraft.current_actor_id).toBeTruthy();
          expect(finalDraft.current_action).toBeTruthy();
          if (!finalDraft.current_actor_id || !finalDraft.current_action) {
            throw new Error('final draft did not expose its server-authoritative actor and action');
          }
          actorContext = contextForParticipant(finalDraft.current_actor_id);
          participant = await readParticipantSnapshotViaApi(actorContext, tournament.id);
          expect(participant.draft?.id).toBe(finalDraft.id);
          currentDraft = participant.draft;
          expect(currentDraft).not.toBeNull();
          if (!currentDraft) {
            throw new Error('final draft disappeared from participant snapshot');
          }
          if (currentDraft.state !== 'active' || currentDraft.current_actor_id === finalDraft.current_actor_id) {
            break;
          }
          if (refresh === 2) {
            throw new Error('final draft actor did not converge to a fresh participant snapshot');
          }
          finalDraft = currentDraft;
        }
        if (!actorContext || !participant || !currentDraft) {
          throw new Error('final draft actor did not converge to a fresh participant snapshot');
        }
        if (currentDraft.state !== 'active') {
          finalDraft = currentDraft;
          continue;
        }
        expect(currentDraft.current_actor_id).toBeTruthy();
        expect(currentDraft.current_action).toBeTruthy();
        if (!currentDraft.current_actor_id || !currentDraft.current_action) {
          throw new Error('fresh final draft did not expose its server-authoritative actor and action');
        }
        expect(participant.lobby?.participant_id).toBe(currentDraft.current_actor_id);
        const turnDeadline = currentDraft.turn_deadline === null
          ? Number.NaN
          : Date.parse(currentDraft.turn_deadline);
        if (Number.isFinite(turnDeadline) && turnDeadline - Date.now() <= 2_000) {
          let automaticDraft: FullStackDraft | undefined;
          await expect.poll(async () => {
            const candidate = (await readParticipantSnapshotViaApi(actorContext, tournament.id)).draft;
            if (
              !candidate ||
              candidate.id !== currentDraft.id ||
              candidate.revision <= currentDraft.revision
            ) {
              return false;
            }
            automaticDraft = candidate;
            return true;
          }, { timeout: 15_000 }).toBe(true);
          if (!automaticDraft) {
            throw new Error(`final draft turn ${currentDraft.turn} did not resolve after its deadline`);
          }
          expect(automaticDraft.actions.at(-1)?.automatic).toBe(true);
          finalDraft = automaticDraft;
          continue;
        }
        const preferredCategories = currentDraft.turn <= 2
          ? (['pwn', 'forensics'] as const)
          : ([] as const);
        const category = preferredCategories.find((candidate) =>
          currentDraft.legal_categories.includes(candidate)) ?? currentDraft.legal_categories[0];
        expect(category, `final draft turn ${currentDraft.turn} did not expose a legal category`).toBeTruthy();
        if (!category) {
          throw new Error(`final draft turn ${currentDraft.turn} did not expose a legal category`);
        }
        const csrfToken = (await actorContext.cookies()).find(
          (cookie) => cookie.name === 'tpm_player_csrf',
        )?.value;
        expect(csrfToken, 'final draft actor did not retain CSRF').toBeTruthy();
        const draftAction = await actorContext.request.post(
          `${backendURL}/api/v1/tournaments/${tournament.id}/participant/series/${currentDraft.series_id}/draft/actions`,
          {
            headers: {
              'X-CSRF-Token': csrfToken ?? '',
              'Idempotency-Key': randomUUID(),
              Origin: frontendURL,
            },
            data: {
              expected_projection_revision: participant.projection_revision,
              expected_draft_revision: currentDraft.revision,
              expected_turn: currentDraft.turn,
              action: currentDraft.current_action,
              category,
            },
          },
        );
        expect(draftAction.status(), `final draft action failed with ${draftAction.status()}`).toBe(200);
        finalDraft = (await draftAction.json()) as FullStackDraft;
      }
      expect(finalDraft.state).toBe('completed');

      playoffSnapshot = await readPlayoffSnapshot();
      const finalSeries = playoffSnapshot.series.find(
        (series) => series.id === finalDraft?.series_id,
      );
      expect(finalSeries, 'completed final draft did not expose its final Series').toBeDefined();
      if (!finalSeries) {
        throw new Error('completed final draft did not expose its final Series');
      }
      expect(finalSeries.format).toBe('bo3');
      await runPlayoffSeries(finalSeries.id, 'first');

      const terminalContext = spectatorContext;
      const terminalPage = spectatorPage;
      expect(terminalContext, 'FE-041 spectator context was lost before terminal public recovery').toBeDefined();
      expect(terminalPage, 'FE-041 spectator page was lost before terminal public recovery').toBeDefined();
      if (!terminalContext || !terminalPage) {
        throw new Error('FE-041 spectator page was lost before terminal public recovery');
      }
      const snapshotHolder: { value?: FullStackPublicSnapshot } = {};
      await expect.poll(async () => {
        const candidate = await readPublicSnapshotViaApi(terminalContext, tournament.id);
        snapshotHolder.value = candidate;
        return candidate.tournament.state === 'completed' && candidate.official_results.some(
          (result) => result.series_id === finalSeries.id && result.state === 'completed',
        );
      }, { timeout: 15_000 }).toBe(true);
      const publicSnapshot = snapshotHolder.value;
      if (!publicSnapshot) {
        throw new Error('public recovery did not expose the terminal snapshot');
      }
      assertFE041PublicPayloadSafe(publicSnapshot);
      expect(publicSnapshot.tournament.state).toBe('completed');
      expect(publicSnapshot.swiss_rounds).toHaveLength(4);
      expect(publicSnapshot.swiss_rounds.filter((round) => round.bye !== null)).toHaveLength(0);
      const swissSeries = publicSnapshot.live_series.filter((series) => series.stage === 'swiss');
      expect(swissSeries).toHaveLength(32);
      const firstRoundSeries = swissSeries.filter((series) => series.round_number === 1);
      expect(firstRoundSeries).toHaveLength(8);
      expect(firstRoundSeries.every((series) => (
        series.state === 'completed' &&
        series.score.first_wins + series.score.second_wins > 0 &&
        series.score.first_wins !== series.score.second_wins
      ))).toBe(true);

      const bracketMatches = publicSnapshot.bracket.matches;
      expect(bracketMatches).toHaveLength(3);
      expect(bracketMatches.map((match) => `${match.stage}:${match.position}`).sort()).toEqual([
        'final:1',
        'semifinal:1',
        'semifinal:2',
      ]);
      const publicFinal = bracketMatches.find(
        (match) => match.stage === 'final' && match.position === 1,
      );
      expect(publicFinal).toBeDefined();
      if (!publicFinal) {
        throw new Error('public snapshot did not expose the final');
      }
      expect(publicFinal.state).toBe('completed');
      expect(publicFinal.score).toEqual({ first_participant_wins: 2, second_participant_wins: 1 });
      expect(publicFinal.winner_display_name).toBe(publicFinal.first_display_name);
      const champion = publicFinal.winner_display_name;
      expect(champion).toBeTruthy();
      if (!champion) {
        throw new Error('public final did not expose a server-derived champion');
      }
      const finalOfficialResult = publicSnapshot.official_results.find(
        (result) => result.series_id === finalSeries.id,
      );
      expect(finalOfficialResult, 'public recovery did not expose the final official result').toBeDefined();
      if (!finalOfficialResult) {
        throw new Error('public recovery did not expose the final official result');
      }
      expect(finalOfficialResult.state).toBe('completed');
      expect(finalOfficialResult.winner_display_name).toBe(champion);

      const scoreboardResponse = await terminalContext.request.get(
        `${backendURL}/api/v1/tournaments/${tournament.id}/scoreboard`,
        { headers: { Origin: frontendURL } },
      );
      expect(scoreboardResponse.status(), `public scoreboard failed with ${scoreboardResponse.status()}`).toBe(200);
      const scoreboard = (await scoreboardResponse.json()) as {
        entries: Array<{ display_name: string; points: number; wins: number; losses: number }>;
        tournament_id: string;
      };
      expect(scoreboard.tournament_id).toBe(tournament.id);
      expect(scoreboard.entries).toHaveLength(16);
      const expectedScoreboard = checkedInRosterParticipants.map((participant) => {
        const player = players.find((candidate) => candidate.id === participant.player_id);
        expect(player, `missing player for participant ${participant.id}`).toBeDefined();
        if (!player) {
          throw new Error(`missing player for participant ${participant.id}`);
        }
        const points = swissPoints.get(participant.id) ?? 0;
        return { display_name: player.username, points };
      });
      expect(new Set(scoreboard.entries.map((entry) => entry.display_name)).size).toBe(16);
      for (const expected of expectedScoreboard) {
        const actual = scoreboard.entries.find((entry) => entry.display_name === expected.display_name);
        expect(actual, `scoreboard lost ${expected.display_name}`).toBeDefined();
        if (!actual) {
          throw new Error(`scoreboard lost ${expected.display_name}`);
        }
        expect(actual.points).toBe(expected.points);
        expect(actual.wins).toBe(expected.points);
        expect(actual.losses).toBe(swissRounds - expected.points);
      }
      expect(scoreboard.entries.some((entry) => entry.display_name === champion)).toBe(true);

      terminalPage.setDefaultTimeout(15_000);
      await terminalPage.goto(`/arena/spectator/${tournament.id}`, { waitUntil: 'domcontentloaded' });
      const broadcast = terminalPage.getByTestId('tournament-broadcast');
      await expect(broadcast).toBeVisible();

      await broadcast.getByRole('tab', { name: 'Swiss' }).click();
      for (const roundNumber of [1, 2, 3, 4]) {
        const round = broadcast.getByTestId(`swiss-round-${roundNumber}`);
        await expect(round).toBeVisible();
        await expect(round).toContainText('Завершена');
        await expect(round.locator('button[data-match-key]')).toHaveCount(8);
      }
      const firstRoundPanel = broadcast.getByTestId('swiss-round-1');
      await expect(firstRoundPanel).toContainText(firstRoundSeries[0]?.first_display_name ?? '');
      await expect(firstRoundPanel).toContainText(firstRoundSeries[0]?.second_display_name ?? '');
      await expect(firstRoundPanel.locator('button[data-match-key]').first()).toContainText(
        `${firstRoundSeries[0]?.score.first_wins ?? 0}:${firstRoundSeries[0]?.score.second_wins ?? 0}`,
      );
      const byeRows = broadcast.locator('[data-testid^="swiss-bye-"]');
      expect(await byeRows.count()).toBe(0);

      await broadcast.getByRole('tab', { name: 'Плей-офф' }).click();
      const playoff = broadcast.getByRole('tabpanel');
      await expect(playoff.getByRole('heading', { name: 'Top 4' })).toBeVisible();
      await expect(playoff.locator('ol').getByRole('listitem')).toHaveCount(4);
      await expect(playoff.locator('[data-testid^="playoff-bracket"]')).toHaveCount(2);
      await expect(playoff.getByTestId('playoff-final')).toHaveCount(1);
      await expect(playoff.getByTestId('playoff-final')).toContainText('2:1');
      await expect(playoff.getByTestId('playoff-final')).toContainText(`Чемпион: ${champion}`);

      await terminalPage.setViewportSize({ width: 390, height: 844 });
      await expect
        .poll(() => terminalPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
        .toBe(true);

      const finalButton = playoff.getByTestId('playoff-final').locator('button[data-match-key]');
      await finalButton.focus();
      await expect(finalButton).toBeFocused();
      await expect(finalButton).toHaveAttribute('aria-pressed', 'false');
      await finalButton.press('Enter');
      await expect(finalButton).toHaveAttribute('aria-pressed', 'true');
      await expect
        .poll(() => new URL(terminalPage.url()).searchParams.get('match'))
        .toBe('bracket:final:1');
      await expect
        .poll(() => terminalPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
        .toBe(true);
    } finally {
      ownedFixture.contexts.push(...playerContexts);
      if (spectatorContext) {
        ownedFixture.contexts.push(spectatorContext);
      }
    }
  });

  test('source upload returns a host-reachable presigned URL', async ({ request, ownedFixture }) => {
    test.setTimeout(90_000);

    const session = await adminLogin(request);
    const title = uniqueName('fullstack-source');
    const payload = Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x66, 0x73]);
    const created = await createTaskViaApi(ownedFixture, request, session, {
      title,
      description: 'Full stack source archive task.',
      category: 'forensics',
      difficulty: 'easy',
      time_limit: 90,
      flag: `flag{${title.replaceAll('-', '_')}}`,
      hints: ['first hint', 'second hint', 'third hint'],
      task_url: null,
    });

    const upload = await uploadSourceViaApi(request, session, created.id, payload);
    expect(upload.source_file_url).toContain('X-Amz-Signature');
    const sourceURL = new URL(upload.source_file_url);
    if (process.env.SEAWEEDFS_PUBLIC_ENDPOINT) {
      expect(sourceURL.host).toBe(process.env.SEAWEEDFS_PUBLIC_ENDPOINT);
    }

    const download = await request.get(upload.source_file_url, { timeout: 10_000 });
    expect(download.ok(), `source download failed with ${download.status()}`).toBeTruthy();
    expect((await download.body()).equals(payload)).toBe(true);
  });

  test('real backend publishes the public catalog and reaches participant assignment', async ({ page, browser, ownedFixture }) => {
    test.setTimeout(600_000);
    page.setDefaultTimeout(20_000);

    await loginThroughAdminUI(page);
    const adminAccessCSRF = (await page.context().cookies()).find(
      (cookie) => cookie.name === 'tpm_admin_access_csrf',
    );
    const adminAccessCSRFToken = adminAccessCSRF?.value ?? '';
    expect(adminAccessCSRFToken, 'admin login did not issue an access CSRF cookie').toBeTruthy();
    const adminRequest = page.context().request;
    const catalogPrefix = uniqueName('fullstack-catalog');

    // The start gate certifies the whole preset, including BO3 and reserve tasks.
    const normalTaskGroups: Array<{ category: FullStackTaskInput['category']; count: number }> = [
      { category: 'web', count: 27 },
      { category: 'crypto', count: 27 },
      { category: 'reverse', count: 27 },
      { category: 'forensics', count: 3 },
      { category: 'pwn', count: 3 },
    ];
    for (const group of normalTaskGroups) {
      for (let index = 0; index < group.count; index += 1) {
        const title = uniqueName(`catalog-${group.category}-${index + 1}`);
        await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
          title,
          description: 'Normal task for the public catalog assignment flow.',
          kind: 'normal',
          category: group.category,
          difficulty: 'easy',
          time_limit: 90,
          flag: `flag{${title.replaceAll('-', '_')}}`,
          hints: ['catalog hint one', 'catalog hint two', 'catalog hint three'],
          task_url: 'https://example.com/fullstack-catalog-normal',
        });
      }
    }
    for (let index = 0; index < 6; index += 1) {
      const title = uniqueName(`catalog-golden-${index + 1}`);
      await createTaskViaApi(ownedFixture, adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
        title,
        description: 'Golden task for the published tournament content flow.',
        kind: 'golden',
        category: 'web',
        difficulty: 'easy',
        time_limit: 90,
        flag: `flag{${title.replaceAll('-', '_')}}`,
        hints: ['catalog hint one', 'catalog hint two', 'catalog hint three'],
        task_url: 'https://example.com/fullstack-catalog-golden',
      });
    }

    const contentRevision = await getTournamentContentRevision(adminRequest);
    const tournaments: AdminTournament[] = [];
    const openRegistration = async (tournamentID: string): Promise<void> => {
      let response: APIResponse | null = null;
      for (let attempt = 0; attempt < 8; attempt += 1) {
        response = await applyOpenRegistrationViaApi(
          adminRequest,
          tournamentID,
          adminAccessCSRFToken,
        );
        if (response.status() === 200) {
          return;
        }
        if (response.status() !== 409 && response.status() !== 429) {
          break;
        }
        await waitForTransientRetry(attempt);
      }
      expect(response?.status(), `open registration failed for ${tournamentID}`).toBe(200);
    };

    for (let index = 0; index < 25; index += 1) {
      const tournament = await createTournamentViaApi(
        ownedFixture,
        adminRequest,
        { access_csrf_token: adminAccessCSRFToken },
        {
          name: `${catalogPrefix}-${index + 1}`,
          content_revision: contentRevision,
          planned_roster_size: 4,
          public_id: `${catalogPrefix}-public-${index + 1}`,
          preset: 'tournament_v1',
          expected_revision: 0,
        },
      );
      await openRegistration(tournament.id);
      tournaments.push(tournament);
    }

    const primaryTournament = tournaments[0];
    const secondaryTournament = tournaments[1];
    expect(primaryTournament).toBeDefined();
    expect(secondaryTournament).toBeDefined();
    if (!primaryTournament || !secondaryTournament) {
      throw new Error('catalog fixture did not create the two target tournaments');
    }

    const anonymousContext = await browser.newContext({ baseURL: frontendURL });
    ownedFixture.contexts.push(anonymousContext);
    const anonymousPage = await anonymousContext.newPage();
    const catalogLinks = anonymousPage.getByRole('link').filter({ hasText: catalogPrefix });
    await anonymousPage.goto(`/arena?q=${encodeURIComponent(catalogPrefix)}`);
    await expect(anonymousPage.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
    await expect(catalogLinks).toHaveCount(20);
    await expect(anonymousPage.getByRole('button', { name: 'Следующая страница' })).toBeVisible();
    await anonymousPage.getByLabel('Состояние').selectOption('upcoming');
    await expect(anonymousPage).toHaveURL(/group=upcoming/);
    await expect(catalogLinks).toHaveCount(20);
    await anonymousPage.getByLabel('Сортировка').selectOption('name');
    await expect(anonymousPage).toHaveURL(/sort=name/);
    await expect(catalogLinks).toHaveCount(20);
    await anonymousPage.getByRole('button', { name: 'Следующая страница' }).click();
    await expect(anonymousPage).toHaveURL(/cursor=/);
    await expect(catalogLinks).toHaveCount(5);

    await anonymousPage.goto(
      `/arena?q=${encodeURIComponent(primaryTournament.name)}&group=upcoming&sort=name`,
    );
    const primaryCatalogLink = anonymousPage
      .getByRole('link')
      .filter({
        has: anonymousPage.getByRole('heading', {
          name: primaryTournament.name,
          exact: true,
        }),
      })
      .first();
    await expect(primaryCatalogLink).toBeVisible();
    const primaryHref = await primaryCatalogLink.getAttribute('href');
    expect(primaryHref).toContain(`/arena/tournaments/${primaryTournament.public_id}`);
    expect(primaryHref).not.toContain(primaryTournament.id);
    await primaryCatalogLink.click();
    await expect(anonymousPage).toHaveURL((url) =>
      url.pathname === `/arena/tournaments/${primaryTournament.public_id}`,
    );
    await expect(
      anonymousPage.getByRole('heading', { name: primaryTournament.name, level: 1, exact: true }),
    ).toBeVisible();
    expect(anonymousPage.url()).toContain(`/arena/tournaments/${primaryTournament.public_id}`);
    expect(anonymousPage.url()).not.toContain(primaryTournament.id);
    await expect(
      anonymousPage.getByRole('link', { name: /Участник|Оператор|Наблюдатель/i }),
    ).toHaveCount(0);
    const anonymousHrefs = await anonymousPage.locator('a').evaluateAll((links) =>
      links.map((link) => link.getAttribute('href') ?? ''),
    );
    expect(anonymousHrefs.some((href) => /\/arena\/(participant|operator|spectator)\//.test(href))).toBe(false);

    type AdmissionView = {
      attendance: string | null;
      participant_id: string | null;
      player_id: string;
      roster_locked: boolean;
      status: string;
      tournament_id: string;
      tournament_state: string;
    };
    const readAdmission = async (
      context: BrowserContext,
      tournamentID: string,
    ): Promise<AdmissionView> => {
      const response = await context.request.get(
        `${backendURL}/api/v1/tournaments/${tournamentID}/participant/queue`,
        { headers: { Origin: frontendURL } },
      );
      expect(response.status(), `queue GET failed with ${response.status()}`).toBe(200);
      return (await response.json()) as AdmissionView;
    };

    type PlayerFixture = {
      context: BrowserContext;
      page: Page;
      player: FullStackPlayer;
    };
    const playerFixtures: PlayerFixture[] = [];
    for (let index = 0; index < 4; index += 1) {
      const context = await browser.newContext({ baseURL: frontendURL });
      ownedFixture.contexts.push(context);
      const playerPage = await context.newPage();
      const username = uniqueName(`catalog-player-${index + 1}`);
      if (index === 0) {
        await playerPage.goto(`/arena/tournaments/${primaryTournament.public_id}`);
        await playerPage.getByRole('button', { name: 'Участвовать', exact: true }).click();
        await playerPage.getByLabel('Никнейм', { exact: true }).fill(username);
        const queuePost = playerPage.waitForResponse((response) =>
          new URL(response.url()).pathname === `/api/v1/tournaments/${primaryTournament.id}/participant/queue`
          && response.request().method() === 'POST',
        );
        await playerPage.getByRole('button', { name: 'Зарегистрироваться', exact: true }).click();
        expect((await queuePost).status()).toBe(200);
        await expect(playerPage.getByTestId('tournament-admission-status')).toContainText('Зарегистрирован');
      } else {
        await joinAsPlayer(playerPage, username);
      }
      const meResponse = await context.request.get(`${backendURL}/api/v1/players/me`, {
        headers: { Origin: frontendURL },
      });
      expect(meResponse.status()).toBe(200);
      const me = (await meResponse.json()) as { player: FullStackPlayer };
      expect(me.player.id).toMatch(/^[0-9a-f-]{36}$/i);
      playerFixtures.push({ context, page: playerPage, player: me.player });
    }

    const joinPlayerToTournament = async (
      playerPage: Page,
      tournament: AdminTournament,
    ): Promise<void> => {
      await playerPage.goto(`/arena/tournaments/${tournament.public_id}?view=overview`);
      await expect(playerPage.getByRole('heading', { name: tournament.name, exact: true })).toBeVisible();
      const participateButton = playerPage.getByRole('button', { name: 'Участвовать', exact: true });
      await expect(participateButton).toBeVisible();
      const queuePost = playerPage.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
            `/api/v1/tournaments/${tournament.id}/participant/queue` &&
          response.request().method() === 'POST',
      );
      await participateButton.click();
      expect((await queuePost).status()).toBe(200);
      await expect(playerPage.getByTestId('tournament-admission-status')).toContainText(
        'Зарегистрирован',
      );
    };

    for (const fixture of playerFixtures.slice(1)) {
      await joinPlayerToTournament(fixture.page, primaryTournament);
    }
    const primaryFixture = playerFixtures[0];
    expect(primaryFixture).toBeDefined();
    if (!primaryFixture) {
      throw new Error('catalog fixture did not create a primary player');
    }
    await expectNoSensitiveAuthStorage(primaryFixture.page);
    const primaryCookieHeader = await cookieHeaderForPage(primaryFixture.page);
    expect(primaryCookieHeader).toContain('tpm_player_session=');
    expect(primaryCookieHeader).toContain('tpm_player_csrf=');
    const restoredPrimaryPage = await primaryFixture.context.newPage();
    await restoredPrimaryPage.addInitScript(() => {
      (window as Window & { __fullStackInitialPlayerID?: string | null }).__fullStackInitialPlayerID =
        window.sessionStorage.getItem('player_id');
    });
    await restoredPrimaryPage.goto(`/arena/tournaments/${primaryTournament.public_id}?view=overview`);
    const initialPlayerID = await restoredPrimaryPage.evaluate(
      () => (window as Window & { __fullStackInitialPlayerID?: string | null }).__fullStackInitialPlayerID ?? null,
    );
    expect(initialPlayerID, 'new page must not restore player identity from sessionStorage').toBeNull();
    await expectNoSensitiveAuthStorage(restoredPrimaryPage);
    await expect(restoredPrimaryPage.getByTestId('tournament-admission-status')).toContainText(
        'Зарегистрирован',
    );
    const restoredPlayerID = await restoredPrimaryPage.evaluate(() => window.sessionStorage.getItem('player_id'));
    expect(restoredPlayerID, 'cookie-backed restore should repopulate the current player id').toBe(
      primaryFixture.player.id,
    );
    await expect(restoredPrimaryPage.getByLabel('Никнейм')).toHaveCount(0);

    const primaryAdmission = await readAdmission(primaryFixture.context, primaryTournament.id);
    expect(primaryAdmission.tournament_id).toBe(primaryTournament.id);
    expect(primaryAdmission.player_id).toBe(primaryFixture.player.id);
    expect(primaryAdmission.status).toBe('registered');
    expect(primaryAdmission.participant_id).toMatch(/^[0-9a-f-]{36}$/i);
    expect(primaryAdmission.roster_locked).toBe(false);

    const secondaryAdmission = await readAdmission(primaryFixture.context, secondaryTournament.id);
    expect(secondaryAdmission.tournament_id).toBe(secondaryTournament.id);
    expect(secondaryAdmission.player_id).toBe(primaryFixture.player.id);
    expect(secondaryAdmission.status).toBe('not_registered');
    expect(secondaryAdmission.participant_id).toBeNull();

    const roster = await getRosterViaApi(adminRequest, primaryTournament.id);
    expect(roster.participants).toHaveLength(playerFixtures.length);
    expect(new Set(roster.participants.map((participant) => participant.player_id))).toEqual(
      new Set(playerFixtures.map((fixture) => fixture.player.id)),
    );
    const checkedIn = await replaceRosterViaApi(
      adminRequest,
      primaryTournament.id,
      adminAccessCSRFToken,
      await getOperatorProjectionRevisionViaApi(adminRequest, primaryTournament.id),
      playerFixtures.map((fixture, index) => ({
        attendance: 'checked_in' as const,
        player_id: fixture.player.id,
        seed: index + 1,
      })),
    );
    const checkedInRoster = await readRosterResponse(checkedIn, primaryTournament.id, roster.id);
    const checkedInParticipants = sortRosterParticipantsBySeed(checkedInRoster.participants);
    expect(checkedInParticipants).toHaveLength(4);
    expect(checkedInParticipants.every((participant) => participant.attendance === 'checked_in')).toBe(true);
    const preflightResponse = await runRosterPreflightViaApi(
      adminRequest,
      primaryTournament.id,
      adminAccessCSRFToken,
    );
    expect(preflightResponse.status()).toBe(200);
    const preflightReport = (await preflightResponse.json()) as FullStackPreflightReport;
    expect(preflightReport.passed, JSON.stringify(preflightReport.checks ?? [])).toBe(true);
    const locked = await lockRosterViaApi(
      adminRequest,
      primaryTournament.id,
      adminAccessCSRFToken,
      preflightReport.id,
      checkedInParticipants.map((participant) => participant.player_id),
    );
    const lockedRoster = await readRosterResponse(locked, primaryTournament.id, roster.id);
    expect(lockedRoster.locked).toBe(true);

    const startSwiss = await applyStartSwissViaApi(
      adminRequest,
      primaryTournament.id,
      adminAccessCSRFToken,
    );
    expect(startSwiss.status(), `start Swiss failed with ${startSwiss.status()}`).toBe(200);
    const startedTournament = (await startSwiss.json()) as { state?: string };
    expect(startedTournament.state).toBe('swiss');
    const secondSnapshot = await readOperatorSnapshotViaApi<OperatorSnapshotEnvelope>(
      adminRequest,
      secondaryTournament.id,
    );
    if (!secondSnapshot.tournament) {
      throw new Error('catalog fixture secondary snapshot returned no tournament');
    }
    expect(secondSnapshot.tournament?.state).toBe('registration');

    const pairingResponse = await adminRequest.post(
      `${backendURL}/api/v1/admin/tournaments/${primaryTournament.id}/pairings`,
      {
        headers: {
          'X-CSRF-Token': adminAccessCSRFToken,
          'Idempotency-Key': randomUUID(),
          Origin: frontendURL,
        },
        data: {
          categories: ['web'],
          category_mode: 'random',
          expected_projection_revision: await getOperatorProjectionRevisionViaApi(
            adminRequest,
            primaryTournament.id,
          ),
          pairing_mode: 'automatic',
          round_number: 1,
        },
      },
    );
    expect(pairingResponse.status(), `Swiss pairing failed with ${pairingResponse.status()}`).toBe(200);
    const pairing = (await pairingResponse.json()) as FullStackSwissRound;
    expect(pairing.pairings.length).toBeGreaterThan(0);

    const operatorSnapshot = await readOperatorSnapshotViaApi<FullStackOperatorSnapshot>(
      adminRequest,
      primaryTournament.id,
    );
    const wave = operatorSnapshot.waves.at(-1);
    expect(wave).toBeDefined();
    if (!wave) {
      throw new Error('catalog fixture did not materialize a Swiss wave');
    }

    const waveAction = async (
      action: 'open_ready_window' | 'start',
      knownProjectionRevision?: number,
    ): Promise<FullStackWave> => {
      const response = await adminRequest.post(
        `${backendURL}/api/v1/admin/tournaments/${primaryTournament.id}/waves/${wave.id}/actions`,
        {
          headers: {
            'X-CSRF-Token': adminAccessCSRFToken,
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            action,
            confirmed: true,
            expected_projection_revision: knownProjectionRevision ?? await getOperatorProjectionRevisionViaApi(
              adminRequest,
              primaryTournament.id,
            ),
          },
        },
      );
      expect(response.status(), `${action} Wave failed with ${response.status()}`).toBe(200);
      return (await response.json()) as FullStackWave;
    };

    const waveProjectionRevision = operatorSnapshot.next_cursor.projection_revision;
    await waveAction('open_ready_window', waveProjectionRevision);
    const contextForParticipant = (participantID: string): BrowserContext => {
      const rosterParticipant = checkedInParticipants.find(
        (participant) => participant.id === participantID,
      );
      expect(rosterParticipant).toBeDefined();
      if (!rosterParticipant) {
        throw new Error(`missing roster participant ${participantID}`);
      }
      const fixture = playerFixtures.find((candidate) => candidate.player.id === rosterParticipant.player_id);
      expect(fixture).toBeDefined();
      if (!fixture) {
        throw new Error(`missing player context for ${rosterParticipant.player_id}`);
      }
      return fixture.context;
    };

    let readinessCheckedThroughUI = false;
    for (const member of wave.members.filter((candidate) => candidate.series_id !== null)) {
      const context = contextForParticipant(member.participant_id);
      if (!readinessCheckedThroughUI) {
        const matchPage = await context.newPage();
        await matchPage.goto(`/arena/participant/${primaryTournament.id}`);
        const readyButton = matchPage.getByTestId('participant-ready-button');
        await expect(readyButton).toBeEnabled();
        const readinessResponse = matchPage.waitForResponse((response) =>
          response.request().method() === 'POST' &&
          response.url().endsWith(`/participant/waves/${wave.id}/ready`),
        );
        await readyButton.click();
        expect((await readinessResponse).status()).toBe(200);
        await expect(matchPage.getByText('Готовность подтверждена.', { exact: true })).toBeVisible();
        readinessCheckedThroughUI = true;
        continue;
      }
      const participant = await readParticipantSnapshotViaApi(context, primaryTournament.id);
      const csrfToken = (await context.cookies()).find(
        (cookie) => cookie.name === 'tpm_player_csrf',
      )?.value;
      expect(csrfToken).toBeTruthy();
      const readiness = await context.request.post(
        `${backendURL}/api/v1/tournaments/${primaryTournament.id}/participant/waves/${wave.id}/ready`,
        {
          headers: {
            'X-CSRF-Token': csrfToken ?? '',
            'Idempotency-Key': randomUUID(),
            Origin: frontendURL,
          },
          data: {
            expected_projection_revision: participant.projection_revision,
            ready: true,
          },
        },
      );
      expect(readiness.status(), `wave readiness failed with ${readiness.status()}`).toBe(200);
    }
    const startedWave = await waveAction('start', waveProjectionRevision);
    expect(startedWave.state).toBe('active');

    const assignmentMember = wave.members.find((candidate) => candidate.series_id !== null);
    expect(assignmentMember).toBeDefined();
    if (!assignmentMember) {
      throw new Error('catalog fixture did not expose a playable Swiss member');
    }
    const assignedContext = contextForParticipant(assignmentMember.participant_id);
    await expect.poll(async () => {
      const snapshot = await readParticipantSnapshotViaApi(assignedContext, primaryTournament.id);
      return snapshot.assignment?.context.series_id ?? null;
    }, { timeout: 30_000, intervals: [500, 1_000] }).toBeTruthy();
    const assignedSnapshot = await readParticipantSnapshotViaApi(
      assignedContext,
      primaryTournament.id,
    );
    expect(assignedSnapshot.assignment?.context.series_id).toBeTruthy();
    expect(assignedSnapshot.assignment?.active_snapshot.task_id).toMatch(/^[0-9a-f-]{36}$/i);

    await restoredPrimaryPage.reload({ waitUntil: 'domcontentloaded' });
    const workspaceLink = restoredPrimaryPage.getByRole('link', { name: 'Открыть мой матч' });
    await expect(workspaceLink).toBeVisible({ timeout: 30_000 });
    await workspaceLink.click();
    await expect(restoredPrimaryPage).toHaveURL(
      new RegExp(`/arena/participant/${primaryTournament.id}`),
    );
    await expect(restoredPrimaryPage.getByTestId('participant-player-panel')).toBeVisible({
      timeout: 30_000,
    });
  });

});
