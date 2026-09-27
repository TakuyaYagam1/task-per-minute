import { expect, test, type Page, type Route } from '@playwright/test';

import { adminSessionResponse } from './support/admin';
import { jsonHeaders } from './support/common';

const tournamentID = '23000000-0000-4000-8000-000000000001';
const projectionRevisionID = '23000000-0000-4000-8000-000000000002';
const bo1PoolID = '23000000-0000-4000-8000-000000000003';
const bo3PoolID = '23000000-0000-4000-8000-000000000004';
const swissSeriesID = '23000000-0000-4000-8000-000000000005';
const goldenSeriesID = '23000000-0000-4000-8000-000000000006';
const semifinalSeriesID = '23000000-0000-4000-8000-000000000007';
const finalSeriesID = '23000000-0000-4000-8000-000000000008';
const cutoffSeriesID = '23000000-0000-4000-8000-000000000009';
const unlockReservationID = '23000000-0000-4000-8000-000000000010';
const unlockOwnerID = '23000000-0000-4000-8000-000000000011';
const unlockSourceRevisionID = '23000000-0000-4000-8000-000000000012';
const baseDate = '2026-09-16T08:00:00Z';

type Category = 'web' | 'crypto' | 'forensics' | 'reverse' | 'pwn';
type Mode = 'random' | 'admin' | 'draft';

type UnlockIntent = {
  reservation_id: string;
  owner_id: string;
  source_revision_id: string;
  expected_revision: number;
  expected_used: boolean;
  expected_disclosed: boolean;
  evidence_digest: string;
  binding_digest: string;
};

type Series = {
  id: string;
  stage: 'swiss' | 'golden' | 'semifinal' | 'final';
  round_number: number;
  revision: number;
  mode: Mode;
  categories: Category[];
  category_pool_revision_id: string;
  category_pool_revision: number;
  locked: boolean;
  started: boolean;
  consumed: boolean;
  disclosed: boolean;
  unlock_intents: UnlockIntent[];
};

type Configuration = {
  tournament_id: string;
  projection_revision_id: string;
  projection_revision: number;
  configuration_revision: number;
  reserve_count: number;
  category_pools: Array<{
    id: string;
    revision: number;
    format: 'bo1' | 'bo3';
    categories: Category[];
  }>;
  swiss_default: { mode: Mode; categories: Category[] };
  golden_default: { mode: Mode; categories: Category[] };
  semifinal_default: { mode: Mode; categories: Category[] };
  final_default: { mode: 'draft'; categories: Category[] };
  series: Series[];
  rounds: [];
  updated_at: string;
};

type RouteOptions = Readonly<{
  onConfigurationPatch?: (
    route: Route,
    body: Record<string, unknown>,
  ) => Promise<void>;
  onSeriesPatch?: (
    route: Route,
    seriesID: string,
    body: Record<string, unknown>,
  ) => Promise<void>;
  tournamentStarted?: boolean;
}>;

type CapturedSeriesPatch = Readonly<{
  seriesID: string;
  body: Record<string, unknown>;
}>;

type CapturedConfigurationPatch = Readonly<{
  body: Record<string, unknown>;
}>;

const unlockIntent = (): UnlockIntent => ({
  reservation_id: unlockReservationID,
  owner_id: unlockOwnerID,
  source_revision_id: unlockSourceRevisionID,
  expected_revision: 4,
  expected_used: false,
  expected_disclosed: false,
  evidence_digest: 'a'.repeat(64),
  binding_digest: 'b'.repeat(64),
});

const series = (
  id: string,
  stage: Series['stage'],
  roundNumber: number,
  revision: number,
  mode: Mode,
  categories: Category[],
  overrides: Partial<Series> = {},
): Series => ({
  id,
  stage,
  round_number: roundNumber,
  revision,
  mode,
  categories,
  category_pool_revision_id: bo1PoolID,
  category_pool_revision: 12,
  locked: false,
  started: false,
  consumed: false,
  disclosed: false,
  unlock_intents: [],
  ...overrides,
});

const configuration = (): Configuration => ({
  tournament_id: tournamentID,
  projection_revision_id: projectionRevisionID,
  projection_revision: 37,
  configuration_revision: 11,
  reserve_count: 2,
  category_pools: [
    {
      id: bo1PoolID,
      revision: 12,
      format: 'bo1',
      categories: ['web', 'crypto', 'reverse'],
    },
    {
      id: bo3PoolID,
      revision: 8,
      format: 'bo3',
      categories: ['web', 'crypto', 'reverse', 'forensics', 'pwn'],
    },
  ],
  swiss_default: { mode: 'random', categories: ['web'] },
  golden_default: { mode: 'admin', categories: ['crypto'] },
  semifinal_default: { mode: 'draft', categories: ['web', 'crypto', 'reverse'] },
  final_default: {
    mode: 'draft',
    categories: ['web', 'crypto', 'reverse', 'forensics', 'pwn'],
  },
  series: [
    series(swissSeriesID, 'swiss', 1, 21, 'random', ['web'], {
      locked: true,
      unlock_intents: [unlockIntent()],
    }),
    series(goldenSeriesID, 'golden', 0, 22, 'admin', ['crypto']),
    series(semifinalSeriesID, 'semifinal', 3, 23, 'draft', ['web', 'crypto', 'reverse']),
    series(finalSeriesID, 'final', 0, 24, 'draft', [
      'web',
      'crypto',
      'reverse',
      'forensics',
      'pwn',
    ], {
      category_pool_revision_id: bo3PoolID,
      category_pool_revision: 8,
      locked: true,
      disclosed: true,
    }),
    series(cutoffSeriesID, 'swiss', 2, 25, 'random', ['web'], {
      started: true,
    }),
  ],
  rounds: [],
  updated_at: baseDate,
});

const tournament = () => ({
  content_revision: 73,
  created_at: baseDate,
  finished_at: null,
  id: tournamentID,
  name: 'Series конфигурация',
  paused_from_state: null,
  planned_roster_size: 4,
  preset: 'tournament_v1',
  public_id: 'series-configuration',
  revision: 4,
  roster_id: '23000000-0000-4000-8000-000000000013',
  roster_size: 4,
  started_at: null,
  state: 'draft',
  updated_at: baseDate,
});

const problem = (
  detail: string,
  status = 409,
  title = 'Projection revision conflict',
) => ({
  type: 'about:blank',
  title,
  status,
  detail,
});

const mutationEvidence = () => ({
  command_id: '23000000-0000-4000-8000-000000000014',
  tournament_id: tournamentID,
  operator_id: '23000000-0000-4000-8000-000000000015',
  reason: 'Обновить настройки Series перед стартом',
  requested_at: baseDate,
  validation_digest: 'c'.repeat(64),
  previous_configuration_revision: 11,
  next_configuration_revision: 12,
  affected_artifact_ids: [],
  superseded_artifact_ids: [],
  rebuilt_artifact_ids: [],
  affected_artifacts: [],
  unlock_intents: [],
});

const fulfillJSON = async (
  route: Route,
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { ...jsonHeaders, ...headers },
    body: JSON.stringify(body),
  });
};

const setupSeriesRoutes = async (
  page: Page,
  options: RouteOptions = {},
): Promise<{
  configurationPatches: CapturedConfigurationPatch[];
  seriesPatches: CapturedSeriesPatch[];
}> => {
  const configurationPatches: CapturedConfigurationPatch[] = [];
  const seriesPatches: CapturedSeriesPatch[] = [];
  let currentConfiguration = configuration();
  const currentTournament = options.tournamentStarted
    ? { ...tournament(), started_at: baseDate, state: 'swiss' }
    : tournament();

  await page.route('**/api/v1/admin/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();

    if (path === '/api/v1/admin/login' && method === 'POST') {
      await fulfillJSON(route, 200, adminSessionResponse(), {
        'X-CSRF-Token': 'fe031-admin-access-csrf',
        'X-Admin-Refresh-CSRF-Token': 'fe031-admin-refresh-csrf',
      });
      return;
    }
    if (path === '/api/v1/admin/refresh' && method === 'POST') {
      await fulfillJSON(route, 200, adminSessionResponse(), {
        'X-CSRF-Token': 'fe031-admin-access-csrf',
        'X-Admin-Refresh-CSRF-Token': 'fe031-admin-refresh-csrf',
      });
      return;
    }
    if (path === '/api/v1/admin/tasks' && method === 'GET') {
      await fulfillJSON(route, 200, []);
      return;
    }
    if (path === '/api/v1/admin/tournament-content' && method === 'GET') {
      await fulfillJSON(route, 200, {
        content_revision: 73,
        publication_id: '23000000-0000-4000-8000-000000000016',
        published_at: baseDate,
        normal_pool_revision_id: bo1PoolID,
        golden_pool_revision_id: bo3PoolID,
      });
      return;
    }
    if (path === '/api/v1/admin/tournaments' && method === 'GET') {
      await fulfillJSON(route, 200, { items: [currentTournament], next_cursor: null });
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentID}/configuration` && method === 'GET') {
      await fulfillJSON(route, 200, currentConfiguration, { 'Cache-Control': 'no-store' });
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentID}/configuration` && method === 'PATCH') {
      const body = request.postDataJSON() as Record<string, unknown>;
      configurationPatches.push({ body });
      if (options.onConfigurationPatch) {
        await options.onConfigurationPatch(route, body);
        return;
      }
      const reserveCount = body.reserve_count as number;
      currentConfiguration = {
        ...currentConfiguration,
        configuration_revision: currentConfiguration.configuration_revision + 1,
        projection_revision: currentConfiguration.projection_revision + 1,
        reserve_count: reserveCount,
      };
      await fulfillJSON(route, 200, mutationEvidence());
      return;
    }
    const seriesPatch = path.match(
      new RegExp(`^/api/v1/admin/tournaments/${tournamentID}/series/([^/]+)/configuration$`),
    );
    if (seriesPatch && method === 'PATCH') {
      const seriesID = seriesPatch[1];
      const body = request.postDataJSON() as Record<string, unknown>;
      seriesPatches.push({ seriesID, body });
      if (options.onSeriesPatch) {
        await options.onSeriesPatch(route, seriesID, body);
        return;
      }
      const mode = body.mode as Mode;
      const categories = body.categories as Category[];
      currentConfiguration = {
        ...currentConfiguration,
        projection_revision: currentConfiguration.projection_revision + 1,
        configuration_revision: currentConfiguration.configuration_revision + 1,
        series: currentConfiguration.series.map((item) =>
          item.id === seriesID
            ? {
                ...item,
                revision: item.revision + 1,
                mode,
                categories,
              }
            : item,
        ),
      };
      await fulfillJSON(route, 200, mutationEvidence());
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentID}/roster` && method === 'GET') {
      await fulfillJSON(route, 200, {
        created_at: baseDate,
        execution_started: false,
        execution_started_at: null,
        id: '23000000-0000-4000-8000-000000000013',
        locked: false,
        locked_at: null,
        participants: [],
        revision: 1,
        tournament_id: tournamentID,
        updated_at: baseDate,
      });
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentID}/snapshot` && method === 'GET') {
      await fulfillJSON(route, 200, {
        next_cursor: {
          audit_sequence: 37,
          authority_revision: 37,
          projection_revision: 37,
        },
        pause_graph: null,
        roster: null,
        series: [],
        tournament: tournament(),
        waves: [],
      });
      return;
    }
    if (path === '/api/v1/admin/players' && method === 'GET') {
      await fulfillJSON(route, 200, []);
      return;
    }
    if (path === '/api/v1/admin/players/events' && method === 'GET') {
      await route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
        body: 'event: ready\ndata: {}\n\n',
      });
      return;
    }
    await fulfillJSON(route, 404, {});
  });

  return { configurationPatches, seriesPatches };
};

const loginAndOpenSeriesEditor = async (page: Page): Promise<ReturnType<Page['getByRole']>> => {
  await page.goto('/admin');
  await page.getByPlaceholder('Введите пароль...').fill('correct-password');
  await page.getByRole('button', { name: 'Войти' }).click();
  await page.getByRole('button', { name: 'Соревнования' }).click();
  const row = page.getByRole('row').filter({ hasText: 'Series конфигурация' });
  await row.getByRole('button', { name: 'Открыть' }).click();
  await page.getByRole('button', { name: 'Сетка и серии' }).click();
  const region = page.getByRole('region', { name: 'Конфигурация серий' });
  await expect(region).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`[?&]tournament=${tournamentID}(?:&|$)`));
  await expect(region.getByRole('article')).toHaveCount(5);
  return region;
};

const seriesCard = (region: ReturnType<Page['getByRole']>, label: RegExp) =>
  region.getByRole('article').filter({ hasText: label }).first();

test.describe('FE-031 per-Series category configuration', () => {
  test('selects 0, 1, and 2 shared normal and Golden reserves with authoritative reloads', async ({ page }) => {
    const { configurationPatches } = await setupSeriesRoutes(page);
    const region = await loginAndOpenSeriesEditor(page);
    const reservePanel = region.locator('section[aria-labelledby="reserve-count-title"]');
    const reserveSelect = region.getByLabel('Количество резервов для обычного режима и дополнительного отбора');
    const reserveSave = region.getByRole('button', { name: 'Сохранить резерв' });

    await expect(reserveSelect).toHaveValue('2');
    await reserveSelect.focus();
    await expect(reserveSelect).toBeFocused();
    await reserveSelect.press('Tab');
    await expect(reserveSave).toBeFocused();

    for (const [index, reserveCount] of ['0', '1', '2'].entries()) {
      await reserveSelect.selectOption(reserveCount);
      await reserveSave.click();
      await expect.poll(() => configurationPatches.length).toBe(index + 1);
      await expect(reserveSelect).toHaveValue(reserveCount);
    }

    expect(configurationPatches).toEqual([
      {
        body: {
          expected_projection_revision: 37,
          expected_configuration_revision: 11,
          reserve_count: 0,
          confirmed: true,
          reason: 'Настройка резерва перед стартом',
          unlock_intents: [],
          swiss_default: { mode: 'random', categories: ['web'] },
          semifinal_default: { mode: 'draft', categories: ['web', 'crypto', 'reverse'] },
        },
      },
      {
        body: {
          expected_projection_revision: 38,
          expected_configuration_revision: 12,
          reserve_count: 1,
          confirmed: true,
          reason: 'Настройка резерва перед стартом',
          unlock_intents: [],
          swiss_default: { mode: 'random', categories: ['web'] },
          semifinal_default: { mode: 'draft', categories: ['web', 'crypto', 'reverse'] },
        },
      },
      {
        body: {
          expected_projection_revision: 39,
          expected_configuration_revision: 13,
          reserve_count: 2,
          confirmed: true,
          reason: 'Настройка резерва перед стартом',
          unlock_intents: [],
          swiss_default: { mode: 'random', categories: ['web'] },
          semifinal_default: { mode: 'draft', categories: ['web', 'crypto', 'reverse'] },
        },
      },
    ]);
    await expect(reservePanel).toContainText('Резерв сохранен');
  });

  test('keeps the reserve draft after a stale top-level configuration revision', async ({ page }) => {
    const { configurationPatches } = await setupSeriesRoutes(page, {
      onConfigurationPatch: async (route) => {
        await fulfillJSON(route, 409, problem('Ревизия конфигурации устарела. Обновите состояние и повторите.'));
      },
    });
    const region = await loginAndOpenSeriesEditor(page);
    const reserveSelect = region.getByLabel('Количество резервов для обычного режима и дополнительного отбора');
    const reserveSave = region.getByRole('button', { name: 'Сохранить резерв' });
    await reserveSelect.selectOption('1');
    await reserveSave.click();

    await expect.poll(() => configurationPatches.length).toBe(1);
    await expect(region.getByRole('alert')).toContainText('Состояние конфигурации устарело');
    await expect(reserveSelect).toHaveValue('1');
    await expect(reserveSelect).toBeEnabled();
  });

  test('disables the reserve selector after a server cutoff response', async ({ page }) => {
    const { configurationPatches } = await setupSeriesRoutes(page, {
      onConfigurationPatch: async (route) => {
        await fulfillJSON(
          route,
          422,
          problem('Соревнование уже началось и не принимает изменение резерва.', 422, 'Invalid configuration cutoff'),
        );
      },
    });
    const region = await loginAndOpenSeriesEditor(page);
    const reserveSelect = region.getByLabel('Количество резервов для обычного режима и дополнительного отбора');
    await reserveSelect.selectOption('1');
    await region.getByRole('button', { name: 'Сохранить резерв' }).click();

    await expect.poll(() => configurationPatches.length).toBe(1);
    await expect(region.getByRole('alert')).toContainText('Резерв больше нельзя менять');
    await expect(reserveSelect).toBeDisabled();
    await expect(region.getByRole('button', { name: 'Сохранить резерв' })).toBeDisabled();
  });

  test('keeps the reserve selector read-only after tournament start', async ({ page }) => {
    await setupSeriesRoutes(page, { tournamentStarted: true });
    const region = await loginAndOpenSeriesEditor(page);
    await expect(region.getByLabel('Количество резервов для обычного режима и дополнительного отбора')).toBeDisabled();
    await expect(region.getByRole('button', { name: 'Сохранить резерв' })).toBeDisabled();
    await expect(region).toContainText('Соревнование уже началось');
  });

  test('keeps independent modes, exposes pool choices, and sends exact Series PATCH requests', async ({ page }) => {
    const { seriesPatches } = await setupSeriesRoutes(page);
    const region = await loginAndOpenSeriesEditor(page);

    const swiss = seriesCard(region, /Квалификация.*1/i);
    const golden = seriesCard(region, /Дополнительный отбор/i);
    const semifinal = seriesCard(region, /Semifinal|Полуфинал/i);
    const final = seriesCard(region, /Серия 4 - Финал/i);
    const cutoff = seriesCard(region, /Серия 5 - Квалификация, раунд 2/i);

    await expect(swiss.getByLabel('Режим серии 1')).toHaveValue('random');
    await expect(golden.getByLabel('Режим серии 2')).toHaveValue('admin');
    await expect(semifinal.getByLabel('Режим серии 3')).toHaveValue('draft');

    const swissCategoryOptions = swiss.getByLabel('Категория серии 1').locator('option');
    await expect(swissCategoryOptions).toHaveCount(3);
    await expect(swissCategoryOptions).toHaveText([
      /Web|Веб/i,
      /Crypto|Крипто/i,
      /Reverse|Реверс/i,
    ]);

    await expect(final).toContainText(/BO3|bo3/i);
    await expect(final).toContainText(/Web|Веб/i);
    await expect(final).toContainText(/Crypto|Крипто/i);
    await expect(final).toContainText(/Reverse|Реверс/i);
    await expect(final).toContainText(/Forensics|Форензик/i);
    await expect(final).toContainText(/Pwn/i);
    await expect(final.getByLabel('Режим серии 4')).toHaveCount(0);
    await expect(final.getByRole('button', { name: 'Сохранить серию 4' })).toBeDisabled();

    await expect(cutoff.getByLabel('Режим серии 5')).toHaveCount(0);
    await expect(cutoff.getByRole('button', { name: 'Сохранить серию 5' })).toBeDisabled();

    await swiss.getByLabel('Режим серии 1').selectOption('admin');
    await swiss.getByLabel('Категория серии 1').selectOption('crypto');
    await golden.getByLabel('Режим серии 2').selectOption('random');
    await golden.getByLabel('Категория серии 2').selectOption('reverse');

    const saveSwiss = swiss.getByRole('button', { name: 'Сохранить серию 1' });
    await expect(saveSwiss).toBeEnabled();
    await saveSwiss.click();
    await expect.poll(() => seriesPatches.length).toBe(1);
    expect(seriesPatches[0]).toEqual({
      seriesID: swissSeriesID,
      body: {
        expected_projection_revision: 37,
        expected_series_revision: 21,
        confirmed: true,
        reason: 'Настройка категорий серии',
        unlock_intents: [unlockIntent()],
        mode: 'admin',
        categories: ['crypto'],
      },
    });
    await expect(swiss.getByLabel('Режим серии 1')).toHaveValue('admin');
    await expect(swiss.getByLabel('Категория серии 1')).toHaveValue('crypto');

    const saveGolden = golden.getByRole('button', { name: 'Сохранить серию 2' });
    await expect(saveGolden).toBeEnabled();
    await saveGolden.click();
    await expect.poll(() => seriesPatches.length).toBe(2);
    expect(seriesPatches[1]).toEqual({
      seriesID: goldenSeriesID,
      body: {
        expected_projection_revision: 38,
        expected_series_revision: 22,
        confirmed: true,
        reason: 'Настройка категорий серии',
        unlock_intents: [],
        mode: 'random',
        categories: ['reverse'],
      },
    });
    await expect(golden.getByLabel('Режим серии 2')).toHaveValue('random');
    await expect(golden.getByLabel('Категория серии 2')).toHaveValue('reverse');

    const saveSemifinal = semifinal.getByRole('button', { name: 'Сохранить серию 3' });
    await expect(saveSemifinal).toBeEnabled();
    await saveSemifinal.click();
    await expect.poll(() => seriesPatches.length).toBe(3);
    expect(seriesPatches[2]).toEqual({
      seriesID: semifinalSeriesID,
      body: {
        expected_projection_revision: 39,
        expected_series_revision: 23,
        confirmed: true,
        reason: 'Настройка категорий серии',
        unlock_intents: [],
        mode: 'draft',
        categories: ['web', 'crypto', 'reverse'],
      },
    });
    await expect(semifinal.getByLabel('Режим серии 3')).toHaveValue('draft');

    await region.getByRole('button', { name: 'Обновить конфигурацию' }).click();
    await expect(swiss.getByLabel('Режим серии 1')).toHaveValue('admin');
    await expect(swiss.getByLabel('Категория серии 1')).toHaveValue('crypto');
    await expect(golden.getByLabel('Режим серии 2')).toHaveValue('random');
    await expect(golden.getByLabel('Категория серии 2')).toHaveValue('reverse');
    await expect(semifinal.getByLabel('Режим серии 3')).toHaveValue('draft');
    await expect(semifinal).toContainText(/Web|Веб/i);
    await expect(semifinal).toContainText(/Crypto|Крипто/i);
    await expect(semifinal).toContainText(/Reverse|Реверс/i);
  });

  test('keeps the draft and shows a recoverable message after a stale Series revision', async ({ page }) => {
    const { seriesPatches } = await setupSeriesRoutes(page, {
      onSeriesPatch: async (route) => {
        await fulfillJSON(route, 409, problem('Ревизия Series устарела. Обновите состояние и повторите.'));
      },
    });
    const region = await loginAndOpenSeriesEditor(page);
    const swiss = seriesCard(region, /Квалификация.*1/i);
    await swiss.getByLabel('Режим серии 1').selectOption('admin');
    await swiss.getByLabel('Категория серии 1').selectOption('crypto');
    await swiss.getByRole('button', { name: 'Сохранить серию 1' }).click();

    await expect.poll(() => seriesPatches.length).toBe(1);
    await expect(region.getByRole('alert')).toContainText('Состояние конфигурации устарело');
    await expect(region.getByRole('alert')).toContainText('Перезагрузите данные');
    await expect(swiss.getByLabel('Режим серии 1')).toHaveValue('admin');
    await expect(swiss.getByLabel('Категория серии 1')).toHaveValue('crypto');
  });

  test('keeps the draft when the server rejects a cutoff Series update', async ({ page }) => {
    const { seriesPatches } = await setupSeriesRoutes(page, {
      onSeriesPatch: async (route) => {
        await fulfillJSON(
          route,
          422,
          problem(
            'Серия уже началась и не принимает изменения.',
            422,
            'Invalid Series configuration',
          ),
        );
      },
    });
    const region = await loginAndOpenSeriesEditor(page);
    const swiss = seriesCard(region, /Квалификация.*1/i);
    await swiss.getByLabel('Режим серии 1').selectOption('admin');
    await swiss.getByLabel('Категория серии 1').selectOption('crypto');
    await swiss.getByRole('button', { name: 'Сохранить серию 1' }).click();

    await expect.poll(() => seriesPatches.length).toBe(1);
    await expect(swiss.getByRole('alert')).toContainText('Изменение недоступно');
    await expect(swiss.getByRole('alert')).toContainText('Серия уже началась');
    await expect(swiss.getByLabel('Режим серии 1')).toHaveValue('admin');
    await expect(swiss.getByLabel('Категория серии 1')).toHaveValue('crypto');
  });

  test('stays rendered in both themes and at 390x844 without horizontal overflow', async ({ page }) => {
    await setupSeriesRoutes(page);
    const region = await loginAndOpenSeriesEditor(page);

    for (const theme of ['light', 'dark'] as const) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      await expect(region).toBeVisible();
      await expect(region).toContainText('Series конфигурация');
    }

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(region).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);
    expect(overflow).toBe(true);
  });
});
