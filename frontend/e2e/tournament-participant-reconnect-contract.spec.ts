import {
  expect,
  test,
  type Page,
  type Route,
  type WebSocketRoute,
} from "@playwright/test";

import {
  createTournamentFixtureSet,
  tournamentFixtureIds,
} from "./tournament/fixtures";
import type { components } from "../lib/shared/api/schema";

const tournamentId = tournamentFixtureIds.tournament;
const publicPath = `/api/v1/tournaments/${tournamentId}`;
const participantLobbyPath = `${publicPath}/participant/lobby`;
const participantSnapshotPath = `${publicPath}/participant/snapshot`;
const participantRealtimePath = `${publicPath}/participant/realtime`;
const participantReadyPath = `${publicPath}/participant/waves/${tournamentFixtureIds.bo3Wave}/ready`;
const participantURL = `/arena/participant/${tournamentId}`;

const serverTimestamp = "2026-09-20T10:00:00Z";
const frozenAt = "2026-09-20T10:00:10Z";
const reconnectDeadline = "2026-09-20T10:01:17Z";
const operatorResumeAt = "2026-09-20T10:00:30Z";
const resumedDeadline = "2026-09-20T10:03:30Z";
const expiredAt = "2026-09-20T10:02:00Z";
const activeDeadline = "2026-09-20T10:03:30Z";
const disconnectResultRevision = "00000000-0000-4000-8000-000000000195";

type Schema = components["schemas"];
type FixtureSet = ReturnType<typeof createTournamentFixtureSet>;
type ParticipantSnapshot = FixtureSet["participant"]["recovery"];
type Runtime = Schema["ParticipantRuntime"];
type Pause = Schema["ParticipantRuntimePause"];
type Presence = Schema["ParticipantRuntimePresence"];
type Reconnect = Schema["ParticipantRuntimeReconnect"];

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

const responseHeaders = { date: new Date(serverTimestamp).toUTCString() };

const presenceFor = (
  participantId: string,
  state: Schema["PresenceState"],
  updatedAt = frozenAt,
  presenceEpoch = 2,
  revision = 2,
): Presence => ({
  connected_at: "2026-09-20T09:59:00Z",
  disconnected_at: state === "disconnected" ? updatedAt : null,
  participant_id: participantId,
  presence_epoch: presenceEpoch,
  revision,
  state,
  updated_at: updatedAt,
});

const pauseFor = (
  reason: Schema["PauseReason"],
  overrides: Partial<Pause> = {},
): Pause => ({
  deadlines_suppressed: true,
  frozen_at: frozenAt,
  frozen_remaining_ms: 197_000,
  pause_id: tournamentFixtureIds.activePause,
  reason,
  reconnect_deadline: reason === "operator" ? null : reconnectDeadline,
  resumed_at: null,
  resumed_deadline: null,
  state: "active",
  ...overrides,
});

const reconnectFor = (
  participantId: string,
  id: string,
  overrides: Partial<Reconnect> = {},
): Reconnect => ({
  closed_at: null,
  continuation_number: 0,
  continued_from_id: null,
  deadline: reconnectDeadline,
  id,
  number: 1,
  opened_at: frozenAt,
  participant_id: participantId,
  pause_id: tournamentFixtureIds.activePause,
  presence_epoch: 2,
  revision: 1,
  state: "open",
  suspended_by_pause_id: null,
  updated_at: frozenAt,
  ...overrides,
});

const runtimeFor = (overrides: Partial<Runtime> = {}): Runtime => ({
  game_id: tournamentFixtureIds.bo3GameTwo,
  game_revision: 4,
  game_state: "active",
  pause: null,
  presence: [
    presenceFor(tournamentFixtureIds.firstParticipant, "connected", serverTimestamp, 1, 1),
    presenceFor(tournamentFixtureIds.secondParticipant, "connected", serverTimestamp, 1, 1),
  ],
  reconnect: [],
  result_reason: null,
  result_revision_id: null,
  winner_id: null,
  ...overrides,
});

type SnapshotOptions = Readonly<{
  assignment?: ParticipantSnapshot["assignment"];
  assignmentGameState?: Schema["GameState"];
  effectiveDeadline?: string | null;
  lobbyState?: Schema["TournamentState"];
  lobbyStatus?: Schema["ParticipantLobbyStatus"];
  projectionRevision?: number;
  requiredAction?: Schema["ParticipantLobbyRequiredAction"];
  runtime: Runtime | null;
  seriesResultRevisionId?: string | null;
  seriesState?: Schema["SeriesState"];
  seriesWinnerId?: string | null;
  wavePausedAt?: string | null;
  waveState?: Schema["WaveState"];
}>;

const snapshotFor = (
  fixtureSet: FixtureSet,
  options: SnapshotOptions,
): ParticipantSnapshot => {
  const base = fixtureSet.participant.recovery;
  if (base.assignment === null || base.series === null || base.wave === null) {
    throw new Error("participant fixture must include assignment, series, and wave");
  }

  const projectionRevision = options.projectionRevision ?? 9;
  const seriesState = options.seriesState ?? "active";
  const waveState = options.waveState ?? "active";
  const assignmentGameState = options.assignmentGameState ?? "active";
  const assignment = options.assignment === undefined
    ? {
        ...base.assignment,
        context: {
          ...base.assignment.context,
          effective_deadline: options.effectiveDeadline !== undefined
            ? options.effectiveDeadline
            : assignmentGameState === "active"
              ? activeDeadline
              : null,
          game_state: assignmentGameState,
        },
      }
    : options.assignment;

  return {
    ...base,
    assignment,
    lobby: {
      ...base.lobby,
      projection_revision: projectionRevision,
      required_action: options.requiredAction ?? "play",
      series: base.lobby.series.map((series) => ({ ...series, state: seriesState })),
      state: options.lobbyState ?? "swiss",
      status: options.lobbyStatus ?? "assigned",
    },
    next_cursor: {
      ...base.next_cursor,
      projection_revision: projectionRevision,
    },
    projection_revision: projectionRevision,
    runtime: options.runtime,
    series: {
      ...base.series,
      current_result_revision_id: options.seriesResultRevisionId ?? base.series.current_result_revision_id,
      state: seriesState,
      winner_id: options.seriesWinnerId ?? base.series.winner_id,
    },
    wave: {
      ...base.wave,
      paused_at: options.wavePausedAt !== undefined
        ? options.wavePausedAt
        : waveState === "paused"
          ? frozenAt
          : null,
      state: waveState,
    },
  };
};

const withGameOutcome = (
  snapshot: ParticipantSnapshot,
  outcome: Pick<Schema["Game"], "result_reason" | "result_revision_id" | "state" | "winner_id">,
): ParticipantSnapshot => {
  if (snapshot.series === null) {
    throw new Error("participant fixture must include series details");
  }

  return {
    ...snapshot,
    series: {
      ...snapshot.series,
      slots: snapshot.series.slots.map((slot) => ({
        ...slot,
        attempts: slot.attempts.map((attempt) =>
          attempt.id === tournamentFixtureIds.bo3GameTwo
            ? { ...attempt, ...outcome }
            : attempt,
        ),
      })),
    },
  };
};

const installParticipantRoutes = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshotForRequest: () => ParticipantSnapshot,
  snapshotRequests: URL[],
  mutationRequests: string[],
  mockRealtime = true,
): Promise<void> => {
  await page.addInitScript(() => {
    document.cookie = "tpm_player_csrf=participant-reconnect-contract; Path=/";
  });
  page.on("request", (request) => {
    if (request.method() !== "GET" && request.url().includes("/api/")) {
      mutationRequests.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });
  await page.route(`**${publicPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${participantLobbyPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, snapshotForRequest().lobby);
  });
  await page.route(`**${participantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    snapshotRequests.push(new URL(route.request().url()));
    await fulfillJSON(route, snapshotForRequest(), 200, responseHeaders);
  });
  if (mockRealtime) {
    await page.context().routeWebSocket(
      (url) => url.pathname === participantRealtimePath,
      async (socket: WebSocketRoute) => {
        socket.send(participantRealtimeFrame(
          tournamentFixtureIds.resume,
          snapshotForRequest().projection_revision,
          1,
        ));
      },
    );
  }
};

const openParticipant = async (
  page: Page,
  fixtureSet: FixtureSet,
  snapshotForRequest: () => ParticipantSnapshot,
  snapshotRequests: URL[],
  mutationRequests: string[],
  mockRealtime = true,
): Promise<void> => {
  await installParticipantRoutes(
    page,
    fixtureSet,
    snapshotForRequest,
    snapshotRequests,
    mutationRequests,
    mockRealtime,
  );
  await page.goto(participantURL);
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
};

const syncFromServer = async (
  page: Page,
  snapshotRequests: URL[],
): Promise<void> => {
  const before = snapshotRequests.length;
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect.poll(() => snapshotRequests.length).toBeGreaterThan(before);
};

const formattedDeadline = async (page: Page, deadline: string): Promise<string> =>
  page.evaluate((value) => new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(Date.parse(value)), deadline);

const officialResult = (page: Page) => page.locator('dl[aria-label="Официальный итог"]');

const participantRealtimeFrame = (
  resumeId: string,
  projectionRevision: number,
  sequence: number,
): string => JSON.stringify({
  type: "tournament.participant",
  payload: {
    envelope: {
      schema_version: 1,
      tournament_id: tournamentId,
      sequence,
      event_id: sequence === 1
        ? tournamentFixtureIds.pauseRevision
        : tournamentFixtureIds.scoreRevision,
      occurred_at: serverTimestamp,
      projection_revision: projectionRevision,
      resume_id: resumeId,
      participant: {},
    },
  },
});

test("FE-023 participant mutations refresh recovery without replacing the realtime connection", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const base = snapshotFor(fixtureSet, {
    assignmentGameState: "ready",
    effectiveDeadline: null,
    requiredAction: "ready",
    runtime: null,
    waveState: "ready_window_open",
  });
  if (base.assignment === null || base.wave === null) {
    throw new Error("participant fixture must include assignment and wave details");
  }
  const current: ParticipantSnapshot = {
    ...base,
    assignment: {
      ...base.assignment,
      context: {
        ...base.assignment.context,
        started_at: null,
      },
    },
    wave: {
      ...base.wave,
      members: base.wave.members.map((member) => (
        member.participant_id === base.lobby.participant_id
          ? { ...member, ready: false }
          : member
      )),
      ready_window: {
        consumed_at: null,
        deadline: reconnectDeadline,
        id: tournamentFixtureIds.readyWindow,
        opened_at: serverTimestamp,
        revision_id: tournamentFixtureIds.pauseRevision,
        state: "open",
        wave_id: tournamentFixtureIds.bo3Wave,
      },
    },
  };
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];
  const connections: URL[] = [];

  await page.context().routeWebSocket(
    (url) => url.pathname === participantRealtimePath,
    async (socket: WebSocketRoute) => {
      connections.push(new URL(socket.url()));
      socket.send(participantRealtimeFrame(tournamentFixtureIds.resume, 9, 1));
    },
  );
  await page.route(`**${participantReadyPath}`, async (route) => {
    await fulfillJSON(route, {
      command_id: tournamentFixtureIds.event,
      occurred_at: serverTimestamp,
      participant_id: tournamentFixtureIds.firstParticipant,
      type: "ready",
      wave_id: tournamentFixtureIds.bo3Wave,
      window_id: tournamentFixtureIds.readyWindow,
    });
  });

  await openParticipant(
    page,
    fixtureSet,
    () => current,
    snapshotRequests,
    mutationRequests,
    false,
  );
  await expect.poll(() => connections.length).toBe(1);
  const snapshotsBeforeMutation = snapshotRequests.length;
  await page.getByTestId("participant-ready-button").click();
  await expect(page.getByText("Готовность подтверждена.", { exact: true })).toBeVisible();
  await expect.poll(() => snapshotRequests.length).toBeGreaterThan(snapshotsBeforeMutation);
  await page.waitForTimeout(250);

  expect(connections).toHaveLength(1);
  expect(mutationRequests).toEqual([
    `POST ${participantReadyPath}`,
  ]);
});

test("FE-023 participant realtime owns presence and recovers with the confirmed resume id", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = snapshotFor(fixtureSet, {
    projectionRevision: 9,
    runtime: runtimeFor(),
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];
  const connections: URL[] = [];
  const clientFrames: string[] = [];
  const resumeId = tournamentFixtureIds.resume;

  await page.context().routeWebSocket(
    (url) => url.pathname === participantRealtimePath,
    async (socket: WebSocketRoute) => {
      connections.push(new URL(socket.url()));
      socket.onMessage((message) => clientFrames.push(String(message)));
      const sequence = connections.length;
      socket.send(participantRealtimeFrame(resumeId, 9 + sequence, sequence));
      if (sequence === 1) {
        await socket.close({ code: 1013, reason: "controlled reconnect" });
      }
    },
  );

  await openParticipant(
    page,
    fixtureSet,
    () => current,
    snapshotRequests,
    mutationRequests,
    false,
  );
  await expect.poll(() => connections.length).toBe(2);
  expect(connections[0]?.searchParams.has("resume_id")).toBe(false);
  expect(connections[1]?.searchParams.get("resume_id")).toBe(resumeId);
  await expect.poll(() => snapshotRequests.length).toBeGreaterThanOrEqual(3);
  expect(clientFrames).toEqual([]);
  expect(mutationRequests).toEqual([]);
});

test("FE-023 participant realtime does not replace an intentionally closed connection", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = snapshotFor(fixtureSet, {
    projectionRevision: 9,
    runtime: runtimeFor(),
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];
  const connections: URL[] = [];

  await page.context().routeWebSocket(
    (url) => url.pathname === participantRealtimePath,
    async (socket: WebSocketRoute) => {
      connections.push(new URL(socket.url()));
      socket.send(participantRealtimeFrame(tournamentFixtureIds.resume, 10, 1));
      await socket.close({ code: 1000, reason: "intentional close" });
    },
  );

  await openParticipant(
    page,
    fixtureSet,
    () => current,
    snapshotRequests,
    mutationRequests,
    false,
  );
  await expect.poll(() => connections.length).toBe(1);
  await page.waitForTimeout(1_100);

  expect(connections).toHaveLength(1);
  expect(mutationRequests).toEqual([]);
});

for (const failure of ["invalid frame", "rejection"] as const) {
  test(`participant realtime closes a browser socket after ${failure}`, async ({ page }) => {
    const fixtureSet = createTournamentFixtureSet();
    const current = snapshotFor(fixtureSet, { runtime: runtimeFor() });
    const snapshotRequests: URL[] = [];
    const mutationRequests: string[] = [];
    const sockets: WebSocketRoute[] = [];
    const closes: Array<{ code: number | undefined; reason: string | undefined }> = [];
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));

    await page.clock.install({ time: serverTimestamp });
    await page.routeWebSocket((url) => url.pathname === participantRealtimePath, (socket) => {
      sockets.push(socket);
      socket.onClose(async (code, reason) => {
        closes.push({ code, reason });
        // route.close also emits onClose; record only the client's request, not our acknowledgement.
        socket.onClose(() => {});
        await socket.close({ code, reason });
      });
      socket.send(participantRealtimeFrame(tournamentFixtureIds.resume, 9, sockets.length));
    });
    await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests, false);
    const fallback = page.getByTestId("participant-recovery-fallback");
    await expect(fallback).toHaveCount(0);
    expect(sockets).toHaveLength(1);
    const originalSocket = sockets[0]!;
    const snapshotsBeforeFailure = snapshotRequests.length;

    originalSocket.send(failure === "invalid frame" ? "{" : JSON.stringify({
      type: "tournament.rejected",
      code: "tournament.forbidden",
      message: "Participant access rejected",
    }));
    await expect.poll(() => closes.length + pageErrors.length).toBeGreaterThan(0);
    expect(pageErrors).toEqual([]);
    const reason = failure === "rejection" ? "participant realtime rejected" : "invalid participant realtime frame";
    expect(closes).toEqual([{ code: 1000, reason }]);
    await expect(fallback).toBeVisible();

    originalSocket.send(participantRealtimeFrame(tournamentFixtureIds.resume, 99, 99));
    await page.clock.fastForward(2_000);
    await expect(fallback).toBeVisible();
    expect(sockets).toHaveLength(1);
    expect(snapshotRequests).toHaveLength(snapshotsBeforeFailure);
    expect(closes).toEqual([{ code: 1000, reason }]);
    expect(pageErrors).toEqual([]);

    if (failure === "invalid frame") {
      // Invalid data stops automatic retries; an explicit retry can still recover.
      await page.getByRole("button", { name: "Повторить", exact: true }).click();
      await expect.poll(() => sockets.length).toBe(2);
      expect(new URL(sockets[1]!.url()).searchParams.get("resume_id")).toBe(tournamentFixtureIds.resume);
      await expect(fallback).toHaveCount(0);
      expect(snapshotRequests.length).toBeGreaterThan(snapshotsBeforeFailure);
    }
    expect(mutationRequests).toEqual([]);
    expect(pageErrors).toEqual([]);
  });
}

test("FE-023 disconnect freezes both participants and reconnect resumes only from a server snapshot", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = snapshotFor(fixtureSet, {
    projectionRevision: 9,
    runtime: null,
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeEnabled();

  current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 10,
    requiredAction: "play",
    runtime: runtimeFor({
      game_revision: 5,
      game_state: "paused",
      pause: pauseFor("disconnect"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected"),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", frozenAt, 1, 2),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision)],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  await syncFromServer(page, snapshotRequests);

  const runtimeStatus = page.getByTestId("participant-runtime-status");
  await expect(runtimeStatus).toHaveAttribute("data-paused", "true");
  await expect(runtimeStatus).toHaveAttribute("data-pause-reason", "disconnect");
  await expect(runtimeStatus).toHaveAttribute("data-deadlines-suppressed", "true");
  await expect(page.getByText("Отключение участника", { exact: true })).toBeVisible();
  await expect(page.getByTestId("participant-runtime-presence").locator("li")).toHaveCount(2);
  await expect(page.getByTestId(`participant-runtime-presence-${tournamentFixtureIds.firstParticipant}`))
    .toHaveAttribute("data-state", "disconnected");
  await expect(page.getByTestId(`participant-runtime-presence-${tournamentFixtureIds.secondParticipant}`))
    .toHaveAttribute("data-state", "connected");
  const deadlineText = await formattedDeadline(page, reconnectDeadline);
  await expect(page.getByTestId(`participant-runtime-reconnect-${tournamentFixtureIds.pauseRevision}`))
    .toContainText(`До ${deadlineText}`);
  await expect(page.getByTestId("participant-submit-button")).toBeDisabled();
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);

  await page.clock.fastForward(180_000);
  await expect(runtimeStatus).toHaveAttribute("data-paused", "true");
  await expect(page.getByTestId("participant-submit-button")).toBeDisabled();
  expect(mutationRequests).toEqual([]);

  current = snapshotFor(fixtureSet, {
    effectiveDeadline: resumedDeadline,
    lobbyState: "swiss",
    projectionRevision: 11,
    runtime: runtimeFor({
      game_revision: 6,
      game_state: "active",
      pause: pauseFor("disconnect", {
        deadlines_suppressed: false,
        reconnect_deadline: null,
        resumed_at: operatorResumeAt,
        resumed_deadline: resumedDeadline,
        state: "resumed",
      }),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "connected", operatorResumeAt, 3, 3),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", operatorResumeAt, 1, 3),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
        closed_at: operatorResumeAt,
        state: "reconnected",
        updated_at: operatorResumeAt,
      })],
    }),
    seriesState: "active",
    waveState: "active",
  });
  await syncFromServer(page, snapshotRequests);
  await expect(runtimeStatus).toHaveAttribute("data-paused", "false");
  await expect(runtimeStatus).toHaveAttribute("data-game-revision", "6");
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li"))
    .toHaveAttribute("data-state", "reconnected");
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeEnabled();
  expect(mutationRequests).toEqual([]);
});

test("FE-023 expiry waits for the server before publishing the disconnect winner and revision", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 20,
    runtime: runtimeFor({
      game_revision: 8,
      game_state: "paused",
      pause: pauseFor("disconnect"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected"),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", frozenAt, 1, 8),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision)],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);
  await expect(officialResult(page)).toHaveCount(0);

  await page.clock.fastForward(120_000);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-paused", "true");
  await expect(officialResult(page)).toHaveCount(0);
  expect(mutationRequests).toEqual([]);

  current = snapshotFor(fixtureSet, {
    assignmentGameState: "completed",
    lobbyState: "completed",
    lobbyStatus: "completed",
    projectionRevision: 21,
    requiredAction: "review_result",
    runtime: runtimeFor({
      game_revision: 9,
      game_state: "completed",
      pause: null,
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected", expiredAt, 2, 9),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", expiredAt, 1, 9),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
        closed_at: expiredAt,
        state: "expired",
        updated_at: expiredAt,
      })],
      result_reason: "operator_forfeit",
      result_revision_id: tournamentFixtureIds.resume,
      winner_id: tournamentFixtureIds.secondParticipant,
    }),
    seriesState: "completed",
    waveState: "completed",
  });
  await syncFromServer(page, snapshotRequests);

  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-game-state", "completed");
  await expect(page.getByTestId(`participant-runtime-reconnect-${tournamentFixtureIds.pauseRevision}`))
    .toHaveAttribute("data-state", "expired");
  await expect(officialResult(page)).toBeVisible();
  await expect(officialResult(page)).toContainText("Проигрыш по решению оператора");
  await expect(officialResult(page)).toContainText("Боб");
  expect(mutationRequests).toEqual([]);
});

test("FE-023 terminal technical loss is read from series when runtime is null", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = withGameOutcome(
    snapshotFor(fixtureSet, {
      assignment: null,
      lobbyState: "completed",
      lobbyStatus: "completed",
      projectionRevision: 30,
      requiredAction: "review_result",
      runtime: null,
      seriesResultRevisionId: tournamentFixtureIds.resume,
      seriesState: "completed",
      seriesWinnerId: tournamentFixtureIds.secondParticipant,
      waveState: "completed",
    }),
    {
      result_reason: "operator_forfeit",
      result_revision_id: disconnectResultRevision,
      state: "completed",
      winner_id: tournamentFixtureIds.secondParticipant,
    },
  );
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);

  const runtimeStatus = page.getByTestId("participant-runtime-status");
  await expect(runtimeStatus).toHaveAttribute("data-game-state", "unknown");
  await expect(runtimeStatus).toHaveAttribute("data-paused", "false");
  await expect(officialResult(page)).toBeVisible();
  await expect(officialResult(page)).toContainText("Игры");
  await expect(officialResult(page)).toContainText("Проигрыш по решению оператора");
  await expect(officialResult(page)).toContainText("Боб");
  expect(mutationRequests).toEqual([]);
});

test("FE-023 double disconnect stays paused and becomes a server-published technical loss", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 40,
    runtime: runtimeFor({
      game_revision: 10,
      game_state: "paused",
      pause: pauseFor("disconnect"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected"),
        presenceFor(tournamentFixtureIds.secondParticipant, "disconnected"),
      ],
      reconnect: [
        reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision),
        reconnectFor(tournamentFixtureIds.secondParticipant, tournamentFixtureIds.scoreRevision, { number: 2 }),
      ],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);
  await expect(page.getByTestId("participant-runtime-presence").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-pause-reason", "disconnect");
  await expect(officialResult(page)).toHaveCount(0);

  await page.clock.fastForward(120_000);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-paused", "true");
  await expect(officialResult(page)).toHaveCount(0);
  expect(mutationRequests).toEqual([]);

  current = snapshotFor(fixtureSet, {
    assignmentGameState: "void",
    lobbyState: "swiss",
    projectionRevision: 41,
    runtime: runtimeFor({
      game_revision: 11,
      game_state: "void",
      pause: null,
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected", expiredAt, 2, 11),
        presenceFor(tournamentFixtureIds.secondParticipant, "disconnected", expiredAt, 2, 11),
      ],
      reconnect: [
        reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
          closed_at: expiredAt,
          state: "expired",
          updated_at: expiredAt,
        }),
        reconnectFor(tournamentFixtureIds.secondParticipant, tournamentFixtureIds.scoreRevision, {
          closed_at: expiredAt,
          number: 2,
          state: "expired",
          updated_at: expiredAt,
        }),
      ],
      result_reason: "disconnect",
      result_revision_id: tournamentFixtureIds.resume,
      winner_id: null,
    }),
    seriesState: "replay_required",
    waveState: "active",
  });
  await syncFromServer(page, snapshotRequests);

  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-game-state", "void");
  await expect(officialResult(page)).toBeVisible();
  await expect(officialResult(page)).toContainText("Отключение участника");
  await expect(officialResult(page)).toContainText("Победитель не определен");
  expect(mutationRequests).toEqual([]);
});

test("FE-023 reconnect during an operator pause remains paused until the server resumes the game", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 50,
    runtime: runtimeFor({
      game_revision: 12,
      game_state: "paused",
      pause: pauseFor("operator"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected"),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", frozenAt, 1, 12),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
        suspended_by_pause_id: tournamentFixtureIds.activePause,
      })],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-pause-reason", "operator");

  current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 51,
    runtime: runtimeFor({
      game_revision: 13,
      game_state: "paused",
      pause: pauseFor("operator"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "connected", operatorResumeAt, 3, 13),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", operatorResumeAt, 1, 13),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
        closed_at: operatorResumeAt,
        state: "reconnected",
        suspended_by_pause_id: tournamentFixtureIds.activePause,
        updated_at: operatorResumeAt,
      })],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  await syncFromServer(page, snapshotRequests);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-paused", "true");
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li"))
    .toHaveAttribute("data-state", "reconnected");
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeDisabled();
  expect(mutationRequests).toEqual([]);

  current = snapshotFor(fixtureSet, {
    effectiveDeadline: resumedDeadline,
    lobbyState: "swiss",
    projectionRevision: 52,
    runtime: runtimeFor({
      game_revision: 14,
      game_state: "active",
      pause: pauseFor("operator", {
        deadlines_suppressed: false,
        resumed_at: operatorResumeAt,
        resumed_deadline: resumedDeadline,
        state: "resumed",
      }),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "connected", operatorResumeAt, 3, 14),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", operatorResumeAt, 1, 14),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
        closed_at: operatorResumeAt,
        state: "reconnected",
        suspended_by_pause_id: tournamentFixtureIds.activePause,
        updated_at: operatorResumeAt,
      })],
    }),
    seriesState: "active",
    waveState: "active",
  });
  await syncFromServer(page, snapshotRequests);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-paused", "false");
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeEnabled();
  expect(mutationRequests).toEqual([]);
});

test("FE-049 double disconnect during an operator pause survives refresh without a local result", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  let current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 70,
    runtime: runtimeFor({
      game_revision: 16,
      game_state: "paused",
      pause: pauseFor("operator"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected"),
        presenceFor(tournamentFixtureIds.secondParticipant, "disconnected"),
      ],
      reconnect: [
        reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
          suspended_by_pause_id: tournamentFixtureIds.activePause,
        }),
        reconnectFor(tournamentFixtureIds.secondParticipant, tournamentFixtureIds.scoreRevision, {
          number: 2,
          suspended_by_pause_id: tournamentFixtureIds.activePause,
        }),
      ],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);
  const runtimeStatus = page.getByTestId("participant-runtime-status");
  await expect(runtimeStatus).toHaveAttribute("data-paused", "true");
  await expect(runtimeStatus).toHaveAttribute("data-pause-reason", "operator");
  await expect(page.getByTestId("participant-runtime-presence").locator("li")).toHaveCount(2);
  await expect(page.getByTestId(`participant-runtime-presence-${tournamentFixtureIds.firstParticipant}`))
    .toHaveAttribute("data-state", "disconnected");
  await expect(page.getByTestId(`participant-runtime-presence-${tournamentFixtureIds.secondParticipant}`))
    .toHaveAttribute("data-state", "disconnected");
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeDisabled();
  await expect(officialResult(page)).toHaveCount(0);

  await page.clock.fastForward(240_000);
  await expect(runtimeStatus).toHaveAttribute("data-paused", "true");
  await expect(officialResult(page)).toHaveCount(0);

  await page.reload();
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(runtimeStatus).toHaveAttribute("data-game-revision", "16");
  await expect(runtimeStatus).toHaveAttribute("data-pause-reason", "operator");
  await expect(page.getByTestId("participant-runtime-presence").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeDisabled();
  await expect(officialResult(page)).toHaveCount(0);

  current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 71,
    runtime: runtimeFor({
      game_revision: 17,
      game_state: "paused",
      pause: pauseFor("operator"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "connected", operatorResumeAt, 3, 17),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", operatorResumeAt, 3, 17),
      ],
      reconnect: [
        reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision, {
          closed_at: operatorResumeAt,
          state: "reconnected",
          suspended_by_pause_id: tournamentFixtureIds.activePause,
          updated_at: operatorResumeAt,
        }),
        reconnectFor(tournamentFixtureIds.secondParticipant, tournamentFixtureIds.scoreRevision, {
          closed_at: operatorResumeAt,
          number: 2,
          state: "reconnected",
          suspended_by_pause_id: tournamentFixtureIds.activePause,
          updated_at: operatorResumeAt,
        }),
      ],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  await page.reload();
  await expect(runtimeStatus).toHaveAttribute("data-game-revision", "17");
  await expect(runtimeStatus).toHaveAttribute("data-paused", "true");
  await expect(runtimeStatus).toHaveAttribute("data-pause-reason", "operator");
  await expect(page.getByTestId(`participant-runtime-presence-${tournamentFixtureIds.firstParticipant}`))
    .toHaveAttribute("data-state", "connected");
  await expect(page.getByTestId(`participant-runtime-presence-${tournamentFixtureIds.secondParticipant}`))
    .toHaveAttribute("data-state", "connected");
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li")).toHaveCount(2);
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li").nth(0))
    .toHaveAttribute("data-state", "reconnected");
  await expect(page.getByTestId("participant-runtime-reconnect").locator("li").nth(1))
    .toHaveAttribute("data-state", "reconnected");
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
  await expect(page.getByTestId("participant-submit-button")).toBeDisabled();
  await expect(officialResult(page)).toHaveCount(0);
  expect(mutationRequests).toEqual([]);
});

test("FE-023 refresh preserves server authority and keeps the participant route usable in light mobile view", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const current = snapshotFor(fixtureSet, {
    assignmentGameState: "paused",
    lobbyState: "technical_pause",
    projectionRevision: 60,
    runtime: runtimeFor({
      game_revision: 15,
      game_state: "paused",
      pause: pauseFor("disconnect"),
      presence: [
        presenceFor(tournamentFixtureIds.firstParticipant, "disconnected"),
        presenceFor(tournamentFixtureIds.secondParticipant, "connected", frozenAt, 1, 15),
      ],
      reconnect: [reconnectFor(tournamentFixtureIds.firstParticipant, tournamentFixtureIds.pauseRevision)],
    }),
    seriesState: "technical_pause",
    wavePausedAt: frozenAt,
    waveState: "paused",
  });
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  await page.clock.install({ time: serverTimestamp });
  await openParticipant(page, fixtureSet, () => current, snapshotRequests, mutationRequests);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-game-revision", "15");
  await expect(page.getByTestId(`participant-runtime-reconnect-${tournamentFixtureIds.pauseRevision}`))
    .toHaveAttribute("data-state", "open");
  const deadlineText = await formattedDeadline(page, reconnectDeadline);
  await expect(page.getByTestId(`participant-runtime-reconnect-${tournamentFixtureIds.pauseRevision}`))
    .toContainText(`До ${deadlineText}`);

  await page.reload();
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-game-revision", "15");
  await expect(page.getByTestId(`participant-runtime-reconnect-${tournamentFixtureIds.pauseRevision}`))
    .toHaveAttribute("data-state", "open");
  await expect(page.getByTestId(`participant-runtime-reconnect-${tournamentFixtureIds.pauseRevision}`))
    .toContainText(`До ${deadlineText}`);

  await page.clock.fastForward(120_000);
  await expect(page.getByTestId("participant-runtime-status")).toHaveAttribute("data-paused", "true");
  await expect(officialResult(page)).toHaveCount(0);
  expect(mutationRequests).toEqual([]);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Светлая тема" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.getByTestId("participant-runtime-status")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  expect(mutationRequests).toEqual([]);
});
