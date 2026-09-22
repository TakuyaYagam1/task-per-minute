import { expect, test, type Page, type Request, type Route, type WebSocketRoute } from "@playwright/test";

import type { OperatorRecoverySnapshot } from "../lib/shared/api";
import {
  operatorSnapshot,
  publicTournament,
  tournamentFixtureIds,
} from "./tournament/fixtures";

const tournamentId = tournamentFixtureIds.tournament;
const publicTournamentPath = `/api/v1/tournaments/${tournamentId}`;
const operatorSnapshotPath = `/api/v1/admin/tournaments/${tournamentId}/snapshot`;
const operatorActionsPath = `/api/v1/admin/tournaments/${tournamentId}/actions`;
const waveActionsPath = `/api/v1/admin/tournaments/${tournamentId}/waves/${tournamentFixtureIds.bo3Wave}/actions`;
const realtimePath = `/api/v1/admin/tournaments/${tournamentId}/realtime`;
const noShowPath = `/api/v1/admin/tournaments/${tournamentId}/waves/${tournamentFixtureIds.bo1Wave}/no-shows`;
const forfeitPath = `/api/v1/admin/tournaments/${tournamentId}/series/${tournamentFixtureIds.bo3Series}/operator-forfeits`;
const dateHeader = "Sun, 13 Sep 2026 10:00:00 GMT";
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const jsonHeaders = { "content-type": "application/json" };

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

const fulfillProblem = async (route: Route, status: number, detail: string): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify({ detail, status, title: "conflict", type: "about:blank" }),
    headers: { "content-type": "application/problem+json" },
    status,
  });
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);

const requestBody = (request: Request): Record<string, unknown> => {
  const value: unknown = request.postDataJSON();
  if (!isRecord(value)) {
    throw new Error("Expected a JSON object request body");
  }
  return value;
};

const activeSnapshot = (revision: number): OperatorRecoverySnapshot => {
  const snapshot = operatorSnapshot(revision);
  return {
    ...snapshot,
    next_cursor: {
      ...snapshot.next_cursor,
      authority_revision: revision,
      projection_revision: revision,
    },
    pause_graph: null,
    series: snapshot.series.map((series) => ({
      ...series,
      state: series.id === tournamentFixtureIds.bo3Series ? "active" : "completed",
    })),
    tournament: {
      ...snapshot.tournament,
      paused_from_state: null,
      revision,
      state: "swiss",
    },
    waves: snapshot.waves.map((wave) => ({
      ...wave,
      paused_at: null,
      state: wave.id === tournamentFixtureIds.bo3Wave ? "active" : "completed",
    })),
  };
};

const pausedSnapshot = (revision: number): OperatorRecoverySnapshot => {
  const snapshot = operatorSnapshot(revision);
  return {
    ...snapshot,
    next_cursor: {
      ...snapshot.next_cursor,
      authority_revision: revision,
      projection_revision: revision,
    },
    tournament: {
      ...snapshot.tournament,
      revision,
      state: "technical_pause",
    },
    waves: snapshot.waves.map((wave) => wave.id === tournamentFixtureIds.bo1Wave
      ? { ...wave, paused_at: null, state: "completed" }
      : wave),
  };
};

const cancelledSnapshot = (revision: number): OperatorRecoverySnapshot => {
  const snapshot = activeSnapshot(revision);
  return {
    ...snapshot,
    tournament: {
      ...snapshot.tournament,
      finished_at: "2026-09-13T10:05:00Z",
      revision,
      state: "cancelled",
    },
  };
};

const noShowSnapshot = (
  revision: number,
  oneReadyParticipant: boolean,
): OperatorRecoverySnapshot => {
  const snapshot = activeSnapshot(revision);
  const candidateWave = snapshot.waves.find((wave) => wave.id === tournamentFixtureIds.bo1Wave);
  if (candidateWave === undefined) {
    throw new Error("No-show fixture must contain the BO1 wave");
  }
  return {
    ...snapshot,
    series: snapshot.series.map((series) => series.id === tournamentFixtureIds.bo1Series
      ? { ...series, state: "ready" }
      : series),
    waves: snapshot.waves.map((wave) => wave.id === candidateWave.id
      ? {
          ...wave,
          members: wave.members.map((member, index) => ({
            ...member,
            ready: oneReadyParticipant && index === 0,
            readiness_revision: revision,
          })),
          ready_window: {
            consumed_at: null,
            deadline: "2026-09-13T09:59:00Z",
            id: tournamentFixtureIds.readyWindow,
            opened_at: "2026-09-13T09:50:00Z",
            revision_id: tournamentFixtureIds.pauseRevision,
            state: "open",
            wave_id: wave.id,
          },
          state: "ready_window_open",
        }
      : wave),
  };
};

const twoNoShowCandidatesSnapshot = (revision: number): OperatorRecoverySnapshot => {
  const snapshot = noShowSnapshot(revision, false);
  return {
    ...snapshot,
    series: snapshot.series.map((series) => series.id === tournamentFixtureIds.bo3Series
      ? { ...series, state: "ready" }
      : series),
    waves: snapshot.waves.map((wave) => wave.id === tournamentFixtureIds.bo1Wave
      ? {
          ...wave,
          members: [
            ...wave.members,
            {
              participant_id: tournamentFixtureIds.thirdParticipant,
              readiness_revision: revision,
              ready: false,
              series_id: tournamentFixtureIds.bo3Series,
            },
            {
              participant_id: tournamentFixtureIds.fourthParticipant,
              readiness_revision: revision,
              ready: false,
              series_id: tournamentFixtureIds.bo3Series,
            },
          ],
        }
      : wave),
  };
};

const forfeitSnapshot = (revision: number): OperatorRecoverySnapshot => {
  const snapshot = activeSnapshot(revision);
  return {
    ...snapshot,
    series: snapshot.series.map((series) => series.id === tournamentFixtureIds.bo3Series
      ? {
          ...series,
          slots: series.slots.map((slot, index) => index === series.slots.length - 1
            ? {
                ...slot,
                attempts: slot.attempts.map((attempt, attemptIndex) => attemptIndex === slot.attempts.length - 1
                  ? { ...attempt, state: "active" }
                  : attempt),
              }
            : slot),
          state: "active",
        }
      : series),
  };
};

const publicFor = (snapshot: OperatorRecoverySnapshot) => ({
  ...publicTournament(snapshot.next_cursor.projection_revision),
  finished_at: snapshot.tournament.finished_at,
  roster_size: snapshot.tournament.roster_size,
  started_at: snapshot.tournament.started_at,
  state: snapshot.tournament.state,
});

const operatorRealtimeMessage = (
  snapshot: OperatorRecoverySnapshot,
  sequence: number,
) => ({
  type: "tournament.operator",
  payload: {
    envelope: {
      event_id: `00000000-0000-4000-8000-${String(900 + sequence).padStart(12, "0")}`,
      occurred_at: "2026-09-13T10:00:00Z",
      operator: {
        audit_links: [],
        golden: [],
        last_sequence: sequence,
        pause: snapshot.pause_graph,
        presence: [],
        replays: [],
        revision: snapshot.next_cursor.projection_revision,
        tournament_id: tournamentId,
        waves: snapshot.waves.map((wave) => ({
          members: wave.members,
          state: wave.state,
          wave_id: wave.id,
        })),
      },
      projection_revision: snapshot.next_cursor.projection_revision,
      resume_id: tournamentFixtureIds.resume,
      schema_version: 1,
      sequence,
      tournament_id: tournamentId,
    },
  },
});

type ActionPlan = Readonly<{
  conflict?: boolean;
  defer?: boolean;
  nextSnapshot: OperatorRecoverySnapshot;
}>;

type RuntimeRoutes = Readonly<{
  actionRequests: readonly { body: Record<string, unknown>; headers: Record<string, string> }[];
  waveActionRequests: readonly { body: Record<string, unknown>; headers: Record<string, string> }[];
  noShowRequests: readonly { body: Record<string, unknown>; headers: Record<string, string> }[];
  forfeitRequests: readonly { body: Record<string, unknown>; headers: Record<string, string> }[];
  releaseAction: () => void;
  setActionPlan: (plan: ActionPlan) => void;
  setSnapshot: (snapshot: OperatorRecoverySnapshot) => void;
}>;

const installRoutes = async (
  page: Page,
  initialSnapshot: OperatorRecoverySnapshot,
): Promise<RuntimeRoutes> => {
  let currentSnapshot = initialSnapshot;
  let actionPlan: ActionPlan = { nextSnapshot: initialSnapshot };
  let releaseAction: (() => void) | null = null;
  const actionRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];
  const waveActionRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];
  const noShowRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];
  const forfeitRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];

  await page.route(`**${publicTournamentPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, publicFor(currentSnapshot));
  });
  await page.route(`**${operatorSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, currentSnapshot, 200, { Date: dateHeader });
  });
  await page.route(`**${operatorActionsPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    const body = requestBody(route.request());
    actionRequests.push({ body, headers: route.request().headers() });
    const plan = actionPlan;
    if (plan.defer) {
      await new Promise<void>((resolve) => {
        releaseAction = resolve;
      });
    }
    if (plan.conflict) {
      currentSnapshot = plan.nextSnapshot;
      await fulfillProblem(route, 409, "Снимок устарел");
      return;
    }
    currentSnapshot = plan.nextSnapshot;
    await fulfillJSON(route, currentSnapshot.tournament);
  });
  await page.route(`**${waveActionsPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    const body = requestBody(route.request());
    waveActionRequests.push({ body, headers: route.request().headers() });
    const plan = actionPlan;
    if (plan.defer) {
      await new Promise<void>((resolve) => {
        releaseAction = resolve;
      });
    }
    if (plan.conflict) {
      currentSnapshot = plan.nextSnapshot;
      await fulfillProblem(route, 409, "Снимок устарел");
      return;
    }
    currentSnapshot = plan.nextSnapshot;
    const wave = currentSnapshot.waves.find((candidate) => candidate.id === tournamentFixtureIds.bo3Wave);
    if (wave === undefined) {
      throw new Error("Wave action fixture lost the controlled Wave");
    }
    await fulfillJSON(route, wave);
  });
  await page.route(`**${noShowPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    noShowRequests.push({ body: requestBody(route.request()), headers: route.request().headers() });
    await route.fulfill({ status: 204 });
  });
  await page.route(`**${forfeitPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    forfeitRequests.push({ body: requestBody(route.request()), headers: route.request().headers() });
    await route.fulfill({ status: 204 });
  });
  await page.context().routeWebSocket(
    (url) => url.pathname === realtimePath,
    (socket: WebSocketRoute) => {
      socket.send(JSON.stringify(operatorRealtimeMessage(currentSnapshot, 1)));
    },
  );

  return {
    actionRequests,
    forfeitRequests,
    noShowRequests,
    waveActionRequests,
    releaseAction: () => releaseAction?.(),
    setActionPlan: (plan) => {
      actionPlan = plan;
    },
    setSnapshot: (snapshot) => {
      currentSnapshot = snapshot;
    },
  };
};

const openOperatorArena = async (page: Page): Promise<void> => {
  await page.goto(`/arena/operator/${tournamentId}`, { waitUntil: "domcontentloaded" });
  await page.evaluate(() => {
    document.cookie = "tpm_admin_access_csrf=operator-access-csrf; Path=/; SameSite=Lax";
  });
  await expect(page.getByRole("heading", { name: "Управление турниром" })).toBeVisible();
  await expect(page.getByText("Снимок подтвержден", { exact: true })).toBeVisible();
};

const fillReasonAndConfirm = async (page: Page, reason: string): Promise<void> => {
  await page.getByLabel("Причина").fill(reason);
  const confirmation = page.getByRole("checkbox", {
    name: "Подтверждаю, что команда соответствует текущему авторитетному снимку.",
    exact: true,
  });
  await confirmation.focus();
  await expect(confirmation).toBeFocused();
  await page.keyboard.press("Space");
  await expect(confirmation).toBeChecked();
};

test("pause sends one exact command, has no optimistic success, and refreshes the snapshot", async ({ page }) => {
  const routes = await installRoutes(page, activeSnapshot(9));
  routes.setActionPlan({ defer: true, nextSnapshot: pausedSnapshot(10) });

  await openOperatorArena(page);
  const actionSelect = page.getByLabel("Команда оператора");
  await actionSelect.selectOption("pause");
  await fillReasonAndConfirm(page, "Платформенная пауза для проверки состояния");

  const submit = page.getByRole("button", { name: "Выполнить: Пауза турнира" });
  await submit.click();
  await expect(submit).toBeDisabled();
  await submit.click({ force: true });
  await expect.poll(() => routes.waveActionRequests.length).toBe(1);
  expect(routes.actionRequests).toHaveLength(0);
  expect(routes.waveActionRequests[0]?.body).toEqual({
    action: "pause",
    confirmed: true,
    expected_projection_revision: 9,
    reason: "Платформенная пауза для проверки состояния",
  });
  expect(routes.waveActionRequests[0]?.headers["idempotency-key"]).toMatch(uuidPattern);
  expect(routes.waveActionRequests[0]?.headers["x-csrf-token"]).toBe("operator-access-csrf");
  await expect(page.getByText("Команда подтверждена", { exact: true })).toHaveCount(0);

  routes.releaseAction();
  await expect(page.getByText("Команда подтверждена", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Управление турниром" })).toContainText("Техническая пауза");
});

test("stale operator command refetches the current snapshot and shows a warning", async ({ page }) => {
  const routes = await installRoutes(page, activeSnapshot(9));
  const freshSnapshot = pausedSnapshot(10);
  routes.setActionPlan({ conflict: true, nextSnapshot: freshSnapshot });

  await openOperatorArena(page);
  await page.getByLabel("Команда оператора").selectOption("pause");
  await fillReasonAndConfirm(page, "Проверка устаревшей ревизии");
  const submit = page.getByRole("button", { name: "Выполнить: Пауза турнира" });
  await submit.click();

  await expect(page.getByText("Снимок устарел", { exact: true })).toBeVisible();
  await expect(page.getByText("Другой оператор изменил состояние. Снимок обновлен, проверьте команду перед повтором.", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Управление турниром" })).toContainText("Техническая пауза");
  expect(routes.actionRequests).toHaveLength(0);
  expect(routes.waveActionRequests).toHaveLength(1);
  expect(routes.waveActionRequests[0]?.body.expected_projection_revision).toBe(9);
});

test("no-show and operator forfeit keep distinct evidence-bound 204 contracts", async ({ page }) => {
  const oneReady = noShowSnapshot(9, true);
  const routes = await installRoutes(page, oneReady);

  await openOperatorArena(page);
  const actionSelect = page.getByLabel("Команда оператора");
  const noShowOption = actionSelect.locator('option[value="no-show"]');
  await expect.soft(noShowOption, "FE-033 requires a one-ready participant no-show candidate after the deadline").not.toHaveAttribute("disabled");

  routes.setSnapshot(noShowSnapshot(10, false));
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByText("Снимок подтвержден", { exact: true })).toBeVisible();
  await actionSelect.selectOption("no-show");
  await fillReasonAndConfirm(page, "Подтвержденная неявка после окна готовности");
  await page.getByRole("button", { name: "Выполнить: Неявка пары" }).click();

  await expect.poll(() => routes.noShowRequests.length).toBe(1);
  const noShow = routes.noShowRequests[0];
  expect(noShow?.body).toMatchObject({
    confirmed: true,
    expected_authority_revision: 10,
    expected_series_state: "ready",
    expected_wave_revision_id: tournamentFixtureIds.pauseRevision,
    expected_window_revision_id: tournamentFixtureIds.pauseRevision,
    reason: "Подтвержденная неявка после окна готовности",
    series_id: tournamentFixtureIds.bo1Series,
    tournament_id: tournamentId,
    wave_id: tournamentFixtureIds.bo1Wave,
    window_id: tournamentFixtureIds.readyWindow,
  });
  expect(noShow?.body.game_result_revision_ids).toEqual([expect.stringMatching(uuidPattern)]);
  expect(noShow?.body.score_revision_id).toEqual(expect.stringMatching(uuidPattern));
  expect(noShow?.body.series_result_revision_id).toEqual(expect.stringMatching(uuidPattern));
  expect(noShow?.headers["idempotency-key"]).toMatch(uuidPattern);
  expect(noShow?.headers["x-csrf-token"]).toBe("operator-access-csrf");

  routes.setSnapshot(forfeitSnapshot(11));
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByText("Снимок подтвержден", { exact: true })).toBeVisible();
  await actionSelect.selectOption("operator-forfeit");
  await page.getByLabel("rule_id").fill("rule.match.integrity");
  await page.getByLabel("Evidence IDs").fill("00000000-0000-4000-8000-000000000999");
  await fillReasonAndConfirm(page, "Нарушение правила целостности матча");
  await page.getByRole("button", { name: "Выполнить: Операторский форфейт" }).click();

  await expect.poll(() => routes.forfeitRequests.length).toBe(1);
  const forfeit = routes.forfeitRequests[0];
  expect(forfeit?.body).toMatchObject({
    basis: "rule_violation",
    confirmed: true,
    evidence_ids: ["00000000-0000-4000-8000-000000000999"],
    expected_authority_revision: 11,
    forfeiting_participant_id: tournamentFixtureIds.firstParticipant,
    reason: "Нарушение правила целостности матча",
    rule_id: "rule.match.integrity",
    series_id: tournamentFixtureIds.bo3Series,
    tournament_id: tournamentId,
  });
  expect(forfeit?.body.expected_game).toEqual({
    attempt_no: 1,
    game_id: tournamentFixtureIds.bo3GameThree,
    slot_id: tournamentFixtureIds.bo3SlotThree,
    state: "active",
  });
  expect(forfeit?.body.game_result_revision_id).toEqual(expect.stringMatching(uuidPattern));
  expect(forfeit?.body.audit_event_id).toEqual(expect.stringMatching(uuidPattern));
  expect(forfeit?.body.outbox_event_id).toEqual(expect.stringMatching(uuidPattern));
  expect(forfeit?.body.projection_revision_id).toEqual(expect.stringMatching(uuidPattern));
  expect(forfeit?.body.score_revision_id).toEqual(expect.stringMatching(uuidPattern));
  expect(forfeit?.body.series_result_revision_id).toEqual(expect.stringMatching(uuidPattern));
  expect(forfeit?.body).not.toHaveProperty("surrender");
  expect(forfeit?.headers["idempotency-key"]).toMatch(uuidPattern);
  expect(forfeit?.headers["x-csrf-token"]).toBe("operator-access-csrf");
  await expect(page.getByText("Операторский форфейт выполнено. Отображается новый авторитетный снимок.", { exact: true })).toBeVisible();
});

test("no-show selection distinguishes series sharing one wave", async ({ page }) => {
  const routes = await installRoutes(page, twoNoShowCandidatesSnapshot(15));

  await openOperatorArena(page);
  await page.getByLabel("Команда оператора").selectOption("no-show");
  const candidateSelect = page.getByLabel("Открытое окно неявки");
  await expect(candidateSelect.locator("option")).toHaveCount(2);
  await candidateSelect.selectOption({
    label: `Волна ${tournamentFixtureIds.bo1Wave} - серия ${tournamentFixtureIds.bo3Series}`,
  });
  await fillReasonAndConfirm(page, "Подтвержденная неявка второй пары после окна готовности");
  await page.getByRole("button", { name: "Выполнить: Неявка пары" }).click();

  await expect.poll(() => routes.noShowRequests.length).toBe(1);
  expect(routes.noShowRequests[0]?.body).toMatchObject({
    expected_series_state: "ready",
    series_id: tournamentFixtureIds.bo3Series,
    wave_id: tournamentFixtureIds.bo1Wave,
  });
});

test("resume follows the server matrix and cancellation requires a dismissible dialog", async ({ page }) => {
  const routes = await installRoutes(page, pausedSnapshot(12));
  routes.setActionPlan({ nextSnapshot: activeSnapshot(13) });

  await openOperatorArena(page);
  const actionSelect = page.getByLabel("Команда оператора");
  await expect(actionSelect.locator('option[value="pause"]')).toBeDisabled();
  await expect(actionSelect.locator('option[value="resume"]')).not.toBeDisabled();
  await actionSelect.selectOption("resume");
  await fillReasonAndConfirm(page, "Инцидент устранен, authority восстановлен");
  await page.getByRole("button", { name: "Выполнить: Возобновить турнир" }).click();

  await expect.poll(() => routes.waveActionRequests.length).toBe(1);
  expect(routes.actionRequests).toHaveLength(0);
  expect(routes.waveActionRequests[0]?.body).toEqual({
    action: "resume",
    confirmed: true,
    expected_projection_revision: 12,
    reason: "Инцидент устранен, authority восстановлен",
  });
  await expect(page.getByRole("region", { name: "Управление турниром" })).toContainText("Швейцарка");

  routes.setActionPlan({ nextSnapshot: cancelledSnapshot(14) });
  await actionSelect.selectOption("cancel");
  await page.getByLabel("Причина").fill("Турнир отменен решением главного судьи");
  const cancellationConfirmation = page.getByRole("checkbox", {
    name: "Подтверждаю, что команда соответствует текущему авторитетному снимку.",
    exact: true,
  });
  await cancellationConfirmation.focus();
  await page.keyboard.press("Space");
  await expect(cancellationConfirmation).toBeChecked();
  await page.getByRole("button", { name: "Выполнить: Отменить турнир" }).click();

  const dialog = page.getByRole("dialog", { name: "Подтвердить отмену турнира" });
  await expect(dialog).toBeVisible();
  const dismissCancellation = dialog.getByRole("button", { name: "Не отменять", exact: true });
  await dismissCancellation.focus();
  await expect(dismissCancellation).toBeFocused();
  await dismissCancellation.press("Enter");
  await expect(dialog).toBeHidden();
  expect(routes.actionRequests).toHaveLength(0);

  await page.getByRole("button", { name: "Выполнить: Отменить турнир" }).click();
  await expect(dialog).toBeVisible();
  const confirmCancellation = dialog.getByRole("button", { name: "Подтвердить отмену", exact: true });
  await confirmCancellation.focus();
  await confirmCancellation.press("Enter");
  await expect.poll(() => routes.actionRequests.length).toBe(1);
  expect(routes.actionRequests[0]?.body).toEqual({
    action: "cancel",
    confirmed: true,
    expected_projection_revision: 13,
    reason: "Турнир отменен решением главного судьи",
  });
  await expect(page.getByRole("region", { name: "Управление турниром" })).toContainText("Отменен");
  await expect(page.getByText("Команда подтверждена", { exact: true })).toBeVisible();
});

test("runtime controls remain usable in both themes at mobile width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await installRoutes(page, activeSnapshot(21));
  await openOperatorArena(page);

  for (const [label, theme] of [["Темная тема", "dark"], ["Светлая тема", "light"]] as const) {
    await page.getByRole("button", { name: label }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await expect(page.getByRole("region", { name: "Управление турниром" })).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  }
});
