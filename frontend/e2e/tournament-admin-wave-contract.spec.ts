import { expect, test, type Page, type Route, type WebSocketRoute } from "@playwright/test";

const tournamentId = "20000000-0000-4000-8000-000000000001";
const rosterId = "20000000-0000-4000-8000-000000000002";
const baseDate = "2026-09-13T10:00:00.000Z";
const deadline = "2026-09-13T10:05:00.000Z";
const accessCSRF = "wave-admin-access-csrf";
const refreshCSRF = "wave-admin-refresh-csrf";

const id = (index: number): string =>
  `20000000-0000-4000-8000-${String(index).padStart(12, "0")}`;

const jsonHeaders = {
  "content-type": "application/json",
};

const fulfillJSON = async (
  route: Route,
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: { ...jsonHeaders, ...headers },
    status,
  });
};

const problem = (status: number, detail: string) => ({
  detail,
  status,
  title: status === 409 ? "conflict" : "request failed",
  type: "about:blank",
});

const participant = (index: number) => ({
  attendance: "checked_in",
  created_at: baseDate,
  id: id(100 + index),
  player_id: id(100 + index),
  roster_id: rosterId,
  seed: index + 1,
  tournament_id: tournamentId,
  updated_at: baseDate,
});

const series = (index: number, state: "planned" | "active" | "completed" | "cancelled" = "planned") => {
  const seriesId = id(300 + index);
  const firstParticipantId = id(100 + index * 2);
  const secondParticipantId = id(101 + index * 2);
  const slotId = id(400 + index);
  const gameId = id(500 + index);
  const category = ["web", "crypto", "forensics", "reverse"][index % 4];
  return {
    current_result_revision_id: null,
    current_score_revision_id: null,
    first_participant_id: firstParticipantId,
    format: "bo1",
    id: seriesId,
    score: { first_participant_wins: state === "completed" ? 1 : 0, second_participant_wins: 0 },
    second_participant_id: secondParticipantId,
    slots: [{
      attempts: [{
        attempt_no: 1,
        id: gameId,
        result_reason: null,
        result_revision_id: null,
        slot_id: slotId,
        state: state === "active" ? "active" : state === "completed" ? "completed" : "planned",
        winner_id: null,
      }],
      category,
      id: slotId,
      position: 1,
      score_before: { first_participant_wins: 0, second_participant_wins: 0 },
      series_id: seriesId,
    }],
    state,
    tournament_id: tournamentId,
    winner_id: state === "completed" ? firstParticipantId : null,
  };
};

const wave = (
  state: "planned" | "ready_window_open" | "ready" | "active" | "completed" | "ready_window_expired" | "superseded",
  readiness = false,
  readyMembers?: number,
) => {
  const waveId = id(200);
  const memberReady = (memberIndex: number): boolean => (
    readyMembers === undefined ? readiness : memberIndex < readyMembers
  );
  return {
    id: waveId,
    members: Array.from({ length: 8 }, (_, index) => [
      {
        participant_id: id(100 + index * 2),
        readiness_revision: memberReady(index * 2) ? 2 : 1,
        ready: memberReady(index * 2),
        series_id: id(300 + index),
      },
      {
        participant_id: id(101 + index * 2),
        readiness_revision: memberReady(index * 2 + 1) ? 2 : 1,
        ready: memberReady(index * 2 + 1),
        series_id: id(300 + index),
      },
    ]).flat(),
    paused_at: null,
    ready_window: state === "ready_window_open" || state === "ready"
      ? {
          consumed_at: null,
          deadline,
          id: id(600),
          opened_at: baseDate,
          revision_id: id(700),
          state: "open",
          wave_id: waveId,
        }
      : state === "ready_window_expired"
        ? {
            consumed_at: null,
            deadline,
            id: id(600),
            opened_at: baseDate,
            revision_id: id(700),
            state: "expired",
            wave_id: waveId,
          }
        : state === "active"
          ? {
              consumed_at: baseDate,
              deadline,
              id: id(600),
              opened_at: baseDate,
              revision_id: id(700),
              state: "consumed",
              wave_id: waveId,
            }
          : null,
    revision: 1,
    revision_id: id(800),
    started_at: state === "active" ? baseDate : null,
    state,
    tournament_id: tournamentId,
  };
};

const operatorSnapshot = (
  revision: number,
  waveValue = wave("planned"),
  seriesState: "planned" | "active" | "completed" | "cancelled" = "planned",
) => ({
  next_cursor: {
    audit_sequence: revision,
    authority_revision: revision,
    projection_revision: revision,
  },
  pause_graph: null,
  recovery_controls: [],
  roster: {
    created_at: baseDate,
    execution_started: true,
    execution_started_at: baseDate,
    id: rosterId,
    locked: true,
    locked_at: baseDate,
    participants: Array.from({ length: 16 }, (_, index) => participant(index)),
    revision: 1,
    tournament_id: tournamentId,
    updated_at: baseDate,
  },
  series: Array.from({ length: 8 }, (_, index) => series(index, seriesState)),
  tournament: {
    content_revision: 1,
    created_at: baseDate,
    finished_at: null,
    id: tournamentId,
    name: "Шестнадцать участников",
    paused_from_state: null,
    planned_roster_size: 16,
    preset: "tournament_v1",
    public_id: "sixteen-participants",
    revision,
    roster_id: rosterId,
    roster_size: 16,
    started_at: baseDate,
    state: "swiss",
    updated_at: baseDate,
  },
  waves: [waveValue],
});

const realtimeWave = (
  snapshot: ReturnType<typeof operatorSnapshot>,
  state = snapshot.waves[0]?.state ?? "planned",
  readiness: boolean | number = snapshot.waves[0]?.members.every((member) => member.ready) ?? false,
) => {
  const currentWave = snapshot.waves[0];
  if (!currentWave) {
    throw new Error("realtime fixture requires a Wave");
  }
  return {
    members: currentWave.members.map((member, index) => {
      const ready = typeof readiness === "number" ? index < readiness : readiness;
      return {
        participant_id: member.participant_id,
        readiness_revision: ready ? 2 : member.readiness_revision,
        ready,
        series_id: member.series_id,
      };
    }),
    state,
    wave_id: currentWave.id,
    ...(currentWave.ready_window?.deadline ? { window_deadline: currentWave.ready_window.deadline } : {}),
  };
};

const realtimeFrame = (
  snapshot: ReturnType<typeof operatorSnapshot>,
  sequence: number,
  state = snapshot.waves[0]?.state ?? "planned",
  readiness: boolean | number = snapshot.waves[0]?.members.every((member) => member.ready) ?? false,
) => ({
  type: "tournament.operator",
  payload: {
    envelope: {
      event_id: id(900 + sequence),
      occurred_at: baseDate,
      operator: {
        audit_links: [],
        golden: [],
        last_sequence: sequence,
        pause: null,
        presence: snapshot.waves.flatMap((item) => item.members.map((member, index) => ({
          participant_id: member.participant_id,
          series_id: member.series_id,
          state: index % 2 === 1 ? "disconnected" : "connected",
          presence_epoch: 1,
          updated_at: baseDate,
        }))),
        replays: [],
        revision: sequence,
        tournament_id: tournamentId,
        waves: [realtimeWave(snapshot, state, readiness)],
      },
      projection_revision: sequence,
      resume_id: id(950),
      schema_version: 1,
      sequence,
      tournament_id: tournamentId,
    },
  },
});

const sendRealtimeFrame = (
  socket: WebSocketRoute,
  snapshot: ReturnType<typeof operatorSnapshot>,
  sequence: number,
  state?: ReturnType<typeof realtimeWave>["state"],
  readiness?: boolean | number,
): void => {
  socket.send(JSON.stringify(realtimeFrame(snapshot, sequence, state, readiness)));
};

const tournamentListItem = () => ({
  content_revision: 1,
  created_at: baseDate,
  finished_at: null,
  id: tournamentId,
  name: "Шестнадцать участников",
  paused_from_state: null,
  planned_roster_size: 16,
  preset: "tournament_v1",
  public_id: "sixteen-participants",
  revision: 1,
  roster_id: rosterId,
  roster_size: 16,
  started_at: baseDate,
  state: "swiss",
  updated_at: baseDate,
});

const contentSelection = () => ({
  content_revision: 1,
  golden_pool_revision_id: id(980),
  normal_pool_revision_id: id(981),
  publication_id: id(982),
  published_at: baseDate,
});

const setupRoutes = async (page: Page): Promise<{
  getSnapshotCount: () => number;
  getWaveActions: () => Array<{ body: Record<string, unknown>; idempotencyKey: string; waveId: string }>;
  setConflictNextWaveAction: () => void;
  setSnapshot: (next: ReturnType<typeof operatorSnapshot>) => void;
}> => {
  let currentSnapshot = operatorSnapshot(1);
  let snapshotCount = 0;
  let conflictNextWaveAction = false;
  const waveActions: Array<{ body: Record<string, unknown>; idempotencyKey: string; waveId: string }> = [];

  await page.route("**/api/v1/admin/login", async (route) => {
    await fulfillJSON(route, 200, { expires_in: 900 }, {
      "X-CSRF-Token": accessCSRF,
      "X-Admin-Refresh-CSRF-Token": refreshCSRF,
    });
  });
  await page.route("**/api/v1/admin/tasks**", async (route) => {
    await fulfillJSON(route, 200, []);
  });
  await page.route("**/api/v1/admin/players**", async (route) => {
    await fulfillJSON(route, 200, []);
  });
  await page.route("**/api/v1/admin/tournament-content**", async (route) => {
    await fulfillJSON(route, 200, contentSelection());
  });
  await page.route("**/api/v1/admin/tournaments**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/admin/tournaments" && request.method() === "GET") {
      await fulfillJSON(route, 200, { items: [tournamentListItem()], next_cursor: null });
      return;
    }
    if (path.endsWith("/snapshot") && request.method() === "GET") {
      snapshotCount += 1;
      await fulfillJSON(route, 200, currentSnapshot, { Date: "Sun, 13 Sep 2026 10:00:00 GMT" });
      return;
    }
    if (path.includes("/waves/") && path.endsWith("/actions") && request.method() === "POST") {
      const waveId = path.split("/").at(-2) ?? "";
      waveActions.push({
        body: request.postDataJSON() as Record<string, unknown>,
        idempotencyKey: request.headers()["idempotency-key"] ?? "",
        waveId,
      });
      const body = request.postDataJSON() as { action?: string };
      const target = currentSnapshot.waves.find((item) => item.id === waveId);
      if (conflictNextWaveAction) {
        conflictNextWaveAction = false;
        await fulfillJSON(route, 409, problem(409, "Предыдущий раунд не завершен"));
        return;
      }
      if (target && body.action === "open_ready_window") {
        currentSnapshot = {
          ...currentSnapshot,
          next_cursor: { ...currentSnapshot.next_cursor, authority_revision: 2, projection_revision: 2 },
          tournament: { ...currentSnapshot.tournament, revision: 2 },
          waves: currentSnapshot.waves.map((item) => item.id === waveId ? wave("ready_window_open") : item),
        };
      }
      if (target && body.action === "start") {
        currentSnapshot = {
          ...currentSnapshot,
          next_cursor: { ...currentSnapshot.next_cursor, authority_revision: 3, projection_revision: 3 },
          tournament: { ...currentSnapshot.tournament, revision: 3 },
          series: Array.from({ length: 8 }, (_, index) => series(index, "active")),
          waves: currentSnapshot.waves.map((item) => item.id === waveId ? wave("active", true) : item),
        };
      }
      await fulfillJSON(route, 200, target ?? currentSnapshot.waves[0]);
      return;
    }
    await fulfillJSON(route, 404, problem(404, "not found"));
  });

  return {
    getSnapshotCount: () => snapshotCount,
    getWaveActions: () => waveActions,
    setConflictNextWaveAction: () => {
      conflictNextWaveAction = true;
    },
    setSnapshot: (next) => {
      currentSnapshot = next;
    },
  };
};

const openAdminTournament = async (page: Page): Promise<void> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("button", { name: "Соревнования" }).click();
  await expect(page.getByRole("heading", { name: "Новое соревнование" })).toBeVisible();
  await page.getByRole("button", { name: "Открыть" }).click();
  await page.getByRole("button", { name: "Проведение" }).click();
  await expect(page.getByTestId("operator-wave-control-panel")).toBeVisible();
};

test("показывает одну Wave с 8 Series, всеми 16 участниками, связью, категориями и счетом", async ({ page }) => {
  const routes = await setupRoutes(page);
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, operatorSnapshot(1), 1);
    },
  );

  await openAdminTournament(page);
  await expect(page.getByTestId("operator-wave")).toHaveCount(1);
  await expect(page.getByTestId("operator-match")).toHaveCount(8);
  await expect(page.getByTestId("operator-wave-board").getByText("Участник", { exact: true })).toHaveCount(16);
  await expect(page.getByTestId("operator-wave-board").locator("[data-series-id]")).toHaveCount(8);
  await expect(page.getByText("Связь потеряна", { exact: true }).first()).toBeVisible();
  await expect(page.getByTestId("operator-wave-connection")).toContainText("Обновляется автоматически");
  await expect(page.getByTestId("operator-wave-board")).toContainText("0:0");
  expect(routes.getSnapshotCount()).toBeGreaterThan(0);
});

test("открывает и запускает волну с текущей ревизией, ключом идемпотентности и refresh", async ({ page }) => {
  const routes = await setupRoutes(page);
  let operatorSocket: WebSocketRoute | null = null;
  let realtimeConnections = 0;
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      realtimeConnections += 1;
      operatorSocket = socket;
      sendRealtimeFrame(socket, operatorSnapshot(1), 1);
    },
  );

  await openAdminTournament(page);
  await expect(page.getByTestId("operator-wave-connection")).toContainText("Обновляется автоматически");
  const connectionsBeforeOpen = realtimeConnections;
  const openButton = page.locator('[data-testid^="wave-open-"]').first();
  await expect(openButton).toBeEnabled();
  await openButton.click();
  await expect.poll(() => routes.getWaveActions().length).toBe(1);
  expect(routes.getWaveActions()[0]?.body).toMatchObject({
    action: "open_ready_window",
    confirmed: true,
    expected_projection_revision: 1,
    reason: expect.any(String),
  });
  expect(routes.getWaveActions()[0]?.idempotencyKey).toMatch(/^[0-9a-f-]{36}$/i);
  await expect.poll(() => routes.getSnapshotCount()).toBeGreaterThan(1);

  const readySnapshot = operatorSnapshot(2, wave("ready", true));
  routes.setSnapshot(readySnapshot);
  await expect.poll(() => realtimeConnections).toBeGreaterThan(connectionsBeforeOpen);
  if (operatorSocket) {
    sendRealtimeFrame(operatorSocket, readySnapshot, 2, "ready", true);
  }
  const startButton = page.locator('[data-testid^="wave-start-"]').first();
  await expect(startButton).toBeEnabled();
  await expect(page.getByTestId("wave-ready-countdown")).toHaveCount(8);
  const connectionsBeforeStart = realtimeConnections;
  await startButton.click();
  await expect.poll(() => routes.getWaveActions().length).toBe(2);
  expect(routes.getWaveActions()[1]?.body).toMatchObject({
    action: "start",
    confirmed: true,
    expected_projection_revision: 2,
    reason: expect.any(String),
  });
  expect(routes.getWaveActions()[1]?.idempotencyKey).toMatch(/^[0-9a-f-]{36}$/i);
  await expect.poll(() => routes.getSnapshotCount()).toBeGreaterThan(2);

  const activeSnapshot = operatorSnapshot(3, wave("active", true), "active");
  routes.setSnapshot(activeSnapshot);
  await expect.poll(() => realtimeConnections).toBeGreaterThan(connectionsBeforeStart);
  if (operatorSocket) {
    sendRealtimeFrame(operatorSocket, activeSnapshot, 3, "active", true);
  }
  await expect(page.getByTestId("operator-match")).toHaveCount(8);
  await expect(page.getByTestId("operator-match").getByText("Идет", { exact: true })).toHaveCount(8);
});

test("после открытия готовности объясняет ожидание даже при частичной готовности", async ({ page }) => {
  const partialSnapshot = operatorSnapshot(1, wave("ready_window_open", false, 4));
  const routes = await setupRoutes(page);
  routes.setSnapshot(partialSnapshot);
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, partialSnapshot, 1, "ready_window_open", 4);
    },
  );

  await openAdminTournament(page);
  await expect(page.getByText("Ожидаем подтверждения участников. Запуск станет доступен после полной готовности пары.")).toBeVisible();
});

test("после полной готовности в открытом окне сообщает о переходе к запуску", async ({ page }) => {
  const readySnapshot = operatorSnapshot(1, wave("ready_window_open", true));
  const routes = await setupRoutes(page);
  routes.setSnapshot(readySnapshot);
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, readySnapshot, 1, "ready_window_open", true);
    },
  );

  await openAdminTournament(page);
  await expect(page.getByText("Все участники подтвердили готовность. Дождитесь перехода волны к запуску.")).toBeVisible();
});

test("для истекшего окна готовности подсказывает обновить данные и проверить неявку", async ({ page }) => {
  const expiredSnapshot = operatorSnapshot(1, wave("ready_window_expired"));
  const routes = await setupRoutes(page);
  routes.setSnapshot(expiredSnapshot);
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, expiredSnapshot, 1, "ready_window_expired", false);
    },
  );

  await openAdminTournament(page);
  await expect(page.getByText("Окно готовности истекло. Обновите данные и проверьте участников, которые не подтвердили готовность.")).toBeVisible();
  await expect(page.locator('[data-testid^="wave-open-"]').first()).toBeDisabled();
  await expect(page.locator('[data-testid^="wave-start-"]').first()).toBeDisabled();
});

test("unfinished previous round 409 блокирует повтор до refresh", async ({ page }) => {
  const routes = await setupRoutes(page);
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, operatorSnapshot(1), 1);
    },
  );
  routes.setConflictNextWaveAction();

  await openAdminTournament(page);
  const staleOpen = page.locator('[data-testid^="wave-open-"]').first();
  await staleOpen.click();
  await expect(page.getByText("Состояние соревнования устарело. Обновите данные перед повтором.")).toBeVisible();
  const actionCountAfterConflict = routes.getWaveActions().length;
  await expect(staleOpen).toBeDisabled();
  await staleOpen.click({ force: true });
  expect(routes.getWaveActions()).toHaveLength(actionCountAfterConflict);
  await page.getByTestId("operator-wave-control-panel").getByRole("button", { name: "Обновить данные" }).click();
  await expect(staleOpen).toBeEnabled();
  expect(routes.getWaveActions()[0]?.body.expected_projection_revision).toBe(1);
});

test("completed и superseded Wave не разрешают restart", async ({ page }) => {
  const routes = await setupRoutes(page);
  routes.setSnapshot(operatorSnapshot(1, wave("completed"), "completed"));
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, operatorSnapshot(1, wave("completed"), "completed"), 1, "completed", false);
    },
  );
  await openAdminTournament(page);
  await expect(page.locator('[data-testid^="wave-open-"]').first()).toBeDisabled();
  await expect(page.locator('[data-testid^="wave-start-"]').first()).toBeDisabled();
  expect(routes.getWaveActions()).toHaveLength(0);
});

test("superseded Wave не разрешает restart", async ({ page }) => {
  const routes = await setupRoutes(page);
  routes.setSnapshot(operatorSnapshot(1, wave("superseded"), "cancelled"));
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, operatorSnapshot(1, wave("superseded"), "cancelled"), 1, "superseded", false);
    },
  );
  await openAdminTournament(page);
  await expect(page.locator('[data-testid^="wave-open-"]').first()).toBeDisabled();
  await expect(page.locator('[data-testid^="wave-start-"]').first()).toBeDisabled();
  expect(routes.getWaveActions()).toHaveLength(0);
});

test("сохраняет все матчи в dark, light и на мобильной ширине", async ({ page }) => {
  await setupRoutes(page);
  await page.context().routeWebSocket(
    (url) => url.pathname === `/api/v1/admin/tournaments/${tournamentId}/realtime`,
    async (socket: WebSocketRoute) => {
      sendRealtimeFrame(socket, operatorSnapshot(1), 1);
    },
  );

  await openAdminTournament(page);
  for (const theme of ["Темная тема", "Светлая тема"] as const) {
    await page.getByRole("button", { name: theme }).click();
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme",
      theme === "Темная тема" ? "dark" : "light",
    );
    await expect(page.getByTestId("operator-match")).toHaveCount(8);
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByTestId("operator-match")).toHaveCount(8);
  const widths = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
});
