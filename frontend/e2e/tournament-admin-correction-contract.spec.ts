import { expect, test, type Page, type Request, type Route } from "@playwright/test";

import type { OperatorRecoverySnapshot } from "../lib/shared/api";
import type { components } from "../lib/shared/api/schema";
import {
  operatorSnapshot,
  publicTournament,
  tournamentFixtureIds,
} from "./tournament/fixtures";

type Schema = components["schemas"];

const tournamentId = tournamentFixtureIds.tournament;
const seriesId = tournamentFixtureIds.bo3Series;
const gameId = tournamentFixtureIds.bo3GameOne;
const snapshotPath = `/api/v1/admin/tournaments/${tournamentId}/snapshot`;
const publicTournamentPath = `/api/v1/tournaments/${tournamentId}`;
const correctionPath = `/api/v1/admin/tournaments/${tournamentId}/series/${seriesId}/games/${gameId}/corrections`;
const preflightPath = `${correctionPath}/preflight`;
const jsonHeaders = { "content-type": "application/json" };
const digest = "1".repeat(64);
const projectionRevisionId = "00000000-0000-4000-8000-000000000195";

const fulfillJSON = async (route: Route, body: unknown, status = 200): Promise<void> => {
  await route.fulfill({ body: JSON.stringify(body), headers: jsonHeaders, status });
};

const fulfillProblem = async (route: Route, rejectionCode: string): Promise<void> => {
  await route.fulfill({
    body: JSON.stringify({
      detail: "Correction rejected",
      code: rejectionCode,
      status: 409,
      title: "Conflict",
      type: "about:blank",
    }),
    headers: { "content-type": "application/problem+json" },
    status: 409,
  });
};

const requestBody = (request: Request): Record<string, unknown> => {
  const value: unknown = request.postDataJSON();
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Expected a JSON object request body");
  }
  return value as Record<string, unknown>;
};

const projectionIntent = (): Schema["CorrectionProjectionIntent"] => ({
  decision_id: tournamentFixtureIds.draftCommand,
  expected_revision: {
    artifact_id: tournamentFixtureIds.bo3Series,
    artifact_kind: "standings",
    created_at: "2026-09-13T09:00:00Z",
    id: tournamentFixtureIds.groupRevision,
    payload_digest: digest,
    previous_revision_id: null,
    revision_no: 4,
    tournament_id: tournamentId,
  },
  next_revision_id: projectionRevisionId,
  payload_digest: digest,
});

const unlockIntent = (): Schema["CorrectionUnlockIntent"] => ({
  binding_digest: digest,
  evidence_digest: digest,
  expected_disclosed: false,
  expected_revision: 3,
  expected_used: false,
  owner_id: seriesId,
  reservation_id: tournamentFixtureIds.assignment,
  source_revision_id: tournamentFixtureIds.groupRevision,
  tournament_id: tournamentId,
});

const preparedRequest = (): Schema["OperatorCorrectionRequest"] => ({
  confirmed: true,
  expected_projection_revision: 9,
  explanation: "Исправление подтверждено протоколом судьи",
  fields: ["winner", "result_reason", "solve_metadata"],
  patch: {
    reason: "operator_forfeit",
    solve_metadata: {
      evidence_digest: "0".repeat(64),
      solved_at: null,
      submission_id: null,
    },
    state: "completed",
    winner_id: tournamentFixtureIds.secondParticipant,
  },
  projection_intents: [projectionIntent()],
  reason: "scorekeeping_error",
  source_result_revision: tournamentFixtureIds.scoreRevision,
  unlock_intents: [unlockIntent()],
});

const correctionEvidence = (): Schema["CorrectionEvidence"] => ({
  command_id: tournamentFixtureIds.draftCommand,
  fields: ["winner", "result_reason", "solve_metadata"],
  game_id: gameId,
  operator_id: tournamentFixtureIds.firstParticipant,
  reason: "scorekeeping_error",
  requested_at: "2026-09-13T10:00:00Z",
  series_id: seriesId,
  supersessions: [{
    artifact_id: tournamentFixtureIds.bo3Series,
    artifact_kind: "standings",
    previous_decision_id: null,
    previous_revision_id: tournamentFixtureIds.groupRevision,
    replacement_decision_id: tournamentFixtureIds.draftCommand,
    successor_revision_id: projectionRevisionId,
  }],
  tournament_id: tournamentId,
  unlock_intents: [unlockIntent()],
  validation_digest: digest,
});

const acceptedSnapshot = (): OperatorRecoverySnapshot => {
  const snapshot = operatorSnapshot(10);
  return {
    ...snapshot,
    series: snapshot.series.map((series) => series.id !== seriesId
      ? series
      : {
          ...series,
          score: { first_participant_wins: 0, second_participant_wins: 1 },
          slots: series.slots.map((slot) => ({
            ...slot,
            attempts: slot.attempts.map((game) => game.id !== gameId
              ? game
              : {
                  ...game,
                  result_reason: "operator_forfeit",
                  result_revision_id: projectionRevisionId,
                  winner_id: tournamentFixtureIds.secondParticipant,
                }),
          })),
        }),
  };
};

const terminalSnapshot = (): OperatorRecoverySnapshot => {
  const snapshot = operatorSnapshot(10);
  return {
    ...snapshot,
    tournament: {
      ...snapshot.tournament,
      finished_at: "2026-09-13T10:10:00Z",
      state: "completed",
    },
  };
};

const installBaseRoutes = async (
  page: Page,
  readSnapshot: () => OperatorRecoverySnapshot,
): Promise<void> => {
  await page.route(`**${publicTournamentPath}`, async (route) => {
    const snapshot = readSnapshot();
    await fulfillJSON(route, {
      ...publicTournament(snapshot.next_cursor.projection_revision),
      state: snapshot.tournament.state,
    });
  });
  await page.route(`**${snapshotPath}*`, async (route) => {
    await fulfillJSON(route, readSnapshot());
  });
};

const openCorrection = async (page: Page): Promise<void> => {
  await page.goto(`/arena/operator/${tournamentId}`, { waitUntil: "domcontentloaded" });
  await page.evaluate(() => {
    document.cookie = "tpm_admin_access_csrf=correction-contract-csrf; Path=/; SameSite=Lax";
  });
  await expect(page.getByRole("heading", { name: "Коррекция результата" })).toBeVisible();
};

const confirmCorrection = async (page: Page): Promise<void> => {
  await page.getByLabel("Объяснение").fill("Исправление подтверждено протоколом судьи");
  await page.getByLabel("Подтверждаю коррекцию результата и атомарную перестройку зависимых проекций.").check();
};

test("FE-036 sends preflight authority and the complete correction with one idempotency key", async ({ page }) => {
  let snapshot = operatorSnapshot(9);
  const preflightRequests: Request[] = [];
  const correctionRequests: Request[] = [];
  await installBaseRoutes(page, () => snapshot);
  await page.route(`**${preflightPath}`, async (route) => {
    preflightRequests.push(route.request());
    await fulfillJSON(route, preparedRequest());
  });
  await page.route(`**${correctionPath}`, async (route) => {
    correctionRequests.push(route.request());
    snapshot = acceptedSnapshot();
    await fulfillJSON(route, correctionEvidence());
  });

  await openCorrection(page);
  await confirmCorrection(page);
  await page.getByRole("button", { name: "Подтвердить коррекцию" }).click();

  await expect(page.getByText("Новая проекция подтверждена.")).toBeVisible();
  expect(preflightRequests).toHaveLength(1);
  expect(correctionRequests).toHaveLength(1);
  const preflight = preflightRequests[0];
  const correction = correctionRequests[0];
  expect(preflight.headers()["idempotency-key"]).toBe(correction.headers()["idempotency-key"]);
  expect(correction.headers()["x-csrf-token"]).toBe("correction-contract-csrf");
  expect(requestBody(preflight)).toMatchObject({
    confirmed: true,
    expected_projection_revision: 9,
    reason: "scorekeeping_error",
    source_result_revision: tournamentFixtureIds.scoreRevision,
  });
  expect(requestBody(correction)).toEqual(preparedRequest());
  await expect(page.getByText(projectionRevisionId, { exact: true })).toBeVisible();
});

test("FE-036 explains stale and incomplete intent rejection without optimistic state", async ({ page }) => {
  let snapshot = operatorSnapshot(9);
  let rejection = "stale_projection";
  let finalCalls = 0;
  await installBaseRoutes(page, () => snapshot);
  await page.route(`**${preflightPath}`, async (route) => {
    if (rejection === "stale_projection") {
      snapshot = { ...operatorSnapshot(10), series: operatorSnapshot(9).series };
      await fulfillProblem(route, rejection);
      return;
    }
    await fulfillJSON(route, preparedRequest());
  });
  await page.route(`**${correctionPath}`, async (route) => {
    finalCalls += 1;
    await fulfillProblem(route, "incomplete_unlock");
  });

  await openCorrection(page);
  await confirmCorrection(page);
  await page.getByRole("button", { name: "Подтвердить коррекцию" }).click();
  await expect(page.getByText("Проекция изменилась.")).toBeVisible();
  expect(finalCalls).toBe(0);
  await expect(page.getByText(tournamentFixtureIds.scoreRevision, { exact: true })).toBeVisible();

  rejection = "incomplete_unlock";
  await page.getByLabel("Объяснение").fill("Повтор после нового server snapshot");
  await page.getByLabel("Подтверждаю коррекцию результата и атомарную перестройку зависимых проекций.").check();
  await page.getByRole("button", { name: "Подтвердить коррекцию" }).click();
  await expect(page.getByText("Набор unlock intents неполный.")).toBeVisible();
  expect(finalCalls).toBe(1);
  await expect(page.getByText(tournamentFixtureIds.scoreRevision, { exact: true })).toBeVisible();
});

test("FE-036 closes correction after cutoff and in terminal state on light mobile UI", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  let snapshot = operatorSnapshot(9);
  await installBaseRoutes(page, () => snapshot);
  await page.route(`**${preflightPath}`, async (route) => {
    await fulfillProblem(route, "cutoff_wave_started");
  });

  await openCorrection(page);
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await confirmCorrection(page);
  await page.getByRole("button", { name: "Подтвердить коррекцию" }).click();
  await expect(page.getByText("Коррекция закрыта", { exact: true })).toBeVisible();
  await expect(page.getByText("Коррекция закрыта после старта зависимой волны.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Подтвердить коррекцию" })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  snapshot = terminalSnapshot();
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByText("Турнир завершен", { exact: true })).toBeVisible();
  await expect(page.getByText("Действия турнира отключены.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Подтвердить коррекцию" })).toHaveCount(0);

  await page.getByRole("button", { name: "Темная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});
