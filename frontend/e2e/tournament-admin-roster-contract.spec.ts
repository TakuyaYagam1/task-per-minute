import { expect, test, type Page, type Route } from "@playwright/test";

import { adminSessionResponse } from "./support/admin";
import { jsonHeaders } from "./support/common";

const adminAccessCSRF = "fe027-admin-access-csrf";
const adminRefreshCSRF = "fe027-admin-refresh-csrf";
const contentRevision = 73;
const tournamentID = "20000000-0000-4000-8000-000000000001";
const rosterID = "20000000-0000-4000-8000-000000000002";
const rosterPath = `/api/v1/admin/tournaments/${tournamentID}/roster`;
const snapshotPath = `/api/v1/admin/tournaments/${tournamentID}/snapshot`;
const preflightPath = `${rosterPath}/preflight`;
const lockPath = `${rosterPath}/lock`;
const unlockPath = `${rosterPath}/unlock`;
const baseDate = "2026-09-14T08:00:00Z";
const preflightID = "60000000-0000-4000-8000-000000000001";

type Attendance = "invited" | "registered" | "checked_in" | "withdrawn";

type Player = {
  id: string;
  username: string;
  created_at: string;
  deleted_at: string | null;
  wins: number;
  average_solve_time_ms: number;
  stats_overridden: boolean;
};

type Participant = {
  attendance: Attendance;
  created_at: string;
  id: string;
  player_id: string;
  roster_id: string;
  seed: number;
  tournament_id: string;
  updated_at: string;
};

type Roster = {
  created_at: string;
  execution_started: boolean;
  execution_started_at: string | null;
  id: string;
  locked: boolean;
  locked_at: string | null;
  participants: Participant[];
  revision: number;
  tournament_id: string;
  updated_at: string;
};

type RosterInput = {
  attendance: Attendance;
  player_id: string;
  seed: number;
};

type PreflightCheck = {
  code: string;
  evidence: string[];
  explanation: string;
  passed: boolean;
};

type PreflightReport = {
  algorithm_version: "tournament-preflight-report-v1";
  checks: PreflightCheck[];
  evaluated_at: string;
  id: string;
  normalized_inputs: string[];
  passed: boolean;
  proof_hash: string;
  revisions: Array<{ source: string; value: string }>;
  tournament_id: string;
};

const playerIDs = [
  "30000000-0000-4000-8000-000000000001",
  "30000000-0000-4000-8000-000000000002",
  "30000000-0000-4000-8000-000000000003",
];

const player = (index: number, username = `player-${index + 1}`): Player => ({
  id: playerIDs[index] ?? `30000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
  username,
  created_at: baseDate,
  deleted_at: null,
  wins: index,
  average_solve_time_ms: 1200 + index,
  stats_overridden: false,
});

const participant = (
  index: number,
  playerID = player(index).id,
  attendance: Attendance = "registered",
  seed = index + 1,
): Participant => ({
  attendance,
  created_at: baseDate,
  id: `40000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
  player_id: playerID,
  roster_id: rosterID,
  seed,
  tournament_id: tournamentID,
  updated_at: baseDate,
});

const roster = (
  participants: Participant[],
  overrides: Partial<
    Pick<
      Roster,
      | "execution_started"
      | "execution_started_at"
      | "locked"
      | "locked_at"
      | "revision"
    >
  > = {},
): Roster => ({
  created_at: baseDate,
  execution_started: false,
  execution_started_at: null,
  id: rosterID,
  locked: false,
  locked_at: null,
  participants,
  revision: 7,
  tournament_id: tournamentID,
  updated_at: baseDate,
  ...overrides,
});

const preflight = (
  passed: boolean,
  checks: PreflightCheck[],
): PreflightReport => ({
  algorithm_version: "tournament-preflight-report-v1",
  checks,
  evaluated_at: baseDate,
  id: preflightID,
  normalized_inputs: ["roster-revision:7", "tournament-revision:1"],
  passed,
  proof_hash: "a".repeat(64),
  revisions: [
    { source: "tournament", value: "1" },
    { source: "roster", value: "7" },
  ],
  tournament_id: tournamentID,
});

const tournament = (rosterSize: number) => ({
  content_revision: contentRevision,
  created_at: baseDate,
  finished_at: null,
  id: tournamentID,
  name: "Турнир состава",
  paused_from_state: null,
  planned_roster_size: Math.max(4, rosterSize),
  preset: "tournament_v1",
  public_id: "fe027-roster",
  revision: 1,
  roster_id: rosterID,
  roster_size: rosterSize,
  started_at: null,
  state: "draft",
  updated_at: baseDate,
});

const problem = (detail: string) => ({
  type: "about:blank",
  title: "conflict",
  status: 409,
  detail,
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

const setupRosterRoutes = async (
  page: Page,
  initialRoster: Roster,
  options: {
    onLock?: (route: Route, body: Record<string, unknown>) => Promise<void>;
    onPreflight?: (route: Route, body: Record<string, unknown>) => Promise<void>;
    players?: Player[];
    onReplace?: (route: Route, body: RosterInput[]) => Promise<void>;
    onUnlock?: (route: Route, body: Record<string, unknown>) => Promise<void>;
    preflight?: PreflightReport;
    projectionRevision?: number;
  } = {},
): Promise<{
  lockRequests: Array<{ body: Record<string, unknown>; key: string }>;
  preflightRequests: Array<{ body: Record<string, unknown>; key: string }>;
  replaceRequests: Array<{ body: Record<string, unknown>; key: string }>;
  unlockRequests: Array<{ body: Record<string, unknown>; key: string }>;
}> => {
  const lockRequests: Array<{ body: Record<string, unknown>; key: string }> = [];
  const preflightRequests: Array<{ body: Record<string, unknown>; key: string }> = [];
  const replaceRequests: Array<{ body: Record<string, unknown>; key: string }> = [];
  const unlockRequests: Array<{ body: Record<string, unknown>; key: string }> = [];
  const players = options.players ?? [player(0, "Алиса"), player(1, "Боб"), player(2, "Вера")];

  await page.route("**/api/v1/admin/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();

    if (path === "/api/v1/admin/login" && method === "POST") {
      await fulfillJSON(route, 200, adminSessionResponse(), {
        "X-CSRF-Token": adminAccessCSRF,
        "X-Admin-Refresh-CSRF-Token": adminRefreshCSRF,
      });
      return;
    }
    if (path === "/api/v1/admin/tasks" && method === "GET") {
      await fulfillJSON(route, 200, []);
      return;
    }
    if (path === "/api/v1/admin/players" && method === "GET") {
      await fulfillJSON(route, 200, players);
      return;
    }
    if (path === "/api/v1/admin/tournament-content" && method === "GET") {
      await fulfillJSON(route, 200, {
        content_revision: contentRevision,
        publication_id: "50000000-0000-4000-8000-000000000001",
        published_at: baseDate,
        normal_pool_revision_id: "50000000-0000-4000-8000-000000000002",
        golden_pool_revision_id: "50000000-0000-4000-8000-000000000003",
      });
      return;
    }
    if (path === "/api/v1/admin/tournaments" && method === "GET") {
      await fulfillJSON(route, 200, {
        items: [tournament(initialRoster.participants.length)],
        next_cursor: null,
      });
      return;
    }
    if (path === rosterPath && method === "GET") {
      await fulfillJSON(route, 200, initialRoster);
      return;
    }
    if (path === snapshotPath && method === "GET") {
      const projectionRevision = options.projectionRevision ?? 1;
      await fulfillJSON(route, 200, {
        next_cursor: {
          audit_sequence: projectionRevision,
          authority_revision: projectionRevision,
          projection_revision: projectionRevision,
        },
        pause_graph: null,
        recovery_controls: [],
        roster: initialRoster,
        series: [],
        tournament: tournament(initialRoster.participants.length),
        waves: [],
      });
      return;
    }
    if (path === preflightPath && method === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>;
      preflightRequests.push({
        body,
        key: request.headers()["idempotency-key"] ?? "",
      });
      if (options.onPreflight) {
        await options.onPreflight(route, body);
        return;
      }
      if (options.preflight) {
        await fulfillJSON(route, 200, options.preflight);
        return;
      }
      await fulfillJSON(route, 404, {});
      return;
    }
    if (path === lockPath && method === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>;
      lockRequests.push({
        body,
        key: request.headers()["idempotency-key"] ?? "",
      });
      if (options.onLock) {
        await options.onLock(route, body);
        return;
      }
      await fulfillJSON(route, 200, roster(initialRoster.participants, {
        execution_started: false,
        execution_started_at: null,
        locked: true,
        locked_at: baseDate,
        revision: initialRoster.revision + 1,
      }));
      return;
    }
    if (path === unlockPath && method === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>;
      unlockRequests.push({
        body,
        key: request.headers()["idempotency-key"] ?? "",
      });
      if (options.onUnlock) {
        await options.onUnlock(route, body);
        return;
      }
      await fulfillJSON(route, 200, roster(initialRoster.participants, {
        locked: false,
        locked_at: null,
        revision: initialRoster.revision + 1,
      }));
      return;
    }
    if (path === rosterPath && method === "PUT") {
      const body = request.postDataJSON() as Record<string, unknown>;
      const bodyParticipants = body.participants as RosterInput[];
      replaceRequests.push({
        body,
        key: request.headers()["idempotency-key"] ?? "",
      });
      if (options.onReplace) {
        await options.onReplace(route, bodyParticipants);
        return;
      }
      await fulfillJSON(route, 200, roster(
        bodyParticipants.map((item, index) => participant(
          index,
          item.player_id,
          item.attendance,
          item.seed,
        )),
        { revision: initialRoster.revision + 1 },
      ));
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

  return { lockRequests, preflightRequests, replaceRequests, unlockRequests };
};

const openRoster = async (page: Page, expectParticipant: boolean = true): Promise<void> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("button", { name: "Турниры" }).click();
  const row = page.getByRole("row").filter({ hasText: "Турнир состава" });
  const open = row.getByRole("button", { name: "Открыть" });
  await expect(open).toBeVisible();
  await open.click();
  await expect(page.getByRole("heading", { name: "Турнир состава" })).toBeVisible();
  await page.getByRole("button", { name: "Участники" }).click();
  await expect(page.getByText(/Состав турнира|Участники турнира|Участники/i).first()).toBeVisible();
  if (expectParticipant) {
    await expect(rosterRegion(page).getByRole("group").first()).toBeVisible();
  }
};

const rosterRegion = (page: Page) =>
  page.getByRole("region", { name: /Состав турнира|Участники турнира/i }).first();

const rosterGroup = (page: Page, index: number) =>
  rosterRegion(page).getByRole("group").nth(index - 1);

const attendanceControl = (group: ReturnType<Page["getByRole"]>) =>
  group.getByRole("combobox", { name: "Участие" });

test("загружает игроков и roster, отображает русскую посещаемость и серверный порядок", async ({ page }) => {
  await setupRosterRoutes(page, roster([
    participant(0, player(0, "Алиса").id, "registered", 2),
    participant(1, player(1, "Боб").id, "checked_in", 1),
  ]));
  await openRoster(page);

  const region = rosterRegion(page);
  await expect(region).toContainText("Алиса");
  await expect(region).toContainText("Зарегистрирован");
  await expect(region).toContainText("Боб");
  await expect(region).toContainText("На месте");
  const visibleText = await region.innerText();
  expect(visibleText).not.toMatch(
    /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i,
  );
  await expect(rosterGroup(page, 1).getByRole("combobox", { name: "Игрок" })).toHaveValue(
    player(0, "Алиса").id,
  );
  await expect(rosterGroup(page, 2).getByRole("combobox", { name: "Игрок" })).toHaveValue(
    player(1, "Боб").id,
  );
  await expect(rosterGroup(page, 1).getByRole("spinbutton", { name: "Позиция" })).toHaveValue("2");
  await expect(rosterGroup(page, 2).getByRole("spinbutton", { name: "Позиция" })).toHaveValue("1");
});

test("добавляет участника и отклоняет duplicate игрока", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
  ];
  await setupRosterRoutes(page, roster([
    participant(0, players[0].id),
    participant(1, players[1].id),
  ]), { players });
  await openRoster(page);

  const region = rosterRegion(page);
  await region.getByRole("button", { name: /Добавить участника/i }).click();
  await expect(region).toContainText("Вера");

  await rosterGroup(page, 3).getByRole("combobox", { name: "Игрок" }).selectOption(players[0].id);
  await expect(region.getByRole("alert")).toContainText(/уже добавлен|дубликат|повтор/i);
});

test("показывает editable empty state и не отправляет PUT для состава меньше минимума", async ({ page }) => {
  const players = [player(0, "Алиса"), player(1, "Боб"), player(2, "Вера"), player(3, "Глеб")];
  const { replaceRequests } = await setupRosterRoutes(page, roster([]), { players });
  await openRoster(page, false);

  const region = rosterRegion(page);
  await expect(region).toContainText("Состав пуст");
  const save = region.getByRole("button", { name: /Сохранить состав/i });
  const add = region.getByRole("button", { name: /Добавить участника/i });
  await expect(add).toBeEnabled();

  for (let count = 0; count <= 3; count += 1) {
    await save.click();
    await expect(region.getByRole("alert")).toContainText(/минимум 4|не менее 4|4 участник/i);
    expect(replaceRequests).toHaveLength(0);
    if (count < 3) {
      await add.click();
      await expect(rosterGroup(page, count + 1)).toBeVisible();
    }
  }
});

test("проверяет позиции в диапазоне 1..N и оставляет draft исправляемым после удаления строки", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
    player(4, "Дима"),
  ];
  const { replaceRequests } = await setupRosterRoutes(page, roster(
    players.map((candidate, index) => participant(index, candidate.id, "registered", index + 1)),
  ), { players });
  await openRoster(page);

  const region = rosterRegion(page);
  await rosterGroup(page, 2).getByRole("button", { name: "Удалить" }).click();
  await region.getByRole("button", { name: /Сохранить состав/i }).click();
  await expect(region.getByRole("alert")).toContainText(/от 1 до 4|без пропусков|последовательн/i);
  expect(replaceRequests).toHaveLength(0);
  await expect(rosterGroup(page, 1).getByRole("spinbutton", { name: "Позиция" })).toBeEditable();

  await rosterGroup(page, 2).getByRole("spinbutton", { name: "Позиция" }).fill("2");
  await rosterGroup(page, 3).getByRole("spinbutton", { name: "Позиция" }).fill("3");
  await rosterGroup(page, 4).getByRole("spinbutton", { name: "Позиция" }).fill("4");
  await region.getByRole("button", { name: /Сохранить состав/i }).click();
  await expect.poll(() => replaceRequests.length).toBe(1);
  expect(replaceRequests[0].body.participants).toEqual([
    { player_id: players[0].id, seed: 1, attendance: "registered" },
    { player_id: players[2].id, seed: 2, attendance: "registered" },
    { player_id: players[3].id, seed: 3, attendance: "registered" },
    { player_id: players[4].id, seed: 4, attendance: "registered" },
  ]);
});

test("явно ограничивает 17-го участника", async ({ page }) => {
  const fullPlayers = Array.from({ length: 16 }, (_, index) => player(index, `Участник ${index + 1}`));
  await setupRosterRoutes(page, roster(
    fullPlayers.map((candidate, index) => participant(index, candidate.id, "registered", index + 1)),
  ), { players: fullPlayers });
  await openRoster(page);
  const fullRegion = rosterRegion(page);
  await fullRegion.getByRole("button", { name: /Добавить участника/i }).click();
  await expect(fullRegion.getByRole("alert")).toContainText(/16|максим|переполн/i);
});

test("изменяет attendance, заменяет и удаляет участника, отправляет полный roster с revision и принимает серверный порядок", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
    player(4, "Дима"),
  ];
  const returnedRoster = roster([
    participant(0, players[2].id, "checked_in", 4),
    participant(1, players[0].id, "checked_in", 3),
    participant(2, players[3].id, "registered", 2),
    participant(3, players[1].id, "registered", 1),
  ], { revision: 8 });
  const { replaceRequests } = await setupRosterRoutes(page, roster([
    participant(0, players[0].id, "registered", 1),
    participant(1, players[1].id, "registered", 2),
    participant(2, players[3].id, "registered", 3),
    participant(3, players[4].id, "registered", 4),
    participant(4, players[2].id, "registered", 5),
  ]), {
    players,
    onReplace: async (route, bodyParticipants) => {
      expect(bodyParticipants).toEqual([
        { player_id: players[0].id, seed: 4, attendance: "checked_in" },
        { player_id: players[2].id, seed: 3, attendance: "registered" },
        { player_id: players[3].id, seed: 2, attendance: "registered" },
        { player_id: players[4].id, seed: 1, attendance: "registered" },
      ]);
      await fulfillJSON(route, 200, returnedRoster);
    },
  });
  await openRoster(page);

  const region = rosterRegion(page);
  await rosterGroup(page, 5).getByRole("button", { name: "Удалить" }).click();
  await rosterGroup(page, 2).getByRole("combobox", { name: "Игрок" }).selectOption(players[2].id);
  await rosterGroup(page, 2).getByRole("spinbutton", { name: "Позиция" }).fill("3");
  await rosterGroup(page, 1).getByRole("spinbutton", { name: "Позиция" }).fill("4");
  await rosterGroup(page, 3).getByRole("spinbutton", { name: "Позиция" }).fill("2");
  await rosterGroup(page, 4).getByRole("spinbutton", { name: "Позиция" }).fill("1");
  await attendanceControl(rosterGroup(page, 1)).selectOption("checked_in");

  await region.getByRole("button", { name: /Сохранить состав/i }).click();
  await expect.poll(() => replaceRequests.length).toBe(1);
  expect(replaceRequests[0].body).toMatchObject({ expected_projection_revision: 1 });
  expect(replaceRequests[0].key).toMatch(/^[0-9a-f-]{36}$/i);
  await expect(rosterGroup(page, 1)).toContainText("Вера");
  await expect(rosterGroup(page, 1).getByRole("spinbutton", { name: "Позиция" })).toHaveValue("4");
  await expect(attendanceControl(rosterGroup(page, 1))).toHaveValue("checked_in");
  await expect(rosterGroup(page, 2)).toContainText("Алиса");
  await expect(rosterGroup(page, 2).getByRole("spinbutton", { name: "Позиция" })).toHaveValue("3");
  await expect(rosterGroup(page, 3)).toContainText("Глеб");
  await expect(rosterGroup(page, 4)).toContainText("Боб");
  await expect(
    rosterGroup(page, 1).getByRole("combobox", { name: "Игрок" }),
  ).toHaveValue(players[2].id);
  await expect(
    rosterGroup(page, 2).getByRole("combobox", { name: "Игрок" }),
  ).toHaveValue(players[0].id);
});

test("сохраняет draft при русском 409 reservation conflict и блокирует редактирование locked roster", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
  ];
  let replaceCalls = 0;
  await setupRosterRoutes(page, roster([
    participant(0, players[0].id, "registered", 1),
    participant(1, players[1].id, "registered", 2),
    participant(2, players[2].id, "registered", 3),
    participant(3, players[3].id, "registered", 4),
  ]), {
    players,
    onReplace: async (route) => {
      replaceCalls += 1;
      await fulfillJSON(route, 409, problem("Игрок уже зарезервирован в другом турнире"));
    },
  });
  await openRoster(page);
  const region = rosterRegion(page);
  await attendanceControl(rosterGroup(page, 1)).selectOption("checked_in");
  await region.getByRole("button", { name: /Сохранить состав/i }).click();
  await expect(region.getByRole("alert")).toContainText("Игрок уже зарезервирован в другом турнире");
  await expect(attendanceControl(rosterGroup(page, 1))).toHaveValue("checked_in");
  expect(replaceCalls).toBe(1);

  await page.reload();
  await setupRosterRoutes(page, roster([
    participant(0, players[0].id, "checked_in", 1),
    participant(1, players[1].id, "registered", 2),
    participant(2, players[2].id, "registered", 3),
    participant(3, players[3].id, "registered", 4),
  ], { locked: true, locked_at: baseDate }), { players });
  await openRoster(page);
  const lockedRegion = rosterRegion(page);
  await expect(lockedRegion.getByRole("button", { name: /Добавить участника/i })).toBeDisabled();
  await expect(lockedRegion.getByRole("button", { name: /Сохранить состав/i })).toBeDisabled();
  await expect(lockedRegion.getByRole("button", { name: "Удалить" }).first()).toBeDisabled();
  await expect(attendanceControl(rosterGroup(page, 1))).toBeDisabled();
});

test("показывает успешный preflight и блокирует состав только с актуальным отчетом", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
  ];
  const initialRoster = roster(
    players.map((candidate, index) => participant(index, candidate.id, "checked_in", index + 1)),
  );
  const report = preflight(true, [
    {
      code: "tournament.preflight.structure.roster_complete",
      evidence: ["participants=4", "planned_roster_size=4"],
      explanation: "Состав заполнен до планового размера.",
      passed: true,
    },
    {
      code: "tournament.preflight.runtime.capacity",
      evidence: ["available_slots=4", "required_slots=4"],
      explanation: "Емкости исполнения достаточно для всех участников.",
      passed: true,
    },
    {
      code: "tournament.preflight.structure.categories",
      evidence: ["coverage=web,crypto,forensics,reverse,pwn"],
      explanation: "Все обязательные категории покрыты.",
      passed: true,
    },
    {
      code: "tournament.preflight.tasks.inventory",
      evidence: ["reserve_tasks=6"],
      explanation: "Резерв задач сформирован.",
      passed: true,
    },
    {
      code: "tournament.preflight.structure.pairings",
      evidence: ["simultaneous_matches=2"],
      explanation: "План одновременных матчей рассчитан.",
      passed: true,
    },
    {
      code: "tournament.preflight.runtime.clock",
      evidence: ["round_limit_minutes=60"],
      explanation: "Лимит раунда составляет 60 минут.",
      passed: true,
    },
  ]);
  const { lockRequests, preflightRequests } = await setupRosterRoutes(page, initialRoster, {
    players,
    preflight: report,
    projectionRevision: 9,
    onLock: async (route, body) => {
      expect(body).toMatchObject({
        checked_in_player_ids: players.map((candidate) => candidate.id),
        expected_projection_revision: 9,
        preflight_revision_id: report.id,
      });
      await fulfillJSON(route, 200, roster(initialRoster.participants, {
        locked: true,
        locked_at: baseDate,
        revision: initialRoster.revision + 1,
      }));
    },
  });
  await openRoster(page);

  const region = rosterRegion(page);
  const lock = region.getByRole("button", { name: "Зафиксировать состав" });
  await expect(lock).toBeDisabled();
  await region.getByRole("button", { name: "Запустить проверку" }).click();
  await expect.poll(() => preflightRequests.length).toBe(1);
  expect(preflightRequests[0].body).toEqual({ expected_projection_revision: 9 });
  expect(preflightRequests[0].key).toMatch(/^[0-9a-f-]{36}$/i);
  await expect(region).toContainText("Проверка пройдена");
  await expect(region).toContainText("tournament.preflight.structure.roster_complete");
  await expect(region).toContainText("Состав заполнен до планового размера.");
  await expect(region).toContainText("participants=4");
  await expect(region).toContainText("tournament.preflight.runtime.capacity");
  await expect(region).toContainText("available_slots=4");
  await expect(region).toContainText("coverage=web,crypto,forensics,reverse,pwn");
  await expect(region).toContainText("reserve_tasks=6");
  await expect(region).toContainText("simultaneous_matches=2");
  await expect(region).toContainText("round_limit_minutes=60");
  await expect(lock).toBeEnabled();

  await lock.click();
  await expect.poll(() => lockRequests.length).toBe(1);
  expect(lockRequests[0].body).toMatchObject({
    checked_in_player_ids: players.map((candidate) => candidate.id),
    expected_projection_revision: 9,
    preflight_revision_id: report.id,
  });
  expect(lockRequests[0].key).toMatch(/^[0-9a-f-]{36}$/i);
  await expect(region).toContainText("Только просмотр");
  await expect(region.getByRole("button", { name: /Сохранить состав/i })).toBeDisabled();
});

test("показывает точные причины capacity и task failure и оставляет блокировку недоступной", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
  ];
  const failedReport = preflight(false, [
    {
      code: "tournament.preflight.runtime.capacity",
      evidence: ["available_slots=2", "required_slots=4"],
      explanation: "Недостаточная емкость исполнения: доступно 2 из 4 слотов.",
      passed: false,
    },
    {
      code: "tournament.preflight.tasks.missing",
      evidence: ["category=web", "missing=2"],
      explanation: "Недостаточно задач для категории web.",
      passed: false,
    },
  ]);
  const { lockRequests } = await setupRosterRoutes(page, roster(
    players.map((candidate, index) => participant(index, candidate.id, "checked_in", index + 1)),
  ), { players, preflight: failedReport });
  await openRoster(page);

  const region = rosterRegion(page);
  await region.getByRole("button", { name: "Запустить проверку" }).click();
  await expect(region).toContainText("Проверка не пройдена");
  await expect(region).toContainText("Недостаточная емкость исполнения: доступно 2 из 4 слотов.");
  await expect(region).toContainText("available_slots=2");
  await expect(region).toContainText("Недостаточно задач для категории web.");
  await expect(region).toContainText("category=web");
  await expect(region).toContainText("Фиксация недоступна");
  await expect(region.getByRole("button", { name: "Зафиксировать состав" })).toBeDisabled();
  expect(lockRequests).toHaveLength(0);
});

test("сбрасывает отчет после stale 409 lock и требует повторного preflight", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
  ];
  const report = preflight(true, [{
    code: "tournament.preflight.runtime.capacity",
    evidence: ["available_slots=4", "required_slots=4"],
    explanation: "Емкости исполнения достаточно.",
    passed: true,
  }]);
  const { lockRequests, preflightRequests } = await setupRosterRoutes(page, roster(
    players.map((candidate, index) => participant(index, candidate.id, "checked_in", index + 1)),
  ), {
    players,
    preflight: report,
    onLock: async (route) => {
      await fulfillJSON(route, 409, problem("Ревизия турнира устарела"));
    },
  });
  await openRoster(page);

  const region = rosterRegion(page);
  await region.getByRole("button", { name: "Запустить проверку" }).click();
  await expect(region.getByRole("button", { name: "Зафиксировать состав" })).toBeEnabled();
  await region.getByRole("button", { name: "Зафиксировать состав" }).click();
  await expect.poll(() => lockRequests.length).toBe(1);
  await expect(region).toContainText("Ревизия турнира устарела");
  await expect(region).toContainText("запустите проверку заново");
  await expect(region).not.toContainText("Емкости исполнения достаточно.");
  await expect(region.getByRole("button", { name: "Зафиксировать состав" })).toBeDisabled();

  await region.getByRole("button", { name: "Запустить проверку" }).click();
  await expect.poll(() => preflightRequests.length).toBe(2);
  await expect(region).toContainText("Емкости исполнения достаточно.");
});

test("требует подтверждение и причину для unlock и скрывает unlock после начала исполнения", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
  ];
  const lockedRoster = roster(
    players.map((candidate, index) => participant(index, candidate.id, "checked_in", index + 1)),
    { locked: true, locked_at: baseDate },
  );
  const { unlockRequests } = await setupRosterRoutes(page, lockedRoster, {
    players,
    projectionRevision: 11,
    onUnlock: async (route, body) => {
      expect(body).toEqual({
        confirmed: true,
        expected_projection_revision: 11,
        reason: "Исправление состава",
      });
      await fulfillJSON(route, 200, roster(lockedRoster.participants, {
        locked: false,
        locked_at: null,
        revision: lockedRoster.revision + 1,
      }));
    },
  });
  await openRoster(page);

  const region = rosterRegion(page);
  const unlock = region.getByRole("button", { name: "Разблокировать состав" });
  await expect(unlock).toBeVisible();
  await expect(unlock).toBeDisabled();
  await region.getByLabel("Причина разблокировки").fill("  Исправление состава  ");
  await region.getByLabel("Подтверждаю разблокировку состава").check();
  await expect(unlock).toBeEnabled();
  await unlock.click();
  await expect.poll(() => unlockRequests.length).toBe(1);
  expect(unlockRequests[0].key).toMatch(/^[0-9a-f-]{36}$/i);
  await expect(region).toContainText("Можно редактировать");
  await expect(region.getByRole("button", { name: "Разблокировать состав" })).toHaveCount(0);

  await page.reload();
  await setupRosterRoutes(page, roster(
    lockedRoster.participants,
    {
      execution_started: true,
      execution_started_at: baseDate,
      locked: true,
      locked_at: baseDate,
    },
  ), { players });
  await openRoster(page);
  const executingRegion = rosterRegion(page);
  await expect(executingRegion).toContainText("Исполнение турнира уже началось");
  await expect(executingRegion.getByRole("button", { name: "Разблокировать состав" })).toHaveCount(0);
});

test("сохраняет читаемый roster editor в обеих темах и на мобильной ширине", async ({ page }) => {
  const players = [
    player(0, "Алиса"),
    player(1, "Боб"),
    player(2, "Вера"),
    player(3, "Глеб"),
  ];
  await setupRosterRoutes(page, roster([
    participant(0, players[0].id, "registered", 1),
    participant(1, players[1].id, "checked_in", 2),
    participant(2, players[2].id, "registered", 3),
    participant(3, players[3].id, "registered", 4),
  ]), { players });
  await openRoster(page);

  const region = rosterRegion(page);
  const surfaces: string[] = [];
  for (const theme of ["Темная тема", "Светлая тема"]) {
    await page.getByRole("button", { name: theme }).click();
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme",
      theme === "Темная тема" ? "dark" : "light",
    );
    surfaces.push(await region.evaluate((element) => getComputedStyle(element).backgroundColor));
    await expect(region.getByRole("button", { name: "Сохранить состав" })).toBeVisible();
  }
  expect(surfaces[0]).not.toBe(surfaces[1]);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(region).toBeVisible();
  await expect(rosterGroup(page, 1).getByRole("combobox", { name: "Игрок" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
