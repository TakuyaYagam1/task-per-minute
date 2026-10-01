import { expect, test, type Page, type Route } from "@playwright/test";

import {
  createTournamentFixtureSet,
  participantDraft,
  participantRecoveryWithDraft,
  tournamentFixtureIds,
} from "./tournament/fixtures";
import type { components } from "../lib/shared/api/schema";
import { selectTheme } from "./support/common";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantDraftPath = `${publicPath}/participant/series/${tournamentFixtureIds.bo1Series}/draft/actions`;
const participantURL = `/arena/participant/${tournamentId}`;
const serverTimestamp = "2026-09-15T10:00:00Z";
const activeDeadline = "2099-09-15T10:15:00Z";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];
type Draft = components["schemas"]["Draft"];
type DraftAction = components["schemas"]["DraftAction"];

const fulfillJSON = async (
  route: Route,
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: {
      "content-type": "application/json",
      ...headers,
    },
    body: JSON.stringify(body),
  });
};

const fulfillProblem = async (
  route: Route,
  status: number,
  title: string,
  headers: Record<string, string> = {},
): Promise<void> => {
  await route.fulfill({
    status,
    headers: {
      "content-type": "application/problem+json",
      ...headers,
    },
    body: JSON.stringify({
      detail: title,
      status,
      title,
      type: "about:blank",
    }),
  });
};

const withDate = { date: new Date(serverTimestamp).toUTCString() };

const automaticActionFor = (
  category: components["schemas"]["Category"],
  turn: number,
): DraftAction => ({
  action: "ban",
  actor_id: tournamentFixtureIds.firstParticipant,
  automatic: true,
  category,
  decision_evidence: {
    algorithm_version: "hmac-sha256-order-v1",
    decided_at: serverTimestamp,
    id: tournamentFixtureIds.draftEvidence,
    normalized_inputs: [tournamentFixtureIds.draft, String(turn), category],
    owner_id: tournamentFixtureIds.draft,
    purpose: "category",
    replay_digest: "a".repeat(64),
    result: [category],
  },
  occurred_at: serverTimestamp,
  turn,
  turn_deadline: activeDeadline,
});

const manualActionFor = (
  category: components["schemas"]["Category"],
  turn: number,
  actorId: string,
): DraftAction => ({
  action: "ban",
  actor_id: actorId,
  automatic: false,
  category,
  decision_evidence: null,
  occurred_at: serverTimestamp,
  turn,
  turn_deadline: activeDeadline,
});

const secondTurnDraft = (): Draft => participantDraft({
  actions: [manualActionFor("web", 1, tournamentFixtureIds.firstParticipant)],
  current_action: "ban",
  current_actor_id: tournamentFixtureIds.secondParticipant,
  legal_categories: ["crypto", "reverse"],
  revision: 2,
  selected_categories: ["web"],
  turn: 2,
  turn_deadline: activeDeadline,
});

const completedDraft = (): Draft => participantDraft({
  actions: [
    manualActionFor("web", 1, tournamentFixtureIds.firstParticipant),
    manualActionFor("crypto", 2, tournamentFixtureIds.secondParticipant),
  ],
  current_action: null,
    current_actor_id: null,
    legal_categories: [],
    revision: 3,
    selected_categories: ["reverse"],
    state: "completed",
    turn: 2,
  turn_deadline: null,
});

const participantSnapshotWithParticipant = (
  snapshot: ParticipantSnapshot,
  participantId: string,
): ParticipantSnapshot => ({
  ...snapshot,
  lobby: {
    ...snapshot.lobby,
    participant_id: participantId,
  },
});

const installParticipantRoutes = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshotForRequest: () => ParticipantSnapshot,
  onSnapshot?: () => void,
): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_player_csrf=participant-draft-contract-csrf; Path=/";
  });
  await page.route(`**${publicPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    await fulfillJSON(route, snapshotForRequest().lobby);
  });
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    onSnapshot?.();
    await fulfillJSON(route, snapshotForRequest(), 200, withDate);
  });
};

const openDraft = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshotForRequest: () => ParticipantSnapshot,
  onSnapshot?: () => void,
): Promise<void> => {
  await installParticipantRoutes(page, fixtureSet, snapshotForRequest, onSnapshot);
  await page.goto(participantURL);
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-draft-panel")).toBeVisible();
};

const draftPanel = (page: Page) => page.getByTestId("participant-draft-panel");
const banButton = (page: Page, category: string) => page.getByTestId(`participant-draft-ban-${category}`);
const pickButtons = (page: Page) => page.locator('[data-testid^="participant-draft-pick-"]');
const banButtons = (page: Page) => page.locator('[data-testid^="participant-draft-ban-"]');

test("FE-021 BO1 exposes only three categories, records two server-authorized bans, and never shows picks", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = participantRecoveryWithDraft();
  const requests: Array<{ body: unknown; key: string | undefined; csrf: string | undefined }> = [];

  await openDraft(page, fixtureSet, () => current);
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText(/web/i);
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText(/crypto/i);
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText(/reverse/i);
  await expect(pickButtons(page)).toHaveCount(0);
  await expect(banButton(page, "web")).toBeEnabled();
  await expect(banButton(page, "crypto")).toBeEnabled();
  await expect(banButton(page, "reverse")).toBeEnabled();

  await page.route(`**${participantDraftPath}`, async (route) => {
    const request = route.request();
    requests.push({
      body: request.postDataJSON(),
      csrf: request.headers()["x-csrf-token"],
      key: request.headers()["idempotency-key"],
    });
    if (requests.length === 1) {
      current = participantSnapshotWithParticipant(
        participantRecoveryWithDraft(secondTurnDraft()),
        tournamentFixtureIds.firstParticipant,
      );
      await fulfillJSON(route, current.draft);
      return;
    }

    current = participantRecoveryWithDraft(completedDraft(), 11);
    await fulfillJSON(route, current.draft);
  });

  await banButton(page, "web").click();
  await expect.poll(() => requests).toHaveLength(1);
  expect(requests[0]?.body).toEqual({
    action: "ban",
    category: "web",
    expected_draft_revision: 1,
    expected_projection_revision: 9,
    expected_turn: 1,
  });
  expect(requests[0]?.key).toMatch(/^[0-9a-f-]{36}$/i);
  expect(requests[0]?.csrf).toBe("participant-draft-contract-csrf");
  await expect(pickButtons(page)).toHaveCount(0);

  current = participantRecoveryWithDraft(secondTurnDraft());
  await page.reload();
  await expect(banButton(page, "crypto")).toBeEnabled();
  await banButton(page, "crypto").click();

  await expect.poll(() => requests).toHaveLength(2);
  expect(requests[1]?.body).toEqual({
    action: "ban",
    category: "crypto",
    expected_draft_revision: 2,
    expected_projection_revision: 9,
    expected_turn: 2,
  });
  await expect(pickButtons(page)).toHaveCount(0);
  await expect(page.locator('[data-testid^="participant-draft-ban-"]:enabled')).toHaveCount(0);
});

test("FE-021 blocks out-of-turn and paused drafts without posting a client timeout action", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const outOfTurnDraft = participantDraft({
    current_actor_id: tournamentFixtureIds.secondParticipant,
    legal_categories: ["web", "crypto", "reverse"],
  });
  const outOfTurn = participantSnapshotWithParticipant(
    participantRecoveryWithDraft(outOfTurnDraft),
    tournamentFixtureIds.firstParticipant,
  );
  let current = outOfTurn;
  let posts = 0;

  await openDraft(page, fixtureSet, () => current);
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    await fulfillJSON(route, current.draft);
  });
  await expect(page.locator('[data-testid^="participant-draft-ban-"]:enabled')).toHaveCount(0);
  await page.waitForTimeout(250);
  expect(posts).toBe(0);

  const pausedDraft = participantDraft({
    current_action: null,
    current_actor_id: null,
    legal_categories: [],
    revision: 2,
    state: "paused",
    turn_deadline: null,
  });
  current = participantRecoveryWithDraft(pausedDraft, 10);
  await page.reload();
  await expect(draftPanel(page)).toHaveAttribute("data-draft-state", "paused");
  await expect(page.locator('[data-testid^="participant-draft-ban-"]:enabled')).toHaveCount(0);
  await page.waitForTimeout(250);
  expect(posts).toBe(0);
});

test("FE-021 renders server automatic bans and hides draft controls for server-assigned categories", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const automaticDraft = participantDraft({
    actions: [automaticActionFor("web", 1)],
    current_action: "ban",
    current_actor_id: tournamentFixtureIds.secondParticipant,
    legal_categories: ["crypto", "reverse"],
    revision: 2,
    selected_categories: [],
    state: "active",
    turn: 2,
    turn_deadline: activeDeadline,
  });
  let current = participantSnapshotWithParticipant(
    participantRecoveryWithDraft(automaticDraft, 12),
    tournamentFixtureIds.firstParticipant,
  );

  await openDraft(page, fixtureSet, () => current);
  await expect(draftPanel(page).getByTestId("participant-draft-action-1")).toHaveAttribute(
    "data-automatic",
    "true",
  );
  await expect(draftPanel(page)).toContainText(/автомат|сервер/i);
  await expect(banButtons(page)).toHaveCount(0);
  await expect(pickButtons(page)).toHaveCount(0);

  current = fixtureSet.participant.recovery;
  await page.reload();
  await expect(page.getByTestId("participant-assignment-state")).toHaveAttribute(
    "data-assignment-state",
    "delivered",
  );
  await expect(page.getByTestId("participant-draft-panel")).toHaveCount(0);
  await expect(page.getByTestId("participant-assignment-state")).toContainText(/задани|достав/i);
});

test("FE-021 conflict recovery keeps history server-owned and 429 never retries", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = participantRecoveryWithDraft();
  let snapshotReads = 0;
  let posts = 0;
  let releaseConflict: (() => void) | undefined;

  await openDraft(page, fixtureSet, () => current, () => {
    snapshotReads += 1;
  });
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    await new Promise<void>((resolve) => {
      releaseConflict = resolve;
    });
    current = participantRecoveryWithDraft(secondTurnDraft(), 10);
    await fulfillProblem(route, 409, "Черновик обновился на сервере");
  });

  await banButton(page, "web").click();
  await expect(draftPanel(page).getByTestId("participant-draft-action-1")).toHaveCount(0);
  releaseConflict?.();
  await expect.poll(() => posts).toBe(1);
  await expect.poll(() => snapshotReads).toBeGreaterThan(1);
  await expect(draftPanel(page)).toHaveAttribute("data-draft-revision", "2");

  await page.unroute(`**${participantDraftPath}`);
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    await fulfillProblem(route, 429, "Слишком много ходов", { "Retry-After": "17" });
  });
  await banButton(page, "crypto").click();
  await expect.poll(() => posts).toBe(2);
  await page.waitForTimeout(250);
  expect(posts).toBe(2);
  await expect(page.locator("body")).toContainText(/лимит|повтор|17|слишком/i);
});

test("FE-021 ignores a slow double click, keeps one idempotency key, and supports keyboard in both themes on mobile", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = participantRecoveryWithDraft();
  const keys: string[] = [];
  let posts = 0;
  let release: (() => void) | undefined;

  await openDraft(page, fixtureSet, () => current);
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    keys.push(route.request().headers()["idempotency-key"] ?? "");
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    current = participantRecoveryWithDraft(secondTurnDraft(), 10);
    await fulfillJSON(route, current.draft);
  });

  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await selectTheme(page, "light");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(draftPanel(page)).toBeVisible();
  await selectTheme(page, "dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  await banButton(page, "web").focus();
  await expect(banButton(page, "web")).toBeFocused();
  await banButton(page, "web").press("Enter");
  await banButton(page, "web").press("Enter").catch(() => undefined);
  await expect.poll(() => posts).toBe(1);
  release?.();
  await expect.poll(() => keys).toHaveLength(1);
  expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/i);
});
