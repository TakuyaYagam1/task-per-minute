import { expect, test, type Page } from '@playwright/test';

import { jsonHeaders, openAccountMenu } from './support/common';
import { adminSessionResponse } from './support/admin';

const operatorTournamentID = '10000000-0000-4000-8000-000000000001';
const navigationPlayers = [
  {
    id: '30000000-0000-4000-8000-000000000001',
    username: 'navigation_alpha',
    created_at: '2026-09-13T10:00:00Z',
    deleted_at: null,
    wins: 1,
    average_solve_time_ms: 90_000,
    stats_overridden: false,
  },
  {
    id: '30000000-0000-4000-8000-000000000002',
    username: 'navigation_beta',
    created_at: '2026-09-13T10:00:00Z',
    deleted_at: null,
    wins: 2,
    average_solve_time_ms: 120_000,
    stats_overridden: false,
  },
];

const setupAdminNavigationApi = async (page: Page): Promise<void> => {
  await page.route('**/api/**', async (route) => {
    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.route('**/api/v1/admin/login', async (route) => {
    await route.fulfill({
      status: 200,
      headers: {
        ...jsonHeaders,
        'X-CSRF-Token': 'navigation-access-csrf',
        'X-Admin-Refresh-CSRF-Token': 'navigation-refresh-csrf',
        'Set-Cookie': 'tpm_admin_refresh_csrf=navigation-refresh-csrf; Path=/',
      },
      body: JSON.stringify(adminSessionResponse()),
    });
  });

  await page.route('**/api/v1/admin/refresh', async (route) => {
    await route.fulfill({
      status: 200,
      headers: {
        ...jsonHeaders,
        'X-CSRF-Token': 'navigation-access-csrf',
        'X-Admin-Refresh-CSRF-Token': 'navigation-refresh-csrf',
        'Set-Cookie': 'tpm_admin_refresh_csrf=navigation-refresh-csrf; Path=/',
      },
      body: JSON.stringify(adminSessionResponse()),
    });
  });

  await page.route('**/api/v1/admin/tournament-content', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        content_revision: 42,
        publication_id: '20000000-0000-4000-8000-000000000001',
        published_at: '2026-09-13T10:00:00Z',
        normal_pool_revision_id: '20000000-0000-4000-8000-000000000002',
        golden_pool_revision_id: '20000000-0000-4000-8000-000000000003',
      }),
    });
  });

  await page.route('**/api/v1/admin/tasks**', async (route) => {
    await route.fulfill({ status: 200, headers: jsonHeaders, body: '[]' });
  });

  await page.route('**/api/v1/admin/players**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/v1/admin/players') {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify(navigationPlayers),
      });
      return;
    }
    await route.fulfill({ status: 404, headers: jsonHeaders, body: '{}' });
  });

  await page.route('**/api/v1/admin/tournaments**', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({ items: [], next_cursor: null }),
    });
  });
};

const loginAdmin = async (page: Page, path = '/admin'): Promise<void> => {
  await page.goto(path);
  await expect(page.getByText('Закрытый раздел', { exact: true })).toHaveCount(0);
  await expect(
    page.getByText('Введите пароль, чтобы продолжить работу с панелью управления.', { exact: true }),
  ).toHaveCount(0);
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByRole('heading', { name: 'Панель управления' })).toBeVisible();
  await expect(page.getByTestId('site-header')).toHaveCSS('position', 'sticky');
  await expect(page.getByRole('link', { name: 'Общий рейтинг', exact: true })).toHaveAttribute('href', '/leaderboard');
  const accountMenu = await openAccountMenu(page);
  await expect(accountMenu.getByRole('button', { name: 'Выйти', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(accountMenu).toBeHidden();
};

test.beforeEach(async ({ page }) => {
  await setupAdminNavigationApi(page);
});

test('admin navigation is reflected in the URL and survives history and reload', async ({ page }) => {
  await loginAdmin(page);
  await expect(page).toHaveURL(/section=tournaments/);

  await page.getByRole('button', { name: 'Задачи' }).click();
  await expect(page).toHaveURL(/section=tasks&view=overview/);

  await page.getByRole('button', { name: 'Игроки' }).click();
  await expect(page).toHaveURL(/section=players&view=overview/);

  await page.goBack();
  await expect(page).toHaveURL(/section=tasks&view=overview/);
  await page.goForward();
  await expect(page).toHaveURL(/section=players&view=overview/);

  await page.reload();
  await expect(page.getByRole('button', { name: 'Игроки' })).toHaveAttribute(
    'aria-current',
    'page',
  );
  await expect(page).toHaveURL(/section=players&view=overview/);
});

test('back to top appears after scrolling and returns to the page start', async ({ page }) => {
  await loginAdmin(page, '/admin?section=players&view=overview');
  const backToTop = page.getByTestId('back-to-top');
  await expect(backToTop).toHaveCount(0);

  await page.evaluate(() => {
    document.body.style.minHeight = '1400px';
    window.scrollTo({ top: 600, behavior: 'auto' });
  });
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(400);
  await expect(backToTop).toBeVisible();

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await backToTop.click();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
});

test('admin task draft is guarded on section changes and stays after cancel', async ({ page }) => {
  await loginAdmin(page, '/admin?section=tasks&view=overview');
  await expect(page.getByRole('region', { name: 'Задания' })).toBeVisible();
  await expect(page.getByText('Каталог и публикация', { exact: true })).toHaveCount(0);
  await expect(page.getByText(/Создавайте задачи, обновляйте их/)).toHaveCount(0);
  await page.getByRole('button', { name: 'Создать задачу', exact: true }).click();
  const taskDialog = page.getByRole('dialog', { name: 'Создать задачу' });
  await expect(taskDialog).toBeVisible();
  const taskTitle = taskDialog.getByPlaceholder('Введите название...');
  await taskTitle.fill('Draft task');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.dismiss();
  });
  await taskDialog.getByRole('button', { name: 'Закрыть редактор задачи' }).click();
  await expect(taskDialog).toBeVisible();
  await expect(taskTitle).toHaveValue('Draft task');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.accept();
  });
  await taskDialog.getByRole('button', { name: 'Закрыть редактор задачи' }).click();
  await expect(taskDialog).toBeHidden();
  await page.getByRole('button', { name: 'Игроки' }).click();
  await expect(page).toHaveURL(/section=players&view=overview/);
  await expect(page.getByRole('button', { name: 'Создать задачу', exact: true })).toHaveCount(0);
});

test('admin dirty history cancellation preserves the forward stack', async ({ page }) => {
  await loginAdmin(page, '/admin?section=tasks&view=overview');
  await expect(page.getByRole('button', { name: 'Создать задачу', exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Игроки' }).click();
  await page.getByRole('button', { name: 'Задачи' }).click();
  await page.getByRole('button', { name: 'Игроки' }).click();
  await page.getByRole('button', { name: 'Задачи' }).click();
  await page.getByRole('button', { name: 'Игроки' }).click();

  await page.goBack();
  await expect(page).toHaveURL(/section=tasks&view=overview/);
  await page.getByRole('button', { name: 'Создать задачу', exact: true }).click();
  const taskDialog = page.getByRole('dialog', { name: 'Создать задачу' });
  await expect(taskDialog).toBeVisible();
  const taskTitle = taskDialog.getByPlaceholder('Введите название...');
  await taskTitle.fill('History draft');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.dismiss();
  });
  await page.goBack();
  await expect(page).toHaveURL(/section=tasks&view=overview/);
  await expect(taskTitle).toHaveValue('History draft');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.accept();
  });
  await page.goBack();
  await expect(page).toHaveURL(/section=players&view=overview/);
  await expect(page.getByPlaceholder('Введите название...')).toHaveCount(0);

  await page.goForward();
  await expect(page).toHaveURL(/section=tasks&view=overview/);
  await page.getByRole('button', { name: 'Создать задачу', exact: true }).click();
  const restoredTaskDialog = page.getByRole('dialog', { name: 'Создать задачу' });
  await expect(restoredTaskDialog).toBeVisible();
  await expect(restoredTaskDialog.getByPlaceholder('Введите название...')).toHaveValue('');

  await page.goForward();
  await expect(page).toHaveURL(/section=players&view=overview/);
  await expect(page.getByText('navigation_alpha')).toBeVisible();
});

test('admin player drafts are guarded when closing, canceling, and navigating back', async ({ page }) => {
  await loginAdmin(page, '/admin?section=players&view=overview');
  await page.getByRole('button', { name: 'Задачи' }).click();
  await page.getByRole('button', { name: 'Игроки' }).click();
  await expect(page.getByText('navigation_alpha')).toBeVisible();
  await page.getByRole('button', { name: 'Редактировать игрока navigation_alpha' }).click();
  const username = page.locator('#admin-player-username');
  await username.fill('navigation_draft');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.dismiss();
  });
  await page.getByRole('button', { name: 'Закрыть редактирование игрока' }).click();
  await expect(username).toHaveValue('navigation_draft');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Закрыть редактирование игрока' }).click();
  await expect(username).toBeHidden();
  await page.getByRole('button', { name: 'Редактировать игрока navigation_beta' }).click();
  await expect(username).toHaveValue('navigation_beta');

  await username.fill('navigation_cancel_draft');
  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.dismiss();
  });
  await page.getByRole('button', { name: 'Отменить' }).click();
  await expect(username).toHaveValue('navigation_cancel_draft');

  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Отменить' }).click();
  await expect(username).toBeHidden();

  await page.getByRole('button', { name: 'Редактировать игрока navigation_alpha' }).click();
  await username.fill('navigation_logout_draft');
  page.once('dialog', async (dialog) => {
    expect(dialog.type()).toBe('confirm');
    await dialog.dismiss();
  });
  await page.goBack();
  await expect(page).toHaveURL(/section=players/);
  await expect(page.getByRole('heading', { name: 'Панель управления' })).toBeVisible();
  await expect(username).toHaveValue('navigation_logout_draft');
});

test('admin login preserves a safe operator return path', async ({ page }) => {
  await page.goto(`/admin?next=/arena/operator/${operatorTournamentID}`);
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page).toHaveURL(`/arena/operator/${operatorTournamentID}`);
});
