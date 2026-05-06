import { expect, test } from '@playwright/test';
import { jsonHeaders, nowISO } from './support/common';
import {
  adminAccessNew,
  adminAccessOld,
  adminRefreshNew,
  adminRefreshOld,
  fillAdminTaskForm,
  loginAdminWithEmptyTaskList,
  type MockAdminTask,
  setupAdminValidationApi,
  taskResponse,
} from './support/admin';
import { adminApi, ApiError } from '../lib/shared/api';

test('admin login shows backend Retry-After after 3 failed attempts', async ({ page }) => {
  let attempts = 0;

  await page.route('**/api/v1/admin/login', async (route) => {
    attempts += 1;
    if (attempts <= 3) {
      await route.fulfill({
        status: 401,
        headers: jsonHeaders,
        body: JSON.stringify({
          type: 'about:blank',
          title: 'unauthorized',
          status: 401,
          detail: 'invalid admin password',
        }),
      });
      return;
    }

    await route.fulfill({
      status: 429,
      headers: {
        ...jsonHeaders,
        'Retry-After': '180',
      },
      body: JSON.stringify({
        type: 'about:blank',
        title: 'rate limited',
        status: 429,
        detail: 'too many login attempts',
      }),
    });
  });

  await page.goto('/admin');

  const password = page.getByPlaceholder('Введите пароль...');
  const submit = page.getByRole('button', { name: 'Войти' });

  for (let i = 0; i < 4; i += 1) {
    await password.fill(`wrong-password-${i}`);
    await submit.click();
  }

  await expect(page.getByText(/Слишком много попыток/)).toBeVisible();
  await expect(page.getByText(/3 мин/)).toBeVisible();
});

test('admin task lifecycle uses bearer auth, refresh retry, and source upload', async ({ page }) => {
  const taskID = '99999999-9999-9999-9999-999999999999';
  let task: MockAdminTask | null = null;
  let refreshCalls = 0;
  let listCalls = 0;
  let createCalls = 0;
  let updateCalls = 0;
  let deleteCalls = 0;
  let uploadCalls = 0;
  const bearerTokens: string[] = [];

  await page.route('**/api/v1/admin/login', async (route) => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ password: 'correct-password' });
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

  await page.route('**/api/v1/admin/refresh', async (route) => {
    refreshCalls += 1;
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ refresh_token: adminRefreshOld });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;
    const authorization = request.headers().authorization;
    if (authorization) {
      bearerTokens.push(authorization);
    }

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      listCalls += 1;
      if (listCalls === 1) {
        expect(authorization).toBe(`Bearer ${adminAccessOld}`);
        await route.fulfill({
          status: 401,
          headers: jsonHeaders,
          body: JSON.stringify({
            type: 'about:blank',
            title: 'unauthorized',
            status: 401,
            detail: 'expired access token',
          }),
        });
        return;
      }

      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(deleteCalls > 0 || !task ? [] : [task]),
      });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      createCalls += 1;
      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      expect(request.postDataJSON()).toEqual({
        title: 'Contract Task',
        description: 'Created through mocked backend',
        category: 'forensics',
        difficulty: 'easy',
        time_limit: 120,
        flag: 'ctf{admin_ok}',
        hints: ['first', 'second', 'third'],
        task_url: null,
      });
      task = taskResponse({ id: taskID, description: 'Created through mocked backend' });
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(task),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}/source` && method === 'POST') {
      uploadCalls += 1;
      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      expect(request.headers()['content-type']).toContain('multipart/form-data');
      expect(task).not.toBeNull();
      if (!task) {
        await route.fulfill({ status: 409, headers: jsonHeaders, body: '{}' });
        return;
      }
      task = {
        ...task,
        source_file_url: 'https://files.example/source.zip',
        updated_at: nowISO(),
      };
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({ source_file_url: task.source_file_url }),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      updateCalls += 1;
      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      expect(task).not.toBeNull();
      if (!task) {
        await route.fulfill({ status: 409, headers: jsonHeaders, body: '{}' });
        return;
      }
      const body = request.postDataJSON() as Partial<MockAdminTask>;
      expect(body.title).toBe('Contract Task Updated');
      task = {
        ...task,
        ...body,
        updated_at: nowISO(),
      };
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(task),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'DELETE') {
      deleteCalls += 1;
      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      await route.fulfill({ status: 204 });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Пока нет созданных задач')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Contract Task');
  await page.getByPlaceholder('Опишите задачу...').fill('Created through mocked backend');
  await page.locator('select').first().selectOption('forensics');
  await page.getByPlaceholder('60').fill('120');
  await page.getByPlaceholder('ctf{...}').fill('ctf{admin_ok}');
  await page.getByPlaceholder('Подсказка 1').fill('first');
  await page.getByPlaceholder('Подсказка 2').fill('second');
  await page.getByPlaceholder('Подсказка 3').fill('third');
  await page.locator('input[type="file"]').setInputFiles({
    name: 'source.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('PK\u0005\u0006contract'),
  });
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Задача успешно создана!')).toBeVisible();
  await expect(page.getByText('Contract Task')).toBeVisible();

  await page.locator('[title="Редактировать задачу"]').click();
  await page.getByPlaceholder('Введите название...').fill('Contract Task Updated');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();

  await expect(page.getByText('Задача успешно обновлена!')).toBeVisible();
  await expect(page.getByText('Contract Task Updated')).toBeVisible();

  page.once('dialog', async (dialog) => dialog.accept());
  await page.locator('[title="Удалить задачу"]').click();

  await expect(page.getByText('Задача удалена')).toBeVisible();
  await expect(page.getByText('Пока нет созданных задач')).toBeVisible();

  expect(refreshCalls).toBe(1);
  expect(createCalls).toBe(1);
  expect(uploadCalls).toBe(1);
  expect(updateCalls).toBe(1);
  expect(deleteCalls).toBe(1);
  expect(bearerTokens).toContain(`Bearer ${adminAccessOld}`);
  expect(bearerTokens).toContain(`Bearer ${adminAccessNew}`);
});

test('admin preserves source_file_url when editing source task to another category', async ({ page }) => {
  const taskID = '57575757-5757-5757-5757-575757575757';
  const title = 'Forensics Source Cleanup';
  let updatePayload: unknown = null;
  let listCalls = 0;

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      listCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([
          taskResponse({
            id: taskID,
            title,
            category: listCalls > 1 ? 'web' : 'forensics',
            task_url: listCalls > 1 ? 'https://example.com/cleanup' : null,
            source_file_url: 'https://files.example/source.zip',
          }),
        ]),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      expect(request.headers().authorization).toBe(`Bearer ${adminAccessNew}`);
      updatePayload = request.postDataJSON();
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({
          id: taskID,
          title,
          category: 'web',
          task_url: 'https://example.com/cleanup',
          source_file_url: 'https://files.example/source.zip',
        })),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText(title)).toBeVisible();

  await page
    .locator('[class*="taskItem"]')
    .filter({ hasText: title })
    .locator('[title="Редактировать задачу"]')
    .click();

  await page.locator('select').first().selectOption('web');
  await page.getByPlaceholder('https://example.com/task').fill('https://example.com/cleanup');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();

  await expect(page.getByText('Задача успешно обновлена!')).toBeVisible();
  expect(updatePayload).toEqual(expect.objectContaining({
    category: 'web',
    task_url: 'https://example.com/cleanup',
  }));
  expect(updatePayload).not.toHaveProperty('source_file_url');
});

test('admin temporary category flip back to forensics preserves source_file_url', async ({ page }) => {
  const taskID = '58585858-5858-5858-5858-585858585858';
  const title = 'Forensics Source Preserve';
  let updatePayload: Record<string, unknown> | null = null;

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([
          taskResponse({
            id: taskID,
            title,
            category: 'forensics',
            task_url: null,
            source_file_url: 'https://files.example/preserve.zip',
          }),
        ]),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      updatePayload = request.postDataJSON() as Record<string, unknown>;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({
          id: taskID,
          title,
          category: 'forensics',
          task_url: null,
          source_file_url: 'https://files.example/preserve.zip',
        })),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText(title)).toBeVisible();

  await page
    .locator('[class*="taskItem"]')
    .filter({ hasText: title })
    .locator('[title="Редактировать задачу"]')
    .click();

  await page.locator('select').first().selectOption('web');
  await page.locator('select').first().selectOption('forensics');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();

  await expect(page.getByText('Задача успешно обновлена!')).toBeVisible();
  expect(updatePayload).toEqual(expect.objectContaining({
    category: 'forensics',
    task_url: null,
  }));
  expect(updatePayload).not.toHaveProperty('source_file_url');
});

test('admin canceling a replacement source file preserves existing source_file_url', async ({ page }) => {
  const taskID = '59595959-5959-5959-5959-595959595959';
  const title = 'Forensics Replacement Cancel';
  let updatePayload: Record<string, unknown> | null = null;

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([
          taskResponse({
            id: taskID,
            title,
            category: 'forensics',
            task_url: null,
            source_file_url: 'https://files.example/current.zip',
          }),
        ]),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      updatePayload = request.postDataJSON() as Record<string, unknown>;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({
          id: taskID,
          title,
          category: 'forensics',
          task_url: null,
          source_file_url: 'https://files.example/current.zip',
        })),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText(title)).toBeVisible();

  await page
    .locator('[class*="taskItem"]')
    .filter({ hasText: title })
    .locator('[title="Редактировать задачу"]')
    .click();

  await expect(page.getByText('Текущий архив сохранён')).toBeVisible();
  await page.locator('input[type="file"]').setInputFiles({
    name: 'replacement.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('PK\u0005\u0006replacement'),
  });
  await expect(page.getByText('replacement.zip')).toBeVisible();
  await page
    .locator('[class*="fileInfo"]')
    .filter({ hasText: 'replacement.zip' })
    .locator('[class*="fileInfoRemove"]')
    .click();
  await expect(page.getByText('Текущий архив сохранён')).toBeVisible();
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();

  await expect(page.getByText('Задача успешно обновлена!')).toBeVisible();
  expect(updatePayload).toEqual(expect.objectContaining({
    category: 'forensics',
    task_url: null,
  }));
  expect(updatePayload).not.toHaveProperty('source_file_url');
});

test('admin pwn task keeps raw host-port task_url on create and update', async ({ page }) => {
  const taskID = '12121212-1212-1212-1212-121212121212';
  let task: MockAdminTask | null = null;
  let createCalls = 0;
  let updateCalls = 0;
  const bearerTokens: string[] = [];

  await page.route('**/api/v1/admin/login', async (route) => {
    expect(route.request().method()).toBe('POST');
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
    const method = request.method();
    const path = new URL(request.url()).pathname;
    const authorization = request.headers().authorization;
    if (authorization) {
      bearerTokens.push(authorization);
    }

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      expect(authorization).toBe(`Bearer ${adminAccessOld}`);
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(task ? [task] : []),
      });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      createCalls += 1;
      expect(authorization).toBe(`Bearer ${adminAccessOld}`);
      expect(request.postDataJSON()).toEqual({
        title: 'Pwn Endpoint',
        description: 'Raw host-port target should reach backend unchanged.',
        category: 'pwn',
        difficulty: 'easy',
        time_limit: 120,
        flag: 'ctf{pwn_ok}',
        hints: ['first', 'second', 'third'],
        task_url: 'pwn.example:31337',
      });
      task = taskResponse({
        id: taskID,
        title: 'Pwn Endpoint',
        description: 'Raw host-port target should reach backend unchanged.',
        category: 'pwn',
        flag: 'ctf{pwn_ok}',
        task_url: 'pwn.example:31337',
      });
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(task),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      updateCalls += 1;
      expect(authorization).toBe(`Bearer ${adminAccessOld}`);
      expect(request.postDataJSON()).toEqual({
        title: 'Pwn Endpoint Updated',
        description: 'Raw host-port target should reach backend unchanged.',
        category: 'pwn',
        difficulty: 'easy',
        time_limit: 120,
        flag: 'ctf{pwn_ok}',
        hints: ['first', 'second', 'third'],
        task_url: 'pwn.example:31338',
      });
      task = taskResponse({
        id: taskID,
        title: 'Pwn Endpoint Updated',
        description: 'Raw host-port target should reach backend unchanged.',
        category: 'pwn',
        flag: 'ctf{pwn_ok}',
        task_url: 'pwn.example:31338',
      });
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(task),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Пока нет созданных задач')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Pwn Endpoint');
  await page.getByPlaceholder('Опишите задачу...').fill('Raw host-port target should reach backend unchanged.');
  await page.locator('select').first().selectOption('pwn');
  await page.getByPlaceholder('60').fill('120');
  await page.getByPlaceholder('ctf{...}').fill('ctf{pwn_ok}');
  await page.getByPlaceholder('host:port').fill('pwn.example:31337');
  await page.getByPlaceholder('Подсказка 1').fill('first');
  await page.getByPlaceholder('Подсказка 2').fill('second');
  await page.getByPlaceholder('Подсказка 3').fill('third');
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Задача успешно создана!')).toBeVisible();
  await expect(page.getByText('Pwn Endpoint')).toBeVisible();

  await page.locator('[title="Редактировать задачу"]').click();
  await page.getByPlaceholder('Введите название...').fill('Pwn Endpoint Updated');
  await page.getByPlaceholder('host:port').fill('pwn.example:31338');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();

  await expect(page.getByText('Задача успешно обновлена!')).toBeVisible();
  await expect(page.getByText('Pwn Endpoint Updated')).toBeVisible();

  expect(createCalls).toBe(1);
  expect(updateCalls).toBe(1);
  expect(bearerTokens).toContain(`Bearer ${adminAccessOld}`);
});

test('admin task form rejects non-decimal time limits before create', async ({ page }) => {
  let createCalls = 0;
  await setupAdminValidationApi(page, () => {
    createCalls += 1;
  });

  await loginAdminWithEmptyTaskList(page);
  await fillAdminTaskForm(page, { timeLimit: '1e2' });
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Лимит времени должен быть целым числом от 1 до 2147483647')).toBeVisible();
  expect(createCalls).toBe(0);
});

test('admin task form keeps valid decimal time_limit as number', async ({ page }) => {
  let createCalls = 0;
  await setupAdminValidationApi(page, (payload) => {
    createCalls += 1;
    expect(payload).toEqual({
      title: 'Validated Task',
      description: 'Admin validation contract task.',
      category: 'web',
      difficulty: 'easy',
      time_limit: 120,
      flag: 'ctf{validated}',
      hints: ['first', 'second', 'third'],
      task_url: 'https://example.com/task',
    });
  });

  await loginAdminWithEmptyTaskList(page);
  await fillAdminTaskForm(page, { timeLimit: '120' });
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Задача успешно создана!')).toBeVisible();
  expect(createCalls).toBe(1);
});

test('admin task form rejects whitespace-only required fields before create', async ({ page }) => {
  let createCalls = 0;
  await setupAdminValidationApi(page, () => {
    createCalls += 1;
  });

  await loginAdminWithEmptyTaskList(page);

  await fillAdminTaskForm(page, { title: '   ' });
  await page.getByRole('button', { name: /Создать задачу/ }).click();
  await expect(page.getByText('Название должно быть от 1 до 255 символов')).toBeVisible();

  await fillAdminTaskForm(page, { description: '   ' });
  await page.getByRole('button', { name: /Создать задачу/ }).click();
  await expect(page.getByText('Описание не должно быть пустым')).toBeVisible();

  await fillAdminTaskForm(page, { flag: '   ' });
  await page.getByRole('button', { name: /Создать задачу/ }).click();
  await expect(page.getByText('Флаг должен быть от 1 до 255 символов')).toBeVisible();

  expect(createCalls).toBe(0);
});

test('admin task form rejects whitespace-only required fields before update', async ({ page }) => {
  const taskID = '23232323-2323-2323-2323-232323232323';
  const task = taskResponse({
    id: taskID,
    title: 'Existing Validated Task',
    description: 'Existing admin validation task.',
    category: 'web',
    flag: 'ctf{existing_validated}',
    task_url: 'https://example.com/task',
  });
  let updateCalls = 0;

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
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([task]),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      updateCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(task),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText('Existing Validated Task')).toBeVisible();
  await page.locator('[title="Редактировать задачу"]').click();

  await page.getByPlaceholder('Введите название...').fill('   ');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();
  await expect(page.getByText('Название должно быть от 1 до 255 символов')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Existing Validated Task');
  await page.getByPlaceholder('Опишите задачу...').fill('   ');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();
  await expect(page.getByText('Описание не должно быть пустым')).toBeVisible();

  await page.getByPlaceholder('Опишите задачу...').fill('Existing admin validation task.');
  await page.getByPlaceholder('ctf{...}').fill('   ');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();
  await expect(page.getByText('Флаг должен быть от 1 до 255 символов')).toBeVisible();

  expect(updateCalls).toBe(0);
});

test('admin task form rejects invalid task_url before create', async ({ page }) => {
  let createCalls = 0;
  await setupAdminValidationApi(page, () => {
    createCalls += 1;
  });

  await loginAdminWithEmptyTaskList(page);

  for (const taskUrl of ['/relative', 'ftp://example.com/task', 'host:99999']) {
    await fillAdminTaskForm(page, {
      category: 'pwn',
      taskUrl,
    });
    await page.getByRole('button', { name: /Создать задачу/ }).click();
    await expect(page.getByText('URL задания должен быть http(s) ссылкой или host:port')).toBeVisible();
  }

  expect(createCalls).toBe(0);
});

test('admin create refresh is reused for source upload in the same submit', async ({ page }) => {
  const taskID = '67676767-6767-6767-6767-676767676767';
  let refreshCalls = 0;
  const listAuthorizations: string[] = [];
  const createAuthorizations: string[] = [];
  const uploadAuthorizations: string[] = [];

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

  await page.route('**/api/v1/admin/refresh', async (route) => {
    refreshCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ refresh_token: adminRefreshOld });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;
    const authorization = request.headers().authorization || '';

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      listAuthorizations.push(authorization);
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([]),
      });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      createAuthorizations.push(authorization);
      if (authorization === `Bearer ${adminAccessOld}`) {
        await route.fulfill({
          status: 401,
          headers: jsonHeaders,
          body: JSON.stringify({
            type: 'about:blank',
            title: 'unauthorized',
            status: 401,
            detail: 'expired access token',
          }),
        });
        return;
      }

      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({ id: taskID, title: 'Refresh Reuse Task' })),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}/source` && method === 'POST') {
      uploadAuthorizations.push(authorization);
      expect(authorization).toBe(`Bearer ${adminAccessNew}`);
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({ source_file_url: 'https://files.example/reused.zip' }),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Пока нет созданных задач')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Refresh Reuse Task');
  await page.getByPlaceholder('Опишите задачу...').fill('Create refresh should feed upload.');
  await page.locator('select').first().selectOption('forensics');
  await page.getByPlaceholder('60').fill('120');
  await page.getByPlaceholder('ctf{...}').fill('ctf{refresh_reuse}');
  await page.getByPlaceholder('Подсказка 1').fill('first');
  await page.getByPlaceholder('Подсказка 2').fill('second');
  await page.getByPlaceholder('Подсказка 3').fill('third');
  await page.locator('input[type="file"]').setInputFiles({
    name: 'source.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('PK\u0005\u0006contract'),
  });
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Задача успешно создана!')).toBeVisible();
  await expect.poll(() => refreshCalls).toBe(1);
  expect(listAuthorizations[0]).toBe(`Bearer ${adminAccessOld}`);
  expect(listAuthorizations).toContain(`Bearer ${adminAccessNew}`);
  expect(createAuthorizations).toEqual([
    `Bearer ${adminAccessOld}`,
    `Bearer ${adminAccessNew}`,
  ]);
  expect(uploadAuthorizations).toEqual([`Bearer ${adminAccessNew}`]);
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBe(adminAccessNew);
});

test('admin invalid source file clears previous selection and prevents stale upload', async ({ page }) => {
  const taskID = '88888888-8888-8888-8888-888888888888';
  let createCalls = 0;
  let uploadCalls = 0;

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([]),
      });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      createCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({ id: taskID })),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}/source` && method === 'POST') {
      uploadCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({ source_file_url: 'https://files.example/source.zip' }),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Пока нет созданных задач')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Invalid Source Guard');
  await page.getByPlaceholder('Опишите задачу...').fill('Invalid source should clear stale file');
  await page.locator('select').first().selectOption('forensics');
  await page.getByPlaceholder('60').fill('120');
  await page.getByPlaceholder('ctf{...}').fill('ctf{admin_ok}');
  await page.getByPlaceholder('Подсказка 1').fill('first');
  await page.getByPlaceholder('Подсказка 2').fill('second');
  await page.getByPlaceholder('Подсказка 3').fill('third');

  const fileInput = page.locator('input[type="file"]');
  await fileInput.setInputFiles({
    name: 'source.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('PK\u0005\u0006contract'),
  });
  await expect(page.getByText('source.zip')).toBeVisible();

  await fileInput.setInputFiles({
    name: 'source.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('not a zip'),
  });
  await expect(page.getByText('Можно загружать только ZIP-архивы')).toBeVisible();
  await expect(page.getByText('source.zip')).toBeHidden();

  await page.getByRole('button', { name: /Создать задачу/ }).click();
  await expect(page.getByText('Задача успешно создана!')).toBeVisible();

  expect(createCalls).toBe(1);
  expect(uploadCalls).toBe(0);
});

test('admin source upload timeout is mapped to controlled api error', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => {
    throw new DOMException('Upload timed out', 'TimeoutError');
  }) as typeof fetch;

  try {
    await adminApi.uploadSource(
      adminAccessOld,
      '90909090-9090-9090-9090-909090909090',
      new File([Buffer.from('PK\u0005\u0006contract')], 'source.zip', {
        type: 'application/zip',
      }),
      { timeoutMs: 1 },
    );
    throw new Error('uploadSource unexpectedly resolved');
  } catch (error) {
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(408);
    expect((error as ApiError).problem?.detail).toBe('Upload exceeded 0s timeout');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('admin malformed successful REST responses do not persist invalid state', async ({ page }) => {
  const taskID = '89898989-8989-8989-8989-898989898989';
  let listCalls = 0;
  let createCalls = 0;
  let uploadCalls = 0;

  await page.route('**/api/v1/admin/login', async (route) => {
    const body = route.request().postDataJSON() as { password?: string };
    if (body.password === 'bad-shape') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({
          access_token: adminAccessNew,
        }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      listCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(listCalls === 1
          ? [taskResponse({
              title: 'Invalid Source URL',
              hints: ['first', 'second'],
              source_file_url: 'javascript:alert(1)',
            })]
          : []),
      });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      createCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({ id: taskID, title: 'Malformed Upload Guard' })),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}/source` && method === 'POST') {
      uploadCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({ source_file_url: 'ftp://files.example/source.zip' }),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('bad-shape');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Ошибка подключения к серверу')).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBeNull();

  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Не удалось загрузить задачи')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Malformed Upload Guard');
  await page.getByPlaceholder('Опишите задачу...').fill('Malformed upload response should be a warning');
  await page.locator('select').first().selectOption('forensics');
  await page.getByPlaceholder('60').fill('120');
  await page.getByPlaceholder('ctf{...}').fill('ctf{admin_ok}');
  await page.getByPlaceholder('Подсказка 1').fill('first');
  await page.getByPlaceholder('Подсказка 2').fill('second');
  await page.getByPlaceholder('Подсказка 3').fill('third');
  await page.locator('input[type="file"]').setInputFiles({
    name: 'source.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('PK\u0005\u0006contract'),
  });
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Задача создана, но файл не загрузился')).toBeVisible();
  await expect(page.getByText('Invalid Source URL')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBe(adminAccessNew);
  expect(createCalls).toBe(1);
  expect(uploadCalls).toBe(1);
});

test('malformed admin refresh clears session without retrying invalid tokens', async ({ page }) => {
  let refreshCalls = 0;
  let listCalls = 0;
  const bearerTokens: string[] = [];

  await page.addInitScript(({ accessToken, refreshToken }) => {
    window.sessionStorage.setItem('admin_access_token', accessToken);
    window.sessionStorage.setItem('admin_refresh_token', refreshToken);
  }, { accessToken: adminAccessOld, refreshToken: adminRefreshOld });

  await page.route('**/api/v1/admin/refresh', async (route) => {
    refreshCalls += 1;
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ refresh_token: adminRefreshOld });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;
    const authorization = request.headers().authorization;
    if (authorization) {
      bearerTokens.push(authorization);
    }

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      listCalls += 1;
      await route.fulfill({
        status: 401,
        headers: jsonHeaders,
        body: JSON.stringify({
          type: 'about:blank',
          title: 'unauthorized',
          status: 401,
          detail: 'expired access token',
        }),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');

  await expect(page.getByText('Сессия истекла. Войдите снова.')).toBeVisible();
  await expect(page.getByText('Авторизация')).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBeNull();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_refresh_token')))
    .toBeNull();

  expect(refreshCalls).toBe(1);
  expect(listCalls).toBe(1);
  expect(bearerTokens).toEqual([`Bearer ${adminAccessOld}`]);
});

test('admin logout ignores delayed refresh and prevents stale retry', async ({ page }) => {
  let releaseRefresh: () => void = () => {};
  let refreshCalls = 0;
  const listAuthorizations: string[] = [];
  const refreshGate = new Promise<void>((resolve) => {
    releaseRefresh = resolve;
  });

  await page.addInitScript(({ accessToken, refreshToken }) => {
    window.sessionStorage.setItem('admin_access_token', accessToken);
    window.sessionStorage.setItem('admin_refresh_token', refreshToken);
  }, { accessToken: adminAccessOld, refreshToken: adminRefreshOld });

  await page.route('**/api/v1/admin/refresh', async (route) => {
    refreshCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ refresh_token: adminRefreshOld });
    await refreshGate;
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/logout', async (route) => {
    expect(route.request().headers().authorization).toBe(`Bearer ${adminAccessOld}`);
    await route.fulfill({ status: 204 });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const authorization = request.headers().authorization;
    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      if (authorization) {
        listAuthorizations.push(authorization);
      }
      if (authorization === `Bearer ${adminAccessOld}`) {
        await route.fulfill({
          status: 401,
          headers: jsonHeaders,
          body: JSON.stringify({
            type: 'about:blank',
            title: 'unauthorized',
            status: 401,
            detail: 'expired access token',
          }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([taskResponse({ title: 'Stale Refreshed Task' })]),
      });
      return;
    }
    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await expect(page.getByRole('button', { name: 'Выйти' })).toBeVisible();
  await expect.poll(() => refreshCalls).toBe(1);

  await page.getByRole('button', { name: 'Выйти' }).click();
  await expect(page.getByText('Авторизация')).toBeVisible();

  releaseRefresh();
  await page.waitForTimeout(150);

  await expect(page.getByText('Авторизация')).toBeVisible();
  await expect(page.getByText('Stale Refreshed Task')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBeNull();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_refresh_token')))
    .toBeNull();
  expect(listAuthorizations).toEqual([`Bearer ${adminAccessOld}`]);
});

test('admin new login ignores delayed refresh from previous session', async ({ page }) => {
  let releaseRefresh: () => void = () => {};
  let refreshCalls = 0;
  const staleAccessToken = 'admin-access-stale-refresh';
  const listAuthorizations: string[] = [];
  const refreshGate = new Promise<void>((resolve) => {
    releaseRefresh = resolve;
  });

  await page.addInitScript(({ accessToken, refreshToken }) => {
    window.sessionStorage.setItem('admin_access_token', accessToken);
    window.sessionStorage.setItem('admin_refresh_token', refreshToken);
  }, { accessToken: adminAccessOld, refreshToken: adminRefreshOld });

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/refresh', async (route) => {
    refreshCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ refresh_token: adminRefreshOld });
    await refreshGate;
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: staleAccessToken,
        refresh_token: 'admin-refresh-stale-refresh',
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/logout', async (route) => {
    await route.fulfill({ status: 204 });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const authorization = request.headers().authorization;
    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      if (authorization) {
        listAuthorizations.push(authorization);
      }
      if (authorization === `Bearer ${adminAccessOld}`) {
        await route.fulfill({
          status: 401,
          headers: jsonHeaders,
          body: JSON.stringify({
            type: 'about:blank',
            title: 'unauthorized',
            status: 401,
            detail: 'expired access token',
          }),
        });
        return;
      }
      if (authorization === `Bearer ${adminAccessNew}`) {
        await route.fulfill({
          status: 200,
          headers: jsonHeaders,
          body: JSON.stringify([taskResponse({ title: 'New Login Task' })]),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([taskResponse({ title: 'Stale Refresh Task' })]),
      });
      return;
    }
    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await expect(page.getByRole('button', { name: 'Выйти' })).toBeVisible();
  await expect.poll(() => refreshCalls).toBe(1);

  await page.getByRole('button', { name: 'Выйти' }).click();
  await expect(page.getByText('Авторизация')).toBeVisible();

  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText('New Login Task')).toBeVisible();

  releaseRefresh();
  await page.waitForTimeout(150);

  await expect(page.getByText('New Login Task')).toBeVisible();
  await expect(page.getByText('Stale Refresh Task')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBe(adminAccessNew);
  expect(listAuthorizations).not.toContain(`Bearer ${staleAccessToken}`);
});

test('admin logout ignores delayed task list response', async ({ page }) => {
  let releaseList: () => void = () => {};
  let listCalls = 0;
  const listGate = new Promise<void>((resolve) => {
    releaseList = resolve;
  });

  await page.addInitScript(({ accessToken, refreshToken }) => {
    window.sessionStorage.setItem('admin_access_token', accessToken);
    window.sessionStorage.setItem('admin_refresh_token', refreshToken);
  }, { accessToken: adminAccessOld, refreshToken: adminRefreshOld });

  await page.route('**/api/v1/admin/logout', async (route) => {
    await route.fulfill({ status: 204 });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      listCalls += 1;
      expect(request.headers().authorization).toBe(`Bearer ${adminAccessOld}`);
      await listGate;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([taskResponse({ title: 'Old Delayed Task' })]),
      });
      return;
    }
    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await expect(page.getByRole('button', { name: 'Выйти' })).toBeVisible();
  await expect.poll(() => listCalls).toBe(1);

  await page.getByRole('button', { name: 'Выйти' }).click();
  await expect(page.getByText('Авторизация')).toBeVisible();

  releaseList();
  await page.waitForTimeout(150);

  await expect(page.getByText('Авторизация')).toBeVisible();
  await expect(page.getByText('Old Delayed Task')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBeNull();
});

test('admin new login is not overwritten by old delayed task list', async ({ page }) => {
  let releaseOldList: () => void = () => {};
  let oldListCalls = 0;
  let newListCalls = 0;
  const oldListGate = new Promise<void>((resolve) => {
    releaseOldList = resolve;
  });

  await page.addInitScript(({ accessToken, refreshToken }) => {
    window.sessionStorage.setItem('admin_access_token', accessToken);
    window.sessionStorage.setItem('admin_refresh_token', refreshToken);
  }, { accessToken: adminAccessOld, refreshToken: adminRefreshOld });

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/logout', async (route) => {
    await route.fulfill({ status: 204 });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const authorization = request.headers().authorization;
    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      if (authorization === `Bearer ${adminAccessOld}`) {
        oldListCalls += 1;
        await oldListGate;
        await route.fulfill({
          status: 200,
          headers: jsonHeaders,
          body: JSON.stringify([taskResponse({ title: 'Old Session Task' })]),
        });
        return;
      }
      if (authorization === `Bearer ${adminAccessNew}`) {
        newListCalls += 1;
        await route.fulfill({
          status: 200,
          headers: jsonHeaders,
          body: JSON.stringify([taskResponse({ title: 'New Session Task' })]),
        });
        return;
      }
    }
    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await expect(page.getByRole('button', { name: 'Выйти' })).toBeVisible();
  await expect.poll(() => oldListCalls).toBe(1);

  await page.getByRole('button', { name: 'Выйти' }).click();
  await expect(page.getByText('Авторизация')).toBeVisible();

  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText('New Session Task')).toBeVisible();
  await expect.poll(() => newListCalls).toBe(1);

  releaseOldList();
  await page.waitForTimeout(150);

  await expect(page.getByText('New Session Task')).toBeVisible();
  await expect(page.getByText('Old Session Task')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('admin_access_token')))
    .toBe(adminAccessNew);
});

test('malformed admin create and update responses keep previous valid task state', async ({ page }) => {
  const taskID = '34343434-3434-3434-3434-343434343434';
  const existingTask = taskResponse({
    id: taskID,
    title: 'Existing Contract Task',
    description: 'Existing valid task remains visible.',
  });
  let createCalls = 0;
  let updateCalls = 0;

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        access_token: adminAccessNew,
        refresh_token: adminRefreshNew,
        token_type: 'Bearer',
        expires_in: 900,
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;

    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify([existingTask]),
      });
      return;
    }

    if (path === '/api/v1/admin/tasks' && method === 'POST') {
      createCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({
          id: '45454545-4545-4545-4545-454545454545',
          title: 'Malformed Created Task',
          time_limit: 120.5,
        })),
      });
      return;
    }

    if (path === `/api/v1/admin/tasks/${taskID}` && method === 'PUT') {
      updateCalls += 1;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(taskResponse({
          id: taskID,
          title: 'Malformed Updated Task',
          time_limit: 90.25,
        })),
      });
      return;
    }

    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();

  await expect(page.getByText('Existing Contract Task')).toBeVisible();

  await page.getByPlaceholder('Введите название...').fill('Malformed Created Task');
  await page.getByPlaceholder('Опишите задачу...').fill('Malformed create should be rejected.');
  await page.locator('select').first().selectOption('forensics');
  await page.getByPlaceholder('60').fill('120');
  await page.getByPlaceholder('ctf{...}').fill('ctf{admin_ok}');
  await page.getByPlaceholder('Подсказка 1').fill('first');
  await page.getByPlaceholder('Подсказка 2').fill('second');
  await page.getByPlaceholder('Подсказка 3').fill('third');
  await page.getByRole('button', { name: /Создать задачу/ }).click();

  await expect(page.getByText('Ошибка при создании задачи')).toBeVisible();
  await expect(page.getByText('Existing Contract Task')).toBeVisible();
  await expect(page.getByText('Malformed Created Task')).toBeHidden();

  await page.locator('[title="Редактировать задачу"]').click();
  await page.getByPlaceholder('Введите название...').fill('Malformed Updated Task');
  await page.getByRole('button', { name: /Сохранить задачу/ }).click();

  await expect(page.getByText('Ошибка при обновлении задачи')).toBeVisible();
  await expect(page.getByText('Existing Contract Task')).toBeVisible();
  await expect(page.getByText('Malformed Updated Task')).toBeHidden();

  expect(createCalls).toBe(1);
  expect(updateCalls).toBe(1);
});
