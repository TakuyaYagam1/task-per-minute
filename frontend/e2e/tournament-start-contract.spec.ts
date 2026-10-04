import { expect, test, type Page, type Request, type Route } from "@playwright/test";

import type { OperatorRecoverySnapshot, Tournament } from "../lib/shared/api";
import { adminSessionResponse } from "./support/admin";
import { jsonHeaders } from "./support/common";
import { operatorSnapshot, tournamentFixtureIds } from "./tournament/fixtures";

const accessCSRF = "tournament-start-access-csrf";
const refreshCSRF = "tournament-start-refresh-csrf";
const tournamentID = tournamentFixtureIds.tournament;
const baseDate = "2026-09-13T10:00:00Z";

type StartState = "draft" | "registration" | "roster_locked" | "swiss";

type HarnessOptions = Readonly<{
  actionDetail?: string;
  actionStatus?: number;
  listItems: Tournament[];
  snapshotTournament?: Tournament;
  snapshotRevision?: number;
  waitForSnapshot?: boolean;
  waitForConflictRecovery?: boolean;
}>;

type ActionRecord = Readonly<{
  body: Record<string, unknown>;
  request: Request;
}>;

type StartHarness = Readonly<{
  actionRequests: ActionRecord[];
  releaseSnapshot: () => void;
  snapshotSettled: Promise<void>;
  snapshotRequests: Request[];
}>;

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

const tournament = (
  state: StartState,
  overrides: Partial<Tournament> = {},
): Tournament => ({
  content_revision: 4,
  created_at: baseDate,
  finished_at: null,
  id: tournamentID,
  name: "Стартовый турнир",
  paused_from_state: null,
  planned_roster_size: 4,
  preset: "tournament_v1",
  public_id: "start-contract",
  revision: 1,
  roster_id: tournamentFixtureIds.roster,
  roster_size: state === "draft" ? 0 : 4,
  started_at: state === "swiss" ? baseDate : null,
  state,
  updated_at: baseDate,
  ...overrides,
});

const contentSelection = () => ({
  content_revision: 4,
  golden_pool_revision_id: tournamentFixtureIds.pauseRevision,
  normal_pool_revision_id: tournamentFixtureIds.scoreRevision,
  publication_id: tournamentFixtureIds.snapshot,
  published_at: baseDate,
});

const snapshotFor = (
  value: Tournament,
  projectionRevision: number,
): OperatorRecoverySnapshot => {
  const snapshot = operatorSnapshot(projectionRevision);
  return {
    ...snapshot,
    next_cursor: {
      ...snapshot.next_cursor,
      authority_revision: projectionRevision,
      projection_revision: projectionRevision,
    },
    tournament: value,
  };
};

const bodyOf = (request: Request): Record<string, unknown> => {
  const body: unknown = request.postDataJSON();
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    throw new Error("Expected a JSON object request body");
  }
  return body as Record<string, unknown>;
};

const setupHarness = async (
  page: Page,
  options: HarnessOptions,
): Promise<StartHarness> => {
  const actionRequests: ActionRecord[] = [];
  const snapshotRequests: Request[] = [];
  let releaseSnapshot: () => void = () => undefined;
  let settleSnapshot: () => void = () => undefined;
  const snapshotGate = options.waitForSnapshot || options.waitForConflictRecovery
    ? new Promise<void>((resolve) => {
        releaseSnapshot = resolve;
      })
    : null;
  const snapshotSettled = new Promise<void>((resolve) => {
    settleSnapshot = resolve;
  });
  const listByID = new Map(options.listItems.map((item) => [item.id, item]));
  let serverTournament = options.snapshotTournament ?? options.listItems[0];
  if (!serverTournament) {
    throw new Error("Start contract requires one tournament");
  }

  await page.route("**/api/v1/admin/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;

    if (path === "/api/v1/admin/login" && request.method() === "POST") {
      await fulfillJSON(route, 200, adminSessionResponse(), {
        "X-CSRF-Token": accessCSRF,
        "X-Admin-Refresh-CSRF-Token": refreshCSRF,
      });
      return;
    }
    if (path === "/api/v1/admin/refresh" && request.method() === "POST") {
      await fulfillJSON(route, 200, adminSessionResponse(), {
        "X-CSRF-Token": accessCSRF,
        "X-Admin-Refresh-CSRF-Token": refreshCSRF,
      });
      return;
    }
    if (path === "/api/v1/admin/tasks" && request.method() === "GET") {
      await fulfillJSON(route, 200, []);
      return;
    }
    if (path === "/api/v1/admin/tournament-content" && request.method() === "GET") {
      await fulfillJSON(route, 200, contentSelection());
      return;
    }
    if (path === "/api/v1/admin/tournaments" && request.method() === "GET") {
      await fulfillJSON(route, 200, { items: options.listItems, next_cursor: null });
      return;
    }

    const snapshotMarker = "/api/v1/admin/tournaments/";
    if (path.endsWith("/snapshot") && path.startsWith(snapshotMarker) && request.method() === "GET") {
      snapshotRequests.push(request);
      if (snapshotGate && (options.waitForSnapshot || actionRequests.length > 0)) {
        await snapshotGate;
      }
      const tournamentIDFromPath = path.slice(snapshotMarker.length, -"/snapshot".length);
      const snapshotTournament =
        tournamentIDFromPath === serverTournament.id
          ? serverTournament
          : listByID.get(tournamentIDFromPath) ?? serverTournament;
      try {
        await fulfillJSON(
          route,
          200,
          snapshotFor(snapshotTournament, options.snapshotRevision ?? 9),
        );
      } catch {
        // The request is expected to be aborted in the navigation guard test.
      } finally {
        settleSnapshot();
      }
      return;
    }

    if (path.endsWith("/actions") && path.startsWith(snapshotMarker) && request.method() === "POST") {
      const body = bodyOf(request);
      actionRequests.push({ body, request });
      if (options.actionStatus && options.actionStatus !== 200) {
        await fulfillJSON(
          route,
          options.actionStatus,
          {
            detail: options.actionDetail ?? "Переход отклонен сервером",
            status: options.actionStatus,
            title: options.actionStatus === 409 ? "conflict" : "validation failed",
            type: "about:blank",
          },
          { "Content-Type": "application/problem+json" },
        );
        return;
      }
      const nextState = body.action === "open_registration" ? "registration" : "swiss";
      serverTournament = tournament(nextState, {
        id: serverTournament.id,
        name: serverTournament.name,
        revision: serverTournament.revision + 1,
      });
      await fulfillJSON(route, 200, serverTournament);
      return;
    }

    await fulfillJSON(route, 404, {});
  });

  return { actionRequests, releaseSnapshot, snapshotSettled, snapshotRequests };
};

const loginAndOpenTournament = async (page: Page): Promise<void> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("button", { name: "Соревнования" }).click();
  await page.getByRole("button", { name: "Открыть соревнование Стартовый турнир", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Стартовый турнир" })).toBeVisible();
  await expect(page.getByTestId("tournament-start-controls")).toBeVisible();
};

test("из черновика открывает регистрацию по свежей ревизии", async ({ page }) => {
  const initial = tournament("draft");
  const harness = await setupHarness(page, {
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 17,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  await expect(panel).toContainText("Откройте регистрацию");
  await panel.getByRole("button", { name: "Открыть регистрацию" }).click();
  await expect.poll(() => harness.actionRequests.length).toBe(1);
  expect(harness.snapshotRequests).toHaveLength(1);
  expect(harness.actionRequests[0]?.body).toEqual({
    action: "open_registration",
    confirmed: true,
    expected_projection_revision: 17,
    reason: "Оператор подтвердил открытие регистрации",
  });
  await expect(panel).toContainText("Регистрация открыта");
});

test("оставляет заметный отступ между подсказкой и кнопкой запуска", async ({ page }) => {
  const initial = tournament("draft");
  await setupHarness(page, {
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 18,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  const guidance = panel.getByRole("status").filter({ hasText: "Регистрация закрыта" });
  const action = panel.getByRole("button", { name: "Открыть регистрацию" });
  const guidanceBox = await guidance.boundingBox();
  const actionBox = await action.boundingBox();
  expect(guidanceBox).not.toBeNull();
  expect(actionBox).not.toBeNull();
  expect((actionBox?.y ?? 0) - ((guidanceBox?.y ?? 0) + (guidanceBox?.height ?? 0))).toBeGreaterThan(8);
});

test("из зафиксированного состава запускает квалификацию и предлагает открыть сетку", async ({ page }) => {
  const initial = tournament("roster_locked");
  const harness = await setupHarness(page, {
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 29,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  await panel.getByRole("button", { name: "Начать квалификацию" }).click();
  await expect.poll(() => harness.actionRequests.length).toBe(1);
  expect(harness.actionRequests[0]?.body).toEqual({
    action: "start_swiss",
    confirmed: true,
    expected_projection_revision: 29,
    reason: "Оператор подтвердил запуск квалификации",
  });
  await expect(panel).toContainText("Квалификация запущена");
  await panel.getByRole("button", { name: "Открыть сетку и серии" }).click();
  await expect(page).toHaveURL(new RegExp(`[?&]view=bracket(?:&|$)`));
});

test("не выполняет действие, если свежий снимок уже в другом состоянии", async ({ page }) => {
  const listed = tournament("draft");
  const serverState = tournament("registration");
  const harness = await setupHarness(page, {
    listItems: [listed],
    snapshotTournament: serverState,
    snapshotRevision: 31,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  await panel.getByRole("button", { name: "Открыть регистрацию" }).click();
  await expect(panel.getByRole("alert")).toContainText("Регистрация");
  expect(harness.actionRequests).toHaveLength(0);
});

test("двойной клик отправляет один снимок и одну команду", async ({ page }) => {
  const initial = tournament("draft");
  const harness = await setupHarness(page, {
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 37,
    waitForSnapshot: true,
  });
  await loginAndOpenTournament(page);

  const clickPromise = page
    .getByTestId("tournament-start-controls")
    .getByRole("button", { name: "Открыть регистрацию" })
    .click({ clickCount: 2 });
  await expect.poll(() => harness.snapshotRequests.length).toBe(1);
  harness.releaseSnapshot();
  await clickPromise;
  await expect.poll(() => harness.actionRequests.length).toBe(1);
  expect(harness.snapshotRequests).toHaveLength(1);
});

test("не применяет поздний снимок после ухода из турнира", async ({ page }) => {
  const initial = tournament("draft");
  const harness = await setupHarness(page, {
    listItems: [initial],
    snapshotTournament: initial,
    waitForSnapshot: true,
  });
  await loginAndOpenTournament(page);

  await page.getByTestId("tournament-start-controls").getByRole("button", { name: "Открыть регистрацию" }).click();
  await expect.poll(() => harness.snapshotRequests.length).toBe(1);
  await page.getByRole("button", { name: "К списку соревнований" }).click();
  await expect(page.getByRole("heading", { name: "Список соревнований" })).toBeVisible();
  harness.releaseSnapshot();
  await harness.snapshotSettled;
  expect(harness.actionRequests).toHaveLength(0);
});

test("409 показывает понятный конфликт и требует обновить данные перед повтором", async ({ page }) => {
  const initial = tournament("roster_locked");
  const harness = await setupHarness(page, {
    actionDetail: "projection revision conflict",
    actionStatus: 409,
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 41,
    waitForConflictRecovery: true,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  const actionButton = panel.getByRole("button", { name: "Начать квалификацию" });
  await actionButton.click();
  const alert = panel.getByRole("alert");
  await expect(alert).toContainText("Действие недоступно");
  await expect(alert).toContainText(
    "Не удалось выполнить переход: состояние соревнования изменилось.",
  );
  await expect(alert).not.toContainText("projection revision conflict");
  await expect(actionButton).toBeDisabled();

  await expect.poll(() => harness.snapshotRequests.length).toBe(2);
  harness.releaseSnapshot();
  await expect(actionButton).toBeEnabled();
  await expect(alert).toBeHidden();
  expect(harness.actionRequests).toHaveLength(1);
});

test("generic 409 показывает русское сообщение о конфликте ревизии", async ({ page }) => {
  const initial = tournament("draft");
  const harness = await setupHarness(page, {
    actionDetail: "projection revision conflict",
    actionStatus: 409,
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 42,
    waitForConflictRecovery: true,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  const actionButton = panel.getByRole("button", { name: "Открыть регистрацию" });
  await actionButton.click();
  const alert = panel.getByRole("alert");
  await expect(alert).toContainText("Действие недоступно");
  await expect(alert).toContainText(
    "Не удалось выполнить переход: состояние соревнования изменилось.",
  );
  await expect(alert).not.toContainText("projection revision conflict");
  await expect(actionButton).toBeDisabled();
  expect(harness.actionRequests).toHaveLength(1);
  harness.releaseSnapshot();
  await expect(actionButton).toBeEnabled();
  await expect(alert).toBeHidden();
});

test("422 показывает серверную причину и оставляет переход доступным для повтора", async ({ page }) => {
  const initial = tournament("roster_locked");
  const detail = "Состав не готов к запуску квалификации";
  const harness = await setupHarness(page, {
    actionDetail: detail,
    actionStatus: 422,
    listItems: [initial],
    snapshotTournament: initial,
    snapshotRevision: 43,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  const actionButton = panel.getByRole("button", { name: "Начать квалификацию" });
  await actionButton.click();
  await expect(panel.getByRole("alert")).toContainText(detail);
  await expect(actionButton).toBeEnabled();

  await actionButton.click();
  await expect.poll(() => harness.actionRequests.length).toBe(2);
});

test("в состоянии регистрации предлагает подтвердить состав без кнопки запуска", async ({ page }) => {
  const initial = tournament("registration");
  await setupHarness(page, {
    listItems: [initial],
    snapshotTournament: initial,
  });
  await loginAndOpenTournament(page);

  const panel = page.getByTestId("tournament-start-controls");
  await expect(panel).toContainText("Подтвердите состав");
  await expect(panel.getByRole("button", { name: "Начать квалификацию" })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Открыть регистрацию" })).toHaveCount(0);
});
