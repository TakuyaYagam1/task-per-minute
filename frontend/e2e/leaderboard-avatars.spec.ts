import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { expect, test, type Page } from '@playwright/test';

import { jsonHeaders } from './support/common';

const pngPlayerID = '11111111-1111-4111-8111-111111111111';
const gifPlayerID = '22222222-2222-4222-8222-222222222222';
const missingPlayerID = '33333333-3333-4333-8333-333333333333';
const videoPlayerID = '44444444-4444-4444-8444-444444444444';
const firstVersion = 'a'.repeat(64);
const secondVersion = 'b'.repeat(64);
const png = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j9pAAAAAASUVORK5CYII=',
  'base64',
);
const gif = Buffer.from(
  'R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7',
  'base64',
);

interface TestAvatar {
  player_id: string;
  version: string;
  content_type: 'image/jpeg' | 'image/png' | 'image/gif' | 'video/mp4';
}

interface TestLeaderboardEntry {
  rank: number;
  username: string;
  wins: number;
  average_solve_time_ms: number;
  avatar?: TestAvatar | null;
}

const leaderboardPayload = (entries: TestLeaderboardEntry[]) => ({
  entries,
  page: 1,
  per_page: 100,
  total: entries.length,
  total_pages: entries.length === 0 ? 0 : 1,
});

const entry = (
  rank: number,
  username: string,
  avatar?: TestAvatar | null,
): TestLeaderboardEntry => ({
  rank,
  username,
  wins: 1,
  average_solve_time_ms: 42_100,
  ...(avatar === undefined ? {} : { avatar }),
});

const installGuestSession = async (page: Page): Promise<void> => {
  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 401,
      headers: { 'content-type': 'application/problem+json' },
      body: JSON.stringify({ type: 'about:blank', title: 'Unauthorized', status: 401 }),
    });
  });
};

const installLeaderboard = async (
  page: Page,
  payload: () => ReturnType<typeof leaderboardPayload>,
  onRequest?: () => void,
): Promise<void> => {
  await page.route('**/api/v1/leaderboard*', async (route) => {
    onRequest?.();
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(payload()),
    });
  });
};

test('public leaderboard shows PNG and GIF avatars and falls back for missing or stale media', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  await installGuestSession(page);
  await installLeaderboard(page, () => leaderboardPayload([
    entry(1, 'png-player', {
      player_id: pngPlayerID,
      version: firstVersion,
      content_type: 'image/png',
    }),
    entry(2, 'gif-player', {
      player_id: gifPlayerID,
      version: firstVersion,
      content_type: 'image/gif',
    }),
    entry(3, 'no-avatar-player'),
    entry(4, 'stale-avatar-player', {
      player_id: missingPlayerID,
      version: firstVersion,
      content_type: 'image/png',
    }),
  ]));

  const requests: string[] = [];
  await page.route('**/api/v1/players/*/avatar*', async (route) => {
    const url = new URL(route.request().url());
    const playerID = url.pathname.split('/').at(-2) ?? '';
    const version = url.searchParams.get('v') ?? '';
    requests.push(`${playerID}:${version}`);
    if (playerID === pngPlayerID) {
      await route.fulfill({ status: 200, headers: { 'content-type': 'image/png' }, body: png });
      return;
    }
    if (playerID === gifPlayerID) {
      await route.fulfill({ status: 200, headers: { 'content-type': 'image/gif' }, body: gif });
      return;
    }
    await route.fulfill({ status: 404, headers: { 'content-type': 'application/problem+json' }, body: '{}' });
  });

  await page.goto('/leaderboard');
  const pngRow = page.locator('tbody tr').filter({ hasText: 'png-player' });
  const gifRow = page.locator('tbody tr').filter({ hasText: 'gif-player' });
  const missingRow = page.locator('tbody tr').filter({ hasText: 'no-avatar-player' });
  const staleRow = page.locator('tbody tr').filter({ hasText: 'stale-avatar-player' });

  for (const row of [pngRow, gifRow]) {
    const image = row.locator('img');
    await expect(image).toBeVisible();
    await expect.poll(() => image.evaluate((element) => {
      const avatarImage = element as HTMLImageElement;
      return avatarImage.complete && avatarImage.naturalWidth > 0;
    })).toBe(true);
    await expect(image).toHaveAttribute('src', /^blob:/);
  }
  await expect(missingRow.locator('svg')).toBeVisible();
  await expect(staleRow.locator('svg')).toBeVisible();
  await expect(staleRow.locator('img')).toHaveCount(0);
  await expect.poll(() => requests.length).toBe(3);
  expect(requests).toEqual(expect.arrayContaining([
    `${pngPlayerID}:${firstVersion}`,
    `${gifPlayerID}:${firstVersion}`,
    `${missingPlayerID}:${firstVersion}`,
  ]));
  await expect(page.getByRole('heading', { name: 'Общий рейтинг', exact: true })).toBeVisible();
});

test('avatar media loads only near the viewport and survives five-second leaderboard polls', async ({ page }) => {
  await page.clock.install();
  await installGuestSession(page);
  let leaderboardRequests = 0;
  await installLeaderboard(
    page,
    () => leaderboardPayload([
      entry(1, 'visible-player', {
        player_id: pngPlayerID,
        version: firstVersion,
        content_type: 'image/png',
      }),
    ]),
    () => { leaderboardRequests += 1; },
  );

  let avatarRequests = 0;
  await page.route(`**/api/v1/players/${pngPlayerID}/avatar*`, async (route) => {
    avatarRequests += 1;
    await route.fulfill({ status: 200, headers: { 'content-type': 'image/png' }, body: png });
  });

  await page.goto('/leaderboard');
  const image = page.locator('tbody tr').filter({ hasText: 'visible-player' }).locator('img');
  await expect(image).toBeVisible();
  await expect.poll(() => image.evaluate((element) => (element as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
  const originalSource = await image.getAttribute('src');
  expect(avatarRequests).toBe(1);

  await page.clock.fastForward(5_000);
  await expect.poll(() => leaderboardRequests).toBe(2);
  await page.waitForTimeout(100);
  await expect(image).toHaveAttribute('src', originalSource!);
  expect(avatarRequests).toBe(1);
});

test('offscreen avatar requests wait until its leaderboard row approaches the viewport', async ({ page }) => {
  await installGuestSession(page);
  const entries = Array.from({ length: 100 }, (_, index) =>
    entry(
      index + 1,
      `player-${index + 1}`,
      index === 99
        ? { player_id: pngPlayerID, version: firstVersion, content_type: 'image/png' }
        : undefined,
    ),
  );
  await installLeaderboard(page, () => leaderboardPayload(entries));

  let avatarRequests = 0;
  await page.route(`**/api/v1/players/${pngPlayerID}/avatar*`, async (route) => {
    avatarRequests += 1;
    await route.fulfill({ status: 200, headers: { 'content-type': 'image/png' }, body: png });
  });

  await page.goto('/leaderboard');
  const row = page.locator('tbody tr').filter({ hasText: 'player-100' });
  await expect(row).toBeAttached();
  await expect(row).not.toBeInViewport();
  await page.waitForTimeout(150);
  expect(avatarRequests).toBe(0);

  await row.scrollIntoViewIfNeeded();
  await expect.poll(() => avatarRequests).toBe(1);
  await expect(row.locator('img')).toBeVisible();
  await expect.poll(() => row.locator('img').evaluate((element) => (element as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);
});

test('changing avatar version replaces the media and revokes the previous object URL', async ({ page }) => {
  await page.clock.install();
  await page.addInitScript(() => {
    const windowWithAvatarURLs = window as Window & { __revokedAvatarURLs?: string[] };
    windowWithAvatarURLs.__revokedAvatarURLs = [];
    const originalRevoke = URL.revokeObjectURL.bind(URL);
    URL.revokeObjectURL = (url: string) => {
      windowWithAvatarURLs.__revokedAvatarURLs?.push(url);
      originalRevoke(url);
    };
  });
  await installGuestSession(page);

  let version = firstVersion;
  let leaderboardRequests = 0;
  await installLeaderboard(
    page,
    () => leaderboardPayload([
      entry(1, 'changing-player', {
        player_id: pngPlayerID,
        version,
        content_type: 'image/png',
      }),
    ]),
    () => { leaderboardRequests += 1; },
  );

  const avatarRequests: string[] = [];
  await page.route(`**/api/v1/players/${pngPlayerID}/avatar*`, async (route) => {
    const requestVersion = new URL(route.request().url()).searchParams.get('v') ?? '';
    avatarRequests.push(requestVersion);
    await route.fulfill({ status: 200, headers: { 'content-type': 'image/png' }, body: png });
  });

  await page.goto('/leaderboard');
  const image = page.locator('tbody tr').filter({ hasText: 'changing-player' }).locator('img');
  await expect.poll(() => image.evaluate((element) => (element as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);
  const previousSource = await image.getAttribute('src');
  expect(previousSource).toMatch(/^blob:/);

  version = secondVersion;
  await page.clock.fastForward(5_000);
  await expect.poll(() => leaderboardRequests).toBe(2);
  await expect.poll(() => avatarRequests).toEqual([firstVersion, secondVersion]);
  await expect.poll(() => image.getAttribute('src')).not.toBe(previousSource);
  await expect.poll(() => image.evaluate((element) => (element as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);
  await expect.poll(() => page.evaluate(() =>
    (window as Window & { __revokedAvatarURLs?: string[] }).__revokedAvatarURLs ?? [],
  )).toContain(previousSource!);
});

test('MP4 leaderboard avatars play silently inline and pause for reduced motion without controls', async ({ page }) => {
  const videoBytes = await readFile(resolve(
    process.cwd(),
    '../backend/internal/adapter/outbound/media/ffmpeg/testdata/avatar.mp4',
  ));
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await installGuestSession(page);
  await installLeaderboard(page, () => leaderboardPayload([
    entry(1, 'video-player', {
      player_id: videoPlayerID,
      version: firstVersion,
      content_type: 'video/mp4',
    }),
  ]));
  await page.route(`**/api/v1/players/${videoPlayerID}/avatar*`, async (route) => {
    await route.fulfill({ status: 200, headers: { 'content-type': 'video/mp4' }, body: videoBytes });
  });

  await page.goto('/leaderboard');
  const video = page.locator('tbody tr').filter({ hasText: 'video-player' }).locator('video');
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState))
    .toBeGreaterThanOrEqual(2);
  expect(await video.evaluate((element) => {
    const media = element as HTMLVideoElement;
    return {
      controls: media.controls,
      muted: media.muted,
      playsInline: media.playsInline,
      loop: media.loop,
      autoplay: media.autoplay,
    };
  })).toEqual({ controls: false, muted: true, playsInline: true, loop: true, autoplay: true });
  await expect.poll(() => video.evaluate((element) => !(element as HTMLVideoElement).paused)).toBe(true);

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect.poll(() => video.evaluate((element) => {
    const media = element as HTMLVideoElement;
    return { paused: media.paused, loop: media.loop, autoplay: media.autoplay };
  })).toEqual({ paused: true, loop: false, autoplay: false });
});

test('a public avatar 401 does not clear the current player session', async ({ page }) => {
  await page.addInitScript(({ playerID }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'signed-in-player');
  }, { playerID: pngPlayerID });
  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: pngPlayerID,
          username: 'signed-in-player',
          created_at: '2026-10-01T00:00:00Z',
        },
      }),
    });
  });
  await page.route('**/api/v1/players/notifications', async (route) => {
    await route.fulfill({ status: 200, headers: jsonHeaders, body: JSON.stringify({ notifications: [] }) });
  });
  await page.route('**/api/v1/players/notifications/events', async (route) => {
    await route.fulfill({ status: 204, body: '' });
  });
  await page.route('**/api/v1/players/account/avatar', async (route) => {
    await route.fulfill({ status: 404, headers: { 'content-type': 'application/problem+json' }, body: '{}' });
  });
  await installLeaderboard(page, () => leaderboardPayload([
    entry(1, 'signed-in-player', {
      player_id: pngPlayerID,
      version: firstVersion,
      content_type: 'image/png',
    }),
  ]));

  let publicRequestCookie: string | undefined;
  let publicRequests = 0;
  await page.route(`**/api/v1/players/${pngPlayerID}/avatar*`, async (route) => {
    publicRequests += 1;
    publicRequestCookie = route.request().headers().cookie;
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

  await page.goto('/login');
  const origin = new URL(page.url()).origin;
  await page.context().addCookies([{
    name: 'tpm_player_session',
    value: 'synthetic-session-cookie',
    url: origin,
    httpOnly: true,
    sameSite: 'Lax',
  }]);
  await page.goto('/leaderboard');

  await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toBeVisible();
  const row = page.locator('tbody tr').filter({ hasText: 'signed-in-player' });
  await expect(row.locator('svg')).toBeVisible();
  await expect.poll(() => publicRequests).toBe(1);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id')))
    .toBe(pngPlayerID);
  await expect(page.getByRole('dialog', { name: 'Аккаунт удален' })).toHaveCount(0);
  expect(publicRequestCookie).toBeUndefined();
});
