import { expect, test } from '@playwright/test';
import { jsonHeaders } from './support/common';

interface TestLeaderboardEntry {
  rank: number;
  username: string;
  wins: number;
  average_solve_time_ms: number;
}

const leaderboardPayload = (
  entries: TestLeaderboardEntry[],
  page = 1,
  total = entries.length,
) => ({
  entries,
  page,
  per_page: 100,
  total,
  total_pages: total === 0 ? 0 : Math.ceil(total / 100),
});

test('leaderboard renders the public page and requests a complete first page', async ({ page }) => {
  const requests: URL[] = [];
  await page.route('**/api/v1/leaderboard*', async (route) => {
    requests.push(new URL(route.request().url()));
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(leaderboardPayload([
        { rank: 1, username: 'alice', wins: 3, average_solve_time_ms: 42_100 },
        { rank: 2, username: 'bob', wins: 2, average_solve_time_ms: 65_000 },
      ], 1, 2)),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('alice')).toBeVisible();
  await expect(page.getByText('bob')).toBeVisible();
  await expect.poll(() => requests.length).toBeGreaterThan(0);
  expect(requests[0].searchParams.get('wins')).toBe('all');
  expect(requests[0].searchParams.get('page')).toBe('1');
  expect(requests[0].searchParams.get('per_page')).toBe('100');
  await expect(page.getByRole('navigation', { name: 'Навигация рейтинга' })).toHaveCount(0);
  await expect(page.locator('main').getByRole('link', { name: 'На главную' })).toHaveCount(0);
  await expect(page.locator('main').getByRole('link', { name: 'Arena', exact: true })).toHaveCount(0);
  await expect(page.getByText(/^live$/i)).toHaveCount(0);
  await expect(page.getByText('Найдено игроков')).toBeVisible();
  await expect(page.getByText('Побед на странице')).toBeVisible();
  await expect(page.getByText('Среднее время на странице')).toBeVisible();
  await expect(page.getByText('Страница 1 из 1')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Предыдущая' })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Следующая' })).toBeDisabled();
  await expect(page.getByText('Победы').first()).toBeVisible();
  await expect(page.getByText('Задач', { exact: true })).toBeHidden();
  await expect(page.getByText('Задачи', { exact: true })).toBeHidden();

  const errorPage = await page.context().newPage();
  await errorPage.route('**/api/v1/leaderboard*', async (route) => {
    await route.fulfill({
      status: 503,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'service unavailable',
        status: 503,
        detail: 'leaderboard unavailable',
      }),
    });
  });
  await errorPage.goto('/leaderboard');
  await expect(errorPage.getByText('leaderboard unavailable')).toBeVisible();
});

test('malformed leaderboard response shows fallback instead of rendering invalid values', async ({ page }) => {
  await page.route('**/api/v1/leaderboard*', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(leaderboardPayload([
        { rank: 1.5, username: 'alice', wins: 3.25, average_solve_time_ms: -1 },
      ])),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('Не удалось загрузить рейтинг')).toBeVisible();
  await expect(page.getByText('NaN')).toBeHidden();
  await expect(page.getByText('undefined')).toBeHidden();
  await expect(page.getByText('alice')).toBeHidden();
});

test('leaderboard ignores a delayed stale polling response after newer data', async ({ page }) => {
  await page.clock.install();
  let requestCount = 0;
  let releaseFirstResponse: () => void = () => {
    throw new Error('First leaderboard response was not requested');
  };

  await page.route('**/api/v1/leaderboard*', async (route) => {
    requestCount += 1;
    if (requestCount === 1) {
      await new Promise<void>((resolve) => {
        releaseFirstResponse = resolve;
      });
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(leaderboardPayload([
          { rank: 1, username: 'stale-player', wins: 1, average_solve_time_ms: 90_000 },
        ])),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(leaderboardPayload([
        { rank: 1, username: 'fresh-player', wins: 5, average_solve_time_ms: 30_000 },
      ])),
    });
  });

  await page.goto('/leaderboard');
  await expect.poll(() => requestCount).toBe(1);
  await page.clock.fastForward(5_000);
  await expect.poll(() => requestCount).toBe(2);
  await expect(page.getByText('fresh-player')).toBeVisible();

  releaseFirstResponse?.();
  await page.waitForTimeout(150);
  await expect(page.getByText('fresh-player')).toBeVisible();
  await expect(page.getByText('stale-player')).toBeHidden();
});

test('leaderboard search and wins filter reset paging and follow browser history', async ({ page }) => {
  const requests: URL[] = [];
  await page.route('**/api/v1/leaderboard*', async (route) => {
    const url = new URL(route.request().url());
    requests.push(url);
    const requestedPage = Number(url.searchParams.get('page') ?? '1');
    const wins = url.searchParams.get('wins');
    const isWithoutWins = wins === 'withoutwins';
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(leaderboardPayload(
        [{
          rank: isWithoutWins ? 300 : requestedPage === 2 ? 102 : 1,
          username: 'alice',
          wins: isWithoutWins ? 0 : 3,
          average_solve_time_ms: 42_100,
        }],
        requestedPage,
        isWithoutWins ? 1 : 101,
      )),
    });
  });

  await page.goto('/leaderboard?search=alice&wins=withwins&page=2');
  await expect(page.getByText('Страница 2 из 2')).toBeVisible();
  const previousCount = requests.length;
  await page.getByLabel('Победы').selectOption('withoutwins');
  await expect(page).toHaveURL(/search=alice&wins=withoutwins/);
  await expect(page).not.toHaveURL(/page=2/);
  await expect(page.getByText('Страница 1 из 1')).toBeVisible();
  await expect.poll(() => requests.length).toBeGreaterThan(previousCount);
  expect(requests.at(-1)?.searchParams.get('search')).toBe('alice');
  expect(requests.at(-1)?.searchParams.get('wins')).toBe('withoutwins');
  expect(requests.at(-1)?.searchParams.get('page')).toBe('1');

  await page.goBack();
  await expect(page).toHaveURL(/search=alice&wins=withwins&page=2/);
  await expect(page.getByText('Страница 2 из 2')).toBeVisible();
  await expect.poll(() => requests.length).toBeGreaterThan(previousCount + 1);
});

test('a delayed search response cannot replace a newer search result', async ({ page }) => {
  const searches: string[] = [];
  let releaseOldSearch: () => void = () => {
    throw new Error('Old search request was not received');
  };
  await page.route('**/api/v1/leaderboard*', async (route) => {
    const search = new URL(route.request().url()).searchParams.get('search') ?? '';
    searches.push(search);
    if (search === 'first') {
      await new Promise<void>((resolve) => {
        releaseOldSearch = resolve;
      });
      try {
        await route.fulfill({
          status: 200,
          headers: jsonHeaders,
          body: JSON.stringify(leaderboardPayload([
            { rank: 1, username: 'first-result', wins: 1, average_solve_time_ms: 90_000 },
          ])),
        });
      } catch {
        // The browser may already have aborted this request after the next search.
      }
      return;
    }
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(leaderboardPayload([
        { rank: 2, username: 'second-result', wins: 2, average_solve_time_ms: 60_000 },
      ])),
    });
  });

  await page.goto('/leaderboard');
  await page.getByLabel('Поиск по имени').fill('first');
  await expect.poll(() => searches.includes('first')).toBe(true);
  await page.getByLabel('Поиск по имени').fill('second');
  await expect(page.getByText('second-result')).toBeVisible();
  releaseOldSearch?.();
  await page.waitForTimeout(150);
  await expect(page.getByText('second-result')).toBeVisible();
  await expect(page.getByText('first-result')).toBeHidden();
});

test('leaderboard keeps last valid entries after a background fetch error', async ({ page }) => {
  await page.clock.install();
  let requestCount = 0;
  await page.route('**/api/v1/leaderboard*', async (route) => {
    requestCount += 1;
    if (requestCount === 1) {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(leaderboardPayload([
          { rank: 1, username: 'stable-player', wins: 4, average_solve_time_ms: 41_000 },
        ])),
      });
      return;
    }
    await route.fulfill({
      status: 503,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'service unavailable',
        status: 503,
        detail: 'background leaderboard unavailable',
      }),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('stable-player')).toBeVisible();
  await page.clock.fastForward(5_000);
  await expect.poll(() => requestCount).toBe(2);
  await page.waitForTimeout(150);
  await expect(page.getByText('stable-player')).toBeVisible();
  await expect(page.getByText('background leaderboard unavailable')).toBeHidden();
});

test('leaderboard keeps last valid entries after a malformed background response', async ({ page }) => {
  await page.clock.install();
  let requestCount = 0;
  await page.route('**/api/v1/leaderboard*', async (route) => {
    requestCount += 1;
    if (requestCount === 1) {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(leaderboardPayload([
          { rank: 1, username: 'valid-player', wins: 7, average_solve_time_ms: 12_000 },
        ])),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify(leaderboardPayload([
        { rank: 2.5, username: 'malformed-player', wins: -1, average_solve_time_ms: Number.NaN },
      ])),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('valid-player')).toBeVisible();
  await page.clock.fastForward(5_000);
  await expect.poll(() => requestCount).toBe(2);
  await page.waitForTimeout(150);
  await expect(page.getByText('valid-player')).toBeVisible();
  await expect(page.getByText('malformed-player')).toBeHidden();
  await expect(page.getByText('NaN')).toBeHidden();
  await expect(page.getByText('undefined')).toBeHidden();
});

test('leaderboard shows initial fetch error when no entries are available', async ({ page }) => {
  await page.route('**/api/v1/leaderboard*', async (route) => {
    await route.fulfill({
      status: 503,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'service unavailable',
        status: 503,
        detail: 'initial leaderboard unavailable',
      }),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('initial leaderboard unavailable')).toBeVisible();
  await expect(page.getByText('Пока нет данных о игроках')).toBeHidden();
});
