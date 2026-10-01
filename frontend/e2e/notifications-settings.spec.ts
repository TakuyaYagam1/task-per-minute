import { expect, test, type Page } from '@playwright/test';

const player = {
  id: '77777777-7777-4777-8777-777777777777',
  username: 'demo',
  created_at: '2026-10-01T00:00:00Z',
};

async function mockNotifications(page: Page, signedIn = true) {
  const createdAt = new Date(Date.now() - 60 * 60 * 1000).toISOString();
  const expiresAt = new Date(Date.now() + 23 * 60 * 60 * 1000).toISOString();
  const notifications = Array.from({ length: 20 }, (_, index) => ({
    id: `88888888-8888-4888-8888-${String(index + 1).padStart(12, '0')}`,
    type: 'tournament_player_removed',
    tournament_id: '99999999-9999-4999-8999-999999999999',
    tournament_name: `Турнир ${index + 1}`,
    created_at: createdAt,
    expires_at: expiresAt,
  }));
  let notificationReads = 0;
  const mutations: string[] = [];

  await page.addInitScript(() => {
    let active = 0;
    let peak = 0;
    class NotificationEvents extends EventTarget {
      onerror: (() => void) | null = null;
      private closed = false;
      constructor() {
        super();
        active += 1;
        peak = Math.max(peak, active);
        document.documentElement.dataset.notificationStreams = String(active);
        document.documentElement.dataset.notificationPeak = String(peak);
        setTimeout(() => {
          if (!this.closed) this.dispatchEvent(new Event('ready'));
        }, 0);
      }
      close() {
        if (this.closed) return;
        this.closed = true;
        active -= 1;
        document.documentElement.dataset.notificationStreams = String(active);
      }
    }
    Object.defineProperty(window, 'EventSource', { value: NotificationEvents });
  });
  await page.route('**/api/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!['GET', 'HEAD'].includes(request.method())) mutations.push(path);
    if (path === '/api/v1/players/me') {
      await route.fulfill({ status: signedIn ? 200 : 401, json: signedIn ? { player } : { status: 401 } });
    } else if (path === '/api/v1/players/notifications') {
      notificationReads += 1;
      await route.fulfill({ json: { notifications } });
    } else if (path === '/api/v1/players/account') {
      await route.fulfill({ json: { username: player.username, email: 'demo@example.test', pending_email: null, pending_email_expires_at: null, email_resend_available_at: null } });
    } else if (path === '/api/v1/players/account/avatar') {
      await route.fulfill({ status: 404 });
    } else if (path === '/api/v1/players/logout') {
      await route.fulfill({ status: 204 });
    } else if (path === '/api/v1/public/tournaments') {
      await route.fulfill({ json: { tournaments: [], next_cursor: null } });
    } else {
      await route.fulfill({ status: 404 });
    }
  });
  return { get notificationReads() { return notificationReads; }, mutations };
}

test('notification popover scrolls and opens the shared read-only settings list', async ({ page }) => {
  const api = await mockNotifications(page);
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Уведомления: 20', exact: true }).click();
  const popover = page.getByTestId('site-header').getByRole('region', { name: 'Уведомления', exact: true });
  await expect(popover.getByRole('listitem')).toHaveCount(20);
  await expect(popover.getByRole('link', { name: 'Просмотреть все', exact: true })).toBeVisible();
  const scrollable = await popover.evaluate((element) => Array.from(element.querySelectorAll('*')).some((child) => {
    const style = getComputedStyle(child);
    return ['auto', 'scroll'].includes(style.overflowY) && child.scrollHeight > child.clientHeight;
  }));
  expect(scrollable).toBe(true);
  await popover.getByRole('link', { name: 'Просмотреть все', exact: true }).click();
  await expect(page).toHaveURL(/\/settings\?section=notifications$/);
  await expect(popover).toHaveCount(0);
  const section = page.locator('main').getByRole('region', { name: 'Уведомления', exact: true });
  await expect(section.getByRole('listitem')).toHaveCount(20);
  await expect(section.getByRole('button', { name: /удалить/i })).toHaveCount(0);
  await expect(page.locator('html')).toHaveAttribute('data-notification-peak', '1');
  await expect(page.locator('html')).toHaveAttribute('data-notification-streams', '1');
  expect(api.mutations).toEqual([]);
  await page.goBack();
  await expect(page.locator('main').getByRole('heading', { name: 'Профиль', exact: true })).toBeVisible();
  await page.goForward();
  await expect(section.getByRole('listitem')).toHaveCount(20);
});

test('notification section can be linked directly and stays private for guests', async ({ page }) => {
  const api = await mockNotifications(page, false);
  await page.goto('/settings?section=notifications');
  await expect(page.locator('main').getByRole('heading', { name: 'Уведомления', exact: true })).toBeVisible();
  await expect(page.locator('main').getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByTestId('site-header').getByRole('button', { name: /^Уведомления/ })).toHaveCount(0);
  expect(api.notificationReads).toBe(0);
  expect(api.mutations).toEqual([]);
});

test('settings header follows the content width and stays inside a narrow viewport', async ({ page }) => {
  await mockNotifications(page);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/settings');
  const header = page.getByTestId('site-header');
  const account = header.getByRole('button', { name: 'Меню аккаунта', exact: true });
  await expect(account).toBeVisible();
  const shell = await page.locator('main > div').first().boundingBox();
  const brand = await header.getByRole('link', { name: 'Arena', exact: true }).boundingBox();
  const avatar = await account.boundingBox();
  expect(shell).not.toBeNull();
  expect(brand).not.toBeNull();
  expect(avatar).not.toBeNull();
  expect(Math.abs(brand!.x - shell!.x)).toBeLessThanOrEqual(1);
  expect(Math.abs(avatar!.x + avatar!.width - shell!.x - shell!.width)).toBeLessThanOrEqual(1);
  await page.setViewportSize({ width: 375, height: 812 });
  await expect(account).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
