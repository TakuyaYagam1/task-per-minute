import { expect, test } from '@playwright/test';

import { jsonHeaders, mockPlayerLogout, nowISO } from './support/common';

test.beforeEach(async ({ page }) => {
  await mockPlayerLogout(page);
});

test('restores a cookie-backed player session without opening a websocket', async ({ page }) => {
  const playerID = '77777777-7777-7777-7777-777777777777';
  let meCalls = 0;
  let websocketCalls = 0;

  await page.addInitScript(({ playerID }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
  }, { playerID });
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
        player: {
          id: playerID,
          username: 'alice',
          created_at: nowISO(),
        },
      }),
    });
  });

  await page.goto('/');

  await expect(page.getByText('Игрок готов')).toBeVisible();
  await expect(page.getByText('alice')).toBeVisible();
  await expect.poll(() => meCalls).toBe(1);
  expect(websocketCalls).toBe(0);
});

test('blocked browser storage does not crash the home page', async ({ page }) => {
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

  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect(page.getByLabel('Никнейм')).toBeVisible();
  await expect(page.getByText('Введите никнейм', { exact: true })).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});

test('valid join stores only the player restore cache', async ({ page }) => {
  const playerID = '7a7a7a7a-7a7a-7a7a-7a7a-7a7a7a7a7a7a';
  let joinCalls = 0;
  await page.route('**/api/v1/players/join', async (route) => {
    joinCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ username: 'alice_01' });
    expect(route.request().headers().authorization).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: { ...jsonHeaders, 'X-CSRF-Token': 'join-player-csrf' },
      body: JSON.stringify({ player_id: playerID }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('  alice_01  ');
  await page.getByRole('button', { name: 'ПОДКЛЮЧИТЬСЯ' }).click();

  await expect(page.getByText('Игрок готов')).toBeVisible();
  expect(joinCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBe('alice_01');
});

test('malformed join response does not persist a player session', async ({ page }) => {
  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({ player_id: 'not-a-uuid' }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: 'ПОДКЛЮЧИТЬСЯ' }).click();

  await expect(page.getByText('Ошибка соединения')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
});

test('join rate limit reports Retry-After without persisting a session', async ({ page }) => {
  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 429,
      headers: {
        ...jsonHeaders,
        'Retry-After': '7',
      },
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Too Many Requests',
        status: 429,
      }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: 'ПОДКЛЮЧИТЬСЯ' }).click();

  await expect(page.getByText('Слишком много попыток. Повторите через 7 секунд.')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
});

test('join form rejects invalid usernames before sending a request', async ({ page }) => {
  let joinCalls = 0;
  await page.route('**/api/v1/players/join', async (route) => {
    joinCalls += 1;
    await route.abort();
  });
  await page.goto('/');

  for (const username of ['a', 'alice!', 'alice bob', 'алиса']) {
    await page.getByPlaceholder('Введите никнейм...').fill(username);
    await page.getByRole('button', { name: 'ПОДКЛЮЧИТЬСЯ' }).click();
    await expect(page.getByText('Никнейм: 2-50 символов, латиница, цифры, _ или -')).toBeVisible();
  }

  expect(joinCalls).toBe(0);
});

test('changing player clears the restore cache and calls logout', async ({ page }) => {
  const playerID = '7b7b7b7b-7b7b-7b7b-7b7b-7b7b7b7b7b7b';
  const playerCSRFToken = 'player-logout-csrf';
  let logoutCalls = 0;
  await page.addInitScript(({ playerID }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    document.cookie = 'tpm_player_csrf=player-logout-csrf; Path=/';
  }, { playerID });
  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: { id: playerID, username: 'alice', created_at: nowISO() },
      }),
    });
  });
  await page.unroute('**/api/v1/players/logout');
  await page.route('**/api/v1/players/logout', async (route) => {
    logoutCalls += 1;
    expect(route.request().headers()['x-csrf-token']).toBe(playerCSRFToken);
    await route.fulfill({ status: 204, body: '' });
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Сменить игрока' }).click();

  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect.poll(() => logoutCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect
    .poll(() => page.evaluate(() => document.cookie.includes('tpm_player_csrf=')))
    .toBe(false);
  await expect
    .poll(() => page.evaluate(() => window.sessionStorage.getItem('player_csrf_token')))
    .toBeNull();
});

test('home exposes leaderboard and Arena navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { name: 'Task Per Minute', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'CTF Соревнования', exact: true })).toBeVisible();
  await expect(page.getByText('Платформа турниров CTF', { exact: true })).toHaveCount(0);
  await expect(page.getByText('CTF турнир', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Войдите как участник, чтобы сохранить браузерную сессию и открыть назначение.', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Используйте никнейм, который будет виден в турнирных списках.', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Введите никнейм, чтобы начать.', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Никнейм')).toHaveAttribute('placeholder', 'Введите никнейм...');

  await expect(page.getByRole('link', { name: 'Общий рейтинг' })).toHaveAttribute('href', '/leaderboard');
  const arenaLink = page.getByRole('link', { name: 'Открыть Arena' });
  await expect(arenaLink).toHaveAttribute('href', '/arena');
  await expect(arenaLink).toHaveCSS('border-style', 'solid');
  await expect(arenaLink).toHaveCSS('text-decoration-line', 'none');
  await expect(arenaLink).toHaveCSS('min-height', '44px');
  await expect(page.getByRole('link')).toHaveCount(3);
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
