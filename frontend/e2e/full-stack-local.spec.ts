import { randomUUID } from 'node:crypto';

import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

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
  id: string;
  name: string;
  public_id: string;
  state: string;
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
  category: 'web' | 'forensics';
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
  await expect(page.getByText('Список задач')).toBeVisible({ timeout: 15_000 });

  const cookies = await page.context().cookies();
  expect(cookies.some((cookie) => cookie.name === 'tpm_admin_access')).toBe(true);
  expect(cookies.some((cookie) => cookie.name === 'tpm_admin_refresh')).toBe(true);
};

const fillAdminTaskForm = async (page: Page, input: FullStackTaskInput): Promise<void> => {
  await page.getByPlaceholder('Введите название...').fill(input.title);
  await page.getByPlaceholder('Опишите задачу...').fill(input.description);
  await page.locator('select').first().selectOption(input.category);
  await page.locator('select').nth(1).selectOption(input.difficulty);
  await page.getByPlaceholder('60').fill(String(input.time_limit));
  await page.getByPlaceholder('flag{...}').fill(input.flag);
  await page.getByPlaceholder('https://example.com/task').fill(input.task_url ?? '');
  await page.getByPlaceholder('Подсказка 1').fill(input.hints[0]);
  await page.getByPlaceholder('Подсказка 2').fill(input.hints[1]);
  await page.getByPlaceholder('Подсказка 3').fill(input.hints[2]);
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
