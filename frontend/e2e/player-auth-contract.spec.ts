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

const problem = (status: number, title: string, detail?: string, code?: string) => ({
  type: 'about:blank',
  status,
  title,
  ...(detail ? { detail } : {}),
  ...(code ? { code } : {}),
});

const storageSnapshot = async (page: import('@playwright/test').Page): Promise<string> =>
  page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));

const expectVerificationPageWithoutResend = async (page: import('@playwright/test').Page): Promise<void> => {
  const panel = page.getByRole('region', { name: 'Подтверждение email' });
  await expect(panel.getByRole('link', { name: 'Task Per Minute - на главную' })).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'Подтверждение email', exact: true })).toBeVisible();
  await expect(page.getByText('Подтвердите адрес из письма, чтобы войти в аккаунт.', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Email для нового письма')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Отправить новое письмо', exact: true })).toHaveCount(0);
  await expect(page.getByText(/Не пришло письмо/)).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Перейти ко входу', exact: true })).toHaveCount(0);
};

const prepareUnverifiedLogin = async (
  page: import('@playwright/test').Page,
  login: string,
  password: string,
): Promise<void> => {
  await page.route('**/api/v1/players/login', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ login, password });
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(
        401,
        'Unauthorized',
        'Activate your account by verifying your email before signing in',
        'player.email_unverified',
      )),
    });
  });

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill(login);
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  await expect(page.getByText(
    'Аккаунт не активирован. Подтвердите email по ссылке из письма.',
    { exact: true },
  )).toBeVisible();
};

test('registration sends the account details and stores no credentials', async ({ page }) => {
  const email = 'alice@example.test';
  const password = 'Éxample parola ٧!';
  let registrationCalls = 0;
  let resendCalls = 0;
  await page.clock.install({ time: '2026-09-30T00:00:00.000Z' });
  await page.clock.pauseAt('2026-09-30T00:00:00.000Z');
  await page.route('**/api/v1/players/resend-verification', async (route) => {
    resendCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ email });
    expect(route.request().headers().authorization).toBeUndefined();
    expect(route.request().headers()['x-csrf-token']).toBeUndefined();
    await route.fulfill({ status: 202, headers: jsonHeaders, body: JSON.stringify(acceptedResponse) });
  });
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
  await page.getByLabel('Email', { exact: true }).fill(` ${email} `);
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByLabel('Повторите пароль').fill(password);
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(page.getByRole('status')).toContainText(
    'Письмо было отправлено на указанную почту.',
  );
  await expect(page.getByRole('link', { name: 'Перейти ко входу' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveCount(1);
  const resend = page.getByRole('button', { name: 'Не пришло письмо?', exact: true });
  await expect(resend).toBeDisabled();
  await page.screenshot({ path: '/home/takuya/.codex/.tmp/registration-resend.png' });
  await page.clock.runFor(59_000);
  await expect(resend).toBeDisabled();
  expect(resendCalls).toBe(0);
  await page.clock.runFor(1_000);
  await expect(resend).toBeEnabled();
  const resendResponse = page.waitForResponse('**/api/v1/players/resend-verification');
  await resend.click();
  expect((await resendResponse).status()).toBe(202);
  await expect(page.getByText('Письмо было повторно отправлено на указанную почту.', { exact: true })).toBeVisible();
  await expect(resend).toBeDisabled();
  expect(resendCalls).toBe(1);
  await expect(page).toHaveURL(/\/register$/);
  expect(registrationCalls).toBe(1);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain(email);
  expect(saved).not.toContain(password);
  expect(saved).not.toContain('alice_01');
});

test('registration rejects nine-character passwords before contacting the API', async ({ page }) => {
  let registrationCalls = 0;
  await page.route('**/api/v1/players/register', async (route) => {
    registrationCalls += 1;
    await route.abort();
  });

  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Email', { exact: true }).fill('alice@example.test');
  await page.getByLabel('Пароль', { exact: true }).fill('Aa12345!b');
  await page.getByLabel('Повторите пароль').fill('Aa12345!b');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(page.getByText('Пароль должен содержать от 10 до 128 символов.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Пароль', { exact: true })).toHaveAttribute('aria-invalid', 'true');
  expect(registrationCalls).toBe(0);
});

test('registration accepts a valid ten-character password', async ({ page }) => {
  const email = 'alice@example.test';
  const password = 'Aa12345!bc';
  let registrationCalls = 0;
  await page.route('**/api/v1/players/register', async (route) => {
    registrationCalls += 1;
    expect(route.request().postDataJSON()).toEqual({
      username: 'alice_01',
      email,
      password,
    });
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
    'Письмо было отправлено на указанную почту.',
  );
  expect(registrationCalls).toBe(1);
});

test('registration validates email on blur and blocks invalid domains', async ({ page }) => {
  let registrationCalls = 0;
  await page.route('**/api/v1/players/register', async (route) => {
    registrationCalls += 1;
    await route.abort();
  });

  await page.goto('/register');
  const emailInput = page.getByLabel('Email', { exact: true });
  await expect(emailInput).not.toHaveAttribute('aria-invalid', 'true');
  await emailInput.fill('alice@gmail');
  await expect(page.getByText('Введите полный email, например name@example.com.', { exact: true })).toHaveCount(0);
  await emailInput.blur();

  const emailError = page.getByText('Введите полный email, например name@example.com.', { exact: true });
  await expect(emailError).toBeVisible();
  await expect(emailInput).toHaveAttribute('aria-invalid', 'true');
  await expect(emailInput).toHaveAttribute('aria-describedby', /register-email-hint register-email-error/);

  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Пароль', { exact: true }).fill('Correct horse battery 7!');
  await page.getByLabel('Повторите пароль').fill('Correct horse battery 7!');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(emailInput).toBeFocused();
  expect(registrationCalls).toBe(0);
});

test('registration rejects passwords without every character class', async ({ page }) => {
  let registrationCalls = 0;
  await page.route('**/api/v1/players/register', async (route) => {
    registrationCalls += 1;
    await route.abort();
  });

  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Email', { exact: true }).fill('alice@example.test');
  const passwordInput = page.getByLabel('Пароль', { exact: true });
  await passwordInput.fill('Correct horse battery7');
  await passwordInput.blur();

  const passwordError = page.getByText('Нужны строчная и заглавная буквы, цифра и спецсимвол.', { exact: true });
  await expect(passwordError).toBeVisible();
  await expect(passwordInput).toHaveAttribute('aria-invalid', 'true');
  await expect(passwordInput).toHaveAttribute('aria-describedby', /register-password-hint register-password-error/);
  await expect(page.getByText(/Пробел не считается спецсимволом/)).toBeVisible();

  await page.getByLabel('Повторите пароль').fill('Correct horse battery7');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(passwordInput).toBeFocused();
  expect(registrationCalls).toBe(0);
});

test('registration delivery failure offers inline resend and clears it when email changes', async ({ page }) => {
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
  await page.getByLabel('Пароль', { exact: true }).fill('Correct horse battery 7!');
  await page.getByLabel('Повторите пароль').fill('Correct horse battery 7!');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();

  await expect(page.getByText('Регистрация временно недоступна. Попробуйте позже.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Не пришло письмо?', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Не пришло письмо?', exact: true })).toHaveCount(0);
  await page.getByLabel('Email', { exact: true }).fill('another@example.test');
  await expect(page.getByRole('button', { name: 'Не пришло письмо?', exact: true })).toHaveCount(0);
});

test('registration resend respects Retry-After and reports delivery failures in place', async ({ page }) => {
  let calls = 0;
  await page.clock.install({ time: '2026-09-30T00:00:00.000Z' });
  await page.clock.pauseAt('2026-09-30T00:00:00.000Z');
  await page.route('**/api/v1/players/register', async (route) => {
    await route.fulfill({ status: 202, headers: jsonHeaders, body: JSON.stringify(acceptedResponse) });
  });
  await page.route('**/api/v1/players/resend-verification', async (route) => {
    calls += 1;
    expect(route.request().postDataJSON()).toEqual({ email: 'alice@example.test' });
    const status = calls === 1 ? 429 : 503;
    await route.fulfill({
      status,
      headers: { 'content-type': 'application/problem+json', ...(status === 429 ? { 'retry-after': '7' } : {}) },
      body: JSON.stringify(problem(status, status === 429 ? 'Too Many Requests' : 'Service Unavailable')),
    });
  });
  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill('alice_01');
  await page.getByLabel('Email', { exact: true }).fill('alice@example.test');
  await page.getByLabel('Пароль', { exact: true }).fill('Correct horse battery 7!');
  await page.getByLabel('Повторите пароль').fill('Correct horse battery 7!');
  await page.getByRole('button', { name: 'Создать аккаунт' }).click();
  const resend = page.getByRole('button', { name: 'Не пришло письмо?', exact: true });
  await expect(resend).toBeDisabled();
  await page.clock.runFor(60_000);
  await expect(resend).toBeEnabled();
  const limitedResponse = page.waitForResponse('**/api/v1/players/resend-verification');
  await resend.click();
  expect((await limitedResponse).status()).toBe(429);
  await expect(page.getByText('Слишком много попыток. Повторите позже.', { exact: true })).toBeVisible();
  await expect(resend).toBeDisabled();
  await page.clock.runFor(6_000);
  await expect(resend).toBeDisabled();
  await expect(page.getByRole('timer')).toContainText('через 1 с.');
  // Allow the next one-second refresh after the server deadline.
  await page.clock.runFor(2_000);
  await expect(resend).toBeEnabled();
  await resend.click();
  await expect(page.getByText('Не удалось отправить письмо. Попробуйте позже.', { exact: true })).toBeVisible();
  await expect(page.getByText('Письмо было повторно отправлено на указанную почту.', { exact: true })).toHaveCount(0);
  await expect(page).toHaveURL(/\/register$/);
  expect(calls).toBe(2);
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
  await page.route('**/api/v1/public/tournaments**', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({ items: [], next_cursor: null }),
    });
  });

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Войти', exact: true }).click();

  await expect(page).toHaveURL('/arena');
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true })).toHaveCount(0);
  await expect(page.getByText(/Не пришло письмо/)).toHaveCount(0);
  expect(loginCalls).toBe(1);
  expect(meCalls).toBeGreaterThan(0);
  const saved = await storageSnapshot(page);
  expect(saved).toContain(playerID);
  expect(saved).toContain('alice_01');
  expect(saved).not.toContain(email);
  expect(saved).not.toContain(password);
});

test('wrong login credentials show generic copy without a resend button', async ({ page }) => {
  await page.route('**/api/v1/players/login', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ login: 'alice_01', password: 'wrong-password' });
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(401, 'Unauthorized', 'Invalid credentials')),
    });
  });

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill('alice_01');
  await page.getByLabel('Пароль', { exact: true }).fill('wrong-password');
  await page.getByRole('button', { name: 'Войти', exact: true }).click();

  await expect(page.getByText('Неверный логин или пароль.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true })).toHaveCount(0);
  await expect(page.getByText('Invalid credentials')).toHaveCount(0);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain('wrong-password');
  expect(saved).not.toContain('alice_01');
});

test('unverified login shows an inline resend button and sends only after a click', async ({ page }) => {
  const email = 'alice@example.test';
  const password = 'correct horse battery';
  let loginCalls = 0;
  let resendCalls = 0;
  await page.addInitScript(() => {
    document.cookie = 'tpm_player_csrf=old-session-token; Path=/';
  });
  await page.route('**/api/v1/players/login', async (route) => {
    loginCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ login: email, password });
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(
        401,
        'Unauthorized',
        'Activate your account by verifying your email before signing in',
        'player.email_unverified',
      )),
    });
  });
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    resendCalls += 1;
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ login: email, password });
    expect(route.request().headers().authorization).toBeUndefined();
    expect(route.request().headers()['x-csrf-token']).toBeUndefined();
    await route.fulfill({
      status: 202,
      headers: jsonHeaders,
      body: JSON.stringify(acceptedResponse),
    });
  });

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Войти', exact: true }).click();

  await expect(page.getByText(
    'Аккаунт не активирован. Подтвердите email по ссылке из письма.',
    { exact: true },
  )).toBeVisible();
  const resendButton = page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true });
  await expect(resendButton).toBeVisible();
  expect(loginCalls).toBe(1);
  expect(resendCalls).toBe(0);

  await resendButton.click();
  await expect(page.getByText('Письмо подтверждения отправлено. Проверьте почту.', { exact: true })).toBeVisible();
  await expect(resendButton).toBeDisabled();
  await expect(page.getByRole('timer')).toContainText('Повторная отправка доступна через');
  expect(resendCalls).toBe(1);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain(email);
  expect(saved).not.toContain(password);
});

test('unverified login can resend to the account selected by username', async ({ page }) => {
  const username = 'alice_01';
  const password = 'correct horse battery';
  let resendCalls = 0;
  await prepareUnverifiedLogin(page, username, password);
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    resendCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ login: username, password });
    await route.fulfill({ status: 202, headers: jsonHeaders, body: JSON.stringify(acceptedResponse) });
  });

  await page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true }).click();

  await expect(page.getByText('Письмо подтверждения отправлено. Проверьте почту.', { exact: true })).toBeVisible();
  expect(resendCalls).toBe(1);
});

test('resend cooldown response disables the button and shows wait guidance', async ({ page }) => {
  await prepareUnverifiedLogin(page, 'alice@example.test', 'correct horse battery');
  let resendCalls = 0;
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    resendCalls += 1;
    await route.fulfill({
      status: 429,
      headers: { 'content-type': 'application/problem+json', 'retry-after': '60' },
      body: JSON.stringify(problem(429, 'Too Many Requests')),
    });
  });
  const resendButton = page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true });

  await resendButton.click();

  await expect(page.getByText('Слишком много попыток. Подождите перед повторным запросом.', { exact: true })).toBeVisible();
  await expect(resendButton).toBeDisabled();
  await expect(page.getByRole('timer')).toContainText('Повторная отправка доступна через 60 с.');
  expect(resendCalls).toBe(1);
});

test('resend hides the activation action when the account is already verified', async ({ page }) => {
  await prepareUnverifiedLogin(page, 'alice@example.test', 'correct horse battery');
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    await route.fulfill({
      status: 409,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(409, 'Conflict', undefined, 'player.email_already_verified')),
    });
  });

  await page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true }).click();

  await expect(page.getByText('Аккаунт уже подтвержден. Нажмите кнопку Войти.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true })).toHaveCount(0);
});

test('resend hides activation guidance when the supplied credentials are wrong', async ({ page }) => {
  await prepareUnverifiedLogin(page, 'alice@example.test', 'correct horse battery');
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(401, 'Unauthorized', 'Invalid credentials')),
    });
  });

  await page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true }).click();

  await expect(page.getByText('Неверный логин или пароль.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true })).toHaveCount(0);
  await expect(page.getByText('Аккаунт не активирован. Подтвердите email по ссылке из письма.', { exact: true })).toHaveCount(0);
});

test('resend failure remains retryable after the server cooldown', async ({ page }) => {
  await prepareUnverifiedLogin(page, 'alice@example.test', 'correct horse battery');
  let resendCalls = 0;
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    resendCalls += 1;
    if (resendCalls === 1) {
      await route.fulfill({
        status: 503,
        headers: { 'content-type': 'application/problem+json', 'retry-after': '1' },
        body: JSON.stringify(problem(503, 'Service Unavailable')),
      });
      return;
    }
    await route.fulfill({ status: 202, headers: jsonHeaders, body: JSON.stringify(acceptedResponse) });
  });
  const resendButton = page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true });

  await resendButton.click();

  await expect(page.getByText('Не удалось отправить письмо. Попробуйте позже.', { exact: true })).toBeVisible();
  await expect(resendButton).toBeDisabled();
  await expect(resendButton).toBeEnabled({ timeout: 3000 });
  await resendButton.click();
  await expect(page.getByText('Письмо подтверждения отправлено. Проверьте почту.', { exact: true })).toBeVisible();
  expect(resendCalls).toBe(2);
});

test('editing credentials clears activation state and aborts pending resend feedback', async ({ page }) => {
  await prepareUnverifiedLogin(page, 'alice@example.test', 'correct horse battery');
  let releaseResend: () => void = () => {};
  const delayedResponse = new Promise<void>((resolve) => {
    releaseResend = resolve;
  });
  await page.route('**/api/v1/players/login/resend-verification', async (route) => {
    await delayedResponse;
    try {
      await route.fulfill({ status: 202, headers: jsonHeaders, body: JSON.stringify(acceptedResponse) });
    } catch {
      // Editing the credentials aborts the in-flight browser request.
    }
  });
  const loginInput = page.getByLabel('Логин или email');

  await page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Отправляем письмо...');
  await expect(page.getByRole('button', { name: 'Войти', exact: true })).toBeDisabled();
  await loginInput.fill('other@example.test');
  releaseResend();

  await expect(page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true })).toHaveCount(0);
  await expect(page.getByText('Аккаунт не активирован. Подтвердите email по ссылке из письма.', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Отправляем письмо...', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Письмо подтверждения отправлено. Проверьте почту.', { exact: true })).toHaveCount(0);
});

test('verification token is removed from the URL and waits for explicit confirmation', async ({ page }) => {
  const token = 'private-verification-token';
  const clockStart = '2026-09-30T00:00:00.000Z';
  let verificationCalls = 0;
  let hashAtRequest = 'not-requested';
  await page.route('**/api/v1/players/verify-email', async (route) => {
    verificationCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ token });
    hashAtRequest = await page.evaluate(() => window.location.hash);
    await route.fulfill({ status: 204, body: '' });
  });

  await page.clock.install({ time: clockStart });
  await page.clock.pauseAt(clockStart);
  await page.goto(`/verify-email#token=${encodeURIComponent(token)}`);
  await expect(page).toHaveURL(/\/verify-email$/);
  await expectVerificationPageWithoutResend(page);
  const verificationPanel = page.getByRole('region', { name: 'Подтверждение email' });
  await expect(verificationPanel.getByRole('button', { name: 'Подтвердить email' })).toBeVisible();
  expect(verificationCalls).toBe(0);
  const saved = await storageSnapshot(page);
  expect(saved).not.toContain(token);
  expect(page.url()).not.toContain(token);

  await page.getByRole('button', { name: 'Подтвердить email' }).click();
  await expect(page.getByRole('status')).toContainText('Email подтверждён. Переход ко входу через 5 секунд.');
  await expectVerificationPageWithoutResend(page);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveAttribute('href', '/login');
  expect(verificationCalls).toBe(1);
  expect(hashAtRequest).toBe('');
  expect((await storageSnapshot(page))).not.toContain(token);

  await page.clock.runFor(4_999);
  await expect(page).toHaveURL(/\/verify-email$/);
  await page.clock.runFor(1);
  await expect(page).toHaveURL(/\/login$/);
});

test('choosing sign in cancels the automatic redirect when leaving the verification page', async ({ page }) => {
  const token = 'manual-sign-in-token';
  const clockStart = '2026-09-30T00:00:00.000Z';
  await page.route('**/api/v1/players/verify-email', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ token });
    await route.fulfill({ status: 204, body: '' });
  });

  await page.clock.install({ time: clockStart });
  await page.clock.pauseAt(clockStart);
  await page.goto(`/verify-email#token=${encodeURIComponent(token)}`);
  await page.getByRole('button', { name: 'Подтвердить email', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Email подтверждён. Переход ко входу через 5 секунд.');
  await expectVerificationPageWithoutResend(page);
  await page.getByRole('link', { name: 'Войти', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);

  await page.getByRole('link', { name: 'Создать аккаунт', exact: true }).click();
  await expect(page).toHaveURL(/\/register$/);
  await page.clock.runFor(5_000);
  await expect(page).toHaveURL(/\/register$/);
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
  await expectVerificationPageWithoutResend(page);
  await page.getByRole('button', { name: 'Подтвердить email' }).click();
  await expect(page.getByText('Слишком много попыток. Повторите позже.', { exact: true })).toBeVisible();
  const verificationPanel = page.getByRole('region', { name: 'Подтверждение email' });
  await expect(verificationPanel.getByRole('button', { name: 'Повторить подтверждение' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveCount(0);
  expect(await storageSnapshot(page)).not.toContain(token);

  await page.getByRole('button', { name: 'Повторить подтверждение' }).click();
  await expect(page.getByRole('status')).toContainText('Email подтверждён. Переход ко входу через 5 секунд.');
  await expectVerificationPageWithoutResend(page);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveAttribute('href', '/login');
  expect(verificationCalls).toBe(2);
});

test('verification page without a token shows no resend or sign-in controls', async ({ page }) => {
  await page.goto('/verify-email');
  await expectVerificationPageWithoutResend(page);
  await expect(page.getByText('В ссылке нет кода подтверждения.', { exact: true })).toBeVisible();
  const verificationPanel = page.getByRole('region', { name: 'Подтверждение email' });
  await expect(verificationPanel.getByRole('button', { name: 'Подтвердить email', exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveCount(0);
});

test('invalid verification token has no resend or sign-in controls', async ({ page }) => {
  const token = 'invalid-verification-token';
  await page.route('**/api/v1/players/verify-email', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ token });
    await route.fulfill({
      status: 400,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify(problem(400, 'Bad Request')),
    });
  });

  await page.goto(`/verify-email#token=${encodeURIComponent(token)}`);
  await expect(page).toHaveURL(/\/verify-email$/);
  await expectVerificationPageWithoutResend(page);
  await page.getByRole('button', { name: 'Подтвердить email', exact: true }).click();
  const invalidTokenError = 'Ссылка недействительна, уже использована или больше не действует.';
  await expect(page.getByRole('alert').filter({ hasText: invalidTokenError })).toHaveText(invalidTokenError);
  const verificationPanel = page.getByRole('region', { name: 'Подтверждение email' });
  await expect(verificationPanel.getByRole('button', { name: 'Подтвердить email', exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveCount(0);
});
