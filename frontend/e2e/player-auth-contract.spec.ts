import { expect, test } from '@playwright/test';

import { jsonHeaders, nowISO } from './support/common';

const playerID = '77777777-7777-7777-7777-777777777777';

const acceptedResponse = { accepted: true };

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/players/notifications', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({ notifications: [] }),
    });
  });
  await page.route('**/api/v1/players/notifications/events', async (route) => {
    await route.fulfill({ status: 204, body: '' });
  });
});

const problem = (status: number, title: string, detail?: string) => ({
  type: 'about:blank',
  status,
  title,
  ...(detail ? { detail } : {}),
});

const storageSnapshot = async (page: import('@playwright/test').Page): Promise<string> =>
  page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));

test('registration sends the account details and stores no credentials', async ({ page }) => {
  const email = 'alice@example.test';
  const password = 'correct horse battery';
  let registrationCalls = 0;
  await page.addInitScript(() => {
    document.cookie = 'tpm_player_csrf=old-session-token; Path=/';
  });
  await page.route('**/api/v1/players/register', async (route) => {
    registrationCalls += 1;
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({
      username: 'alice_01',
      email,
      password,
    });
    expect(route.request().headers().authorization).toBeUndefined();
    expect(route.request().headers()['x-csrf-token']).toBeUndefined();
    await route.fulfill({
      status: 202,
      headers: jsonHeaders,
      body: JSON.stringify(acceptedResponse),
    });
  });

  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Email', { exact: true }).fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByLabel('Повторите пароль').fill(password);
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(page.getByRole('status')).toContainText(
    'Если этот адрес можно зарегистрировать, мы отправили письмо со ссылкой для подтверждения.',
  );
  await expect(page.getByRole('link', { name: 'Перейти ко входу' })).toHaveAttribute('href', '/login');
  await expect(page.getByRole('link', { name: 'Не пришло письмо?', exact: true })).toHaveAttribute('href', '/verify-email');
  expect(registrationCalls).toBe(1);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain(email);
  expect(saved).not.toContain(password);
  expect(saved).not.toContain('alice_01');
});

test('registration rejects short passwords before contacting the API', async ({ page }) => {
  let registrationCalls = 0;
  await page.route('**/api/v1/players/register', async (route) => {
    registrationCalls += 1;
    await route.abort();
  });

  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Email', { exact: true }).fill('alice@example.test');
  await page.getByLabel('Пароль', { exact: true }).fill('short');
  await page.getByLabel('Повторите пароль').fill('short');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(page.getByText('Пароль должен содержать от 15 до 128 символов.', { exact: true })).toBeVisible();
  expect(registrationCalls).toBe(0);
});

test('registration delivery failure offers the resend page', async ({ page }) => {
  await page.route('**/api/v1/players/register', async (route) => {
    await route.fulfill({
      status: 503,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(503, 'Service Unavailable')),
    });
  });

  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Email', { exact: true }).fill('alice@example.test');
  await page.getByLabel('Пароль', { exact: true }).fill('correct horse battery');
  await page.getByLabel('Повторите пароль').fill('correct horse battery');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(page.getByText('Регистрация временно недоступна. Попробуйте позже.', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Не пришло письмо?', exact: true })).toHaveAttribute('href', '/verify-email');
});

test('login accepts the returned account and restores only its public identity', async ({ page }) => {
  const email = 'alice@example.test';
  const password = 'correct horse battery';
  let loginCalls = 0;
  let meCalls = 0;
  await page.addInitScript(() => {
    document.cookie = 'tpm_player_csrf=old-session-token; Path=/';
  });
  await page.route('**/api/v1/players/login', async (route) => {
    loginCalls += 1;
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ login: email, password });
    expect(route.request().headers().authorization).toBeUndefined();
    expect(route.request().headers()['x-csrf-token']).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: { ...jsonHeaders, 'X-CSRF-Token': 'new-session-token' },
      body: JSON.stringify({ player_id: playerID }),
    });
  });
  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers().authorization).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice_01',
          created_at: nowISO(),
        },
      }),
    });
  });

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Войти', exact: true }).click();

  await expect(page.getByText('Игрок готов')).toBeVisible();
  await expect(page.getByText('alice_01', { exact: true })).toBeVisible();
  expect(loginCalls).toBe(1);
  expect(meCalls).toBeGreaterThan(0);
  const saved = await storageSnapshot(page);
  expect(saved).toContain(playerID);
  expect(saved).toContain('alice_01');
  expect(saved).not.toContain(email);
  expect(saved).not.toContain(password);
});

test('invalid and unverified login failures share generic copy', async ({ page }) => {
  await page.route('**/api/v1/players/login', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ login: 'alice_01', password: 'wrong-password' });
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(401, 'Unauthorized', 'Invalid credentials or unverified account')),
    });
  });

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill('alice_01');
  await page.getByLabel('Пароль', { exact: true }).fill('wrong-password');
  await page.getByRole('button', { name: 'Войти', exact: true }).click();

  await expect(page.getByText('Проверьте логин, пароль и подтверждение email.', { exact: true })).toBeVisible();
  await expect(page.getByText('Invalid credentials or unverified account')).toHaveCount(0);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain('wrong-password');
  expect(saved).not.toContain('alice_01');
});

test('verification token is removed from the URL and waits for explicit confirmation', async ({ page }) => {
  const token = 'private-verification-token';
  let verificationCalls = 0;
  let hashAtRequest = 'not-requested';
  await page.route('**/api/v1/players/verify-email', async (route) => {
    verificationCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ token });
    hashAtRequest = await page.evaluate(() => window.location.hash);
    await route.fulfill({ status: 204, body: '' });
  });

  await page.goto(`/verify-email#token=${encodeURIComponent(token)}`);
  await expect(page).toHaveURL(/\/verify-email$/);
  await expect(page.getByRole('button', { name: 'Подтвердить email' })).toBeVisible();
  expect(verificationCalls).toBe(0);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain(token);
  expect(page.url()).not.toContain(token);

  await page.getByRole('button', { name: 'Подтвердить email' }).click();
  await expect(page.getByRole('status')).toContainText('Email подтверждён. Теперь можно войти.');
  expect(verificationCalls).toBe(1);
  expect(hashAtRequest).toBe('');
  expect((await storageSnapshot(page))).not.toContain(token);
});

test('verification keeps the token in memory for a retryable failure', async ({ page }) => {
  const token = 'retryable-verification-token';
  let verificationCalls = 0;
  await page.route('**/api/v1/players/verify-email', async (route) => {
    verificationCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ token });
    if (verificationCalls === 1) {
      await route.fulfill({
        status: 429,
        headers: { 'content-type': 'application/problem+json' },
        body: JSON.stringify(problem(429, 'Too Many Requests')),
      });
      return;
    }
    await route.fulfill({ status: 204, body: '' });
  });

  await page.goto(`/verify-email#token=${encodeURIComponent(token)}`);
  await page.getByRole('button', { name: 'Подтвердить email' }).click();
  await expect(page.getByText('Слишком много попыток. Повторите позже.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Повторить подтверждение' })).toBeVisible();
  expect(await storageSnapshot(page)).not.toContain(token);

  await page.getByRole('button', { name: 'Повторить подтверждение' }).click();
  await expect(page.getByRole('status')).toContainText('Email подтверждён. Теперь можно войти.');
  expect(verificationCalls).toBe(2);
});

test('resend verification returns a generic acknowledgement', async ({ page }) => {
  const email = 'alice@example.test';
  let resendCalls = 0;
  await page.route('**/api/v1/players/resend-verification', async (route) => {
    resendCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ email });
    expect(route.request().headers().authorization).toBeUndefined();
    await route.fulfill({
      status: 202,
      headers: jsonHeaders,
      body: JSON.stringify(acceptedResponse),
    });
  });

  await page.goto('/verify-email');
  await page.getByLabel('Email для нового письма').fill(email);
  await page.getByRole('button', { name: 'Отправить новое письмо' }).click();

  await expect(page.getByText(
    'Если для этого адреса доступно подтверждение, мы отправили письмо.',
    { exact: true },
  )).toBeVisible();
  expect(resendCalls).toBe(1);
  expect(await storageSnapshot(page)).not.toContain(email);
});
