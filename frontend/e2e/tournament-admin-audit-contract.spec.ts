import { readFile } from "node:fs/promises";

import { expect, test, type Page, type Route } from "@playwright/test";

import { isCancellationAuditEvent } from "../lib/shared/api/guards";
import { jsonHeaders } from "./support/common";

const tournamentId = "10000000-0000-4000-8000-000000000010";
const rosterId = "10000000-0000-4000-8000-000000000011";
const seriesId = "10000000-0000-4000-8000-000000000012";
const actorId = "10000000-0000-4000-8000-000000000013";
const oldRevisionId = "10000000-0000-4000-8000-000000000014";
const currentRevisionId = "10000000-0000-4000-8000-000000000015";
const oldEventId = "10000000-0000-4000-8000-000000000016";
const currentEventId = "10000000-0000-4000-8000-000000000017";
const replayEventId = "10000000-0000-4000-8000-000000000018";
const correctionEventId = "10000000-0000-4000-8000-000000000019";
const resultEventId = "10000000-0000-4000-8000-000000000020";
const winnerId = "10000000-0000-4000-8000-000000000021";
const baseTime = "2026-09-13T10:00:00Z";
const secretText = "closed-incident-evidence-must-not-render";

type IncidentMode = "allowed" | "anonymous" | "player";

const tournament = {
  content_revision: 42,
  created_at: baseTime,
  finished_at: "2026-09-13T11:00:00Z",
  id: tournamentId,
  name: "Аудит Кубка",
  paused_from_state: null,
  planned_roster_size: 4,
  preset: "tournament_v1",
  public_id: "audit-cup",
  revision: 8,
  roster_id: rosterId,
  roster_size: 4,
  started_at: "2026-09-13T10:10:00Z",
  state: "completed",
  updated_at: "2026-09-13T11:00:00Z",
};

const contentSelection = {
  content_revision: 42,
  publication_id: "10000000-0000-4000-8000-000000000022",
  published_at: baseTime,
  normal_pool_revision_id: "10000000-0000-4000-8000-000000000023",
  golden_pool_revision_id: "10000000-0000-4000-8000-000000000024",
};

type AuditEventOverrides = Partial<{
    audit_event_id: string;
    event_type: string;
    is_current: boolean;
    is_superseded: boolean;
    official_result_revision_id: string;
    previous_revision_id: string;
    result_reason: string;
    revision_number: number;
  }>;

const auditEvent = ({
  previous_revision_id: previousRevisionId,
  ...overrides
}: AuditEventOverrides = {}) => ({
  actor_id: actorId,
  actor_kind: "operator",
  audit_event_id: currentEventId,
  created_at: baseTime,
  entity_id: seriesId,
  entity_kind: "series",
  event_type: "result.recorded",
  is_current: true,
  is_superseded: false,
  occurred_at: baseTime,
  official_result_revision_id: currentRevisionId,
  result_event_id: resultEventId,
  result_reason: "score_complete",
  result_state: "completed",
  revision_number: 2,
  roster_id: rosterId,
  series_id: seriesId,
  tournament_id: tournamentId,
  winner_id: winnerId,
  ...overrides,
  redacted_payload: {
    entity_id: seriesId,
    ...(previousRevisionId
      ? { previous_revision_id: previousRevisionId }
      : {}),
    result_reason: overrides.result_reason ?? "score_complete",
    revision_number: overrides.revision_number ?? 2,
  },
});

const oldResult = auditEvent({
  audit_event_id: oldEventId,
  is_current: false,
  is_superseded: true,
  official_result_revision_id: oldRevisionId,
  revision_number: 1,
});

const currentResult = auditEvent({
  previous_revision_id: oldRevisionId,
});

const replayEvent = auditEvent({
  audit_event_id: replayEventId,
  event_type: "replay.requested",
  official_result_revision_id: currentRevisionId,
  previous_revision_id: oldRevisionId,
  result_reason: "operator_replay",
  revision_number: 3,
});

const correctionEvent = auditEvent({
  audit_event_id: correctionEventId,
  event_type: "correction.committed",
  official_result_revision_id: currentRevisionId,
  previous_revision_id: oldRevisionId,
  result_reason: "operator_correction",
  revision_number: 4,
});

const nextCursor = {
  audit_event_id: currentEventId,
  occurred_at: baseTime,
  revision_id: currentRevisionId,
  snapshot_bound: "audit-snapshot-42",
};

const incidentBundle = {
  algorithm: "hmac-sha256-v1",
  canonical_content: Buffer.from(JSON.stringify({ evidence: secretText })).toString("base64"),
  canonical_content_encoding: "base64",
  canonical_content_length: Buffer.byteLength(JSON.stringify({ evidence: secretText })),
  canonical_content_type: "application/json",
  generated_at: "2026-09-13T11:05:00Z",
  key_id: "audit-contract",
  mac: "cd".repeat(32),
  projection_revision: 8,
  sha256: "ab".repeat(32),
  tournament_id: tournamentId,
};

const problem = (status: number, detail: string) => ({
  type: "about:blank",
  title: status === 401 ? "Unauthorized" : "Forbidden",
  status,
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

const installAdminRoutes = async (
  page: Page,
  auditQueries: URLSearchParams[],
  getIncidentMode: () => IncidentMode = () => "allowed",
): Promise<void> => {
  await page.route("**/api/v1/admin/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/admin/login") {
      await fulfillJSON(route, 200, { expires_in: 900 }, {
        "X-CSRF-Token": "audit-access-csrf",
        "X-Admin-Refresh-CSRF-Token": "audit-refresh-csrf",
      });
      return;
    }
    if (url.pathname === "/api/v1/admin/tasks") {
      await fulfillJSON(route, 200, []);
      return;
    }
    if (url.pathname === "/api/v1/admin/tournament-content") {
      await fulfillJSON(route, 200, contentSelection);
      return;
    }
    if (url.pathname === "/api/v1/admin/tournaments") {
      await fulfillJSON(route, 200, { items: [tournament], next_cursor: null });
      return;
    }
    if (url.pathname === "/api/v1/admin/tournament-audit") {
      auditQueries.push(url.searchParams);
      const eventFilter = url.searchParams.get("event_type");
      if (eventFilter === "replay.requested") {
        await fulfillJSON(route, 200, { events: [replayEvent], next_cursor: null });
        return;
      }
      if (eventFilter === "correction.committed") {
        await fulfillJSON(route, 200, { events: [correctionEvent], next_cursor: null });
        return;
      }
      if (eventFilter === "result.recorded") {
        await fulfillJSON(route, 200, { events: [oldResult, currentResult], next_cursor: null });
        return;
      }
      if (url.searchParams.has("cursor[audit_event_id]")) {
        await fulfillJSON(route, 200, { events: [correctionEvent], next_cursor: null });
        return;
      }
      await fulfillJSON(route, 200, {
        events: [oldResult, currentResult, replayEvent],
        next_cursor: nextCursor,
      });
      return;
    }
    if (url.pathname === `/api/v1/admin/tournaments/${tournamentId}/incident-export`) {
      const mode = getIncidentMode();
      if (mode === "anonymous") {
        await fulfillJSON(route, 401, problem(401, "Требуется операторская сессия"));
        return;
      }
      if (mode === "player") {
        await fulfillJSON(route, 403, problem(403, "Игрок не может экспортировать инцидент"));
        return;
      }
      await fulfillJSON(route, 200, incidentBundle);
      return;
    }
    await fulfillJSON(route, 404, problem(404, "Маршрут не найден"));
  });
};

const loginAndOpenAudit = async (page: Page): Promise<void> => {
  await page.goto("/admin");
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();
  await page.getByRole("button", { name: "Журнал" }).click();
  await expect(page.getByRole("heading", { name: "Журнал соревнования" })).toBeVisible();
  await page.locator("#tournament-journal-select").selectOption(tournamentId);
  await expect(page.getByRole("heading", { name: "История соревнования" })).toBeVisible();
  await expect(page.getByText("Страница 1, событий: 3")).toBeVisible();
};

test("FE-037 filters result, replay and correction events and walks two server pages", async ({ page }) => {
  const auditQueries: URLSearchParams[] = [];
  await installAdminRoutes(page, auditQueries);
  await loginAndOpenAudit(page);

  await expect(page.getByText("Исправлено")).toBeVisible();
  await expect(page.getByText("Действует")).toHaveCount(2);
  await expect(page.getByText(tournament.name, { exact: true }).first()).toBeVisible();

  await page.getByRole("button", { name: "Следующая страница" }).click();
  await expect(page.getByText("Страница 2, событий: 1")).toBeVisible();
  await expect(page.getByText("Другое событие", { exact: true })).toBeVisible();
  const cursorQuery = auditQueries.at(-1);
  expect(cursorQuery?.get("cursor[audit_event_id]")).toBe(currentEventId);
  expect(cursorQuery?.get("cursor[revision_id]")).toBe(currentRevisionId);
  expect(cursorQuery?.get("cursor[snapshot_bound]")).toBe("audit-snapshot-42");

  await page.getByRole("button", { name: "Предыдущая страница" }).click();
  await expect(page.getByText("Страница 1, событий: 3")).toBeVisible();

  const technicalFilters = page.locator("details").filter({ hasText: "Технические фильтры" }).first();
  await technicalFilters.locator("summary").click();
  const eventCode = page.getByLabel("Код события");
  for (const [filter, visibleEvent] of [
    ["replay.requested", "replay.requested"],
    ["correction.committed", "correction.committed"],
    ["result.recorded", "result.recorded"],
  ] as const) {
    await eventCode.fill(filter);
    await page.getByRole("button", { name: "Применить фильтры" }).click();
    await expect(page.getByText("Другое событие", { exact: true }).first()).toBeVisible();
    expect(auditQueries.at(-1)?.get("event_type")).toBe(visibleEvent);
  }

  await page.getByLabel("Что изменилось").selectOption("series");
  await page.getByLabel("ID записи").fill(seriesId);
  await page.getByLabel("Кто выполнил", { exact: true }).selectOption("operator");
  await page.getByLabel("ID администратора").fill(actorId);
  await page.getByLabel("Причина результата").selectOption("score_complete");
  await page.getByLabel("С даты, ваше время").fill("2026-09-13T09:00");
  await page.getByLabel("По дату, ваше время").fill("2026-09-13T12:00");
  await page.getByRole("button", { name: "Применить фильтры" }).click();

  const fullQuery = auditQueries.at(-1);
  expect(fullQuery?.get("tournament_id")).toBe(tournamentId);
  expect(fullQuery?.get("entity_kind")).toBe("series");
  expect(fullQuery?.get("entity_id")).toBe(seriesId);
  expect(fullQuery?.get("event_type")).toBe("result.recorded");
  expect(fullQuery?.get("actor_kind")).toBe("operator");
  expect(fullQuery?.get("actor_id")).toBe(actorId);
  expect(fullQuery?.get("result_reason")).toBe("score_complete");
  expect(fullQuery?.get("occurred_from")).toBeTruthy();
  expect(fullQuery?.get("occurred_to")).toBeTruthy();
  expect(fullQuery?.get("page_size")).toBe("25");
});

test("сохраняет недоступный ID турнира в журнале и показывает явное состояние", async ({ page }) => {
  const unavailableTournamentId = "10000000-0000-4000-8000-000000000099";
  const auditQueries: URLSearchParams[] = [];
  await installAdminRoutes(page, auditQueries);

  await page.goto(`/admin?section=audit&tournament=${unavailableTournamentId}&view=audit`);
  await page.getByPlaceholder("Введите пароль...").fill("correct-password");
  await page.getByRole("button", { name: "Войти" }).click();

  await expect(page.getByRole("heading", { name: "Журнал соревнования" })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`[?&]tournament=${unavailableTournamentId}(?:&|$)`));
  await expect(page.getByRole("alert").filter({ hasText: "Соревнование недоступно" })).toBeVisible();
});

test("FE-037 downloads the permitted envelope without exposing closed content", async ({ page }) => {
  const auditQueries: URLSearchParams[] = [];
  const consoleMessages: string[] = [];
  page.on("console", (message) => consoleMessages.push(message.text()));
  await installAdminRoutes(page, auditQueries);
  await loginAndOpenAudit(page);

  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Скачать отчет" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe(`Отчет - ${tournament.name} - ${incidentBundle.generated_at.slice(0, 10)}.json`);
  expect(download.suggestedFilename()).not.toContain(tournamentId);
  const downloadPath = await download.path();
  expect(downloadPath).toBeTruthy();
  const savedBundle = JSON.parse(await readFile(downloadPath ?? "", "utf8")) as Record<string, unknown>;
  expect(savedBundle.tournament_id).toBe(tournamentId);
  expect(savedBundle.projection_revision).toBe(8);
  expect(savedBundle.generated_at).toBe("2026-09-13T11:05:00Z");
  expect(savedBundle.sha256).toBe("ab".repeat(32));
  expect(savedBundle.canonical_content).toBe(incidentBundle.canonical_content);

  await expect(page.getByText(tournament.name, { exact: true }).last()).toBeVisible();
  const bundleTechnicalDetails = page.locator("details").filter({ hasText: "Версия данных" }).first();
  await bundleTechnicalDetails.locator("summary").click();
  await expect(bundleTechnicalDetails).toContainText("Версия данных: 8");
  await expect(page.getByText(secretText, { exact: false })).toHaveCount(0);
  expect(page.url()).not.toContain(secretText);
  expect(consoleMessages.join("\n")).not.toContain(secretText);
  const storage = await page.evaluate(() => ({
    local: JSON.stringify(window.localStorage),
    session: JSON.stringify(window.sessionStorage),
  }));
  expect(storage.local).not.toContain(secretText);
  expect(storage.session).not.toContain(secretText);
});

test("FE-037 shows safe refusals for player and anonymous export attempts", async ({ page }) => {
  const auditQueries: URLSearchParams[] = [];
  let mode: IncidentMode = "player";
  await installAdminRoutes(page, auditQueries, () => mode);
  await loginAndOpenAudit(page);

  await page.getByRole("button", { name: "Скачать отчет" }).click();
  await expect(page.getByText("У этой сессии нет доступа к операторскому аудиту.")).toBeVisible();

  mode = "anonymous";
  const anonymousRefusal = page.waitForResponse(
    (response) => new URL(response.url()).pathname.endsWith("/incident-export"),
  );
  await page.getByRole("button", { name: "Скачать отчет" }).click();
  expect((await anonymousRefusal).status()).toBe(401);
  await expect(page.getByPlaceholder("Введите пароль...")).toBeVisible();
  await expect(page.getByText(secretText, { exact: false })).toHaveCount(0);
});

test("FE-037 remains usable in both themes at mobile width", async ({ page }) => {
  const auditQueries: URLSearchParams[] = [];
  await page.setViewportSize({ width: 390, height: 844 });
  await installAdminRoutes(page, auditQueries);
  await loginAndOpenAudit(page);

  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator("#tournament-journal-select")).toHaveValue(tournamentId);
  await expect(page.getByRole("button", { name: "Скачать отчет" })).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await page.getByRole("button", { name: "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("FE-050 rejects cancellation audit reasons with unsafe whitespace or length", () => {
  const validCancellation = {
    actor_id: actorId,
    audit_event_id: currentEventId,
    command_id: correctionEventId,
    reason: "operator cancellation",
    resulting_revision: 9,
    roster_id: rosterId,
    source_projection_revision: 8,
    source_projection_revision_id: currentRevisionId,
    source_revision: 8,
    tournament_id: tournamentId,
  };

  expect(isCancellationAuditEvent(validCancellation)).toBe(true);
  expect(isCancellationAuditEvent({ ...validCancellation, reason: " operator cancellation" })).toBe(false);
  expect(isCancellationAuditEvent({ ...validCancellation, reason: "operator cancellation " })).toBe(false);
  expect(isCancellationAuditEvent({ ...validCancellation, reason: "x".repeat(513) })).toBe(false);
});
