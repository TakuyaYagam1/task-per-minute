import { expect, test, type APIRequestContext } from '@playwright/test';

const backendURL = (process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');

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

const uniqueName = (prefix: string) => `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;

const skipUnlessHealthy = async (request: APIRequestContext) => {
  try {
    const response = await request.get(`${backendURL}/health`, { timeout: 5000 });
    test.skip(!response.ok(), `/health returned ${response.status()} from ${backendURL}`);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    test.skip(true, `backend is not reachable at ${backendURL}: ${message}`);
  }
};

const adminLogin = async (request: APIRequestContext, password: string): Promise<AdminTokens> => {
  const response = await request.post(`${backendURL}/api/v1/admin/login`, {
    data: { password },
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
) => {
  const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`, {
    headers: { Authorization: `Bearer ${tokens.access_token}` },
  });
  if (!listResponse.ok()) {
    return;
  }

  const tasks = (await listResponse.json()) as AdminTask[];
  await Promise.all(
    tasks
      .filter((task) => task.title === title)
      .map((task) => request.delete(`${backendURL}/api/v1/admin/tasks/${task.id}`, {
        headers: {
          Authorization: `Bearer ${tokens.access_token}`,
          'X-CSRF-Token': tokens.access_csrf_token,
        },
      })),
  );
};

test.describe('live backend smoke', () => {
  test.skip(process.env.E2E_LIVE !== '1', 'set E2E_LIVE=1 to run against a disposable backend stack');

  test('admin login, create task, delete task, and leaderboard health', async ({ page, request }) => {
    const adminPassword = process.env.E2E_ADMIN_PASSWORD;
    if (!adminPassword) {
      test.skip(true, 'E2E_ADMIN_PASSWORD is required for live admin smoke');
      return;
    }

    await skipUnlessHealthy(request);

    const title = uniqueName('live-admin-contract');
    let cleanupTokens: AdminTokens | null = null;

    try {
      await page.goto('/admin');
      await page.getByPlaceholder('Введите пароль...').fill(adminPassword);
      await page.getByRole('button', { name: 'Войти' }).click();
      await expect(page.getByText('Список задач')).toBeVisible({ timeout: 10000 });

      const sessionState = await page.evaluate(() => ({
        marker: window.sessionStorage.getItem('admin_session_active'),
        accessToken: window.sessionStorage.getItem('admin_access_token'),
        refreshToken: window.sessionStorage.getItem('admin_refresh_token'),
      }));
      expect(sessionState.marker).toBe('1');
      expect(sessionState.accessToken).toBeNull();
      expect(sessionState.refreshToken).toBeNull();
      cleanupTokens = await adminLogin(request, adminPassword);

      await page.getByPlaceholder('Введите название...').fill(title);
      await page.getByPlaceholder('Опишите задачу...').fill('Live backend contract smoke task');
      await page.locator('select').first().selectOption('web');
      await page.getByPlaceholder('https://example.com/task').fill('https://example.com/live-smoke');
      await page.getByPlaceholder('60').fill('90');
      await page.getByPlaceholder('ctf{...}').fill('ctf{live_admin}');
      await page.getByPlaceholder('Подсказка 1').fill('one');
      await page.getByPlaceholder('Подсказка 2').fill('two');
      await page.getByPlaceholder('Подсказка 3').fill('three');
      await page.getByRole('button', { name: /Создать задачу/ }).click();

      await expect(page.getByText(title)).toBeVisible({ timeout: 10000 });

      page.once('dialog', async (dialog) => dialog.accept());
      await page
        .locator('[class*="taskItem"]')
        .filter({ hasText: title })
        .locator('[title="Удалить задачу"]')
        .click();

      await expect(page.getByText(title)).toBeHidden({ timeout: 10000 });

      const leaderboard = await request.get(`${backendURL}/api/v1/leaderboard`);
      expect(leaderboard.ok(), `leaderboard failed with ${leaderboard.status()}`).toBeTruthy();
    } finally {
      if (cleanupTokens) {
        await cleanupTaskByTitle(request, cleanupTokens, title);
      }
    }
  });

  test('two-player duel flow against isolated disposable backend', async ({ browser, request }) => {
    test.skip(
      process.env.E2E_LIVE_ISOLATED !== '1',
      'set E2E_LIVE_ISOLATED=1 only for a disposable DB where this task can be selected deterministically',
    );
    const adminPassword = process.env.E2E_ADMIN_PASSWORD;
    if (!adminPassword) {
      test.skip(true, 'E2E_ADMIN_PASSWORD is required for live player smoke');
      return;
    }

    await skipUnlessHealthy(request);

    const tokens = await adminLogin(request, adminPassword);
    const title = uniqueName('live-player-contract');
    let taskID: string | null = null;

    try {
      const createResponse = await request.post(`${backendURL}/api/v1/admin/tasks`, {
        headers: {
          Authorization: `Bearer ${tokens.access_token}`,
          'X-CSRF-Token': tokens.access_csrf_token,
        },
        data: {
          title,
          description: 'Live player flow smoke task',
          category: 'web',
          difficulty: 'easy',
          time_limit: 120,
          flag: 'ctf{live_player}',
          hints: ['one', 'two', 'three'],
          task_url: 'https://example.com/live-player',
        },
      });
      expect(createResponse.ok(), `task create failed with ${createResponse.status()}`).toBeTruthy();
      taskID = ((await createResponse.json()) as AdminTask).id;

      const contextA = await browser.newContext();
      const contextB = await browser.newContext();
      const playerA = await contextA.newPage();
      const playerB = await contextB.newPage();
      const playerAName = uniqueName('alice');
      const playerBName = uniqueName('bob');

      try {
        await Promise.all([playerA.goto('/'), playerB.goto('/')]);
        await playerA.getByPlaceholder('Введите никнейм...').fill(playerAName);
        await playerB.getByPlaceholder('Введите никнейм...').fill(playerBName);
        await playerA.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
        await playerB.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
        await expect(playerA.getByText('Игрок готов')).toBeVisible({ timeout: 10000 });
        await expect(playerB.getByText('Игрок готов')).toBeVisible({ timeout: 10000 });

        await Promise.all([
          playerA.getByRole('button', { name: /ИГРАТЬ/ }).click(),
          playerB.getByRole('button', { name: /ИГРАТЬ/ }).click(),
        ]);

        await expect(playerA).toHaveURL(/\/task$/, { timeout: 20000 });
        await expect(playerB).toHaveURL(/\/task$/, { timeout: 20000 });

        await playerA.getByPlaceholder('ctf{...}').fill('ctf{wrong}');
        await playerA.getByRole('button', { name: /Отправить/ }).click();
        await expect(playerA.getByText('Неверный флаг')).toBeVisible({ timeout: 10000 });

        await playerA.getByPlaceholder('ctf{...}').fill('ctf{live_player}');
        await playerA.getByRole('button', { name: /Отправить/ }).click();
        await expect(playerA.getByText('ПОБЕДА!')).toBeVisible({ timeout: 15000 });

        await playerA.goto('/leaderboard');
        await expect(playerA.getByText(playerAName)).toBeVisible({ timeout: 15000 });
      } finally {
        await contextA.close();
        await contextB.close();
      }
    } finally {
      if (taskID) {
        await request.delete(`${backendURL}/api/v1/admin/tasks/${taskID}`, {
          headers: {
            Authorization: `Bearer ${tokens.access_token}`,
            'X-CSRF-Token': tokens.access_csrf_token,
          },
        });
      }
    }
  });
});
