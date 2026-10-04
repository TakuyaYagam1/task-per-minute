import { expect, test } from '@playwright/test';

import { jsonHeaders, mockPlayerLogout, nowISO, openAccountMenu } from './support/common';

const playerID = '77777777-7777-7777-7777-777777777777';

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
  await page.route('**/api/v1/public/tournaments**', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({ items: [], next_cursor: null }),
    });
  });
  await mockPlayerLogout(page);
  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify({ type: 'about:blank', title: 'Unauthorized', status: 401 }),
    });
  });
});

test('restores a cookie-backed player and opens the public Arena catalog', async ({ page }) => {
  let meCalls = 0;
  let websocketCalls = 0;
  page.on('websocket', (socket) => {
    if (!new URL(socket.url()).pathname.startsWith('/_next/')) {
      websocketCalls += 1;
    }
  });
  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers().authorization).toBeUndefined();
    expect(route.request().headers().cookie).toContain('tpm_player_session=synthetic-cookie-session');
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: { id: playerID, username: 'alice', created_at: nowISO() },
      }),
    });
  });

  await page.goto('/login');
  const frontendOrigin = new URL(page.url()).origin;
  await page.context().addCookies([
    {
      name: 'tpm_player_session',
      value: 'synthetic-cookie-session',
      url: frontendOrigin,
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);

  await page.goto('/');

  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
  await expect.poll(() => meCalls).toBeGreaterThan(0);
  expect(websocketCalls).toBe(0);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBe('alice');
  const accountMenu = await openAccountMenu(page);
  await expect(accountMenu.getByRole('button', { name: 'Выйти', exact: true })).toBeVisible();
});

test('a guest entering at the root can browse the public arena', async ({ page }) => {
  await page.goto('/');

  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
  await expect(page.getByTestId('site-header').getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toHaveCount(0);
});

test('an expired session clears the cache and keeps the public arena open', async ({ page }) => {
  await page.addInitScript(({ playerID }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'stale-player');
  }, { playerID });

  await page.goto('/');

  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Регистрация', exact: true })).toBeVisible();
  await expect(page.getByText('stale-player', { exact: true })).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect(page.getByRole('dialog', { name: 'Аккаунт удален' })).toHaveCount(0);
});

test('an administrator-deleted account shows a notice and routes to sign-in after acknowledgement', async ({ page }) => {
  await page.addInitScript(({ playerID }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'deleted-player');
  }, { playerID });
  let meCalls = 0;
  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Unauthorized',
        status: 401,
        code: 'player.account_deleted',
      }),
    });
  });

  await page.goto('/');

  const notice = page.getByRole('dialog', { name: 'Аккаунт удален' });
  await expect(notice).toBeVisible();
  await expect(notice.getByText('Администратор удалил ваш аккаунт.', { exact: true })).toBeVisible();
  await expect(notice.getByRole('button', { name: 'Понятно', exact: true })).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  expect(meCalls).toBeGreaterThan(0);

  await notice.getByRole('button', { name: 'Понятно', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();
});

test('an administrator-deleted cookie session shows a notice without cached identity', async ({ page }) => {
  await page.goto('/login');
  const frontendOrigin = new URL(page.url()).origin;
  await page.context().addCookies([
    {
      name: 'tpm_player_session',
      value: 'synthetic-cookie-session',
      url: frontendOrigin,
      httpOnly: true,
      sameSite: 'Lax',
    },
    {
      name: 'tpm_player_csrf',
      value: 'synthetic-cookie-csrf',
      url: frontendOrigin,
      sameSite: 'Lax',
    },
  ]);
  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers().cookie).toContain('tpm_player_session=synthetic-cookie-session');
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Unauthorized',
        status: 401,
        code: 'player.account_deleted',
      }),
    });
  });

  await page.goto('/');

  const notice = page.getByRole('dialog', { name: 'Аккаунт удален' });
  await expect(notice).toBeVisible();
  await expect(notice.getByText('Администратор удалил ваш аккаунт.', { exact: true })).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => document.cookie.includes('tpm_player_csrf='))).toBe(false);
});

test('a session network failure does not block the public catalog or report a deleted account', async ({ page }) => {
  const failedRequest = page.waitForEvent('requestfailed', (request) => (
    new URL(request.url()).pathname === '/api/v1/players/me'
  ));
  await page.route('**/api/v1/players/me', async (route) => {
    await route.abort('timedout');
  });

  await page.goto('/');

  const request = await failedRequest;
  expect(request.failure()).not.toBeNull();
  await expect(page.getByRole('dialog', { name: 'Аккаунт удален' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toHaveCount(0);
  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
});

test('blocked browser storage does not crash the account links', async ({ page }) => {
  const pageErrors: string[] = [];
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });
  await page.addInitScript(() => {
    const throwSecurityError = () => {
      throw new DOMException('blocked', 'SecurityError');
    };
    Storage.prototype.getItem = throwSecurityError;
    Storage.prototype.setItem = throwSecurityError;
    Storage.prototype.removeItem = throwSecurityError;
  });

  await page.goto('/');

  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Регистрация', exact: true })).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('changing player clears the restore cache and sends logout CSRF', async ({ page }) => {
  const csrfToken = 'player-logout-csrf';
  let logoutCalls = 0;
  let releaseLogout: () => void = () => {};
  const logoutGate = new Promise<void>((resolve) => {
    releaseLogout = resolve;
  });
  await page.addInitScript(({ playerID, csrfToken }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    document.cookie = `tpm_player_csrf=${csrfToken}; Path=/`;
  }, { playerID, csrfToken });
  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: { ...jsonHeaders, 'X-CSRF-Token': csrfToken },
      body: JSON.stringify({
        player: { id: playerID, username: 'alice', created_at: nowISO() },
      }),
    });
  });
  await page.unroute('**/api/v1/players/logout');
  await page.route('**/api/v1/players/logout', async (route) => {
    logoutCalls += 1;
    expect(route.request().headers()['x-csrf-token']).toBe(csrfToken);
    await logoutGate;
    await route.fulfill({ status: 204, body: '' });
  });

  await page.goto('/');
  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();
  const accountMenu = await openAccountMenu(page);
  await accountMenu.getByRole('button', { name: 'Выйти', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toHaveCount(0);
  await expect.poll(() => logoutCalls).toBe(1);
  releaseLogout();

  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('heading', { name: 'Вход участника', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();
  await expect.poll(() => logoutCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => document.cookie.includes('tpm_player_csrf='))).toBe(false);
});

test('a late deleted-account response cannot replace a newer player session', async ({ page }) => {
  const newPlayerID = '88888888-8888-8888-8888-888888888888';
  const csrfToken = 'player-login-csrf';
  let releaseStaleResponse = (): void => {};
  const staleResponseBarrier = new Promise<void>((resolve) => {
    releaseStaleResponse = resolve;
  });
  let meCalls = 0;
  await page.addInitScript(({ playerID, csrfToken }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'old-player');
    document.cookie = `tpm_player_csrf=${csrfToken}; Path=/`;
  }, { playerID, csrfToken });
  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    if (meCalls === 1) {
      await staleResponseBarrier;
      await route.fulfill({
        status: 401,
        headers: { 'content-type': 'application/problem+json' },
        body: JSON.stringify({
          type: 'about:blank',
          title: 'Unauthorized',
          status: 401,
          code: 'player.account_deleted',
        }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: { id: newPlayerID, username: 'new-player', created_at: nowISO() },
      }),
    });
  });
  await page.route('**/api/v1/players/login', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ login: 'new@example.test', password: 'new-password' });
    await route.fulfill({
      status: 200,
      headers: { ...jsonHeaders, 'X-CSRF-Token': csrfToken },
      body: JSON.stringify({ player_id: newPlayerID }),
    });
  });
  const staleRequestPromise = page.waitForRequest((request) => (
    new URL(request.url()).pathname === '/api/v1/players/me'
      && request.method() === 'GET'
  ));

  await page.goto('/login');
  await staleRequestPromise;
  await page.getByLabel('Логин или email').fill('new@example.test');
  await page.getByLabel('Пароль', { exact: true }).fill('new-password');
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  await expect(page).toHaveURL('/arena');
  await expect(page.getByRole('heading', { name: 'Соревнования', exact: true })).toBeVisible();

  releaseStaleResponse();
  await page.waitForEvent('requestfinished', (request) => (
    new URL(request.url()).pathname === '/api/v1/players/me'
  ));
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBe(newPlayerID);
  await expect(page.getByRole('dialog', { name: 'Аккаунт удален' })).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBe(newPlayerID);
});

test('the root route contains no extra landing screen', async ({ page }) => {
  await page.goto('/');

  await expect(page).toHaveURL(/\/arena$/);
  await expect(page.getByText('CTF Соревнования', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Arena', exact: true })).toHaveAttribute('href', '/arena');
});
