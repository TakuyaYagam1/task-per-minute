import { expect, test, type Page, type Route } from "@playwright/test";

import {
  createTournamentFixtureSet,
  tournamentFixtureIds,
} from "./tournament/fixtures";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantReadyPath = `${publicPath}/participant/waves/${tournamentFixtureIds.bo3Wave}/ready`;
const participantURL = `/arena/participant/${tournamentId}`;
const serverTimestamp = "2026-09-15T10:00:00Z";

type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];

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
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({
      detail: title,
      status,
      title,
      type: "about:blank",
    }),
  });
};

const withDate = { date: new Date(serverTimestamp).toUTCString() };

const readyWindow = (id: string, state: "open" | "expired" | "consumed" = "open") => ({
  consumed_at: state === "consumed" ? "2026-09-15T10:01:00Z" : null,
  deadline: "2026-09-15T10:01:00Z",
  id,
  opened_at: "2026-09-15T09:59:00Z",
  revision_id: tournamentFixtureIds.pauseRevision,
  state,
  wave_id: tournamentFixtureIds.bo3Wave,
});

const withRecovery = (
  base: ParticipantSnapshot,
  overrides: {
    assignment?: ParticipantSnapshot["assignment"];
    lobby?: Partial<ParticipantSnapshot["lobby"]>;
    projection_revision?: number;
    series?: ParticipantSnapshot["series"];
    wave?: ParticipantSnapshot["wave"];
  } = {},
): ParticipantSnapshot => {
  const projectionRevision = overrides.projection_revision ?? base.projection_revision;
  const lobby = {
    ...base.lobby,
    ...overrides.lobby,
    projection_revision: projectionRevision,
  };
  return {
    ...base,
    assignment: overrides.assignment === undefined ? base.assignment : overrides.assignment,
    lobby,
    next_cursor: {
      ...base.next_cursor,
      projection_revision: projectionRevision,
    },
    projection_revision: projectionRevision,
    series: overrides.series === undefined ? base.series : overrides.series,
    wave: overrides.wave === undefined ? base.wave : overrides.wave,
  };
};

const readyRecovery = (
  base: ParticipantSnapshot,
  revision = 9,
  windowId: string = tournamentFixtureIds.readyWindow,
  attemptId: string = tournamentFixtureIds.attempt,
  windowState: "open" | "expired" | "consumed" = "open",
): ParticipantSnapshot => {
  const wave = base.wave;
  if (wave === null) {
    throw new Error("participant fixture requires a wave");
  }
  return withRecovery(base, {
    assignment: base.assignment === null
      ? null
      : {
          ...base.assignment,
          attempt_id: attemptId,
          context: {
            ...base.assignment.context,
            effective_deadline: null,
            game_state: "ready",
            started_at: null,
          },
          receipt: { ...base.assignment.receipt, attempt_id: attemptId },
        },
    projection_revision: revision,
    series: base.series === null
      ? null
      : {
          ...base.series,
          state: "active",
        },
    lobby: {
      ...base.lobby,
      required_action: "ready",
      status: "assigned",
    },
    wave: {
      ...wave,
      members: wave.members.map((member) => (
        member.participant_id === base.lobby.participant_id
          ? { ...member, ready: false }
          : member
      )),
      ready_window: readyWindow(windowId, windowState),
      paused_at: null,
      state: windowState === "open" ? "ready_window_open" : "ready_window_expired",
    },
  });
};

const installAccess = async (
  page: Page,
  fixtureSet: FixtureSet,
  lobbyStatus = 200,
): Promise<void> => {
  await page.route(`**${publicPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.public.tournament, 200);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    if (lobbyStatus === 200) {
      await fulfillJSON(route, fixtureSet.participant.lobby);
      return;
    }
    await fulfillProblem(
      route,
      lobbyStatus,
      lobbyStatus === 401 ? "Требуется вход" : "Доступ запрещен",
    );
  });
};

const installSnapshot = async (
  page: Page,
  response: ParticipantSnapshot | (() => ParticipantSnapshot),
): Promise<void> => {
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    await fulfillJSON(
      route,
      typeof response === "function" ? response() : response,
      200,
      withDate,
    );
  });
};

test("participant waiting, assignment, and direct task context stay on the tournament route", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = withRecovery(fixtureSet.participant.recovery, {
    assignment: null,
    lobby: {
      ...fixtureSet.participant.recovery.lobby,
      current_swiss_round: null,
      required_action: "wait",
      series: [],
      status: "waiting",
    },
    wave: fixtureSet.participant.recovery.wave,
  });

  await installAccess(page, fixtureSet);
  await installSnapshot(page, () => current);
  await page.goto(participantURL);

  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "waiting");
  await expect(page.getByText("Следующий матч еще не назначен")).toBeVisible();
  await expect(page.getByText("Соперник не назначен")).toBeVisible();

  const taskURL = "/tasks/assigned?from=tournament";
  const baseAssignment = fixtureSet.participant.recovery.assignment;
  const baseWave = fixtureSet.participant.recovery.wave;
  if (baseAssignment === null) {
    throw new Error("participant fixture requires an assignment");
  }
  if (baseWave === null) {
    throw new Error("participant fixture requires a wave");
  }
  current = withRecovery(fixtureSet.participant.recovery, {
    assignment: {
      ...baseAssignment,
      active_snapshot: {
        ...baseAssignment.active_snapshot,
        task_url: taskURL,
      },
    },
    projection_revision: 10,
    wave: {
      ...baseWave,
      members: baseWave.members.map((member) => (
        member.participant_id === fixtureSet.participant.recovery.lobby.participant_id
          ? { ...member, ready: false }
          : member
      )),
    },
  });
  await page.getByRole("button", { name: "Повторить синхронизацию" }).click();

  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "assigned");
  await expect(page.getByRole("link", { name: "Открыть назначенное задание" })).toHaveAttribute("href", taskURL);
  expect(page.url()).toContain(`/arena/participant/${tournamentId}`);
  await expect(page.getByText("Очки")).toBeVisible();
  await expect(page.getByText("Проверка участия")).toBeVisible();
  await expect(page.getByText("Категория")).toBeVisible();
  await expect(page.getByText("Требуемое действие")).toBeVisible();
});

test("participant opens readiness only for the current window and ignores the old attempt", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = readyRecovery(fixtureSet.participant.recovery);
  let readyRequests = 0;
  let releaseOldRequest: (() => void) | undefined;

  await installAccess(page, fixtureSet);
  await installSnapshot(page, () => current);
  await page.route(`**${participantReadyPath}`, async (route) => {
    readyRequests += 1;
    await new Promise<void>((resolve) => {
      releaseOldRequest = resolve;
    });
    await fulfillJSON(route, {
      command_id: tournamentFixtureIds.event,
      occurred_at: serverTimestamp,
      participant_id: tournamentFixtureIds.firstParticipant,
      type: "ready",
      wave_id: tournamentFixtureIds.bo3Wave,
      window_id: tournamentFixtureIds.readyWindow,
    });
  });
  await page.goto(participantURL);

  const readyButton = page.getByTestId("participant-ready-button");
  await expect(readyButton).toBeEnabled();
  const oldKey = await page.getByTestId("participant-player-panel").getAttribute("data-ready-key");
  const pendingClick = readyButton.click();
  await expect.poll(() => readyRequests).toBe(1);

  current = readyRecovery(
    fixtureSet.participant.recovery,
    10,
    "00000000-0000-4000-8000-000000000199",
    "00000000-0000-4000-8000-000000000198",
  );
  await page.getByRole("button", { name: "Повторить синхронизацию" }).click();
  await expect.poll(async () => page.getByTestId("participant-player-panel").getAttribute("data-ready-key")).not.toBe(oldKey);
  releaseOldRequest?.();
  await pendingClick;
  await expect(page.getByText("Готовность подтверждена сервером.")).toHaveCount(0);
  await expect(readyButton).toBeEnabled();
});

test("participant keeps the action disabled after the ready window closes", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await installAccess(page, fixtureSet);
  await installSnapshot(page, readyRecovery(fixtureSet.participant.recovery, 9, tournamentFixtureIds.readyWindow, tournamentFixtureIds.attempt, "expired"));
  await page.goto(participantURL);

  await expect(page.getByTestId("participant-ready-button")).toBeDisabled();
  await expect(page.getByText("Действие откроется только в активном окне готовности.")).toBeVisible();
});

test("participant renders bye, eliminated, and completed as separate states", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const baseWave = fixtureSet.participant.recovery.wave;
  if (baseWave === null) {
    throw new Error("participant fixture requires a wave");
  }

  await installAccess(page, fixtureSet);
  let current = withRecovery(fixtureSet.participant.recovery, {
    assignment: null,
    lobby: {
      ...fixtureSet.participant.recovery.lobby,
      required_action: "wait",
      series: [],
      status: "bye",
    },
    wave: {
      ...baseWave,
      members: baseWave.members.map((member) => ({ ...member, series_id: null })),
    },
  });
  await installSnapshot(page, () => current);
  await page.goto(participantURL);
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "bye");
  await expect(page.getByText("Проход без матча")).toBeVisible();

  current = withRecovery(fixtureSet.participant.recovery, {
    assignment: null,
    projection_revision: 10,
    lobby: {
      ...fixtureSet.participant.recovery.lobby,
      required_action: "none",
      series: [],
      state: "cancelled",
      status: "eliminated",
    },
  });
  await page.getByRole("button", { name: "Повторить синхронизацию" }).click();
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "eliminated");

  current = withRecovery(fixtureSet.participant.recovery, {
    projection_revision: 11,
    lobby: {
      ...fixtureSet.participant.recovery.lobby,
      required_action: "none",
      state: "completed",
      status: "completed",
    },
  });
  await page.getByRole("button", { name: "Повторить синхронизацию" }).click();
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "completed");
  await expect(page.getByText("Турнир завершен")).toBeVisible();
});

test("participant preserves 401 and 403 access states", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let lobbyStatus: 401 | 403 = 401;
  await page.route(`**${publicPath}`, async (route) => {
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    await fulfillProblem(
      route,
      lobbyStatus,
      lobbyStatus === 401 ? "Требуется вход" : "Доступ запрещен",
    );
  });
  await page.goto(participantURL);
  await expect(page.getByText("Требуется вход")).toBeVisible();
  await expect(page.getByRole("link", { name: "Войти как участник" })).toBeVisible();

  lobbyStatus = 403;
  await page.reload();
  await expect(
    page.getByTestId("arena-status").getByText("Доступ запрещен", { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Войти как участник" })).toHaveCount(0);
});

test("participant reports a ready rate limit without retrying the command", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let readyRequests = 0;
  await installAccess(page, fixtureSet);
  await installSnapshot(page, readyRecovery(fixtureSet.participant.recovery));
  await page.route(`**${participantReadyPath}`, async (route) => {
    readyRequests += 1;
    await fulfillProblem(route, 429, "Слишком много попыток");
  });
  await page.goto(participantURL);
  await page.getByTestId("participant-ready-button").click();
  await expect(page.getByText("Слишком много попыток. Повторите после паузы.")).toBeVisible();
  expect(readyRequests).toBe(1);
});

test("participant keeps the last valid view while recovery becomes stale", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let initial = true;
  await installAccess(page, fixtureSet);
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    await fulfillJSON(
      route,
      initial
        ? readyRecovery(fixtureSet.participant.recovery, 9)
        : readyRecovery(fixtureSet.participant.recovery, 8),
      200,
      withDate,
    );
    initial = false;
  });
  await page.goto(participantURL);
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "assigned");
  await page.getByRole("button", { name: "Повторить синхронизацию" }).click();
  await expect(page.getByText("Данные устарели", { exact: true })).toBeVisible();
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute("data-state", "assigned");
});

test("participant lobby remains keyboard reachable on the light 390px surface", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  await installAccess(page, fixtureSet);
  await installSnapshot(page, readyRecovery(fixtureSet.participant.recovery));
  await page.goto(participantURL);
  await page.getByRole("button", { name: "Светлая тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });

  await expect.poll(() => page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }))).toEqual({ body: 390, document: 390, viewport: 390 });
  await page.getByTestId("participant-ready-button").focus();
  await expect(page.getByTestId("participant-ready-button")).toBeFocused();
});
