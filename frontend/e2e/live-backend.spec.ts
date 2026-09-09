import { expect, test, type APIRequestContext } from '@playwright/test';

const backendURL = (process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');

type AdminSession = {
  expires_in: number;
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

const adminLogin = async (request: APIRequestContext, password: string): Promise<AdminSession> => {
  const response = await request.post(`${backendURL}/api/v1/admin/login`, {
    data: { password },
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
) => {
  const listResponse = await request.get(`${backendURL}/api/v1/admin/tasks`);
  if (!listResponse.ok()) {
    return;
  }

  const tasks = (await listResponse.json()) as AdminTask[];
  await Promise.all(
    tasks
      .filter((task) => task.title === title)
      .map((task) => request.delete(`${backendURL}/api/v1/admin/tasks/${task.id}`, {
        headers: {
          'X-CSRF-Token': session.access_csrf_token,
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
    let cleanupSession: AdminSession | null = null;

    try {
      await page.goto('/admin');
      await page.getByPlaceholder('Введите пароль...').fill(adminPassword);
      await page.getByRole('button', { name: 'Войти' }).click();
      await expect(page.getByText('Список задач')).toBeVisible({ timeout: 10000 });

      const cookies = await page.context().cookies();
      expect(cookies.some((cookie) => cookie.name === 'tpm_admin_access')).toBe(true);
      expect(cookies.some((cookie) => cookie.name === 'tpm_admin_refresh')).toBe(true);
      cleanupSession = await adminLogin(request, adminPassword);

      await page.getByPlaceholder('Введите название...').fill(title);
      await page.getByPlaceholder('Опишите задачу...').fill('Live backend contract smoke task');
      await page.locator('select').first().selectOption('web');
      await page.getByPlaceholder('https://example.com/task').fill('https://example.com/live-smoke');
      await page.getByPlaceholder('60').fill('90');
      await page.getByPlaceholder('flag{...}').fill('flag{live_admin}');
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
      if (cleanupSession) {
        await cleanupTaskByTitle(request, cleanupSession, title);
      }
    }
  });

});
