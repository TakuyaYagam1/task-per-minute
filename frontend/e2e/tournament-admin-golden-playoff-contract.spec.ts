import { expect, test, type Page, type Request, type Route } from "@playwright/test";

import { operatorSnapshot, tournamentFixtureIds } from "./tournament/fixtures";
import type { components } from "../lib/shared/api/schema";

const tournamentId = tournamentFixtureIds.tournament;
const rosterId = tournamentFixtureIds.roster;
const groupId = tournamentFixtureIds.group;
const groupRevisionId = tournamentFixtureIds.groupRevision;
const attemptId = tournamentFixtureIds.attempt;
const readyWindowId = tournamentFixtureIds.readyWindow;
const firstParticipantId = tournamentFixtureIds.firstParticipant;
const secondParticipantId = tournamentFixtureIds.secondParticipant;
const baseDate = "2026-09-19T10:00:00Z";
const startedDate = "2026-09-19T10:05:00Z";
const finishedDate = "2026-09-19T11:00:00Z";
const accessCSRF = "golden-playoff-access-csrf";
const refreshCSRF = "golden-playoff-refresh-csrf";
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

type Schema = components["schemas"];
type Tournament = Schema["Tournament"];
type GoldenOperatorResponse = Schema["GoldenOperatorResponse"];
type GoldenRuntimeState = Schema["GoldenRuntimeState"];
type PublicTournamentResponse = Schema["PublicTournamentResponse"];
type PublicBracketResponse = Schema["PublicBracketResponse"];
type PublicScoreboardResponse = Schema["PublicScoreboardResponse"];
type Phase =
  | "swiss"
  | "golden-prepared"
  | "golden-ready"
  | "golden-active"
  | "golden-completed"
  | "playoffs"
  | "playoffs-final"
  | "completed";

type RequestRecord = Readonly<{
  body: Record<string, unknown>;
  headers: Record<string, string>;
}>;

type GoldenHarness = Readonly<{
  actionRequests: RequestRecord[];
  openRequests: RequestRecord[];
  startRequests: RequestRecord[];
  setPhase: (phase: Phase) => void;
}>;

const jsonHeaders = { "content-type": "application/json" };

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);

const requestBody = (request: Request): Record<string, unknown> => {
  const value: unknown = request.postDataJSON();
  if (!isRecord(value)) {
    throw new Error("Expected a JSON object request body");
  }
  return value;
};

const fulfillJSON = async (
  route: Route,
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: { ...jsonHeaders, ...headers },
    status,
  });
};

const fulfillProblem = async (
  route: Route,
  status: number,
  detail: string,
): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify({ detail, status, title: "conflict", type: "about:blank" }),
    headers: { "content-type": "application/problem+json" },
    status,
  });
};

const phaseRevision = (phase: Phase): number => {
  switch (phase) {
    case "swiss":
      return 9;
    case "golden-prepared":
      return 10;
    case "golden-ready":
      return 11;
    case "golden-active":
      return 12;
    case "golden-completed":
      return 13;
    case "playoffs":
      return 14;
    case "playoffs-final":
      return 15;
    case "completed":
      return 16;
  }
};

const tournamentState = (phase: Phase): Tournament["state"] => {
  if (phase === "swiss") {
    return "swiss";
  }
  if (phase === "playoffs" || phase === "playoffs-final") {
    return "playoffs";
  }
  if (phase === "completed") {
    return "completed";
  }
  return "golden";
};

const tournament = (phase: Phase): Tournament => ({
  content_revision: 1,
  created_at: baseDate,
  finished_at: phase === "completed" ? finishedDate : null,
  id: tournamentId,
  name: "Golden playoff контракт",
  paused_from_state: null,
  planned_roster_size: 4,
  preset: "tournament_v1",
  public_id: "golden-playoff-contract",
  revision: phaseRevision(phase),
  roster_id: rosterId,
  roster_size: 4,
  started_at: startedDate,
  state: tournamentState(phase),
  updated_at: baseDate,
});

const publicTournament = (phase: Phase): PublicTournamentResponse => ({
  finished_at: phase === "completed" ? finishedDate : null,
  preset: "tournament_v1",
  projection_revision: phaseRevision(phase),
  roster_size: 4,
  started_at: startedDate,
  state: tournamentState(phase),
  tournament_id: tournamentId,
});

const goldenState = (phase: Phase): GoldenRuntimeState => {
  switch (phase) {
    case "golden-ready":
      return "ready";
    case "golden-active":
      return "active";
    case "golden-completed":
    case "playoffs":
    case "playoffs-final":
    case "completed":
      return "completed";
    default:
      return "prepared";
  }
};

const goldenOperator = (phase: Phase): GoldenOperatorResponse => {
  const state = goldenState(phase);
  if (phase === "swiss" || phase === "golden-prepared") {
    return {
      groups: [],
      observed_at: baseDate,
      tournament_id: tournamentId,
    };
  }
  const ready = state !== "prepared";
  return {
    groups: [{
      attempt_id: attemptId,
      deadline: state === "active" ? "2026-09-19T10:35:00Z" : null,
      group_id: groupId,
      group_revision_id: groupRevisionId,
      members: [
        {
          participant_id: firstParticipantId,
          position: 1,
          ready,
          submitted: state === "completed",
        },
        {
          participant_id: secondParticipantId,
          position: 2,
          ready,
          submitted: state === "completed",
        },
      ],
      position_from: 1,
      position_to: 2,
      ready_window_id: readyWindowId,
      runtime_revision: 4,
      started_at: state === "active" || state === "completed" ? startedDate : null,
      state,
    }],
    observed_at: baseDate,
    tournament_id: tournamentId,
  };
};

const publicScoreboard = (phase: Phase): PublicScoreboardResponse => ({
  entries: [
    {
      buchholz: 7,
      display_name: "Алиса",
      effective_time_ms: 38_500,
      points: 10,
      wins: 3,
      losses: 0,
      bye_count: 0,
      provisional_tie: false,
      qualification_status: "qualified",
      rank: 1,
    },
    {
      buchholz: 5,
      display_name: "Боб",
      effective_time_ms: 42_000,
      points: 7,
      wins: 2,
      losses: 1,
      bye_count: 0,
      provisional_tie: false,
      qualification_status: "qualified",
      rank: 2,
    },
    {
      buchholz: 4,
      display_name: "Вера",
      effective_time_ms: 44_000,
      points: 6,
      wins: 2,
      losses: 1,
      bye_count: 0,
      provisional_tie: false,
      qualification_status: "qualified",
      rank: 3,
    },
    {
      buchholz: 2,
      display_name: "Глеб",
      effective_time_ms: 46_000,
      points: 4,
      wins: 1,
      losses: 2,
      bye_count: 0,
      provisional_tie: false,
      qualification_status: "qualified",
      rank: 4,
    },
  ],
  projection_revision: phaseRevision(phase),
  tournament_id: tournamentId,
});

const publicBracket = (phase: Phase): PublicBracketResponse => ({
  matches: [
    {
      first_display_name: "Алиса",
      format: "bo1",
      position: 1,
      score: { first_participant_wins: 1, second_participant_wins: 0 },
      scheduled_at: null,
      second_display_name: "Глеб",
      stage: "semifinal",
      state: "completed",
      winner_display_name: "Алиса",
    },
    {
      first_display_name: "Боб",
      format: "bo1",
      position: 2,
      score: { first_participant_wins: 1, second_participant_wins: 0 },
      scheduled_at: null,
      second_display_name: "Вера",
      stage: "semifinal",
      state: "completed",
      winner_display_name: "Боб",
    },
    {
      first_display_name: phase === "playoffs" ? null : "Алиса",
      format: "bo3" as const,
      position: 1,
      score: phase === "playoffs"
        ? { first_participant_wins: 0, second_participant_wins: 0 }
        : { first_participant_wins: 2, second_participant_wins: 1 },
      scheduled_at: null,
      second_display_name: phase === "playoffs" ? null : "Боб",
      stage: "final" as const,
      state: phase === "playoffs"
        ? "planned" as const
        : phase === "completed"
          ? "completed" as const
          : "active" as const,
      winner_display_name: phase === "completed" ? "Алиса" : null,
    },
  ],
  projection_revision: phaseRevision(phase),
  tournament_id: tournamentId,
});

const configuration = (): Schema["TournamentConfiguration"] => ({
  category_pools: [
    {
      categories: ["web", "crypto", "pwn"],
      format: "bo1",
      id: "00000000-0000-4000-8000-000000000201",
      revision: 1,
    },
    {
      categories: ["web", "crypto", "pwn", "reverse", "osint"],
      format: "bo3",
      id: "00000000-0000-4000-8000-000000000202",
      revision: 1,
    },
  ],
  configuration_revision: 1,
  reserve_count: 1,
  final_default: {
    categories: ["web", "crypto", "pwn", "reverse", "osint"],
    mode: "draft",
  },
  golden_default: { categories: ["web"], mode: "random" },
  projection_revision: 9,
  projection_revision_id: "00000000-0000-4000-8000-000000000203",
  rounds: [],
  semifinal_default: { categories: ["web", "crypto"], mode: "admin" },
  series: [],
  swiss_default: { categories: ["web", "crypto"], mode: "random" },
  tournament_id: tournamentId,
  updated_at: baseDate,
});

const installRoutes = async (
  page: Page,
  options: Readonly<{ initialPhase: Phase; staleOpen?: boolean }>,
): Promise<GoldenHarness> => {
  let phase = options.initialPhase;
  let staleOpen = options.staleOpen ?? false;
  const actionRequests: RequestRecord[] = [];
  const openRequests: RequestRecord[] = [];
  const startRequests: RequestRecord[] = [];

  await page.route("**/api/v1/admin/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();

    if (path === "/api/v1/admin/login" && method === "POST") {
      await fulfillJSON(route, { expires_in: 900 }, 200, {
        "X-CSRF-Token": accessCSRF,
        "X-Admin-Refresh-CSRF-Token": refreshCSRF,
      });
      return;
    }
    if (path === "/api/v1/admin/tasks" && method === "GET") {
      await fulfillJSON(route, []);
      return;
    }
    if (path === "/api/v1/admin/tournament-content" && method === "GET") {
      await fulfillJSON(route, {
        content_revision: 1,
        golden_pool_revision_id: "00000000-0000-4000-8000-000000000211",
        normal_pool_revision_id: "00000000-0000-4000-8000-000000000212",
        publication_id: "00000000-0000-4000-8000-000000000213",
        published_at: baseDate,
      });
      return;
    }
    if (path === "/api/v1/admin/tournaments" && method === "GET") {
      await fulfillJSON(route, { items: [tournament(phase)], next_cursor: null });
      return;
    }
    if (path === "/api/v1/admin/players" && method === "GET") {
      await fulfillJSON(route, []);
      return;
    }
    if (path === "/api/v1/admin/players/events" && method === "GET") {
      await route.fulfill({
        body: "event: ready\ndata: {}\n\n",
        headers: { "content-type": "text/event-stream" },
        status: 200,
      });
      return;
    }

    const tournamentPath = `/api/v1/admin/tournaments/${tournamentId}`;
    if (path === `${tournamentPath}/golden` && method === "GET") {
      await fulfillJSON(route, goldenOperator(phase));
      return;
    }
    if (path === `${tournamentPath}/roster` && method === "GET") {
      await fulfillJSON(route, operatorSnapshot(phaseRevision(phase)).roster);
      return;
    }
    if (path === `${tournamentPath}/configuration` && method === "GET") {
      await fulfillJSON(route, configuration());
      return;
    }
    if (path === `${tournamentPath}/snapshot` && method === "GET") {
      await fulfillJSON(route, operatorSnapshot(phaseRevision(phase)), 200, {
        date: "Sat, 19 Sep 2026 10:00:00 GMT",
      });
      return;
    }
    if (path === `${tournamentPath}/actions` && method === "POST") {
      const body = requestBody(request);
      actionRequests.push({ body, headers: request.headers() });
      if (body.action === "start_golden") {
        phase = "golden-prepared";
      }
      if (body.action === "start_playoffs") {
        phase = "playoffs";
      }
      await fulfillJSON(route, tournament(phase));
      return;
    }
    if (path === `${tournamentPath}/golden/open` && method === "POST") {
      openRequests.push({ body: requestBody(request), headers: request.headers() });
      if (staleOpen) {
        staleOpen = false;
        phase = "golden-ready";
        await fulfillProblem(route, 409, "Состояние Golden устарело");
        return;
      }
      phase = "golden-ready";
      await fulfillJSON(route, goldenOperator(phase));
      return;
    }
    const startMatch = path.match(
      new RegExp(`^${tournamentPath}/golden/attempts/([^/]+)/start$`),
    );
    if (startMatch && method === "POST") {
      expect(startMatch[1]).toBe(attemptId);
      startRequests.push({ body: requestBody(request), headers: request.headers() });
      phase = "golden-active";
      await fulfillJSON(route, goldenOperator(phase));
      return;
    }

    await fulfillJSON(route, {}, 404);
  });

  await page.route("**/api/v1/tournaments/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const publicPath = `/api/v1/tournaments/${tournamentId}`;
    if (path === publicPath && request.method() === "GET") {
      await fulfillJSON(route, publicTournament(phase));
      return;
    }
    if (path === `${publicPath}/scoreboard` && request.method() === "GET") {
      await fulfillJSON(route, publicScoreboard(phase));
      return;
    }
    if (path === `${publicPath}/bracket` && request.method() === "GET") {
      await fulfillJSON(route, publicBracket(phase));
      return;
    }
    await fulfillJSON(route, {}, 404);
  });

  return {
    actionRequests,
    openRequests,
    startRequests,
    setPhase: (nextPhase) => {
      phase = nextPhase;
    },
  };
};

const loginAndSelectTournament = async (page: Page): Promise<ReturnType<Page["getByTestId"]>> => {
  await page.goto("/admin", { waitUntil: "domcontentloaded" });
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("heading", { name: "Новый турнир" })).toBeVisible();
  await page.getByRole("button", { name: "Турниры" }).click();
  const row = page.getByRole("row").filter({ hasText: "Golden playoff контракт" });
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Редактировать состав" }).click();
  const panel = page.getByTestId("operator-golden-playoff-control-panel");
  await expect(panel).toBeVisible();
  await expect(panel.getByText(tournamentId, { exact: true })).toBeVisible();
  return panel;
};

const expectMutationHeaders = (record: RequestRecord): void => {
  expect(record.headers["idempotency-key"]).toMatch(uuidPattern);
  expect(record.headers["x-csrf-token"]).toBe(accessCSRF);
};

test("управляет Golden и отображает официальный bracket без нижней сетки", async ({ page }) => {
  const harness = await installRoutes(page, { initialPhase: "swiss" });
  const panel = await loginAndSelectTournament(page);

  await expect(panel).toContainText("Golden группы не сформированы");
  await expect(panel.getByTestId("golden-start-lifecycle")).toBeEnabled();
  await expect(panel.getByTestId("golden-open-runtime")).toBeDisabled();
  await expect(panel.getByTestId("golden-start-playoffs")).toBeEnabled();

  await panel.getByTestId("golden-start-lifecycle").click();
  await expect.poll(() => harness.actionRequests.length).toBe(1);
  expect(harness.actionRequests[0]?.body).toEqual({
    action: "start_golden",
    confirmed: true,
    expected_projection_revision: 9,
    reason: "Оператор подтвердил переход в Golden",
  });
  expectMutationHeaders(harness.actionRequests[0]!);
  await expect(panel.getByTestId("golden-open-runtime")).toBeEnabled();
  await expect(panel.getByTestId("golden-start-playoffs")).toBeDisabled();

  await panel.getByTestId("golden-open-runtime").click();
  await expect.poll(() => harness.openRequests.length).toBe(1);
  expect(harness.openRequests[0]?.body).toEqual({
    expected_projection_revision: 10,
    expected_runtime_revision: 0,
  });
  expectMutationHeaders(harness.openRequests[0]!);
  await expect(panel).toContainText("Диапазон допуска: позиции 1-2");
  await expect(panel).toContainText(firstParticipantId);
  await expect(panel).toContainText(secondParticipantId);
  await expect(panel.getByText("Готов к старту", { exact: true })).toBeVisible();
  await expect(panel.getByTestId(`golden-start-attempt-${groupId}`)).toBeEnabled();

  await panel.getByTestId(`golden-start-attempt-${groupId}`).click();
  await expect.poll(() => harness.startRequests.length).toBe(1);
  expect(harness.startRequests[0]?.body).toEqual({
    expected_runtime_revision: 4,
    ready_window_id: readyWindowId,
  });
  expectMutationHeaders(harness.startRequests[0]!);
  await expect(panel.getByText("Идет", { exact: true })).toBeVisible();
  await expect(panel.getByTestId("golden-start-playoffs")).toBeDisabled();

  harness.setPhase("golden-completed");
  await panel.getByRole("button", { name: "Обновить Golden" }).click();
  await expect(panel.getByText("Завершен", { exact: true })).toBeVisible();
  await expect(panel.getByTestId("golden-start-playoffs")).toBeEnabled();

  await panel.getByTestId("golden-start-playoffs").click();
  await expect.poll(() => harness.actionRequests.length).toBe(2);
  expect(harness.actionRequests[1]?.body).toEqual({
    action: "start_playoffs",
    confirmed: true,
    expected_projection_revision: 13,
    reason: "Оператор подтвердил переход в плей-офф",
  });
  expectMutationHeaders(harness.actionRequests[1]!);

  const bracket = panel.getByTestId("server-playoff-bracket");
  await expect(bracket).toBeVisible();
  await expect(panel.getByTestId("server-seed")).toHaveCount(4);
  const matches = bracket.getByTestId("playoff-match");
  await expect(matches).toHaveCount(3);
  await expect(matches.nth(0)).toHaveAttribute("data-stage", "semifinal");
  await expect(matches.nth(0)).toContainText("Полуфинал 1 - BO1");
  await expect(matches.nth(0)).toContainText("Алиса (посев #1)");
  await expect(matches.nth(0)).toContainText("Глеб (посев #4)");
  await expect(matches.nth(0)).toContainText("1:0");
  await expect(matches.nth(1)).toHaveAttribute("data-stage", "semifinal");
  await expect(matches.nth(1)).toContainText("Полуфинал 2 - BO1");
  await expect(matches.nth(1)).toContainText("Боб (посев #2)");
  await expect(matches.nth(1)).toContainText("Вера (посев #3)");
  await expect(matches.nth(1)).toContainText("1:0");
  await expect(matches.nth(2)).toHaveAttribute("data-stage", "final");
  await expect(matches.nth(2)).toContainText("Финал - BO3");
  await expect(matches.nth(2)).toContainText("Ожидается");
  await expect(bracket).not.toContainText(/нижн|lower/i);

  harness.setPhase("playoffs-final");
  await panel.getByRole("button", { name: "Обновить Golden" }).click();
  await expect(matches).toHaveCount(3);
  await expect(matches.nth(2)).toHaveAttribute("data-stage", "final");
  await expect(matches.nth(2)).toContainText("Финал - BO3");
  await expect(matches.nth(2)).toContainText("2:1");

  harness.setPhase("completed");
  await panel.getByRole("button", { name: "Обновить Golden" }).click();
  await expect(panel.getByTestId("server-champion")).toContainText("Алиса");
  await expect(panel.getByTestId("golden-start-lifecycle")).toBeDisabled();
  await expect(panel.getByTestId("golden-start-playoffs")).toBeDisabled();
});

test("409 stale Golden refreshes state and keeps runtime command blocked until refresh", async ({ page }) => {
  const harness = await installRoutes(page, {
    initialPhase: "golden-prepared",
    staleOpen: true,
  });
  const panel = await loginAndSelectTournament(page);
  const open = panel.getByTestId("golden-open-runtime");

  await expect(open).toBeEnabled();
  await open.click();
  await expect.poll(() => harness.openRequests.length).toBe(1);
  expect(harness.openRequests[0]?.body).toEqual({
    expected_projection_revision: 10,
    expected_runtime_revision: 0,
  });
  expectMutationHeaders(harness.openRequests[0]!);
  await expect(panel.getByRole("alert")).toContainText(
    "Состояние Golden устарело. Обновите данные перед повтором.",
  );
  await expect(open).toBeDisabled();

  await panel.getByRole("button", { name: "Обновить Golden" }).click();
  await expect(open).toBeDisabled();
  await expect(panel.getByTestId(`golden-start-attempt-${groupId}`)).toBeEnabled();
  expect(harness.openRequests).toHaveLength(1);
});

test("Golden and playoff panel keeps dark/light themes and no horizontal overflow at 390px", async ({ page }) => {
  await page.setViewportSize({ height: 844, width: 390 });
  await installRoutes(page, { initialPhase: "golden-prepared" });
  await loginAndSelectTournament(page);

  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  const widths = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
});
