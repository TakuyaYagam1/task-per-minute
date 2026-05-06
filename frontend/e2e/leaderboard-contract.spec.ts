import { expect, test } from '@playwright/test';
import { jsonHeaders } from './support/common';

test('leaderboard renders backend payload and user-facing fetch errors', async ({ page }) => {
  await page.route('**/api/v1/leaderboard', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        entries: [
          {
            rank: 1,
            username: 'alice',
            tasks_solved: 3,
            total_solve_time_ms: 42100,
          },
          {
            rank: 2,
            username: 'bob',
            tasks_solved: 2,
            total_solve_time_ms: 65000,
          },
        ],
      }),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('alice')).toBeVisible();
  await expect(page.getByText('bob')).toBeVisible();
  await expect(page.getByText('Всего игроков')).toBeVisible();

  const errorPage = await page.context().newPage();
  await errorPage.route('**/api/v1/leaderboard', async (route) => {
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
  await page.route('**/api/v1/leaderboard', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        entries: [
          {
            rank: 1.5,
            username: 'alice',
            tasks_solved: 3.25,
            total_solve_time_ms: -1,
          },
        ],
      }),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('Не удалось загрузить рейтинг')).toBeVisible();
  await expect(page.getByText('NaN')).toBeHidden();
  await expect(page.getByText('undefined')).toBeHidden();
  await expect(page.getByText('alice')).toBeHidden();
});

test('leaderboard ignores delayed stale polling response after newer data', async ({ page }) => {
  await page.clock.install();
  let requestCount = 0;
  let releaseFirstResponse: () => void = () => {
    throw new Error('First leaderboard response was not requested');
  };

  await page.route('**/api/v1/leaderboard', async (route) => {
    requestCount += 1;

    if (requestCount === 1) {
      await new Promise<void>((resolve) => {
        releaseFirstResponse = resolve;
      });
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({
          entries: [
            {
              rank: 1,
              username: 'stale-player',
              tasks_solved: 1,
              total_solve_time_ms: 90000,
            },
          ],
        }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        entries: [
          {
            rank: 1,
            username: 'fresh-player',
            tasks_solved: 5,
            total_solve_time_ms: 30000,
          },
        ],
      }),
    });
  });

  await page.goto('/leaderboard');
  await expect.poll(() => requestCount).toBe(1);

  await page.clock.fastForward(5000);
  await expect.poll(() => requestCount).toBe(2);
  await expect(page.getByText('fresh-player')).toBeVisible();

  releaseFirstResponse?.();
  await page.waitForTimeout(150);

  await expect(page.getByText('fresh-player')).toBeVisible();
  await expect(page.getByText('stale-player')).toBeHidden();
});

test('leaderboard keeps last valid entries after background fetch error', async ({ page }) => {
  await page.clock.install();
  let requestCount = 0;

  await page.route('**/api/v1/leaderboard', async (route) => {
    requestCount += 1;
    if (requestCount === 1) {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({
          entries: [
            {
              rank: 1,
              username: 'stable-player',
              tasks_solved: 4,
              total_solve_time_ms: 41000,
            },
          ],
        }),
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

  await page.clock.fastForward(5000);
  await expect.poll(() => requestCount).toBe(2);
  await page.waitForTimeout(150);

  await expect(page.getByText('stable-player')).toBeVisible();
  await expect(page.getByText('background leaderboard unavailable')).toBeHidden();
});

test('leaderboard keeps last valid entries after background malformed response', async ({ page }) => {
  await page.clock.install();
  let requestCount = 0;

  await page.route('**/api/v1/leaderboard', async (route) => {
    requestCount += 1;
    if (requestCount === 1) {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({
          entries: [
            {
              rank: 1,
              username: 'valid-player',
              tasks_solved: 7,
              total_solve_time_ms: 12000,
            },
          ],
        }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        entries: [
          {
            rank: 2.5,
            username: 'malformed-player',
            tasks_solved: -1,
            total_solve_time_ms: Number.NaN,
          },
        ],
      }),
    });
  });

  await page.goto('/leaderboard');
  await expect(page.getByText('valid-player')).toBeVisible();

  await page.clock.fastForward(5000);
  await expect.poll(() => requestCount).toBe(2);
  await page.waitForTimeout(150);

  await expect(page.getByText('valid-player')).toBeVisible();
  await expect(page.getByText('malformed-player')).toBeHidden();
  await expect(page.getByText('NaN')).toBeHidden();
  await expect(page.getByText('undefined')).toBeHidden();
});

test('leaderboard shows initial fetch error when no entries are available', async ({ page }) => {
  await page.route('**/api/v1/leaderboard', async (route) => {
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
