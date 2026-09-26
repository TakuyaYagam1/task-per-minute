import { expect, test, type Locator, type Page, type Route } from '@playwright/test';

import type { components } from '../lib/shared/api/schema';
import { createTournamentFixtureSet, tournamentFixtureIds } from './tournament/fixtures';
import { adminSessionResponse, taskResponse } from './support/admin';
import { jsonHeaders } from './support/common';

type Schema = components['schemas'];

const fixtureSet = createTournamentFixtureSet();
const visualTournament: Schema['Tournament'] = {
  ...fixtureSet.operator.snapshot.tournament,
  paused_from_state: null,
  state: 'golden',
};
const visualPublicTournament: Schema['PublicTournamentResponse'] = {
  ...fixtureSet.public.tournament,
  state: 'golden',
};
const visualSnapshot: Schema['OperatorRecoverySnapshot'] = {
  ...fixtureSet.operator.snapshot,
  pause_graph: null,
  tournament: visualTournament,
};
const visualRoster = fixtureSet.operator.snapshot.roster;

const visualContent: Schema['TournamentContentSelection'] = {
  content_revision: 42,
  publication_id: '10000000-0000-4000-8000-000000000201',
  published_at: '2026-09-13T10:00:00Z',
  normal_pool_revision_id: '00000000-0000-4000-8000-000000000202',
  golden_pool_revision_id: '00000000-0000-4000-8000-000000000203',
};

const visualPlayers: Schema['PlayerManagementView'][] = visualRoster.participants.map(
  (participant, index) => ({
    id: participant.player_id,
    username: `visual_${['alpha', 'beta', 'gamma', 'delta'][index] ?? `player_${index + 1}`}`,
    created_at: '2026-09-13T10:00:00Z',
    deleted_at: null,
    wins: index + 1,
    average_solve_time_ms: (index + 1) * 30_000,
    stats_overridden: false,
  }),
);

const visualTask = taskResponse({
  id: '00000000-0000-4000-8000-000000000204',
  title: 'Визуальная задача',
  description: 'Синтетическая задача для проверки загруженного каталога.',
  category: 'web',
  task_url: 'https://example.invalid/visual-task',
});

const serverDateHeader = 'Sun, 13 Sep 2026 10:00:00 GMT';

const visualConfiguration: Schema['TournamentConfiguration'] = {
  tournament_id: tournamentFixtureIds.tournament,
  projection_revision_id: '00000000-0000-4000-8000-000000000205',
  projection_revision: 9,
  configuration_revision: 4,
  reserve_count: 2,
  category_pools: [
    {
      id: '00000000-0000-4000-8000-000000000206',
      revision: 2,
      format: 'bo1',
      categories: ['web', 'crypto', 'pwn'],
    },
    {
      id: '00000000-0000-4000-8000-000000000207',
      revision: 3,
      format: 'bo3',
      categories: ['web', 'crypto', 'pwn', 'reverse', 'osint'],
    },
  ],
  swiss_default: { mode: 'random', categories: ['web'] },
  golden_default: { mode: 'random', categories: ['crypto'] },
  semifinal_default: { mode: 'admin', categories: ['web', 'crypto'] },
  final_default: {
    mode: 'draft',
    categories: ['web', 'crypto', 'pwn', 'reverse', 'osint'],
  },
  series: [
    {
      id: '00000000-0000-4000-8000-000000000208',
      stage: 'swiss',
      round_number: 1,
      revision: 6,
      mode: 'admin',
      categories: ['web'],
      category_pool_revision_id: '00000000-0000-4000-8000-000000000206',
      category_pool_revision: 2,
      locked: false,
      started: false,
      consumed: false,
      disclosed: false,
      unlock_intents: [],
    },
  ],
  rounds: [
    {
      id: '00000000-0000-4000-8000-000000000209',
      round_number: 1,
      revision: 6,
      mode: 'admin',
      categories: ['web'],
      pairings: [
        {
          first_participant_id: tournamentFixtureIds.firstParticipant,
          second_participant_id: tournamentFixtureIds.secondParticipant,
        },
      ],
      bye_participant_id: null,
      locked: false,
      started: false,
      consumed: false,
      disclosed: false,
      unlock_intents: [],
    },
  ],
  updated_at: '2026-09-13T10:00:00Z',
};

const visualAuditEvent = {
  actor_id: tournamentFixtureIds.firstParticipant,
  actor_kind: 'operator',
  audit_event_id: '00000000-0000-4000-8000-000000000210',
  created_at: '2026-09-13T10:00:00Z',
  entity_id: tournamentFixtureIds.bo3Series,
  entity_kind: 'series',
  event_type: 'result.recorded',
  is_current: true,
  is_superseded: false,
  occurred_at: '2026-09-13T10:00:00Z',
  official_result_revision_id: tournamentFixtureIds.scoreRevision,
  redacted_payload: {
    entity_id: tournamentFixtureIds.bo3Series,
    result_reason: 'score_complete',
    revision_number: 2,
  },
  result_event_id: '00000000-0000-4000-8000-000000000211',
  result_reason: 'score_complete',
  result_state: 'completed',
  revision_number: 2,
  roster_id: tournamentFixtureIds.roster,
  series_id: tournamentFixtureIds.bo3Series,
  tournament_id: tournamentFixtureIds.tournament,
  winner_id: tournamentFixtureIds.firstParticipant,
};

const fulfillJSON = async (route: Route, body: unknown, status = 200): Promise<void> => {
  await route.fulfill({
    status,
    headers: { ...jsonHeaders, Date: serverDateHeader },
    body: JSON.stringify(body),
  });
};

const setupAdminVisualApi = async (page: Page): Promise<void> => {
  await page.route('**/api/v1/admin/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const { pathname } = url;

    if (pathname === '/api/v1/admin/login') {
      await route.fulfill({
        status: 200,
        headers: {
          ...jsonHeaders,
          Date: serverDateHeader,
          'X-CSRF-Token': 'visual-access-csrf',
          'X-Admin-Refresh-CSRF-Token': 'visual-refresh-csrf',
        },
        body: JSON.stringify(adminSessionResponse()),
      });
      return;
    }
    if (pathname === '/api/v1/admin/refresh') {
      await route.fulfill({
        status: 200,
        headers: {
          ...jsonHeaders,
          Date: serverDateHeader,
          'X-CSRF-Token': 'visual-access-csrf',
          'X-Admin-Refresh-CSRF-Token': 'visual-refresh-csrf',
        },
        body: JSON.stringify(adminSessionResponse()),
      });
      return;
    }
    if (pathname === '/api/v1/admin/logout') {
      await route.fulfill({ status: 204, body: '' });
      return;
    }
    if (pathname === '/api/v1/admin/players/events') {
      await route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
        body: 'retry: 60000\n\n',
      });
      return;
    }
    if (pathname === '/api/v1/admin/players') {
      await fulfillJSON(route, visualPlayers);
      return;
    }
    if (/^\/api\/v1\/admin\/players\/[^/]+\/audit$/.test(pathname)) {
      await fulfillJSON(route, []);
      return;
    }
    if (pathname === '/api/v1/admin/tasks') {
      await fulfillJSON(route, [visualTask]);
      return;
    }
    if (pathname === '/api/v1/admin/tournament-content') {
      await fulfillJSON(route, visualContent);
      return;
    }
    if (pathname === '/api/v1/admin/tournaments') {
      await fulfillJSON(route, { items: [visualTournament], next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/admin/tournaments/${visualTournament.id}/roster`) {
      await fulfillJSON(route, visualRoster);
      return;
    }
    if (pathname === `/api/v1/admin/tournaments/${visualTournament.id}/configuration`) {
      await fulfillJSON(route, visualConfiguration);
      return;
    }
    if (pathname === `/api/v1/admin/tournaments/${visualTournament.id}/snapshot`) {
      await fulfillJSON(route, visualSnapshot);
      return;
    }
    if (pathname === `/api/v1/admin/tournaments/${visualTournament.id}/golden`) {
      await fulfillJSON(route, fixtureSet.golden.operator);
      return;
    }
    if (pathname === '/api/v1/admin/tournament-audit') {
      await fulfillJSON(route, { events: [visualAuditEvent], next_cursor: null });
      return;
    }
    await fulfillJSON(route, {}, 404);
  });

  await page.route('**/api/v1/tournaments/**', async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === `/api/v1/tournaments/${visualTournament.id}`) {
      await fulfillJSON(route, visualPublicTournament);
      return;
    }
    await fulfillJSON(route, {}, 404);
  });
};

const setTheme = async (page: Page, theme: 'dark' | 'light'): Promise<void> => {
  const label = theme === 'dark' ? 'Темная тема' : 'Светлая тема';
  const button = page.getByRole('button', { name: label });
  await button.click();
  await expect(button).toHaveAttribute('aria-pressed', 'true');
  await expect.poll(() => page.evaluate(() => document.documentElement.dataset.theme)).toBe(theme);
};

const assertOpaqueSurface = async (locator: Locator, label: string): Promise<void> => {
  const style = await locator.evaluate((element) => {
    const computed = getComputedStyle(element);
    return {
      backgroundColor: computed.backgroundColor,
      color: computed.color,
      borderColor: computed.borderTopColor,
    };
  });
  const message = `${label} surface: ${JSON.stringify(style)}`;
  expect(style.backgroundColor, message).toMatch(/^rgb\(/);
  expect(style.color, message).toMatch(/^rgb\(/);
  expect(style.borderColor, message).toMatch(/^rgb\(/);
};

const assertActiveNavigationContrast = async (
  page: Page,
  label: string,
): Promise<void> => {
  const item = page.getByRole('button', { name: label, exact: true });
  await expect(item).toHaveAttribute('aria-current', 'page');
  await item.hover();
  const style = await item.evaluate((element) => {
    const computed = getComputedStyle(element);
    const parseColor = (value: string): [number, number, number] | null => {
      const match = value.match(/^rgba?\((\d+),\s*(\d+),\s*(\d+)/);
      return match
        ? [Number(match[1]), Number(match[2]), Number(match[3])]
        : null;
    };
    const luminance = (channels: [number, number, number]): number =>
      channels.reduce((total, channel, index) => {
        const normalized = channel / 255;
        const linear = normalized <= 0.03928
          ? normalized / 12.92
          : ((normalized + 0.055) / 1.055) ** 2.4;
        return total + linear * [0.2126, 0.7152, 0.0722][index];
      }, 0);
    const foreground = parseColor(computed.color);
    const background = parseColor(computed.backgroundColor);
    return {
      color: computed.color,
      backgroundColor: computed.backgroundColor,
      contrastRatio:
        foreground && background
          ? (Math.max(luminance(foreground), luminance(background)) + 0.05) /
            (Math.min(luminance(foreground), luminance(background)) + 0.05)
          : null,
    };
  });
  const message = `${label} hovered navigation item: ${JSON.stringify(style)}`;
  expect(style.contrastRatio, message).not.toBeNull();
  if (style.contrastRatio !== null) {
    expect(style.contrastRatio, message).toBeGreaterThanOrEqual(4.5);
  }
};

const checkVisualState = async (
  page: Page,
  stateName: string,
  focusTarget: Locator,
  authenticated: boolean,
): Promise<void> => {
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  const geometry = await page.evaluate((isAuthenticated) => {
    const theme = document.querySelector<HTMLElement>('[aria-label="Тема интерфейса"]');
    const target = isAuthenticated
      ? document.querySelector<HTMLElement>('main > header')
      : document.querySelector<HTMLElement>('#admin-login-title')?.closest('section');
    if (!theme || !target) {
      return null;
    }
    const themeRect = theme.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const intersects =
      themeRect.left < targetRect.right &&
      themeRect.right > targetRect.left &&
      themeRect.top < targetRect.bottom &&
      themeRect.bottom > targetRect.top;
    return {
      intersects,
      headerTop: targetRect.top,
      themeBottom: themeRect.bottom,
      viewportWidth: window.innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      scrollY: window.scrollY,
    };
  }, authenticated);
  expect(geometry, `${stateName} geometry was not measurable`).not.toBeNull();
  if (geometry) {
    const geometryMessage = `${stateName} geometry: ${JSON.stringify(geometry)}`;
    expect(geometry.intersects, geometryMessage).toBe(false);
    expect(geometry.scrollWidth, geometryMessage).toBeLessThanOrEqual(
      geometry.viewportWidth + 1,
    );
  }

  await focusTarget.evaluate((element) => {
    const marker = document.createElement('button');
    marker.type = 'button';
    marker.tabIndex = 0;
    marker.dataset.adminVisualFocusMarker = 'true';
    marker.setAttribute('aria-hidden', 'true');
    marker.style.cssText =
      'position:fixed;left:0;top:0;width:1px;height:1px;opacity:0;';
    element.parentElement?.insertBefore(marker, element);
    marker.focus();
  });
  await page.keyboard.press('Tab');
  await expect(focusTarget).toBeFocused();
  const focusRing = await focusTarget.evaluate((element) => {
    const style = getComputedStyle(element);
    return {
      outlineStyle: style.outlineStyle,
      outlineWidth: style.outlineWidth,
    };
  });
  expect(focusRing.outlineStyle).not.toBe('none');
  expect(focusRing.outlineWidth).not.toBe('0px');
  await page.locator('[data-admin-visual-focus-marker="true"]').evaluate((marker) => {
    marker.remove();
  });
  await page.evaluate(() => window.scrollTo(0, 0));
  const screenshotPath = test.info().outputPath('admin', `admin-${stateName}.png`);
  await page.screenshot({ path: screenshotPath, fullPage: true, animations: 'disabled' });
  await test.info().attach(`admin-${stateName}`, {
    path: screenshotPath,
    contentType: 'image/png',
  });
};

const loginAdmin = async (page: Page, theme: 'dark' | 'light'): Promise<void> => {
  await page.goto('/admin');
  await setTheme(page, theme);
  await expect(page.getByRole('heading', { name: 'Вход администратора' })).toBeVisible();
};

test('admin visual matrix covers loaded sections, themes, focus, and mobile overflow', async ({ page }) => {
  test.setTimeout(300_000);
  await setupAdminVisualApi(page);

  for (const viewport of [375, 768, 1440]) {
    await page.setViewportSize({ width: viewport, height: 900 });
    for (const theme of ['dark', 'light'] as const) {
      await loginAdmin(page, theme);
      await checkVisualState(
        page,
        `${theme}-${viewport}-login`,
        page.getByLabel('Пароль администратора'),
        false,
      );

      await page.getByLabel('Пароль администратора').fill('correct-password');
      await page.getByRole('button', { name: 'Войти' }).click();
      await expect(page.getByRole('heading', { name: 'Панель управления' })).toBeVisible();
      await expect(page.getByText(visualTournament.name, { exact: true })).toBeVisible();
      const loginNotification = page.getByText('Успешный вход в админ-панель', { exact: true });
      await expect(loginNotification).toBeVisible();
      await assertOpaqueSurface(loginNotification, 'Уведомление об успешном входе');

      await page.getByRole('button', { name: 'Игроки', exact: true }).click();
      await assertActiveNavigationContrast(page, 'Игроки');
      await expect(page.getByText('visual_alpha', { exact: true })).toBeVisible();
      await checkVisualState(
        page,
        `${theme}-${viewport}-players`,
        page.getByRole('button', { name: 'Редактировать игрока visual_alpha' }),
        true,
      );
      await page.getByRole('button', { name: 'История игрока visual_alpha', exact: true }).click();
      const playerAuditDialog = page.getByRole('dialog', { name: 'История игрока' });
      await expect(playerAuditDialog).toBeVisible();
      await assertOpaqueSurface(playerAuditDialog, 'Модальное окно истории игрока');
      await playerAuditDialog.getByRole('button', { name: 'Закрыть историю' }).click();

      await page.getByRole('button', { name: 'Задачи', exact: true }).click();
      await assertActiveNavigationContrast(page, 'Задачи');
      await expect(page.getByText('Визуальная задача', { exact: true })).toBeVisible();
      await checkVisualState(
        page,
        `${theme}-${viewport}-tasks`,
        page.getByPlaceholder('Введите название...'),
        true,
      );

      await page.getByRole('button', { name: 'Турниры', exact: true }).click();
      await assertActiveNavigationContrast(page, 'Турниры');
      const tournamentRow = page.getByRole('row').filter({ hasText: visualTournament.name });
      await expect(tournamentRow).toBeVisible();
      await checkVisualState(
        page,
        `${theme}-${viewport}-tournaments`,
        page.getByLabel('Название турнира'),
        true,
      );
      await tournamentRow.getByRole('button', { name: 'Открыть', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Рабочая область турнира' })).toBeVisible();

      const tournamentViews = page.getByRole('navigation', { name: 'Разделы турнира' });
      await checkVisualState(
        page,
        `${theme}-${viewport}-tournament-overview`,
        tournamentViews.getByRole('button', { name: 'Обзор', exact: true }),
        true,
      );

      await tournamentViews.getByRole('button', { name: 'Участники', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Состав турнира' })).toBeVisible();
      await expect(
        page.getByRole('group', { name: /^Участник \d+$/ }),
      ).toHaveCount(visualRoster.participants.length);
      await checkVisualState(
        page,
        `${theme}-${viewport}-tournament-participants`,
        tournamentViews.getByRole('button', { name: 'Участники', exact: true }),
        true,
      );

      await tournamentViews.getByRole('button', { name: 'Сетка и серии', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Конфигурация серий' })).toBeVisible();
      await expect(page.getByRole('heading', { name: 'Пары Swiss' })).toBeVisible();
      await expect(page.locator('[data-series-id]')).toHaveCount(visualConfiguration.series.length);
      await expect(page.getByRole('heading', { name: 'Настройки раунда 1' })).toBeVisible();
      await checkVisualState(
        page,
        `${theme}-${viewport}-tournament-bracket`,
        tournamentViews.getByRole('button', { name: 'Сетка и серии', exact: true }),
        true,
      );

      await tournamentViews.getByRole('button', { name: 'Проведение', exact: true }).click();
      await expect(page.getByTestId('operator-wave-control-panel')).toBeVisible();
      await expect(page.getByTestId('operator-golden-playoff-control-panel')).toBeVisible();
      await expect(page.getByTestId('operator-wave')).toHaveCount(fixtureSet.operator.snapshot.waves.length);
      await expect(page.getByTestId('operator-golden-playoff-control-panel').getByTestId('golden-group'))
        .toHaveCount(fixtureSet.golden.operator.groups.length);
      await checkVisualState(
        page,
        `${theme}-${viewport}-tournament-conduct`,
        page.getByTestId('operator-wave-control-panel').getByRole('button', { name: 'Обновить матчи' }),
        true,
      );

      await tournamentViews.getByRole('button', { name: 'Журнал', exact: true }).click();
      const tournamentAudit = page.getByRole('region', { name: 'Аудит и incident bundle' });
      await expect(tournamentAudit).toBeVisible();
      await expect(tournamentAudit.getByText('result.recorded', { exact: true })).toBeVisible();
      await expect(tournamentAudit.getByRole('list', { name: 'События аудита' })).toBeVisible();
      await checkVisualState(
        page,
        `${theme}-${viewport}-tournament-audit`,
        tournamentAudit.getByLabel('Entity ID'),
        true,
      );

      await page.getByRole('navigation', { name: 'Разделы админ-панели' })
        .getByRole('button', { name: 'Журнал', exact: true })
        .click();
      await assertActiveNavigationContrast(page, 'Журнал');
      const adminAudit = page.locator('section[aria-label="Журнал турнира"]');
      await expect(adminAudit).toBeVisible();
      await expect(adminAudit.getByText('result.recorded', { exact: true })).toBeVisible();
      await expect(adminAudit.getByRole('list', { name: 'События аудита' })).toBeVisible();
      await checkVisualState(
        page,
        `${theme}-${viewport}-audit`,
        adminAudit.getByLabel('Турнир', { exact: true }),
        true,
      );

      await page.getByRole('button', { name: 'Выйти', exact: true }).click();
      await expect(page.getByRole('heading', { name: 'Вход администратора' })).toBeVisible();
    }
  }
});
