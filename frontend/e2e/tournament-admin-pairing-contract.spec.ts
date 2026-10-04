import { expect, test, type Page, type Route } from "@playwright/test";

import { adminSessionResponse } from "./support/admin";
import { jsonHeaders } from "./support/common";
import type { components } from "../lib/shared/api/schema";

const tournamentId = "21000000-0000-4000-8000-000000000001";
const rosterId = "21000000-0000-4000-8000-000000000002";
const baseDate = "2026-09-15T08:00:00Z";
const contentRevision = 73;

type Tournament = components["schemas"]["Tournament"];
type Player = components["schemas"]["PlayerManagementView"];
type Roster = components["schemas"]["Roster"];
type Configuration = components["schemas"]["TournamentConfiguration"];
type Snapshot = components["schemas"]["OperatorRecoverySnapshot"];
type SwissRound = components["schemas"]["SwissRound"];
type PairingRequest = components["schemas"]["PairingConfigurationRequest"];
type Participant = Roster["participants"][number];

type PairingRouteOptions = {
  configuration?: Configuration;
  onPairings?: (route: Route, body: PairingRequest) => Promise<void>;
  projectionRevision?: number;
  round?: SwissRound;
  roster?: Roster;
  tournamentState?: Tournament["state"];
};

const playerId = (index: number): string =>
  `31000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`;

const participantId = (index: number): string =>
  `41000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`;

const player = (index: number, username = `Игрок ${index + 1}`): Player => ({
  id: playerId(index),
  username,
  created_at: baseDate,
  deleted_at: null,
  wins: index,
  average_solve_time_ms: 1000 + index,
  stats_overridden: false,
});

const participant = (
  index: number,
  attendance: components["schemas"]["AttendanceState"] = "checked_in",
): Participant => ({
  attendance,
  created_at: baseDate,
  id: participantId(index),
  player_id: playerId(index),
  roster_id: rosterId,
  seed: index + 1,
  tournament_id: tournamentId,
  updated_at: baseDate,
});

const tournament = (
  size: number,
  revision = 2,
  state: Tournament["state"] = "swiss",
): Tournament => ({
  content_revision: contentRevision,
  created_at: baseDate,
  finished_at: null,
  id: tournamentId,
  name: "Swiss контракт",
  paused_from_state: null,
  planned_roster_size: Math.max(4, size),
  preset: "tournament_v1",
  public_id: "swiss-contract",
  revision,
  roster_id: rosterId,
  roster_size: size,
  started_at: state === "draft" || state === "registration" || state === "roster_locked"
    ? null
    : baseDate,
  state,
  updated_at: baseDate,
});

const roster = (size: number): Roster => ({
  created_at: baseDate,
  execution_started: false,
  execution_started_at: null,
  id: rosterId,
  locked: true,
  locked_at: baseDate,
  participants: Array.from({ length: size }, (_, index) => participant(index)),
  revision: 4,
  tournament_id: tournamentId,
  updated_at: baseDate,
});

const configuration = (size: number): Configuration => ({
  tournament_id: tournamentId,
  projection_revision_id: "51000000-0000-4000-8000-000000000001",
  projection_revision: 9,
  configuration_revision: 3,
  reserve_count: 1,
  category_pools: [
    {
      id: "51000000-0000-4000-8000-000000000002",
      revision: 1,
      format: "bo1",
      categories: ["web", "crypto", "pwn"],
    },
    {
      id: "51000000-0000-4000-8000-000000000003",
      revision: 1,
      format: "bo3",
      categories: ["web", "crypto", "pwn", "reverse", "osint"],
    },
  ],
  swiss_default: { mode: "random", categories: ["web", "crypto"] },
  golden_default: { mode: "random", categories: ["web"] },
  semifinal_default: { mode: "admin", categories: ["web", "crypto"] },
  final_default: {
    mode: "draft",
    categories: ["web", "crypto", "pwn", "reverse", "osint"],
  },
  series: [],
  rounds: [{
    id: "52000000-0000-4000-8000-000000000001",
    round_number: 1,
    revision: 5,
    mode: "random",
    categories: ["web", "crypto"],
    pairings: [],
    bye_participant_id: null,
    locked: false,
    started: false,
    consumed: false,
    disclosed: false,
    unlock_intents: [],
  }, ...(size > 8 ? [{
    id: "52000000-0000-4000-8000-000000000002",
    round_number: 2,
    revision: 1,
    mode: "random" as const,
    categories: ["web" as const],
    pairings: [],
    bye_participant_id: null,
    locked: false,
    started: false,
    consumed: false,
    disclosed: false,
    unlock_intents: [],
  }] : [])],
  updated_at: baseDate,
});

const standing = (index: number, size: number): components["schemas"]["SwissStanding"] => ({
  participant_id: participantId(index),
  position: index + 1,
  points: Math.max(0, size - index - 1),
  points_label: "provisional",
  buchholz: Math.max(0, size - index - 1),
  buchholz_status: "provisional",
  head_to_head_points: 0,
  head_to_head_applied: false,
  effective_time_ms: 1000 + index,
  accepted_solve_time_ms: null,
  stable_seed: index + 1,
});

const swissRound = (size: number, revision = 6): SwissRound => {
  const pairings = Array.from({ length: Math.floor(size / 2) }, (_, index) => ({
    evidence_id: `61000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    first_participant_id: participantId(index * 2),
    id: `62000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    round_id: "63000000-0000-4000-8000-000000000001",
    second_participant_id: participantId(index * 2 + 1),
    repeated: false,
  }));
  return {
    id: "63000000-0000-4000-8000-000000000001",
    tournament_id: tournamentId,
    round_number: 1,
    revision,
    roster_participant_ids: Array.from({ length: size }, (_, index) => participantId(index)),
    pairings,
    bye: size % 2 === 1 ? {
      id: "64000000-0000-4000-8000-000000000001",
      round_id: "63000000-0000-4000-8000-000000000001",
      participant_id: participantId(size - 1),
      points_awarded: 1,
      revision_id: "65000000-0000-4000-8000-000000000001",
      evidence_id: "66000000-0000-4000-8000-000000000001",
    } : null,
    standings: Array.from({ length: size }, (_, index) => standing(index, size)),
    locked: false,
    locked_at: null,
    started_at: null,
    completed_at: null,
    created_at: baseDate,
    updated_at: baseDate,
  };
};

const snapshot = (
  size: number,
  projectionRevision: number,
  state: Tournament["state"] = "swiss",
): Snapshot => ({
  next_cursor: {
    audit_sequence: projectionRevision,
    authority_revision: projectionRevision,
    projection_revision: projectionRevision,
  },
  pause_graph: null,
  recovery_controls: [],
  roster: roster(size),
  series: [],
  tournament: tournament(size, projectionRevision, state),
  waves: [],
});

const problem = (status: number, detail: string) => ({
  type: "about:blank",
  title: status === 422 ? "invalid pairing" : "projection conflict",
  status,
  detail,
});

const fulfillJSON = async (
  route: Route,
  status: number,
  body: unknown,
): Promise<void> => {
  await route.fulfill({
    status,
    headers: jsonHeaders,
    body: JSON.stringify(body),
  });
};

const setupPairingRoutes = async (
  page: Page,
  size: number,
  options: PairingRouteOptions = {},
): Promise<{ pairingRequests: PairingRequest[]; snapshotRequests: number[] }> => {
  const currentRoster = options.roster ?? roster(size);
  const currentConfiguration = options.configuration ?? configuration(size);
  const responseRound = options.round ?? swissRound(size);
  const tournamentState = options.tournamentState ?? "swiss";
  const pairingRequests: PairingRequest[] = [];
  const snapshotRequests: number[] = [];
  let snapshotCall = 0;

  await page.route("**/api/v1/admin/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();

    if (path === "/api/v1/admin/login" && method === "POST") {
      await fulfillJSON(route, 200, adminSessionResponse());
      return;
    }
    if (path === "/api/v1/admin/tasks" && method === "GET") {
      await fulfillJSON(route, 200, []);
      return;
    }
    if (path === "/api/v1/admin/players" && method === "GET") {
      await fulfillJSON(route, 200, Array.from({ length: size }, (_, index) => player(index)));
      return;
    }
    if (path === "/api/v1/admin/tournament-content" && method === "GET") {
      await fulfillJSON(route, 200, {
        content_revision: contentRevision,
        publication_id: "67000000-0000-4000-8000-000000000001",
        published_at: baseDate,
        normal_pool_revision_id: "67000000-0000-4000-8000-000000000002",
        golden_pool_revision_id: "67000000-0000-4000-8000-000000000003",
      });
      return;
    }
    if (path === "/api/v1/admin/tournaments" && method === "GET") {
      await fulfillJSON(route, 200, {
        items: [tournament(size, 2, tournamentState)],
        next_cursor: null,
      });
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentId}/roster` && method === "GET") {
      await fulfillJSON(route, 200, currentRoster);
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentId}/configuration` && method === "GET") {
      await fulfillJSON(route, 200, currentConfiguration);
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentId}/snapshot` && method === "GET") {
      snapshotCall += 1;
      snapshotRequests.push(options.projectionRevision ?? 9);
      await fulfillJSON(
        route,
        200,
        snapshot(size, options.projectionRevision ?? 9, tournamentState),
      );
      return;
    }
    if (path === `/api/v1/admin/tournaments/${tournamentId}/pairings` && method === "POST") {
      const body = request.postDataJSON() as PairingRequest;
      pairingRequests.push(body);
      if (options.onPairings) {
        await options.onPairings(route, body);
        return;
      }
      await fulfillJSON(route, 200, responseRound);
      return;
    }
    if (path === "/api/v1/admin/players/events" && method === "GET") {
      await route.fulfill({
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
        body: "event: ready\ndata: {}\n\n",
      });
      return;
    }
    await fulfillJSON(route, 404, {});
  });

  return { pairingRequests, snapshotRequests };
};

const pairingRegion = (page: Page) =>
  page.getByRole("region", { name: "Пары квалификации" });

const openPairingEditor = async (page: Page): Promise<ReturnType<typeof pairingRegion>> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("button", { name: "Соревнования" }).click();
  await page.getByRole("button", { name: "Открыть соревнование Swiss контракт", exact: true }).click();
  await expect(page.locator("#tournament-detail-title")).toHaveText("Swiss контракт");
  await page.getByRole("button", { name: "Сетка и серии" }).click();
  const region = pairingRegion(page);
  await expect(region).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`[?&]tournament=${tournamentId}(?:&|$)`));
  await expect(region).toContainText("Следующий раунд");
  await expect(region).toContainText("Присутствуют");
  await expect(region.getByRole("heading", { name: /Настройки раунда/ })).toBeVisible();
  await expect(region.getByRole("button", { name: "Сформировать пары" })).toBeVisible();
  return region;
};

for (const [state, hint] of [
  ["registration", 'Проверьте состав, затем нажмите "Зафиксировать состав" и "Начать квалификацию". После этого можно формировать пары.'],
  ["roster_locked", 'Нажмите "Начать квалификацию". После этого можно формировать пары.'],
] as const) {
  test(`не отправляет пары до запуска турнира в состоянии ${state}`, async ({ page }) => {
    const { pairingRequests } = await setupPairingRoutes(page, 4, {
      tournamentState: state,
    });
    const region = await openPairingEditor(page);
    const submit = region.getByRole("button", { name: "Сформировать пары" });

    await expect(submit).toBeDisabled();
    await expect(region).toContainText("Соревнование не запущено");
    await expect(region).toContainText(hint);
    expect(pairingRequests).toHaveLength(0);
  });
}

test("отправляет automatic план и отображает полный ответ Swiss сервера", async ({ page }) => {
  const round = swissRound(4);
  const { pairingRequests, snapshotRequests } = await setupPairingRoutes(page, 4, { round });
  const region = await openPairingEditor(page);

  await expect(region.getByText("Пары появятся здесь после отправки запроса.", { exact: false })).toBeVisible();
  await region.getByRole("button", { name: "Сформировать пары" }).click();
  await expect.poll(() => pairingRequests.length).toBe(1);
  expect(snapshotRequests.length).toBeGreaterThanOrEqual(2);
  expect(pairingRequests[0]).toEqual({
    expected_projection_revision: 9,
    round_number: 1,
    pairing_mode: "automatic",
    category_mode: "random",
    categories: ["web", "crypto", "pwn"],
  });
  await expect(region).toContainText("План раунда 1");
  await expect(region).toContainText("Игрок 1");
  await expect(region).toContainText("Игрок 4");
  await expect(region).toContainText("Статус блокировки");
  await expect(region.getByText("Нет", { exact: true })).toBeVisible();
  await expect(region).toContainText("Таблица раунда");
  await expect(region.getByRole("button", { name: "Перейти к проведению" })).toBeVisible();
  await region.getByRole("button", { name: "Перейти к проведению" }).click();
  await expect(page).toHaveURL(new RegExp(`[?&]view=conduct(?:&|$)`));
});

test("берет следующий тур с сервера и показывает пары без повторных соперников", async ({ page }) => {
  const nextConfiguration = configuration(4);
  nextConfiguration.rounds[0] = {
    ...nextConfiguration.rounds[0],
    locked: true,
    started: true,
    consumed: true,
  };
  nextConfiguration.rounds.push({
    ...nextConfiguration.rounds[0],
    id: "52000000-0000-4000-8000-000000000002",
    round_number: 2,
    revision: 1,
    pairings: [],
    locked: false,
    started: false,
    consumed: false,
    disclosed: false,
  });
  const baseRound = swissRound(4);
  const nextRound: SwissRound = {
    ...baseRound,
    id: "63000000-0000-4000-8000-000000000002",
    round_number: 2,
    pairings: [
      {
        ...baseRound.pairings[0],
        id: "62000000-0000-4000-8000-000000000003",
        round_id: "63000000-0000-4000-8000-000000000002",
        first_participant_id: participantId(0),
        second_participant_id: participantId(2),
      },
      {
        ...baseRound.pairings[1],
        id: "62000000-0000-4000-8000-000000000004",
        round_id: "63000000-0000-4000-8000-000000000002",
        first_participant_id: participantId(1),
        second_participant_id: participantId(3),
      },
    ],
  };
  const { pairingRequests } = await setupPairingRoutes(page, 4, {
    configuration: nextConfiguration,
    round: nextRound,
  });
  const region = await openPairingEditor(page);

  await expect(region).toContainText("Настройки раунда 2");
  await region.getByRole("button", { name: "Сформировать пары" }).click();
  await expect.poll(() => pairingRequests.length).toBe(1);
  expect(pairingRequests[0].round_number).toBe(2);
  expect(nextRound.pairings.map((pairing) => [
    pairing.first_participant_id,
    pairing.second_participant_id,
  ])).toEqual([
    [participantId(0), participantId(2)],
    [participantId(1), participantId(3)],
  ]);
  expect(nextRound.pairings.every((pairing) => pairing.repeated === false)).toBe(true);
  await expect(region).toContainText("План раунда 2");
});

test("собирает manual пары с одним bye для нечетного состава", async ({ page }) => {
  const round = swissRound(5);
  const { pairingRequests } = await setupPairingRoutes(page, 5, { round });
  const region = await openPairingEditor(page);

  await region.getByLabel("Вручную").check();
  await region.getByLabel("Пара 1 - первый участник").selectOption(participantId(0));
  await region.getByLabel("Пара 1 - второй участник").selectOption(participantId(1));
  await region.getByLabel("Пара 2 - первый участник").selectOption(participantId(2));
  await region.getByLabel("Пара 2 - второй участник").selectOption(participantId(3));
  await region.getByLabel("Участник с bye").selectOption(participantId(4));
  await region.getByRole("button", { name: "Сформировать пары" }).click();
  await expect.poll(() => pairingRequests.length).toBe(1);
  expect(pairingRequests[0]).toMatchObject({
    pairing_mode: "manual",
    manual_pairings: [
      { first_participant_id: participantId(0), second_participant_id: participantId(1) },
      { first_participant_id: participantId(2), second_participant_id: participantId(3) },
    ],
    manual_bye_participant_id: participantId(4),
  });
  await expect(region).toContainText("Игрок 5");
  await expect(region).toContainText("1 очк.");
});

test("не отправляет duplicate и omission manual сетки", async ({ page }) => {
  const { pairingRequests } = await setupPairingRoutes(page, 4);
  const region = await openPairingEditor(page);
  await region.getByLabel("Вручную").check();

  await region.getByLabel("Пара 1 - первый участник").selectOption(participantId(0));
  await region.getByLabel("Пара 1 - второй участник").selectOption(participantId(0));
  await region.getByRole("button", { name: "Сформировать пары" }).click();
  await expect(region.getByRole("alert")).toContainText(/одного и того же участника/i);
  expect(pairingRequests).toHaveLength(0);

  await region.getByLabel("Пара 1 - второй участник").selectOption(participantId(1));
  await region.getByLabel("Пара 2 - первый участник").selectOption(participantId(1));
  await region.getByLabel("Пара 2 - второй участник").selectOption(participantId(2));
  await region.getByRole("button", { name: "Сформировать пары" }).click();
  await expect(region.getByRole("alert")).toContainText(/не может встречаться более одного раза/i);
  expect(pairingRequests).toHaveLength(0);
});

test("оставляет manual draft после серверного 422 о повторной встрече", async ({ page }) => {
  const { pairingRequests } = await setupPairingRoutes(page, 4, {
    onPairings: async (route) => {
      await fulfillJSON(route, 422, problem(422, "Пара уже встречалась в предыдущем раунде"));
    },
  });
  const region = await openPairingEditor(page);
  await region.getByLabel("Вручную").check();
  await region.getByLabel("Пара 1 - первый участник").selectOption(participantId(0));
  await region.getByLabel("Пара 1 - второй участник").selectOption(participantId(1));
  await region.getByLabel("Пара 2 - первый участник").selectOption(participantId(2));
  await region.getByLabel("Пара 2 - второй участник").selectOption(participantId(3));
  await region.getByRole("button", { name: "Сформировать пары" }).click();
  await expect.poll(() => pairingRequests.length).toBe(1);
  await expect(region.getByRole("alert")).toContainText("Повторные пары запрещены");
  await expect(region.getByLabel("Пара 1 - первый участник")).toHaveValue(participantId(0));
  await expect(region.getByLabel("Пара 2 - второй участник")).toHaveValue(participantId(3));
});

test("сохраняет прежний server result после 409 и дает понятное recovery действие", async ({ page }) => {
  const round = swissRound(4, 8);
  let pairingCall = 0;
  const { pairingRequests } = await setupPairingRoutes(page, 4, {
    round,
    onPairings: async (route) => {
      pairingCall += 1;
      if (pairingCall === 1) {
        await fulfillJSON(route, 200, round);
      } else {
        await fulfillJSON(route, 409, problem(409, "Ревизия соревнования устарела"));
      }
    },
  });
  const region = await openPairingEditor(page);
  const submit = region.getByRole("button", { name: "Сформировать пары" });
  await submit.click();
  await expect.poll(() => pairingRequests.length).toBe(1);
  await expect(region).toContainText("План раунда 1");
  await expect(region).toContainText("Статус блокировки");

  await submit.click();
  await expect.poll(() => pairingRequests.length).toBe(2);
  await expect(region.getByRole("alert")).toContainText("Ревизия соревнования устарела");
  await expect(region.getByRole("alert")).toContainText("Настройки сохранены в форме");
  await expect(region).toContainText("План раунда 1");
  await expect(region).toContainText("Игрок 4");
});

test("остается непустым в светлой, темной и мобильной темах", async ({ page }) => {
  await setupPairingRoutes(page, 4);
  const region = await openPairingEditor(page);
  for (const theme of ["light", "dark"] as const) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    await expect(region).toBeVisible();
    await expect(region).toContainText("Настройки раунда 1");
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(region).toBeVisible();
  await expect(page.locator("#tournament-detail-title")).toHaveText("Swiss контракт");
  await expect(page.getByRole("button", { name: "Сетка и серии" })).toHaveAttribute("aria-current", "page");
  await expect(page).toHaveURL(new RegExp(`[?&]tournament=${tournamentId}(?:&|$)`));
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);
  expect(overflow).toBe(true);
});
