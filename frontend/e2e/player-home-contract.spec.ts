import { expect, test } from '@playwright/test';

import { jsonHeaders, mockPlayerLogout, nowISO } from './support/common';

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
  await mockPlayerLogout(page);
  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify({ type: 'about:blank', title: 'Unauthorized', status: 401 }),
    });
  });
});

test('restores a cookie-backed player from the server without requiring local identity', async ({ page }) => {
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
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: { id: playerID, username: 'alice', created_at: nowISO() },
      }),
    });
  });

  await page.goto('/');

  await expect(page.getByText('Игрок готов')).toBeVisible();
  await expect(page.getByText('alice', { exact: true })).toBeVisible();
  await expect.poll(() => meCalls).toBeGreaterThan(0);
  expect(websocketCalls).toBe(0);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBe('alice');
});

test('an expired session clears the cache and shows account links', async ({ page }) => {
  await page.addInitScript(({ playerID }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'stale-player');
  }, { playerID });

  await page.goto('/');

  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();
  await expect(page.getByText('stale-player', { exact: true })).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
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

  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('changing player clears the restore cache and sends logout CSRF', async ({ page }) => {
  const csrfToken = 'player-logout-csrf';
  let logoutCalls = 0;
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
    await route.fulfill({ status: 204, body: '' });
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Выйти' }).click();

  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect.poll(() => logoutCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => document.cookie.includes('tpm_player_csrf='))).toBe(false);
});

test('home offers account links and Arena navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { name: 'Task Per Minute', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'CTF Соревнования', exact: true })).toBeVisible();
  await expect(page.getByText('Платформа турниров CTF', { exact: true })).toHaveCount(0);
  await expect(page.getByText('CTF турнир', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toHaveAttribute('href', '/login');
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toHaveAttribute('href', '/register');
  await expect(page.getByRole('link', { name: 'Общий рейтинг' })).toHaveAttribute('href', '/leaderboard');
  const arenaLink = page.getByRole('link', { name: 'Открыть Arena' });
  await expect(arenaLink).toHaveAttribute('href', '/arena');
  await expect(arenaLink).toHaveCSS('border-style', 'solid');
  await expect(arenaLink).toHaveCSS('text-decoration-line', 'none');
  await expect(arenaLink).toHaveCSS('min-height', '44px');
});

test('home centers its branding and keeps the Arena action touch-sized on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 900 });
  await page.goto('/');

  const layout = await page.evaluate(() => {
    const title = document.getElementById('home-title');
    const intro = title?.parentElement;
    const subtitle = intro?.querySelector<HTMLElement>('h2');
    if (!(title instanceof HTMLElement) || !(intro instanceof HTMLElement) || !subtitle) {
      throw new Error('Home branding layout is unavailable');
    }

    const pane = intro.getBoundingClientRect();
    const titleRect = title.getBoundingClientRect();
    const subtitleRect = subtitle.getBoundingClientRect();
    return {
      contentCenterY: (titleRect.top + subtitleRect.bottom) / 2,
      paneCenterX: pane.left + pane.width / 2,
      paneCenterY: pane.top + pane.height / 2,
      titleCenterX: titleRect.left + titleRect.width / 2,
    };
  });

  expect(Math.abs(layout.titleCenterX - layout.paneCenterX)).toBeLessThanOrEqual(1);
  expect(Math.abs(layout.contentCenterY - layout.paneCenterY)).toBeLessThanOrEqual(1);

  const arenaLink = page.getByRole('link', { name: 'Открыть Arena' });
  const linkBox = await arenaLink.boundingBox();
  expect(linkBox?.height ?? 0).toBeGreaterThanOrEqual(44);
});
