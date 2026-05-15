import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test';

const frontendURL = (process.env.E2E_FRONTEND_URL || 'http://127.0.0.1:3000').replace(/\/+$/, '');
const backendURL = (process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const adminPassword = process.env.E2E_ADMIN_PASSWORD || '';

type AdminTokens = {
  access_token: string;
  refresh_token: string;
  access_csrf_token: string;
  refresh_csrf_token: string;
};

type AdminTask = {
  id: string;
  title: string;
};

type UploadSourceResponse = {
  source_file_url: string;
};

type LeaderboardResponse = {
  entries: Array<{
    username: string;
    wins: number;
  }>;
};

type BrowserAuthStorage = {
  sessionToken: string | null;
  localSessionToken: string | null;
  adminAccessToken: string | null;
  adminRefreshToken: string | null;
};

type FullStackTaskInput = {
  title: string;
  description: string;
  category: 'web' | 'forensics';
  difficulty: 'easy';
  time_limit: number;
  flag: string;
  hints: [string, string, string];
  task_url?: string | null;
};

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

const adminLogin = async (request: APIRequestContext): Promise<AdminTokens> => {
  const response = await request.post(`${backendURL}/api/v1/admin/login`, {
    data: { password: adminPassword },
  });
  expect(response.ok(), `admin login failed with ${response.status()}`).toBeTruthy();
  const accessCSRFToken = response.headers()['x-csrf-token'];
  const refreshCSRFToken = response.headers()['x-admin-refresh-csrf-token'];
  expect(accessCSRFToken, 'admin login did not return access CSRF token').toBeTruthy();
  expect(refreshCSRFToken, 'admin login did not return refresh CSRF token').toBeTruthy();
  return {
    ...((await response.json()) as Omit<AdminTokens, 'access_csrf_token' | 'refresh_csrf_token'>),
    access_csrf_token: accessCSRFToken,
    refresh_csrf_token: refreshCSRFToken,
  };
};

const cleanupTaskByTitle = async (
  request: APIRequestContext,
  tokens: AdminTokens,
  title: string,
): Promise<void> => {
  const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`, {
    headers: { Authorization: `Bearer ${tokens.access_token}` },
  });
  if (!listResponse.ok()) {
    return;
  }

  const tasks = (await listResponse.json()) as AdminTask[];
  for (const task of tasks.filter((candidate) => candidate.title === title)) {
    await request.delete(`${backendURL}/api/v1/admin/tasks/${task.id}`, {
      headers: {
        Authorization: `Bearer ${tokens.access_token}`,
        'X-CSRF-Token': tokens.access_csrf_token,
      },
    });
  }
};

const loginThroughAdminUI = async (page: Page): Promise<void> => {
  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill(adminPassword);
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText('Список задач')).toBeVisible({ timeout: 15_000 });

  const sessionState = await page.evaluate(() => ({
    marker: window.sessionStorage.getItem('admin_session_active'),
    accessToken: window.sessionStorage.getItem('admin_access_token'),
    refreshToken: window.sessionStorage.getItem('admin_refresh_token'),
  }));
  expect(sessionState.marker, 'admin UI did not persist session marker').toBe('1');
  expect(sessionState.accessToken, 'admin access token must stay out of sessionStorage').toBeNull();
  expect(sessionState.refreshToken, 'admin refresh token must stay out of sessionStorage').toBeNull();
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
  tokens: AdminTokens,
  input: FullStackTaskInput,
): Promise<AdminTask> => {
  const response = await request.post(`${backendURL}/api/v1/admin/tasks`, {
    headers: {
      Authorization: `Bearer ${tokens.access_token}`,
      'X-CSRF-Token': tokens.access_csrf_token,
    },
    data: input,
  });
  expect(response.ok(), `task create failed with ${response.status()}`).toBeTruthy();
  return (await response.json()) as AdminTask;
};

const uploadSourceViaApi = async (
  request: APIRequestContext,
  tokens: AdminTokens,
  taskID: string,
  payload: Buffer,
): Promise<UploadSourceResponse> => {
  const response = await request.post(`${backendURL}/api/v1/admin/tasks/${taskID}/source`, {
    headers: {
      Authorization: `Bearer ${tokens.access_token}`,
      'X-CSRF-Token': tokens.access_csrf_token,
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
    adminAccessToken: window.sessionStorage.getItem('admin_access_token'),
    adminRefreshToken: window.sessionStorage.getItem('admin_refresh_token'),
  }));
  expect(storage.sessionToken, 'player session token must stay out of sessionStorage').toBeNull();
  expect(storage.localSessionToken, 'player session token must stay out of localStorage').toBeNull();
  expect(storage.adminAccessToken, 'admin access token must stay out of sessionStorage').toBeNull();
  expect(storage.adminRefreshToken, 'admin refresh token must stay out of sessionStorage').toBeNull();
};

const expectLeaderboardContains = async (
  request: APIRequestContext,
  username: string,
): Promise<void> => {
  await expect.poll(async () => {
    const response = await request.get(`${backendURL}/api/v1/leaderboard`);
    if (!response.ok()) {
      return [];
    }
    const body = (await response.json()) as LeaderboardResponse;
    return body.entries.map((entry) => entry.username);
  }, { timeout: 20_000 }).toContain(username);
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
    let cleanupTokens: AdminTokens | null = null;

    try {
      await loginThroughAdminUI(page);
      cleanupTokens = await adminLogin(request);
      await fillAdminTaskForm(page, taskInput);
      await page.getByRole('button', { name: /Создать задачу/ }).click();
      await expect(page.getByText(title)).toBeVisible({ timeout: 15_000 });

      const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`, {
        headers: { Authorization: `Bearer ${cleanupTokens.access_token}` },
      });
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
      if (cleanupTokens) {
        await cleanupTaskByTitle(request, cleanupTokens, title);
      }
    }
  });

  test('source upload returns a host-reachable presigned URL', async ({ request }) => {
    test.setTimeout(90_000);

    const tokens = await adminLogin(request);
    const title = uniqueName('fullstack-source');
    const payload = Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x66, 0x73]);
    let created: AdminTask | null = null;

    try {
      created = await createTaskViaApi(request, tokens, {
        title,
        description: 'Full stack source archive task.',
        category: 'forensics',
        difficulty: 'easy',
        time_limit: 90,
        flag: `flag{${title.replaceAll('-', '_')}}`,
        hints: ['first hint', 'second hint', 'third hint'],
        task_url: null,
      });

      const upload = await uploadSourceViaApi(request, tokens, created.id, payload);
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
        await cleanupTaskByTitle(request, tokens, title);
      }
    }
  });

  test('two real browser players complete a duel over REST and WebSocket', async ({ browser, request }) => {
    test.setTimeout(120_000);
    test.skip(
      process.env.E2E_FULL_STACK_ISOLATED !== '1',
      'set E2E_FULL_STACK_ISOLATED=1 only for a disposable compose project with a clean DB',
    );

    const tokens = await adminLogin(request);
    const title = uniqueName('fullstack-duel');
    const flag = `flag{${title.replaceAll('-', '_')}}`;
    await createTaskViaApi(request, tokens, {
      title,
      description: 'Full stack duel task selected from an isolated local compose database.',
      category: 'web',
      difficulty: 'easy',
      time_limit: 120,
      flag,
      hints: ['first hint', 'second hint', 'third hint'],
      task_url: 'https://example.com/full-stack-duel',
    });

    const contextA = await browser.newContext({ baseURL: frontendURL });
    const contextB = await browser.newContext({ baseURL: frontendURL });
    const playerA = await contextA.newPage();
    const playerB = await contextB.newPage();
    const playerAName = uniqueName('alice');
    const playerBName = uniqueName('bob');

    try {
      await Promise.all([
        joinAsPlayer(playerA, playerAName),
        joinAsPlayer(playerB, playerBName),
      ]);

      await Promise.all([
        playerA.getByRole('button', { name: /ИГРАТЬ/ }).click(),
        playerB.getByRole('button', { name: /ИГРАТЬ/ }).click(),
      ]);

      await expect(playerA).toHaveURL(/\/task$/, { timeout: 25_000 });
      await expect(playerB).toHaveURL(/\/task$/, { timeout: 25_000 });
      await expect(playerA.getByRole('heading', { name: title })).toBeVisible({ timeout: 15_000 });
      await expect(playerB.getByRole('heading', { name: title })).toBeVisible({ timeout: 15_000 });

      await playerA.reload();
      await expect(playerA).toHaveURL(/\/task$/, { timeout: 25_000 });
      await expect(playerA.getByRole('heading', { name: title })).toBeVisible({ timeout: 15_000 });

      await playerA.getByPlaceholder('flag{...}').fill('flag{wrong}');
      await playerA.getByRole('button', { name: /Отправить/ }).click();
      await expect(playerA.getByText('Неверный флаг')).toBeVisible({ timeout: 10_000 });

      await playerA.getByPlaceholder('flag{...}').fill(flag);
      await playerA.getByRole('button', { name: /Отправить/ }).click();
      await expect(playerA.getByText('Флаг верный!')).toBeVisible({ timeout: 10_000 });
      await expect(playerA.getByText('ПОБЕДА!')).toBeVisible({ timeout: 20_000 });
      await expect(playerB.getByText('ПОРАЖЕНИЕ')).toBeVisible({ timeout: 20_000 });

      await expectLeaderboardContains(request, playerAName);
      await playerA.goto('/leaderboard');
      await expect(playerA.getByText(playerAName)).toBeVisible({ timeout: 20_000 });
    } finally {
      await contextA.close();
      await contextB.close();
    }
  });
});
