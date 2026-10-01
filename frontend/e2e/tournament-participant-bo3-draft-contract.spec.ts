import { expect, test, type Page, type Route } from "@playwright/test";

import {
  createTournamentFixtureSet,
  participantBo3Draft,
  participantRecoveryWithDraft,
  tournamentFixtureIds,
} from "./tournament/fixtures";
import type { components } from "../lib/shared/api/schema";
import { selectTheme } from "./support/common";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantDraftPath = `${publicPath}/participant/series/${tournamentFixtureIds.bo3Series}/draft/actions`;
const participantURL = `/arena/participant/${tournamentId}`;
const serverTimestamp = "2026-09-15T10:00:00Z";
const activeDeadline = "2099-09-15T10:15:00Z";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];
type Draft = components["schemas"]["Draft"];
type DraftAction = components["schemas"]["DraftAction"];
type Category = components["schemas"]["Category"];

const bo3Pool: readonly Category[] = ["crypto", "forensics", "pwn", "reverse", "web"];

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

const manualActionFor = (
  action: components["schemas"]["DraftActionType"],
  category: Category,
  turn: number,
  actorId: string,
): DraftAction => ({
  action,
  actor_id: actorId,
  automatic: false,
  category,
  decision_evidence: null,
  occurred_at: serverTimestamp,
  turn,
  turn_deadline: activeDeadline,
});

const automaticActionFor = (
  category: Category,
  turn: number,
  actorId: string,
): DraftAction => ({
  action: "ban",
  actor_id: actorId,
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

const activeBo3Draft = (turn: number, actions: readonly DraftAction[] = []): Draft => {
  const used = new Set(actions.map((action) => action.category));
  const legalCategories = bo3Pool.filter((category) => !used.has(category));
  const actorId = turn % 2 === 1
    ? tournamentFixtureIds.firstParticipant
    : tournamentFixtureIds.secondParticipant;
  const action = turn <= 2 ? "ban" : "pick";

  return participantBo3Draft({
    actions: [...actions],
    current_action: action,
    current_actor_id: actorId,
    legal_categories: legalCategories,
    revision: turn,
    selected_categories: [],
    state: "active",
    turn,
    turn_deadline: activeDeadline,
  });
};

const completedBo3Draft = (): Draft => participantBo3Draft({
  actions: [
    manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
    manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
    manualActionFor("pick", "pwn", 3, tournamentFixtureIds.firstParticipant),
    manualActionFor("pick", "reverse", 4, tournamentFixtureIds.secondParticipant),
  ],
  current_action: null,
  current_actor_id: null,
  legal_categories: [],
  revision: 4,
  selected_categories: ["pwn", "reverse", "web"],
  state: "completed",
  turn: 4,
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
    document.cookie = "tpm_player_csrf=participant-bo3-draft-contract-csrf; Path=/";
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
const actionButton = (page: Page, action: "ban" | "pick", category: string) =>
  page.getByTestId(`participant-draft-${action}-${category}`);
const actionButtons = (page: Page, action: "ban" | "pick") =>
  page.locator(`[data-testid^="participant-draft-${action}-"]`);

test("FE-022 completes the server-ordered BO3 ban-ban-pick-pick draft", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const requests: Array<{ body: unknown; key: string | undefined }> = [];
  const transitions = [
    activeBo3Draft(2, [manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant)]),
    activeBo3Draft(3, [
      manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
      manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
    ]),
    activeBo3Draft(4, [
      manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
      manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
      manualActionFor("pick", "pwn", 3, tournamentFixtureIds.firstParticipant),
    ]),
    completedBo3Draft(),
  ];
  let current = participantRecoveryWithDraft(activeBo3Draft(1), 9);

  await openDraft(page, fixtureSet, () => current);
  await expect(draftPanel(page)).toHaveAttribute("data-draft-format", "bo3");
  await expect(page.locator('[data-testid^="participant-draft-ban-"]')).toHaveCount(5);
  await expect(actionButtons(page, "pick")).toHaveCount(0);
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText("Web");
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText("Crypto");
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText("Forensics");
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText("Reverse");
  await expect(draftPanel(page).getByTestId("participant-draft-pool")).toContainText("Pwn");

  await page.route(`**${participantDraftPath}`, async (route) => {
    const request = route.request();
    requests.push({
      body: request.postDataJSON(),
      key: request.headers()["idempotency-key"],
    });
    const transition = transitions[requests.length - 1];
    if (transition === undefined) {
      await fulfillProblem(route, 409, "Драфт уже завершен");
      return;
    }
    current = participantRecoveryWithDraft(transition, 9);
    await fulfillJSON(route, transition);
  });

  await actionButton(page, "ban", "crypto").click();
  await expect.poll(() => requests).toHaveLength(1);
  expect(requests[0]?.body).toEqual({
    action: "ban",
    category: "crypto",
    expected_draft_revision: 1,
    expected_projection_revision: 9,
    expected_turn: 1,
  });
  expect(requests[0]?.key).toMatch(/^[0-9a-f-]{36}$/i);
  await page.reload();
  await expect(actionButton(page, "ban", "crypto")).toBeDisabled();
  await expect(actionButtons(page, "pick")).toHaveCount(0);

  await actionButton(page, "ban", "forensics").click();
  await expect.poll(() => requests).toHaveLength(2);
  expect(requests[1]?.body).toEqual({
    action: "ban",
    category: "forensics",
    expected_draft_revision: 2,
    expected_projection_revision: 9,
    expected_turn: 2,
  });
  expect(requests[1]?.key).toMatch(/^[0-9a-f-]{36}$/i);
  await page.reload();
  await expect(actionButtons(page, "ban")).toHaveCount(0);
  await expect(actionButtons(page, "pick")).toHaveCount(5);
  await expect(actionButton(page, "pick", "crypto")).toBeDisabled();
  await expect(actionButton(page, "pick", "forensics")).toBeDisabled();
  await expect(actionButton(page, "pick", "pwn")).toBeEnabled();
  await expect(actionButton(page, "pick", "reverse")).toBeEnabled();
  await expect(actionButton(page, "pick", "web")).toBeEnabled();

  await actionButton(page, "pick", "pwn").click();
  await expect.poll(() => requests).toHaveLength(3);
  expect(requests[2]?.body).toEqual({
    action: "pick",
    category: "pwn",
    expected_draft_revision: 3,
    expected_projection_revision: 9,
    expected_turn: 3,
  });
  expect(requests[2]?.key).toMatch(/^[0-9a-f-]{36}$/i);
  await page.reload();
  await expect(actionButton(page, "pick", "pwn")).toBeDisabled();
  await expect(actionButton(page, "pick", "reverse")).toBeEnabled();

  await actionButton(page, "pick", "reverse").click();
  await expect.poll(() => requests).toHaveLength(4);
  expect(requests[3]?.body).toEqual({
    action: "pick",
    category: "reverse",
    expected_draft_revision: 4,
    expected_projection_revision: 9,
    expected_turn: 4,
  });
  expect(requests[3]?.key).toMatch(/^[0-9a-f-]{36}$/i);
  expect(new Set(requests.map((request) => request.key)).size).toBe(4);
  await page.reload();

  await expect(draftPanel(page)).toHaveAttribute("data-draft-state", "completed");
  await expect(actionButtons(page, "ban")).toHaveCount(0);
  await expect(actionButtons(page, "pick")).toHaveCount(0);
  await expect(page.getByTestId("participant-draft-selected")).toBeVisible();
  await expect(page.getByTestId("participant-draft-game-1")).toContainText(/Игра 1[\s\S]*Pwn/);
  await expect(page.getByTestId("participant-draft-game-2")).toContainText(/Игра 2[\s\S]*Reverse/);
  await expect(page.getByTestId("participant-draft-game-3")).toContainText(/Игра 3[\s\S]*Web/);
  for (const [turn, action, category] of [
    [1, "Бан", "Crypto"],
    [2, "Бан", "Forensics"],
    [3, "Выбор", "Pwn"],
    [4, "Выбор", "Reverse"],
  ] as const) {
    const history = page.getByTestId(`participant-draft-action-${turn}`);
    await expect(history).toContainText(action);
    await expect(history).toContainText(category);
    await expect(history).toHaveAttribute("data-automatic", "false");
  }
});

test("FE-022 keeps out-of-turn actions disabled and recovers 409/422 without optimistic history", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const outOfTurn = participantSnapshotWithParticipant(
    participantRecoveryWithDraft(activeBo3Draft(1)),
    tournamentFixtureIds.secondParticipant,
  );
  let current = outOfTurn;
  let posts = 0;

  await openDraft(page, fixtureSet, () => current);
  await expect(actionButtons(page, "ban")).toHaveCount(0);
  await page.waitForTimeout(250);
  expect(posts).toBe(0);

  let releaseConflict: (() => void) | undefined;
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    await new Promise<void>((resolve) => {
      releaseConflict = resolve;
    });
    const recovered = activeBo3Draft(2, [
      manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
    ]);
    current = participantRecoveryWithDraft(recovered, 10);
    await fulfillProblem(route, 409, "Драфт изменился на сервере");
  });

  current = participantRecoveryWithDraft(activeBo3Draft(1), 9);
  await page.reload();
  await actionButton(page, "ban", "crypto").click();
  await expect.poll(() => posts).toBe(1);
  await expect(page.getByTestId("participant-draft-action-1")).toHaveCount(0);
  releaseConflict?.();
  await expect(page.getByTestId("participant-draft-action-1")).toBeVisible();
  await expect(draftPanel(page)).toHaveAttribute("data-draft-revision", "2");
  await expect(page.getByTestId("participant-draft-action-1")).toContainText("Crypto");

  await page.unroute(`**${participantDraftPath}`);
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    await fulfillProblem(route, 422, "Категория уже выбрана", {});
  });
  current = participantRecoveryWithDraft(activeBo3Draft(3, [
    manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
    manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
  ]), 10);
  await page.reload();
  await actionButton(page, "pick", "pwn").click();
  await expect.poll(() => posts).toBe(2);
  await expect(page.getByTestId("participant-draft-action-3")).toHaveCount(0);
  await expect(draftPanel(page)).toHaveAttribute("data-draft-revision", "3");
  await expect(page.getByTestId("participant-draft-status")).toContainText(/Категория уже выбрана|драфт/i);
  await page.waitForTimeout(250);
  expect(posts).toBe(2);
});

test("FE-022 keeps automatic server action evidence after refresh and never creates a client timeout", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = participantRecoveryWithDraft(activeBo3Draft(1), 9);
  let posts = 0;

  await openDraft(page, fixtureSet, () => current);
  await page.waitForTimeout(250);
  expect(posts).toBe(0);

  const automatic = activeBo3Draft(2, [
    automaticActionFor("crypto", 1, tournamentFixtureIds.firstParticipant),
  ]);
  current = participantSnapshotWithParticipant(
    participantRecoveryWithDraft(automatic, 10),
    tournamentFixtureIds.firstParticipant,
  );
  await page.reload();
  await expect(page.getByTestId("participant-draft-action-1")).toHaveAttribute("data-automatic", "true");
  await expect(page.getByTestId("participant-draft-action-1")).toContainText("Автоматический ход");
  await expect(page.getByTestId("participant-draft-action-1")).toContainText("Решение подтверждено");
  await expect(actionButtons(page, "ban")).toHaveCount(0);
  await page.waitForTimeout(400);
  expect(posts).toBe(0);

  const midDraft = activeBo3Draft(3, [
    automaticActionFor("crypto", 1, tournamentFixtureIds.firstParticipant),
    manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
  ]);
  current = participantRecoveryWithDraft(midDraft, 11);
  await page.reload();
  await expect(draftPanel(page)).toHaveAttribute("data-draft-revision", "3");
  await expect(page.getByTestId("participant-draft-action-1")).toHaveAttribute("data-automatic", "true");
  await expect(page.getByTestId("participant-draft-action-2")).toHaveAttribute("data-automatic", "false");
  await expect(actionButton(page, "pick", "pwn")).toBeEnabled();
  expect(posts).toBe(0);
});

test("FE-022 sends one idempotent pick on slow double click, honors 429, and stays usable on mobile themes", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = participantRecoveryWithDraft(activeBo3Draft(3, [
    manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
    manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
  ]), 9);
  const keys: string[] = [];
  let posts = 0;
  let release: (() => void) | undefined;

  await openDraft(page, fixtureSet, () => current);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await selectTheme(page, "light");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await selectTheme(page, "dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    keys.push(route.request().headers()["idempotency-key"] ?? "");
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    const next = activeBo3Draft(4, [
      manualActionFor("ban", "crypto", 1, tournamentFixtureIds.firstParticipant),
      manualActionFor("ban", "forensics", 2, tournamentFixtureIds.secondParticipant),
      manualActionFor("pick", "pwn", 3, tournamentFixtureIds.firstParticipant),
    ]);
    current = participantRecoveryWithDraft(next, 10);
    await fulfillJSON(route, next);
  });

  await actionButton(page, "pick", "pwn").focus();
  await expect(actionButton(page, "pick", "pwn")).toBeFocused();
  await actionButton(page, "pick", "pwn").press("Enter");
  await actionButton(page, "pick", "pwn").press("Enter").catch(() => undefined);
  await expect.poll(() => posts).toBe(1);
  expect(keys).toHaveLength(1);
  expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/i);
  release?.();
  await expect.poll(() => current.draft?.turn).toBe(4);

  await page.unroute(`**${participantDraftPath}`);
  await page.route(`**${participantDraftPath}`, async (route) => {
    posts += 1;
    await fulfillProblem(route, 429, "Слишком много ходов", { "Retry-After": "17" });
  });
  await page.reload();
  await actionButton(page, "pick", "reverse").click();
  await expect.poll(() => posts).toBe(2);
  await page.waitForTimeout(300);
  expect(posts).toBe(2);
  await expect(page.locator("body")).toContainText(/17|лимит|повтор/i);
});
