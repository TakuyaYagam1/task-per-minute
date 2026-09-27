import { expect, test, type Page, type Route } from "@playwright/test";

import {
  createTournamentFixtureSet,
  tournamentFixtureIds,
} from "./tournament/fixtures";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantURL = `/arena/participant/${tournamentId}`;
const serverTimestamp = "2026-09-15T10:00:00Z";
const readyWindowDeadline = "2026-09-15T10:02:00Z";
const gameDeadline = "2026-09-15T10:03:00Z";
const assignmentTitle = "FE-018: HTTP Relay";
const assignmentDescription = "Соберите HTTP relay без создания нового инстанса.";
const provisioningCopyPattern = /provision|контейнер|инстанс|создани[ея] окружения/i;

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];

const fulfillJSON = async (
  route: Route,
  body: unknown,
  headers: Record<string, string> = {},
  status = 200,
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

const serverDateHeader = (): Record<string, string> => ({
  date: new Date(serverTimestamp).toUTCString(),
});

const installParticipantRoutes = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshot: ParticipantSnapshot | (() => ParticipantSnapshot),
): Promise<void> => {
  await page.route(`**${publicPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.participant.lobby);
  });
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(
      route,
      typeof snapshot === "function" ? snapshot() : snapshot,
      serverDateHeader(),
    );
  });
};

const snapshotWithRevision = (
  snapshot: ParticipantSnapshot,
  projectionRevision: number,
): ParticipantSnapshot => ({
  ...snapshot,
  lobby: {
    ...snapshot.lobby,
    projection_revision: projectionRevision,
  },
  next_cursor: {
    ...snapshot.next_cursor,
    projection_revision: projectionRevision,
  },
  projection_revision: projectionRevision,
});

const waitingSnapshot = (fixtureSet: FixtureSet): ParticipantSnapshot => {
  const snapshot = fixtureSet.participant.recovery;
  return snapshotWithRevision(
    {
      ...snapshot,
      assignment: null,
      lobby: {
        ...snapshot.lobby,
        current_swiss_round: null,
        required_action: "wait",
        series: [],
        status: "waiting",
      },
      series: null,
      wave: null,
    },
    9,
  );
};

const supersededSnapshot = (fixtureSet: FixtureSet): ParticipantSnapshot => {
  const delivered = deliveredSnapshot(fixtureSet, 10);
  if (delivered.wave === null) {
    throw new Error("FE-018 fixture requires a wave for superseded state");
  }
  return {
    ...delivered,
    assignment: null,
    lobby: {
      ...delivered.lobby,
      required_action: "wait",
      series: [],
      status: "waiting",
    },
    series: null,
    wave: {
      ...delivered.wave,
      ready_window: delivered.wave.ready_window === null
        ? null
        : { ...delivered.wave.ready_window, state: "superseded" as const },
      state: "superseded" as const,
    },
  };
};

const deliveredSnapshot = (fixtureSet: FixtureSet, projectionRevision = 9): ParticipantSnapshot => {
  const snapshot = fixtureSet.participant.recovery;
  const assignment = snapshot.assignment;
  const series = snapshot.series;
  const wave = snapshot.wave;
  if (assignment === null || series === null || wave === null) {
    throw new Error("FE-018 fixture requires an assignment, series, and wave");
  }

  const activeSeries = {
    ...series,
    score: { first_participant_wins: 0, second_participant_wins: 0 },
    slots: series.slots.map((slot) => ({
      ...slot,
      attempts: slot.attempts.map((game) => game.id === tournamentFixtureIds.bo3GameTwo
        ? { ...game, state: "active" as const }
        : game),
    })),
    state: "active" as const,
  };
  const activeWave = {
    ...wave,
    paused_at: null,
    ready_window: {
      consumed_at: null,
      deadline: readyWindowDeadline,
      id: tournamentFixtureIds.readyWindow,
      opened_at: serverTimestamp,
      revision_id: tournamentFixtureIds.pauseRevision,
      state: "open" as const,
      wave_id: tournamentFixtureIds.bo3Wave,
    },
    started_at: serverTimestamp,
    state: "active" as const,
  };

  return snapshotWithRevision(
    {
      ...snapshot,
      assignment: {
        ...assignment,
        active_snapshot: {
          ...assignment.active_snapshot,
          description: assignmentDescription,
          time_limit: 180,
          title: assignmentTitle,
          version: 7,
        },
        context: {
          effective_deadline: gameDeadline,
          game_id: tournamentFixtureIds.bo3GameTwo,
          game_number: 2,
          game_state: "active" as const,
          series_id: tournamentFixtureIds.bo3Series,
          series_score: { first_participant_wins: 1, second_participant_wins: 0 },
          slot_id: tournamentFixtureIds.bo3SlotTwo,
          stage: "swiss" as const,
          started_at: serverTimestamp,
          swiss_round: 2,
          wave_id: tournamentFixtureIds.bo3Wave,
        },
      },
      lobby: {
        ...snapshot.lobby,
        current_swiss_round: 1,
        required_action: "play",
        series: snapshot.lobby.series.map((lobbySeries) => ({
          ...lobbySeries,
          state: "active" as const,
        })),
        state: "technical_pause",
        status: "assigned",
      },
      series: activeSeries,
      wave: activeWave,
    },
    projectionRevision,
  );
};

const pausedSnapshot = (fixtureSet: FixtureSet): ParticipantSnapshot => {
  const delivered = deliveredSnapshot(fixtureSet);
  if (delivered.assignment === null || delivered.wave === null) {
    throw new Error("FE-018 fixture requires a delivered assignment and wave");
  }
  return {
    ...delivered,
    assignment: {
      ...delivered.assignment,
      context: {
        ...delivered.assignment.context,
        effective_deadline: null,
        game_state: "paused" as const,
      },
    },
    wave: {
      ...delivered.wave,
      paused_at: serverTimestamp,
      state: "paused" as const,
    },
  };
};

const expectNoProvisioningUI = async (page: Page): Promise<void> => {
  for (const locator of [page.getByRole("heading"), page.getByRole("button"), page.getByRole("status")]) {
    await expect(locator.filter({ hasText: provisioningCopyPattern })).toHaveCount(0);
  }
};

test("FE-018 hides an undelivered assignment and never exposes provisioning UI", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await installParticipantRoutes(page, fixtureSet, waitingSnapshot(fixtureSet));

  await page.goto(participantURL, { waitUntil: "domcontentloaded" });

  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "waiting");
  const assignment = page.getByRole("region", { name: "Задание для игры" });
  await expect(assignment).toBeVisible();
  await expect(assignment).toHaveAttribute("data-assignment-state", "waiting");
  await expect(assignment).not.toContainText(assignmentTitle);
  await expect(assignment.getByText("Версия задания", { exact: true })).toHaveCount(0);
  await expectNoProvisioningUI(page);
});

test("FE-018 shows the server-delivered task and current series context", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const snapshot = deliveredSnapshot(fixtureSet);
  expect(Date.parse(gameDeadline) - Date.parse(serverTimestamp)).toBe(180_000);
  await page.clock.install({ time: serverTimestamp });
  await installParticipantRoutes(page, fixtureSet, snapshot);

  await page.goto(participantURL, { waitUntil: "domcontentloaded" });

  const assignment = page.getByRole("region", { name: "Задание для игры" });
  await expect(assignment).toBeVisible();
  await expect(assignment).toContainText(assignmentTitle);
  await expect(assignment).toContainText(assignmentDescription);
  await expect(assignment).toContainText("Web");
  await expect(
    assignment.getByText("Версия задания", { exact: true }).locator(".."),
  ).toContainText("7");
  await expect(assignment).toContainText("180 с");
  const expectedTaskDeadline = await page.evaluate((deadline) => new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(Date.parse(deadline)), gameDeadline);
  await expect(
    assignment.getByText("Дедлайн задания", { exact: true }).locator(".."),
  ).toContainText(expectedTaskDeadline);
  await expect(
    page.getByText("Стадия", { exact: true }).locator("..").getByText("Квалификация", { exact: true }),
  ).toContainText("Квалификация");
  await expect(
    page.getByText("Раунд", { exact: true }).locator("..").getByText("Раунд 2", { exact: true }),
  ).toContainText("Раунд 2");
  await expect(
    page.getByText("Игра", { exact: true }).locator("..").getByText("Игра 2", { exact: true }),
  ).toContainText("Игра 2");
  await expect(
    page.getByText("Счет серии", { exact: true }).locator("..").getByText("1:0", { exact: true }),
  ).toContainText("1:0");
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expectNoProvisioningUI(page);
});

test("FE-018 keeps one immutable task id and version for both players", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const snapshot = deliveredSnapshot(fixtureSet);
  const secondPlayer = await page.context().newPage();
  await page.clock.install({ time: serverTimestamp });
  await secondPlayer.clock.install({ time: serverTimestamp });
  await installParticipantRoutes(page, fixtureSet, snapshot);
  await installParticipantRoutes(secondPlayer, fixtureSet, snapshot);

  const firstSnapshotResponse = page.waitForResponse((response) =>
    response.url().includes(participantSnapshotPath),
  );
  const secondSnapshotResponse = secondPlayer.waitForResponse((response) =>
    response.url().includes(participantSnapshotPath),
  );
  await Promise.all([
    page.goto(participantURL, { waitUntil: "domcontentloaded" }),
    secondPlayer.goto(participantURL, { waitUntil: "domcontentloaded" }),
  ]);
  const [firstSnapshot, secondSnapshot] = await Promise.all([
    firstSnapshotResponse.then((response) => response.json() as Promise<ParticipantSnapshot>),
    secondSnapshotResponse.then((response) => response.json() as Promise<ParticipantSnapshot>),
  ]);
  expect(firstSnapshot.assignment?.active_snapshot.task_id).toBe(
    secondSnapshot.assignment?.active_snapshot.task_id,
  );
  expect(firstSnapshot.assignment?.active_snapshot.version).toBe(
    secondSnapshot.assignment?.active_snapshot.version,
  );

  const firstAssignment = page.getByRole("region", { name: "Задание для игры" });
  const secondAssignment = secondPlayer.getByRole("region", { name: "Задание для игры" });
  await expect(firstAssignment).toBeVisible();
  await expect(secondAssignment).toBeVisible();
  await expect(firstAssignment).toContainText(assignmentTitle);
  await expect(secondAssignment).toContainText(assignmentTitle);
  await expect(
    firstAssignment.getByText("Версия задания", { exact: true }).locator(".."),
  ).toContainText("7");
  await expect(
    secondAssignment.getByText("Версия задания", { exact: true }).locator(".."),
  ).toContainText("7");
  expect(await firstAssignment.innerText()).toBe(await secondAssignment.innerText());

  await secondPlayer.close();
});

test("FE-018 removes superseded assignment content after authoritative refresh", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = deliveredSnapshot(fixtureSet, 9);
  await page.routeWebSocket(`**${publicPath}/participant/realtime*`, (socket) => {
    socket.close({ code: 1008, reason: "Test reconnect" });
  });
  await installParticipantRoutes(page, fixtureSet, () => current);

  await page.goto(participantURL, { waitUntil: "domcontentloaded" });
  const assignment = page.getByRole("region", { name: "Задание для игры" });
  await expect(assignment).toBeVisible();
  await expect(assignment).toHaveAttribute("data-assignment-state", "delivered");

  current = supersededSnapshot(fixtureSet);
  await page.getByTestId("participant-recovery-fallback")
    .getByRole("button", { name: "Повторить", exact: true }).click();

  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "waiting");
  await expect(assignment).toHaveAttribute("data-assignment-state", "superseded");
  await expect(assignment).not.toContainText(assignmentTitle);
  await expect(page.getByText(assignmentTitle, { exact: true })).toHaveCount(0);
  await expect(assignment.getByText("Версия задания", { exact: true })).toHaveCount(0);
  await expectNoProvisioningUI(page);
});

test("FE-018 preserves the published 180-second deadline across clock changes, reload and themes", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await page.clock.install({ time: serverTimestamp });
  await installParticipantRoutes(page, fixtureSet, deliveredSnapshot(fixtureSet));

  await page.goto(participantURL, { waitUntil: "domcontentloaded" });
  const assignment = page.getByRole("region", { name: "Задание для игры" });
  const deadline = assignment.getByText("Дедлайн задания", { exact: true }).locator("..");
  const expectedDeadline = await page.evaluate((value) => new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(Date.parse(value)), gameDeadline);
  expect(Date.parse(gameDeadline) - Date.parse(serverTimestamp)).toBe(180_000);
  await expect(deadline).toContainText(expectedDeadline);

  await page.clock.setSystemTime("2026-09-15T10:10:00Z");
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(deadline).toContainText(expectedDeadline);
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);

  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  const bodyWidth = await page.evaluate(() => document.body.scrollWidth);
  expect(bodyWidth).toBeLessThanOrEqual(390);
});

test("FE-018 suppresses stale ready-window timing while the assigned game is paused", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await page.clock.install({ time: serverTimestamp });
  await installParticipantRoutes(page, fixtureSet, pausedSnapshot(fixtureSet));

  await page.goto(participantURL, { waitUntil: "domcontentloaded" });

  const assignment = page.getByRole("region", { name: "Задание для игры" });
  await expect(assignment).toHaveAttribute("data-assignment-state", "delivered");
  await expect(
    assignment.getByText("Дедлайн задания", { exact: true }).locator(".."),
  ).toContainText("Приостановлен");
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
});
