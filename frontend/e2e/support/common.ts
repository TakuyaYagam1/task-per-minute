import { expect, type Locator, type Page } from '@playwright/test';

export const jsonHeaders = {
  'Content-Type': 'application/json',
};

export const nowISO = () => new Date().toISOString();

export const inSecondsISO = (seconds: number) => new Date(Date.now() + seconds * 1000).toISOString();

export const expectWebSocketURLDoesNotLeakSession = (websocketURL: string): void => {
  const params = new URL(websocketURL).searchParams;
  expect(params.has('token')).toBe(false);
  expect(params.has('player_id')).toBe(false);
  expect(params.has('session_id')).toBe(false);
};

export const mockPlayerLogout = async (page: Page): Promise<void> => {
  await page.route('**/api/v1/players/logout', async (route) => {
    await route.fulfill({ status: 204, body: '' });
  });
};

export const openAccountMenu = async (page: Page): Promise<Locator> => {
  const trigger = page.getByRole('button', { name: 'Меню аккаунта', exact: true });
  await expect(trigger).toBeVisible();
  if ((await trigger.getAttribute('aria-expanded')) !== 'true') {
    await trigger.click();
  }

  const menu = page.getByRole('region', { name: 'Аккаунт', exact: true });
  await expect(menu).toBeVisible();
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');
  return menu;
};

export const selectTheme = async (page: Page, theme: 'dark' | 'light'): Promise<void> => {
  const expectedChecked = theme === 'light' ? 'true' : 'false';
  const themeSwitch = page.getByRole('switch', { name: 'Светлая тема', exact: true });

  if (await themeSwitch.count() > 0) {
    await expect(themeSwitch).toHaveCount(1);
    await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toHaveCount(0);
    await expect(page.getByRole('region', { name: 'Аккаунт', exact: true })).toHaveCount(0);
    if (await themeSwitch.getAttribute('aria-checked') !== expectedChecked) {
      await themeSwitch.click();
    }
    await expect(themeSwitch).toHaveAttribute('aria-checked', expectedChecked);
  } else {
    const accountMenu = await openAccountMenu(page);
    await accountMenu.getByRole('button', {
      name: theme === 'light' ? 'Светлая тема' : 'Темная тема',
      exact: true,
    }).click();
    await page.keyboard.press('Escape');
    await expect(accountMenu).toBeHidden();
  }

  await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
};
