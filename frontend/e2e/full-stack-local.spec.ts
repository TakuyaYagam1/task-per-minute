import { randomUUID } from 'node:crypto';

import {
  expect,
  test,
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
  title: string;
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
  id: string;
  passed: boolean;
  tournament_id: string;
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

const uniqueName = (prefix: string) =>
  `${prefix}-${Date.now().toString(36)}-${Math.random().toString(16).slice(2, 8)}`;

const ensureFullStackEnabled = async (request: APIRequestContext): Promise<void> => {
  test.skip(process.env.E2E_FULL_STACK !== '1', 'set E2E_FULL_STACK=1 to run local compose e2e');
  test.skip(!adminPassword, 'E2E_ADMIN_PASSWORD is required for local compose e2e');

  const backendHealth = await request.get(`${backendURL}/health`, { timeout: 10_000 });
  expect(backendHealth.ok(), `backend /health failed at ${backendURL}`).toBeTruthy();

  const frontendHealth = await request.get(`${frontendURL}/`, { timeout: 10_000 });
  expect(frontendHealth.ok(), `frontend / failed at ${frontendURL}`).toBeTruthy();
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

const cleanupTaskByTitle = async (
  request: APIRequestContext,
  session: AdminSession,
  title: string,
): Promise<void> => {
  const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`);
  if (!listResponse.ok()) {
    return;
  }

  const tasks = (await listResponse.json()) as AdminTask[];
  for (const task of tasks.filter((candidate) => candidate.title === title)) {
    await request.delete(`${backendURL}/api/v1/admin/tasks/${task.id}`, {
      headers: {
        'X-CSRF-Token': session.access_csrf_token,
      },
    });
  }
};

const loginThroughAdminUI = async (page: Page): Promise<void> => {
  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill(adminPassword);
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByRole('button', { name: 'Турниры' })).toBeVisible({ timeout: 15_000 });

  const cookies = await page.context().cookies();
  expect(cookies.some((cookie) => cookie.name === 'tpm_admin_access')).toBe(true);
  expect(cookies.some((cookie) => cookie.name === 'tpm_admin_refresh')).toBe(true);
};

const fillAdminTaskForm = async (page: Page, input: FullStackTaskInput): Promise<void> => {
  const form = page.locator('form').filter({ has: page.getByPlaceholder('Введите название...') }).first();
  await form.getByPlaceholder('Введите название...').fill(input.title);
  await form.getByPlaceholder('Опишите задачу...').fill(input.description);
  await form.locator('select').first().selectOption(input.category);
  await form.locator('select').nth(1).selectOption(input.difficulty);
  await form.getByPlaceholder('60').fill(String(input.time_limit));
  await form.getByPlaceholder('flag{...}').fill(input.flag);
  await form.getByPlaceholder('https://example.com/task').fill(input.task_url ?? '');
  await form.getByPlaceholder('Подсказка 1').fill(input.hints[0]);
  await form.getByPlaceholder('Подсказка 2').fill(input.hints[1]);
  await form.getByPlaceholder('Подсказка 3').fill(input.hints[2]);
};

const createTaskViaApi = async (
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
  expect(response.ok(), `task create failed with ${response.status()}`).toBeTruthy();
  return (await response.json()) as AdminTask;
};

const getTournamentContentRevision = async (
  request: APIRequestContext,
): Promise<number> => {
  const contentURL = `${backendURL}/api/v1/admin/tournament-content`;
  const response = await request.get(contentURL);
  expect(response.ok(), `tournament content GET failed at ${contentURL} with ${response.status()}`).toBeTruthy();
  const content = (await response.json()) as { content_revision?: unknown };
  const contentRevision = content.content_revision;
  expect(contentRevision, 'tournament content did not return content_revision').toEqual(
    expect.any(Number),
  );
  if (typeof contentRevision !== 'number') {
    throw new Error(`tournament content returned an invalid content_revision at ${contentURL}`);
  }
  return contentRevision;
};

const createTournamentViaApi = async (
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

const getOperatorProjectionRevisionViaApi = async (
  request: APIRequestContext,
  tournamentID: string,
): Promise<number> => {
  const response = await request.get(
    `${backendURL}/api/v1/admin/tournaments/${tournamentID}/snapshot`,
    { headers: { Origin: frontendURL } },
  );
  expect(response.ok(), `operator snapshot GET failed with ${response.status()}`).toBeTruthy();
  const snapshot = (await response.json()) as {
    next_cursor?: { projection_revision?: unknown };
    tournament?: { id?: unknown };
  };
  expect(snapshot.tournament?.id, 'operator snapshot returned the wrong tournament id').toBe(
    tournamentID,
  );
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
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
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

  test('admin UI creates and deletes a task through the real backend', async ({ page, request }) => {
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
    let cleanupSession: AdminSession | null = null;

    try {
      await loginThroughAdminUI(page);
      cleanupSession = await adminLogin(request);
      await page.getByRole('button', { name: 'Турниры' }).click();
      await expect(page.getByPlaceholder('Введите название...')).toBeVisible();
      await fillAdminTaskForm(page, taskInput);
      await page.getByRole('button', { name: /Создать задачу/ }).click();
      await expect(page.getByText(title)).toBeVisible({ timeout: 15_000 });

      const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`);
      expect(listResponse.ok(), `admin task list failed with ${listResponse.status()}`).toBeTruthy();
      const tasks = (await listResponse.json()) as AdminTask[];
      expect(tasks.some((task) => task.title === title)).toBe(true);

      page.once('dialog', async (dialog) => dialog.accept());
      await page
        .locator('[class*="taskItem"]')
        .filter({ hasText: title })
        .locator('[title="Удалить задачу"]')
        .click();
      await expect(page.getByText(title)).toBeHidden({ timeout: 15_000 });

      await page.goto('/leaderboard');
      await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
    } finally {
      if (cleanupSession) {
        await cleanupTaskByTitle(request, cleanupSession, title);
      }
    }
  });

  test('admin UI creates a tournament from the current content revision', async ({ page }) => {
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
    await createTaskViaApi(adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
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
    await createTaskViaApi(adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
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

    await page.getByRole('button', { name: 'Турниры' }).click();
    await expect(page.getByRole('heading', { name: 'Новый турнир' })).toBeVisible();
    const reloadPublication = page.getByRole('button', { name: 'Обновить публикацию' });
    if (await reloadPublication.isVisible().catch(() => false)) {
      await reloadPublication.click();
    }
    await expect(
      page.getByRole('region', { name: 'Каталог контента' }).getByText(`Ревизия ${contentRevision}`),
    ).toBeVisible();
    await page.getByLabel('Название турнира').fill(tournamentName);
    await page.getByLabel('Плановый размер состава').selectOption('4');

    const createResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/v1/admin/tournaments' &&
        response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Создать демо-турнир' }).click();

    const response = await createResponse;
    expect(response.status()).toBe(201);

    const tournament = (await response.json()) as AdminTournament;
    expect(tournament.content_revision).toBe(contentRevision);
    expect(tournament.name).toBe(tournamentName);
    expect(tournament.state).toBe('draft');
    await expect(page).toHaveURL(
      new URL(`/arena/operator/${tournament.id}`, frontendURL).toString(),
    );
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page).toHaveURL(
      new URL(`/arena/operator/${tournament.id}`, frontendURL).toString(),
    );
    await expect(page.getByRole('heading').filter({ hasText: tournamentName })).toBeVisible({
      timeout: 15_000,
    });

    const listResponse = await adminRequest.get(`${backendURL}/api/v1/admin/tournaments`);
    expect(listResponse.ok(), `tournament list failed with ${listResponse.status()}`).toBeTruthy();
    const list = (await listResponse.json()) as { items: AdminTournament[] };
    expect(list.items.some((item) => item.id === tournament.id && item.name === tournamentName)).toBe(true);
  });

  test('FE-028 admin UI creates, uploads, downloads, and updates a task through the relocated catalog', async ({ page, request }) => {
    test.setTimeout(120_000);

    const title = uniqueName('fullstack-fe028');
    const updatedTitle = `${title}-updated`;
    const taskInput: FullStackTaskInput = {
      title,
      description: 'FE-028 task created through the tournament admin catalog.',
      category: 'forensics',
      difficulty: 'easy',
      time_limit: 120,
      flag: `flag{${title.replaceAll('-', '_')}}`,
      hints: ['fe028 hint one', 'fe028 hint two', 'fe028 hint three'],
      task_url: null,
    };
    let cleanupSession: AdminSession | null = null;

    try {
      await loginThroughAdminUI(page);
      await page.getByRole('button', { name: 'Турниры' }).click();
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
      await page.getByRole('button', { name: /Создать задачу/ }).click();

      const createResponse = await createResponsePromise;
      expect(createResponse.status()).toBe(201);
      const createdTask = (await createResponse.json()) as AdminTask;
      expect(createdTask.id).toMatch(/^[0-9a-f-]{36}$/i);
      expect(createdTask.title).toBe(title);

      const uploadResponse = await uploadResponsePromise;
      expect(uploadResponse.status()).toBe(200);
      const upload = (await uploadResponse.json()) as UploadSourceResponse;
      expect(upload.source_file_url).toMatch(/^https?:\/\//);
      await expect(page.getByText('Исходники загружены в SeaweedFS')).toBeVisible({ timeout: 15_000 });
      const downloadLink = page.getByRole('link', { name: /Скачать.*ZIP/ });
      await expect(downloadLink).toHaveAttribute('href', upload.source_file_url);

      const download = await page.context().request.get(upload.source_file_url, { timeout: 15_000 });
      expect(download.ok(), `source download failed with ${download.status()}`).toBeTruthy();
      expect((await download.body()).subarray(0, 2).toString()).toBe('PK');

      await expect(page.getByText(title, { exact: true })).toBeVisible();
      await page.getByRole('button', { name: `Редактировать задачу ${title}` }).click();
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

      cleanupSession = await adminLogin(request);
      const refreshedTasks = await request.get(`${backendURL}/api/v1/admin/tasks`);
      expect(refreshedTasks.ok(), `admin task list failed with ${refreshedTasks.status()}`).toBeTruthy();
      const taskList = (await refreshedTasks.json()) as AdminTask[];
      expect(taskList.some((task) => task.id === createdTask.id && task.title === updatedTitle)).toBe(true);
    } finally {
      if (!cleanupSession) {
        try {
          cleanupSession = await adminLogin(request);
        } catch {
          cleanupSession = null;
        }
      }
      if (cleanupSession) {
        await cleanupTaskByTitle(request, cleanupSession, title);
        await cleanupTaskByTitle(request, cleanupSession, updatedTitle);
      }
    }
  });

  test('FE-027 composes, checks in, replaces, and protects a real backend roster', async ({ page, browser }) => {
    test.setTimeout(180_000);

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
          const normalTaskName = uniqueName(`roster-${group.category}-${index + 1}`);
          await createTaskViaApi(adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
            title: normalTaskName,
            description: 'Healthy normal task for the roster flow.',
            kind: 'normal',
            category: group.category,
            difficulty: 'easy',
            time_limit: 90,
            flag: `flag{${normalTaskName.replaceAll('-', '_')}}`,
            hints: ['normal hint one', 'normal hint two', 'normal hint three'],
            task_url: 'https://example.com/roster-normal',
          });
        }
      }
      for (let index = 0; index < 6; index += 1) {
        const goldenTaskName = uniqueName(`roster-golden-${index + 1}`);
        await createTaskViaApi(adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
          title: goldenTaskName,
          description: 'Healthy golden task for the roster flow.',
          kind: 'golden',
          category: 'web',
          difficulty: 'easy',
          time_limit: 90,
          flag: `flag{${goldenTaskName.replaceAll('-', '_')}}`,
          hints: ['golden hint one', 'golden hint two', 'golden hint three'],
          task_url: 'https://example.com/roster-golden',
        });
      }

      const contentRevision = await getTournamentContentRevision(adminRequest);
      const tournament = await createTournamentViaApi(
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

      await page.getByRole('button', { name: 'Турниры' }).click();
      const tournamentListRefresh = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/api/v1/admin/tournaments' &&
          response.request().method() === 'GET',
      );
      await page
        .getByRole('region', { name: 'Турниры' })
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
      const editRosterButton = tournamentRow.getByRole('button', { name: 'Редактировать состав' });
      await expect(editRosterButton).toBeVisible({ timeout: 15_000 });
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
      await editRosterButton.click();
      expect((await playersResponse).status()).toBe(200);
      const initialRoster = await readRosterResponse(await rosterResponse, tournament.id);

      const rosterRegion = page.getByRole('region', { name: 'Состав турнира' });
      await expect(rosterRegion).toBeVisible();
      expect(initialRoster.participants).toHaveLength(0);
      await expect(rosterRegion.getByText('Состав пуст')).toBeVisible();
      await expect(rosterRegion.getByRole('button', { name: 'Добавить участника' })).toBeEnabled();
      await rosterRegion.getByRole('button', { name: 'Добавить участника' }).click({ clickCount: 4 });
      await expect(rosterRegion.getByRole('group')).toHaveCount(4);

      for (const [index, player] of players.slice(0, 4).entries()) {
        const group = rosterRegion.getByRole('group', { name: `Участник ${index + 1}` });
        await group.getByRole('combobox', { name: 'Игрок' }).selectOption(player.id);
        await group.getByRole('spinbutton', { name: 'Seed / позиция' }).fill(String(index + 1));
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
      await loginThroughAdminUI(page);
      const refreshedAdminAccessCSRF = (await page.context().cookies()).find(
        (cookie) => cookie.name === 'tpm_admin_access_csrf',
      );
      adminAccessCSRFToken = refreshedAdminAccessCSRF?.value ?? '';
      expect(adminAccessCSRFToken, 'admin relogin did not issue an access CSRF cookie').toBeTruthy();
      await page.getByRole('button', { name: 'Турниры' }).click();
      const reopenedTournamentRow = page.getByRole('row').filter({ hasText: tournamentName });
      const reopenedEditRosterButton = reopenedTournamentRow.getByRole('button', {
        name: 'Редактировать состав',
      });
      await expect(reopenedEditRosterButton).toBeVisible({ timeout: 15_000 });
      await reopenedEditRosterButton.click();
      await expect(rosterRegion).toBeVisible({ timeout: 15_000 });
      await expect(rosterRegion.getByRole('group')).toHaveCount(4);
      for (const [index, participant] of savedParticipants.entries()) {
        const group = rosterRegion.getByRole('group', { name: `Участник ${index + 1}` });
        await expect(group.getByRole('combobox', { name: 'Игрок' })).toHaveValue(
          participant.player_id,
        );
        await expect(group.getByRole('spinbutton', { name: 'Seed / позиция' })).toHaveValue(
          String(index + 1),
        );
      }

      await rosterRegion.getByRole('group', { name: 'Участник 1' })
        .getByRole('combobox', { name: 'Посещаемость' })
        .selectOption('checked_in');
      await rosterRegion.getByRole('group', { name: 'Участник 2' })
        .getByRole('combobox', { name: 'Игрок' })
        .selectOption(players[4].id);

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
        'checked_in',
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

      const secondTournament = await createTournamentViaApi(
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
    } finally {
      for (const context of playerContexts) {
        await context.close();
      }
    }
  });

  test('source upload returns a host-reachable presigned URL', async ({ request }) => {
    test.setTimeout(90_000);

    const session = await adminLogin(request);
    const title = uniqueName('fullstack-source');
    const payload = Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x66, 0x73]);
    let created: AdminTask | null = null;

    try {
      created = await createTaskViaApi(request, session, {
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
    } finally {
      if (created) {
        await cleanupTaskByTitle(request, session, title);
      }
    }
  });

  test('real backend preserves Arena entry context across roles', async ({ page, browser }) => {
    test.setTimeout(120_000);

    const tournamentName = uniqueName('fullstack-arena');
    const tournamentPublicID = uniqueName('arena-public');
    const playerName = uniqueName('arena-player');
    await loginThroughAdminUI(page);
    const adminAccessCSRF = (await page.context().cookies()).find(
      (cookie) => cookie.name === 'tpm_admin_access_csrf',
    );
    const adminAccessCSRFToken = adminAccessCSRF?.value ?? '';
    expect(adminAccessCSRFToken, 'admin login did not issue an access CSRF cookie').toBeTruthy();
    const adminRequest = page.context().request;
    const normalTaskName = uniqueName('arena-normal');
    const goldenTaskName = uniqueName('arena-golden');
    await createTaskViaApi(adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
      title: normalTaskName,
      description: 'Healthy normal Arena content task.',
      kind: 'normal',
      category: 'web',
      difficulty: 'easy',
      time_limit: 90,
      flag: `flag{${normalTaskName.replaceAll('-', '_')}}`,
      hints: ['normal hint one', 'normal hint two', 'normal hint three'],
      task_url: 'https://example.com/arena-normal',
    });
    await createTaskViaApi(adminRequest, { access_csrf_token: adminAccessCSRFToken }, {
      title: goldenTaskName,
      description: 'Healthy golden Arena content task.',
      kind: 'golden',
      category: 'web',
      difficulty: 'easy',
      time_limit: 90,
      flag: `flag{${goldenTaskName.replaceAll('-', '_')}}`,
      hints: ['golden hint one', 'golden hint two', 'golden hint three'],
      task_url: 'https://example.com/arena-golden',
    });
    const contentRevision = await getTournamentContentRevision(adminRequest);
    const tournament = await createTournamentViaApi(
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
    const spectatorPath = `/arena/spectator/${tournament.id}`;
    const operatorPath = `/arena/operator/${tournament.id}`;
    const participantPath = `/arena/participant/${tournament.id}`;
    const spectatorURL = `${spectatorPath}?source=e2e`;

    const adminCookies = await page.context().cookies();
    expect(adminCookies.some((cookie) => cookie.name === 'tpm_admin_access')).toBe(true);
    expect(adminCookies.some((cookie) => cookie.name === 'tpm_admin_refresh')).toBe(true);

    await page.goto(spectatorURL);
    await expect(page).toHaveURL(new URL(spectatorURL, frontendURL).toString());
    await expect(page.getByRole('main')).toBeVisible();
    await expect(page.getByText(tournament.id, { exact: false }).first()).toBeVisible();
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page).toHaveURL(new URL(spectatorURL, frontendURL).toString());
    await expect(page.getByText(tournament.id, { exact: false }).first()).toBeVisible();

    await page.goto(operatorPath);
    await expect(page).toHaveURL(new URL(operatorPath, frontendURL).toString());
    await expect(page.getByRole('main')).toBeVisible();
    await expect(page.getByRole('heading').filter({ hasText: tournamentName })).toBeVisible();

    const playerContext = await browser.newContext({ baseURL: frontendURL });
    try {
      const playerPage = await playerContext.newPage();
      await joinAsPlayer(playerPage, playerName);
      await playerPage.goto(participantPath);
      await expect(playerPage).toHaveURL(new URL(participantPath, frontendURL).toString());
      await expect(playerPage.getByRole('main')).toBeVisible();
      await expect(
        playerPage.getByText(
          /forbidden|access denied|нет доступа|доступ запрещ|запрещен|запрещён|недоступ|403/i,
        ).first(),
      ).toBeVisible();
      await expect(
        playerPage.getByRole('heading').filter({ hasText: /spectator|operator|наблюдател|оператор/i }),
      ).toHaveCount(0);
    } finally {
      await playerContext.close();
    }

    const logoutButton = page.getByRole('button', { name: /log ?out|выйти/i });
    await expect(logoutButton).toHaveCount(1);
    const logoutResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/v1/admin/logout' &&
        response.request().method() === 'POST',
    );
    await logoutButton.click();
    expect((await logoutResponse).status()).toBe(204);
    await expect(page).toHaveURL(new URL(operatorPath, frontendURL).toString());
    await expect(page.getByRole('link', { name: 'Войти как оператор' })).toBeVisible({
      timeout: 15_000,
    });
  });

});
