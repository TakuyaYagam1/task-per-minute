import { expect, type Page } from '@playwright/test';
import { jsonHeaders, nowISO } from './common';

export const adminAccessOld = 'admin-access-old';
export const adminAccessNew = 'admin-access-new';
export const adminRefreshOld = 'admin-refresh-old';
export const adminRefreshNew = 'admin-refresh-new';

export type MockAdminTask = {
  id: string;
  title: string;
  description: string;
  category: string;
  difficulty: string;
  time_limit: number;
  flag: string;
  hints: string[];
  task_url: string | null;
  source_file_url: string | null;
  created_at: string;
  updated_at: string;
};

export const taskResponse = (overrides: Partial<MockAdminTask> = {}): MockAdminTask => ({
  id: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
  title: 'Contract Task',
  description: 'Admin contract task',
  category: 'forensics',
  difficulty: 'easy',
  time_limit: 120,
  flag: 'ctf{admin_ok}',
  hints: ['first', 'second', 'third'],
  task_url: null,
  source_file_url: null,
  created_at: nowISO(),
  updated_at: nowISO(),
  ...overrides,
});

export const loginAdminWithEmptyTaskList = async (page: Page): Promise<void> => {
  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText('Пока нет созданных задач')).toBeVisible();
};

export const fillAdminTaskForm = async (
  page: Page,
  overrides: Partial<{
    title: string;
    description: string;
    category: string;
    timeLimit: string;
    flag: string;
    taskUrl: string;
    hints: [string, string, string];
  }> = {},
): Promise<void> => {
  const values = {
    title: 'Validated Task',
    description: 'Admin validation contract task.',
    category: 'web',
    timeLimit: '120',
    flag: 'ctf{validated}',
    taskUrl: 'https://example.com/task',
    hints: ['first', 'second', 'third'] as [string, string, string],
    ...overrides,
  };

  await page.getByPlaceholder('Введите название...').fill(values.title);
  await page.getByPlaceholder('Опишите задачу...').fill(values.description);
  await page.locator('select').first().selectOption(values.category);
  await page.getByPlaceholder('60').fill(values.timeLimit);
  await page.getByPlaceholder('ctf{...}').fill(values.flag);
  await page.locator('input[placeholder="https://example.com/task"], input[placeholder="host:port"]').fill(values.taskUrl);
  await page.getByPlaceholder('Подсказка 1').fill(values.hints[0]);
  await page.getByPlaceholder('Подсказка 2').fill(values.hints[1]);
  await page.getByPlaceholder('Подсказка 3').fill(values.hints[2]);
};

export const setupAdminValidationApi = async (
  page: Page,
  onCreate?: (payload: unknown) => void,
): Promise<void> => {
  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessOld,
        refresh_token: adminRefreshOld,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await route.fulfill({ status: 200, headers: jsonHeaders, body: '[]' });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      onCreate?.(request.postDataJSON());
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({
          title: 'Validated Task',
          description: 'Admin validation contract task.',
          category: 'web',
          flag: 'ctf{validated}',
          task_url: 'https://example.com/task',
        })),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });
};
