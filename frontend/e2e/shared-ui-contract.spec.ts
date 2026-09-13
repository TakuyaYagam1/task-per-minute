import { spawn, type ChildProcessByStdio } from 'node:child_process';
import { createServer } from 'node:net';
import { resolve } from 'node:path';
import { type Readable } from 'node:stream';

import { expect, test, type Page } from '@playwright/test';

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, 'lib/shared/ui/__fixture__');
const nextBinary = resolve(frontendRoot, 'node_modules/.bin/next');
const fixtureHost = '127.0.0.1';
const readinessTimeoutMs = 60_000;

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = '';
let fixtureOutput = '';

const wait = (durationMs: number) => new Promise<void>((resolvePromise) => {
  setTimeout(resolvePromise, durationMs);
});

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once('error', reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === 'string') {
      server.close();
      reject(new Error('Could not reserve a loopback port for the UI fixture'));
      return;
    }

    const port = address.port;
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolvePort(port);
    });
  });
});

const appendFixtureOutput = (chunk: Buffer | string): void => {
  fixtureOutput = `${fixtureOutput}${chunk.toString()}`.slice(-6_000);
};

const isFixtureReady = async (): Promise<boolean> => {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 1_000);

  try {
    const response = await fetch(fixtureURL, { signal: controller.signal });
    return response.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
};

const startFixture = async (): Promise<void> => {
  const port = await reservePort();
  fixtureURL = `http://${fixtureHost}:${port}`;
  fixtureOutput = '';

  const fixtureChild = spawn(
    nextBinary,
    ['dev', '--hostname', fixtureHost, '--port', String(port)],
    {
      cwd: fixtureRoot,
      detached: true,
      env: {
        NEXT_TELEMETRY_DISABLED: '1',
        NODE_ENV: 'development',
        PATH: process.env.PATH ?? '/usr/bin:/bin',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  fixtureProcess = fixtureChild;
  fixtureChild.stdout.on('data', appendFixtureOutput);
  fixtureChild.stderr.on('data', appendFixtureOutput);

  const deadline = Date.now() + readinessTimeoutMs;
  while (Date.now() < deadline) {
    if (fixtureChild.exitCode !== null) {
      throw new Error(`The shared UI fixture exited before readiness.\n${fixtureOutput}`);
    }
    if (await isFixtureReady()) {
      return;
    }
    await wait(250);
  }

  throw new Error(`The shared UI fixture did not become ready.\n${fixtureOutput}`);
};

const stopFixture = async (): Promise<void> => {
  const processToStop = fixtureProcess;
  fixtureProcess = undefined;
  if (!processToStop || processToStop.pid === undefined) {
    return;
  }

  const exited = new Promise<void>((resolveExit) => {
    if (processToStop.exitCode !== null) {
      resolveExit();
      return;
    }
    processToStop.once('exit', () => resolveExit());
  });

  try {
    process.kill(-processToStop.pid, 'SIGTERM');
  } catch {
    if (processToStop.exitCode === null) {
      processToStop.kill('SIGTERM');
    }
  }

  await Promise.race([exited, wait(5_000)]);
  if (processToStop.exitCode === null) {
    try {
      process.kill(-processToStop.pid, 'SIGKILL');
    } catch {
      processToStop.kill('SIGKILL');
    }
  }
};

const openFixture = async (page: Page): Promise<void> => {
  await page.goto(fixtureURL, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: 'Общие компоненты интерфейса' })).toBeVisible();
};

test.describe('shared UI primitives', () => {
  test.beforeAll(async () => {
    await startFixture();
  });

  test.afterAll(async () => {
    await stopFixture();
  });

  test('renders all primitives with explicit loading, empty, error and disabled semantics', async ({ page }) => {
    await openFixture(page);

    await expect(page.getByRole('heading', { name: 'Стенд компонентов' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Статусы' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Сообщения' })).toBeVisible();
    await expect(page.getByRole('tablist', { name: 'Разделы стенда' })).toBeVisible();
    const tableRegion = page.getByRole('region', { name: 'Таблица результатов' });
    await expect(tableRegion.getByRole('table')).toBeVisible();

    await expect(page.getByRole('status').filter({ hasText: 'Загрузка данных' })).toBeVisible();
    await expect(page.getByRole('status').filter({ hasText: 'Нет участников' })).toBeVisible();
    await expect(page.getByRole('alert').filter({ hasText: 'Ошибка загрузки' })).toBeVisible();
    await expect(page.getByRole('status').filter({ hasText: 'Данные загружаются' })).toBeVisible();
    await expect(page.getByRole('status').filter({ hasText: 'Список пока пуст' })).toBeVisible();

    const disabledButton = page.getByRole('button', { name: 'Недоступное действие' });
    await expect(disabledButton).toBeDisabled();
    await expect(page.getByRole('status').filter({ hasText: 'Недоступно' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );

    await page.locator('main').click({ position: { x: 5, y: 5 } });
    await page.keyboard.press('Tab');
    const firstThemeButton = page.getByRole('button', { name: 'Темная тема' });
    await expect(firstThemeButton).toBeFocused();
    await expect(firstThemeButton).toHaveCSS('outline-style', 'solid');
    await expect(firstThemeButton).toHaveCSS('outline-width', '2px');

    const loadingButton = page.getByRole('button', { name: 'Запустить действие' });
    await loadingButton.click();
    const activeLoadingButton = page.locator('button[aria-busy="true"]');
    await expect(activeLoadingButton).toBeDisabled();
    await expect(activeLoadingButton).toContainText('Действие выполняется');
  });

  test('exposes table data and each table state as accessible content', async ({ page }) => {
    await openFixture(page);

    await expect(page.getByRole('cell', { name: 'Лиса' })).toBeVisible();
    await expect(page.getByRole('cell', { name: '12' })).toBeVisible();

    await page.getByRole('button', { name: 'Загрузка', exact: true }).click();
    const tableRegion = page.getByRole('region', { name: 'Таблица результатов' });
    await expect(tableRegion.getByRole('table')).toHaveAttribute('aria-busy', 'true');
    await expect(tableRegion.getByRole('status').filter({ hasText: 'Загрузка данных' })).toBeVisible();

    await page.getByRole('button', { name: 'Пусто', exact: true }).click();
    await expect(tableRegion.getByRole('cell', { name: 'В этом раунде пока нет участников' })).toBeVisible();

    await page.getByRole('button', { name: 'Ошибка', exact: true }).click();
    await expect(tableRegion.getByRole('alert').filter({ hasText: 'Сервер результатов недоступен' })).toBeVisible();
    await expect(tableRegion.getByRole('alert').filter({ hasText: 'Ошибка.' })).toContainText(
      'Сервер результатов недоступен',
    );
  });

  test('supports both themes, reduced motion and narrow viewports without page overflow', async ({ page }) => {
    await openFixture(page);

    const html = page.locator('html');
    await expect(html).toHaveAttribute('data-theme', 'dark');
    const darkBackground = await html.evaluate((element) => getComputedStyle(element).getPropertyValue('--bg').trim());

    await page.getByRole('button', { name: 'Светлая тема' }).click();
    await expect(html).toHaveAttribute('data-theme', 'light');
    const lightBackground = await html.evaluate((element) => getComputedStyle(element).getPropertyValue('--bg').trim());
    expect(lightBackground).not.toBe(darkBackground);

    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect.poll(() => page.evaluate(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true);
    await expect(page.locator('.motion-page')).toHaveCSS('animation-name', 'none');

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { name: 'Общие компоненты интерфейса' })).toBeVisible();
    const pageWidths = await page.evaluate(() => ({
      body: document.body.scrollWidth,
      document: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }));
    expect(pageWidths.document).toBeLessThanOrEqual(pageWidths.viewport);
    expect(pageWidths.body).toBeLessThanOrEqual(pageWidths.viewport);
  });

  test('moves through enabled tabs with ArrowLeft, ArrowRight, Home and End', async ({ page }) => {
    await openFixture(page);

    const summary = page.getByRole('tab', { name: 'Сводка' });
    const ranking = page.getByRole('tab', { name: 'Рейтинг' });
    const rounds = page.getByRole('tab', { name: 'Раунды' });
    const archive = page.getByRole('tab', { name: 'Архив' });

    await expect(summary).toHaveAttribute('aria-selected', 'true');
    await expect(archive).toBeDisabled();

    await summary.focus();
    await summary.press('ArrowRight');
    await expect(ranking).toHaveAttribute('aria-selected', 'true');
    await expect(ranking).toBeFocused();

    await ranking.press('ArrowRight');
    await expect(rounds).toHaveAttribute('aria-selected', 'true');
    await expect(rounds).toBeFocused();

    await rounds.press('ArrowLeft');
    await expect(ranking).toHaveAttribute('aria-selected', 'true');
    await expect(ranking).toBeFocused();

    await ranking.press('Home');
    await expect(summary).toHaveAttribute('aria-selected', 'true');
    await expect(summary).toBeFocused();

    await summary.press('End');
    await expect(rounds).toHaveAttribute('aria-selected', 'true');
    await expect(rounds).toBeFocused();
  });

  test('opens Dialog from the trigger, contains focus and restores it after Escape', async ({ page }) => {
    await openFixture(page);

    const trigger = page.getByRole('button', { name: 'Открыть диалог' });
    const dialog = page.getByRole('dialog');
    const close = page.getByRole('button', { name: 'Закрыть диалог' });

    await trigger.focus();
    await trigger.click();
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAccessibleName('Подтвердить действие');
    await expect(close).toBeFocused();

    for (let index = 0; index < 5; index += 1) {
      await page.keyboard.press('Tab');
      await expect.poll(() => page.evaluate(() => {
        const current = document.activeElement;
        const owner = document.querySelector('dialog[open]');
        return Boolean(current && owner?.contains(current));
      })).toBe(true);
    }

    await page.keyboard.press('Escape');
    await expect(dialog).not.toBeVisible();
    await expect(trigger).toBeFocused();
  });
});
