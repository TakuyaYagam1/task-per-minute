import { expect, test, type Page, type Request, type Route } from "@playwright/test";

import type { OperatorRecoverySnapshot } from "../lib/shared/api";
import type { components } from "../lib/shared/api/schema";
import { isOperatorRecoverySnapshot } from "../lib/shared/api/guards";
import {
  operatorSnapshot,
  publicTournament,
  tournamentFixtureIds,
} from "./tournament/fixtures";

type Schema = components["schemas"];

const tournamentId = tournamentFixtureIds.tournament;
const snapshotPath = `/api/v1/admin/tournaments/${tournamentId}/snapshot`;
const publicTournamentPath = `/api/v1/tournaments/${tournamentId}`;
const replayPath = `/api/v1/admin/tournaments/${tournamentId}/series/${tournamentFixtureIds.bo3Series}/games/${tournamentFixtureIds.bo3GameTwo}/replays`;
const reservePath = `/api/v1/admin/tournaments/${tournamentId}/series/${tournamentFixtureIds.bo1Series}/assignments/${tournamentFixtureIds.assignment}/operator-reserves`;
const jsonHeaders = { "content-type": "application/json" };
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const fulfillJSON = async (
  route: Route,
  body: unknown,
  status = 200,
): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify(body),
    headers: jsonHeaders,
    status,
  });
};

const fulfillProblem = async (route: Route, status: number): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify({ detail: "Снимок устарел", status, title: "conflict", type: "about:blank" }),
    headers: { "content-type": "application/problem+json" },
    status,
  });
};

const requestBody = (request: Request): Record<string, unknown> => {
  const body: unknown = request.postDataJSON();
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    throw new Error("Expected an object request body");
  }
  return body as Record<string, unknown>;
};

const failedAttempt = (
  snapshot: OperatorRecoverySnapshot,
  seriesId: string,
  slotId: string,
  reason: Schema["GameResultReason"],
): Schema["Game"] => {
  const series = snapshot.series.find((candidate) => candidate.id === seriesId);
  const slot = series?.slots.find((candidate) => candidate.id === slotId);
  const attempt = slot?.attempts.at(-1);
  if (attempt === undefined) {
    throw new Error("Recovery fixture is missing the failed attempt");
  }
  return {
    ...attempt,
    result_reason: reason,
    result_revision_id: tournamentFixtureIds.scoreRevision,
    state: "void",
  };
};

const replaySnapshot = (revision = 9, available = true): OperatorRecoverySnapshot => {
  const base = operatorSnapshot(revision);
  return {
    ...base,
    recovery_controls: [{
      assignment_id: tournamentFixtureIds.assignment,
      attempts: [failedAttempt(base, tournamentFixtureIds.bo3Series, tournamentFixtureIds.bo3SlotTwo, "no_solve")],
      category: "forensics",
      expected_authority_revision: revision,
      kind: "replay",
      old_wave_id: tournamentFixtureIds.bo3Wave,
      pause_reason: "platform",
      reason: "Сервер зафиксировал no-solve после сбоя платформы",
      replay: {
        available,
        expected_closure_revision_id: tournamentFixtureIds.pauseRevision,
      },
      reserve_exhausted: null,
      series_id: tournamentFixtureIds.bo3Series,
      slot_id: tournamentFixtureIds.bo3SlotTwo,
    }],
  };
};

const reserveSnapshot = (revision = 9, candidates: Schema["OperatorRecoveryReserveCandidate"][] = []): OperatorRecoverySnapshot => {
  const base = operatorSnapshot(revision);
  return {
    ...base,
    recovery_controls: [{
      assignment_id: tournamentFixtureIds.assignment,
      attempts: [failedAttempt(base, tournamentFixtureIds.bo1Series, tournamentFixtureIds.bo1Slot, "task_failure")],
      category: "crypto",
      expected_authority_revision: revision,
      kind: "reserve_exhausted",
      old_wave_id: tournamentFixtureIds.bo1Wave,
      pause_reason: "operator",
      reason: "Серверный резерв исчерпан",
      replay: null,
      reserve_exhausted: {
        candidates,
        current_snapshot_id: tournamentFixtureIds.snapshot,
        expected_artifact_revision: 2,
        expected_artifact_revision_id: tournamentFixtureIds.scoreRevision,
        expected_assignment_revision: 3,
        expected_category_revision: 4,
        expected_category_revision_id: tournamentFixtureIds.draftEvidence,
        expected_exhaustion_command_id: tournamentFixtureIds.draftCommand,
        expected_history_revision: 5,
        expected_history_revision_id: tournamentFixtureIds.event,
        expected_pool_revision: 6,
        expected_pool_revision_id: tournamentFixtureIds.groupRevision,
        expected_reservation_revision: 7,
        expected_reservation_revision_id: tournamentFixtureIds.pauseRevision,
        expected_snapshot_id: tournamentFixtureIds.snapshot,
      },
      series_id: tournamentFixtureIds.bo1Series,
      slot_id: tournamentFixtureIds.bo1Slot,
    }],
  };
};

test("runtime guard rejects duplicate recovery control identities", () => {
  const snapshot = replaySnapshot();
  const control = snapshot.recovery_controls[0];
  if (control === undefined) {
    throw new Error("Recovery fixture is missing a control");
  }
  const duplicateSnapshot: OperatorRecoverySnapshot = {
    ...snapshot,
    recovery_controls: [control, { ...control, reason: "duplicate server control" }],
  };
  expect(isOperatorRecoverySnapshot(duplicateSnapshot)).toBe(false);
});

type RecoveryRoutes = Readonly<{
  replayRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }>;
  reserveRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }>;
  setConflict: (next: boolean) => void;
}>;

const installRoutes = async (
  page: Page,
  initialSnapshot: OperatorRecoverySnapshot,
): Promise<RecoveryRoutes> => {
  let currentSnapshot = initialSnapshot;
  let conflict = false;
  const replayRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];
  const reserveRequests: Array<{ body: Record<string, unknown>; headers: Record<string, string> }> = [];

  await page.route(`**${publicTournamentPath}`, async (route) => {
    await fulfillJSON(route, publicTournament(currentSnapshot.next_cursor.projection_revision));
  });
  await page.route(`**${snapshotPath}*`, async (route) => {
    await fulfillJSON(route, currentSnapshot);
  });
  await page.route(`**${replayPath}`, async (route) => {
    replayRequests.push({ body: requestBody(route.request()), headers: route.request().headers() });
    if (conflict) {
      conflict = false;
      currentSnapshot = { ...currentSnapshot, recovery_controls: [] };
      await fulfillProblem(route, 409);
      return;
    }
    currentSnapshot = { ...currentSnapshot, recovery_controls: [] };
    await route.fulfill({ status: 204 });
  });
  await page.route(`**${reservePath}`, async (route) => {
    reserveRequests.push({ body: requestBody(route.request()), headers: route.request().headers() });
    currentSnapshot = { ...currentSnapshot, recovery_controls: [] };
    await route.fulfill({ status: 204 });
  });

  return {
    replayRequests,
    reserveRequests,
    setConflict: (next) => { conflict = next; },
  };
};

const openOperator = async (page: Page): Promise<void> => {
  await page.goto(`/arena/operator/${tournamentId}`, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Управление соревнованием" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Восстановление игр" })).toBeVisible();
  await page.evaluate(() => {
    document.cookie = "tpm_admin_access_csrf=recovery-contract-csrf; Path=/; SameSite=Lax";
  });
};

const confirmRecovery = async (page: Page, reason: string): Promise<void> => {
  await page.getByLabel("Причина восстановления").fill(reason);
  await page.getByLabel("Я проверил причину остановки и подтверждаю выбранное действие.").check();
};

test("replay sends exact server evidence with fresh identities and never offers ordinary draw replay", async ({ page }) => {
  const routes = await installRoutes(page, replaySnapshot());
  await openOperator(page);

  await expect(page.getByText("Никто не решил задачу")).toBeVisible();
  await expect(page.getByText("Сбой платформы")).toBeVisible();
  await expect(page.getByText("Forensics", { exact: true })).toBeVisible();
  await expect(page.getByRole("radio", {
    name: "Переиграть Forensics - Участник 1 / Участник 2, игра 2",
    exact: true,
  })).toBeChecked();
  await confirmRecovery(page, "Повтор после подтвержденного no-solve");
  await page.getByRole("button", { name: "Повторить игру" }).click();

  expect(routes.replayRequests).toHaveLength(1);
  const { body, headers } = routes.replayRequests[0];
  expect(headers["idempotency-key"]).toMatch(uuidPattern);
  expect(headers["x-csrf-token"]).toBe("recovery-contract-csrf");
  expect(body).toMatchObject({
    assignment_id: tournamentFixtureIds.assignment,
    confirmed: true,
    expected_authority_revision: 9,
    expected_closure_revision_id: tournamentFixtureIds.pauseRevision,
    failed_game_id: tournamentFixtureIds.bo3GameTwo,
    old_wave_id: tournamentFixtureIds.bo3Wave,
    reason: "Повтор после подтвержденного no-solve",
    series_id: tournamentFixtureIds.bo3Series,
    slot_id: tournamentFixtureIds.bo3SlotTwo,
    tournament_id: tournamentId,
  });
  for (const key of [
    "assignment_attempt_id",
    "replacement_game_id",
    "replacement_wave_id",
    "replacement_wave_revision_id",
    "ready_window_id",
    "ready_window_revision_id",
  ]) {
    expect(body[key]).toMatch(uuidPattern);
    expect(body[key]).not.toBe(tournamentFixtureIds.bo3GameTwo);
  }
  await expect(page.getByRole("button", { name: "Повторить игру" })).toHaveCount(0);

  await page.unroute(`**${snapshotPath}*`);
  await page.route(`**${snapshotPath}*`, async (route) => {
    await fulfillJSON(route, operatorSnapshot());
  });
  await expect(page.getByText("Сейчас нет игр, которым нужна переигровка или замена задачи.")).toBeVisible();
});

test("reserve assignment uses only server candidates and preserves source revisions", async ({ page }) => {
  const candidate: Schema["OperatorRecoveryReserveCandidate"] = {
    task_id: "00000000-0000-4000-8000-000000000201",
    version: 3,
  };
  const routes = await installRoutes(page, reserveSnapshot(9, [candidate]));
  await openOperator(page);
  await expect(page.getByLabel("Задача для замены")).toHaveValue(
    `${candidate.task_id}:${candidate.version}`,
  );
  await expect(page.getByText("Категория: Crypto.")).toBeVisible();
  await confirmRecovery(page, "Назначение из серверного резерва");
  await page.getByRole("button", { name: "Назначить резерв" }).click();

  expect(routes.reserveRequests).toHaveLength(1);
  const { body } = routes.reserveRequests[0];
  expect(body).toMatchObject({
    assignment_id: tournamentFixtureIds.assignment,
    confirmed: true,
    expected_artifact_revision: 2,
    expected_artifact_revision_id: tournamentFixtureIds.scoreRevision,
    expected_assignment_revision: 3,
    expected_authority_revision: 9,
    expected_category_revision: 4,
    expected_category_revision_id: tournamentFixtureIds.draftEvidence,
    expected_exhaustion_command_id: tournamentFixtureIds.draftCommand,
    expected_history_revision: 5,
    expected_history_revision_id: tournamentFixtureIds.event,
    expected_pool_revision: 6,
    expected_pool_revision_id: tournamentFixtureIds.groupRevision,
    expected_reservation_revision: 7,
    expected_reservation_revision_id: tournamentFixtureIds.pauseRevision,
    expected_snapshot_id: tournamentFixtureIds.snapshot,
    old_wave_id: tournamentFixtureIds.bo1Wave,
    proposed_task_id: candidate.task_id,
    proposed_version: candidate.version,
    reason: "Назначение из серверного резерва",
    series_id: tournamentFixtureIds.bo1Series,
    slot_id: tournamentFixtureIds.bo1Slot,
    tournament_id: tournamentId,
  });
  expect(body).not.toHaveProperty("category");
  expect(body.assignment_attempt_id).toBe(tournamentFixtureIds.bo1Game);
  expect(body.evidence_id).toMatch(uuidPattern);
  expect(body.proposed_snapshot_id).toMatch(uuidPattern);
  for (const key of ["evidence_id", "proposed_snapshot_id"]) {
    expect(body[key]).not.toBe(tournamentFixtureIds.assignment);
    expect(body[key]).not.toBe(tournamentFixtureIds.snapshot);
    expect(body[key]).not.toBe(tournamentFixtureIds.bo1Game);
    expect(body[key]).not.toBe(candidate.task_id);
  }
});

test("an exhausted reserve with no candidates stays disabled", async ({ page }) => {
  await installRoutes(page, reserveSnapshot(9, []));
  await openOperator(page);
  await expect(page.getByText("Подходящих кандидатов нет. Задачу и категорию нельзя указать вручную.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Назначить резерв" })).toBeDisabled();
});

test("stale recovery refreshes the snapshot without an optimistic restart and works on mobile themes", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const routes = await installRoutes(page, replaySnapshot());
  routes.setConflict(true);
  await openOperator(page);
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await confirmRecovery(page, "Проверка stale снимка");
  const button = page.getByRole("button", { name: "Повторить игру" });
  await button.click();
  await expect(page.getByText("Другой оператор изменил восстановление.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Повторить игру" })).toHaveCount(0);
  expect(routes.replayRequests).toHaveLength(1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  await page.getByRole("button", { name: "Темная тема" }).focus();
  await page.keyboard.press("Tab");
  await expect(page.locator(":focus-visible")).toHaveCount(1);
});
