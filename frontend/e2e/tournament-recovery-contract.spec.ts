import { spawn, type ChildProcessByStdio } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { type Readable } from "node:stream";

import { expect, test, type Page, type Route, type WebSocketRoute } from "@playwright/test";

import {
  ApiError,
  applyRoleRecoverySnapshot,
  applyOperatorRealtime,
  applyPublicRealtime,
  classifyRoleRecoveryError,
  isPublicRecoveryCursorConflict,
  isPublicRecoverySnapshot,
  openOperatorRealtime,
  openPublicRealtime,
  isOperatorRealtimeRejection,
  isPublicRealtimeGap,
  isPublicRealtimeRejection,
  isPublicRealtimeTerminal,
  operatorRealtimeUrl,
  openPublicRealtimeMessage,
  parsePublicRealtimeMessage,
  publicRealtimeUrl,
  parseOperatorRealtimeMessage,
  recoverRoleSnapshot,
  recoverPublicTournament,
} from "../lib/shared/api";
import {
  createServerCountdown,
  keepCurrentCountdownUnlessCandidateIsShorter,
  viewServerCountdown,
} from "../lib/features/tournament-live";
import { selectTheme } from "./support/common";
import {
  createTournamentFixtureSet,
  operatorSnapshot,
  participantRecovery,
  publicRecovery,
  publicRecoveryWithRoster,
  tournamentFixtureIds,
} from "./tournament/fixtures";

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, "e2e/fixtures/tournament-live");
const nextBinary = resolve(frontendRoot, "node_modules/.bin/next");
const fixtureHost = "127.0.0.1";
const staleServerTimestamp = "2026-09-13T09:59:59Z";
const staleDeadline = "2026-09-13T10:01:30Z";
const newerServerTimestamp = "2026-09-13T10:00:01Z";
const newerDeadline = "2026-09-13T10:01:00Z";

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = "";
let fixtureOutput = "";

const setTheme = async (page: Page, theme: "Темная тема" | "Светлая тема"): Promise<void> => {
  await selectTheme(page, theme === "Темная тема" ? "dark" : "light");
};

const wait = (durationMs: number) => new Promise<void>((resolveWait) => {
  setTimeout(resolveWait, durationMs);
});

const reservePort = async (): Promise<number> => new Promise((resolvePort, reject) => {
  const server = createServer();
  server.once("error", reject);
  server.listen(0, fixtureHost, () => {
    const address = server.address();
    if (!address || typeof address === "string") {
      server.close();
      reject(new Error("Не удалось зарезервировать порт live fixture"));
      return;
    }
    const port = address.port;
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolvePort(port);
    });
  });
});

const startFixture = async (): Promise<void> => {
  const port = await reservePort();
  fixtureURL = `http://${fixtureHost}:${port}`;
  fixtureOutput = "";
  const fixtureChild = spawn(
    nextBinary,
    ["dev", "--hostname", fixtureHost, "--port", String(port)],
    {
      cwd: fixtureRoot,
      detached: true,
      env: {
        NEXT_TELEMETRY_DISABLED: "1",
        NODE_ENV: "development",
        PATH: process.env.PATH ?? "/usr/bin:/bin",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  fixtureProcess = fixtureChild;
  fixtureChild.stdout.on("data", (chunk) => {
    fixtureOutput = `${fixtureOutput}${chunk.toString()}`.slice(-6_000);
  });
  fixtureChild.stderr.on("data", (chunk) => {
    fixtureOutput = `${fixtureOutput}${chunk.toString()}`.slice(-6_000);
  });

  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    if (fixtureChild.exitCode !== null) {
      throw new Error(`Live fixture завершился до запуска.\n${fixtureOutput}`);
    }
    try {
      if ((await fetch(fixtureURL)).ok) {
        return;
      }
    } catch {
      // Next.js еще не занял loopback-порт.
    }
    await wait(250);
  }
  throw new Error(`Live fixture не запустился.\n${fixtureOutput}`);
};

const stopFixture = async (): Promise<void> => {
  const processToStop = fixtureProcess;
  fixtureProcess = undefined;
  if (!processToStop?.pid) {
    return;
  }
  try {
    process.kill(-processToStop.pid, "SIGTERM");
  } catch {
    processToStop.kill("SIGTERM");
  }
};

test.beforeAll(startFixture);
test.afterAll(stopFixture);

const tournamentId = "00000000-0000-4000-8000-000000000001";
const otherTournamentId = "00000000-0000-4000-8000-000000000002";
const seriesId = "00000000-0000-4000-8000-000000000010";
const revisionId = "00000000-0000-4000-8000-000000000011";
const firstEventId = "00000000-0000-4000-8000-000000000020";
const secondEventId = "00000000-0000-4000-8000-000000000021";
const resumeId = "00000000-0000-4000-8000-000000000030";

const arenaTournamentId = tournamentFixtureIds.tournament;
const arenaPublicPath = `/api/v1/tournaments/${arenaTournamentId}`;
const arenaPublicSnapshotPath = `${arenaPublicPath}/snapshot`;
const arenaParticipantLobbyPath = `${arenaPublicPath}/participant/lobby`;
const arenaParticipantSnapshotPath = `${arenaPublicPath}/participant/snapshot`;
const arenaParticipantRealtimePath = `${arenaPublicPath}/participant/realtime`;
const arenaOperatorSnapshotPath = `/api/v1/admin/tournaments/${arenaTournamentId}/snapshot`;
const adminRefreshPath = "/api/v1/admin/refresh";

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

const fulfillProblem = async (
  route: Route,
  status: number,
): Promise<void> => {
  await route.fulfill({
    status,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({
      detail: `Курсор отклонен с кодом ${status}`,
      status,
      title: "Курсор отклонен",
      type: "about:blank",
    }),
  });
};

const installArenaAccessRoutes = async (
  page: Page,
  fixtureSet: ReturnType<typeof createTournamentFixtureSet>,
): Promise<void> => {
  await page.route(`**${arenaPublicPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.public.tournament);
  });
  await page.route(`**${arenaParticipantLobbyPath}`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, fixtureSet.participant.lobby);
  });
};

const installOperatorWebSocketStub = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    type StubSocket = {
      close: (code?: number) => void;
      onclose: ((event: { code: number }) => void) | null;
      onerror: (() => void) | null;
      onmessage: ((event: { data: string }) => void) | null;
      onopen: (() => void) | null;
      readyState: number;
      sent: string[];
      url: string;
    };
    const sockets: StubSocket[] = [];
    class OperatorWebSocket implements StubSocket {
      static readonly CLOSING = 2;
      static readonly CLOSED = 3;
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      readonly sent: string[] = [];
      readyState = OperatorWebSocket.CONNECTING;
      onclose: ((event: { code: number }) => void) | null = null;
      onerror: (() => void) | null = null;
      onmessage: ((event: { data: string }) => void) | null = null;
      onopen: (() => void) | null = null;

      constructor(readonly url: string) {
        sockets.push(this);
        queueMicrotask(() => {
          this.readyState = OperatorWebSocket.OPEN;
          this.onopen?.();
        });
      }

      close(code = 1000): void {
        if (this.readyState === OperatorWebSocket.CLOSED) {
          return;
        }
        this.readyState = OperatorWebSocket.CLOSED;
        this.onclose?.({ code });
      }

      send(value: string): void {
        this.sent.push(value);
      }
    }

    const WebSocketProxy = new Proxy(NativeWebSocket, {
      construct(target, args) {
        const url = String(args[0] ?? "");
        if (
          (url.includes("/api/v1/admin/tournaments/") || url.includes("/api/v1/tournaments/")) &&
          url.includes("/realtime")
        ) {
          return new OperatorWebSocket(url);
        }
        return Reflect.construct(target, args);
      },
    });
    Object.defineProperty(window, "WebSocket", { configurable: true, value: WebSocketProxy });
    Object.defineProperty(window, "__operatorWebSocketControl", {
      configurable: true,
      value: {
        close: (index: number, code: number) => sockets[index]?.close(code),
        emit: (index: number, value: unknown) => {
          const socket = sockets[index];
          socket?.onmessage?.({ data: JSON.stringify(value) });
        },
        get: () => sockets.map((socket) => ({
          readyState: socket.readyState,
          sent: socket.sent,
          url: socket.url,
        })),
      },
    });
  });
};

const installParticipantWebSocketRejection = async (page: Page): Promise<void> => {
  await page.routeWebSocket(arenaParticipantRealtimePath, (socket) => {
    socket.send(JSON.stringify({
      code: "tournament.forbidden",
      message: "Participant access rejected",
      type: "tournament.rejected",
    }));
  });
};

const participantRecoveryWithDeadline = (
  projectionRevision: number,
  serverTimestamp: string,
  deadline: string,
) => {
  const recovery = participantRecovery(projectionRevision);
  if (recovery.wave === null) {
    throw new Error("Participant fixture must include a wave");
  }
  return {
    ...recovery,
    assignment: recovery.assignment === null
      ? null
      : {
          ...recovery.assignment,
          context: {
            ...recovery.assignment.context,
            effective_deadline: deadline,
            game_state: "active" as const,
          },
        },
    draft: {
      actions: [],
      current_action: "ban" as const,
      current_actor_id: tournamentFixtureIds.firstParticipant,
      first_participant_id: tournamentFixtureIds.firstParticipant,
      format: "bo3",
      id: tournamentFixtureIds.groupRevision,
      legal_categories: ["web", "crypto", "forensics"],
      pool: ["web", "crypto", "forensics"],
      revision: 1,
      second_participant_id: tournamentFixtureIds.secondParticipant,
      selected_categories: [],
      series_id: tournamentFixtureIds.bo3Series,
      state: "active",
      turn: 1,
      turn_deadline: deadline,
    },
    series: recovery.series === null
      ? null
      : {
          ...recovery.series,
          state: "active" as const,
        },
    wave: {
      ...recovery.wave,
      members: recovery.wave.members.map((member) => ({
        ...member,
        ready: false,
      })),
      paused_at: null,
      ready_window: {
        consumed_at: null,
        deadline,
        id: tournamentFixtureIds.readyWindow,
        opened_at: serverTimestamp,
        revision_id: tournamentFixtureIds.pauseRevision,
        state: "open",
        wave_id: tournamentFixtureIds.bo3Wave,
      },
      state: "active" as const,
    },
  };
};

const restSnapshot = (
  projectionRevision = 4,
  eventSequence = 7,
  snapshotTournamentId = tournamentId,
  displayName = "alice",
) => ({
  tournament: {
    tournament_id: snapshotTournamentId,
    projection_revision: projectionRevision,
    preset: "tournament_v1",
    state: "swiss",
    roster_size: 8,
    started_at: "2026-09-08T10:00:00Z",
    finished_at: null,
  },
  scoreboard: {
    tournament_id: snapshotTournamentId,
    projection_revision: projectionRevision,
    entries: [
      {
        rank: 1,
        display_name: displayName,
        points: 3,
        wins: 1,
        losses: 0,
        bye_count: 0,
        provisional_tie: false,
        qualification_status: "pending",
        buchholz: 2,
        effective_time_ms: 42000,
      },
    ],
  },
  bracket: {
    tournament_id: snapshotTournamentId,
    projection_revision: projectionRevision,
    matches: [
      {
        format: "bo1",
        stage: "semifinal",
        position: 1,
        first_display_name: "alice",
        second_display_name: "bob",
        score: { first_participant_wins: 1, second_participant_wins: 0 },
        scheduled_at: null,
        state: "active",
        winner_display_name: null,
      },
      {
        format: "bo1",
        stage: "semifinal",
        position: 2,
        first_display_name: "charlie",
        second_display_name: "dana",
        score: { first_participant_wins: 0, second_participant_wins: 0 },
        scheduled_at: null,
        state: "planned",
        winner_display_name: null,
      },
      {
        format: "bo3",
        stage: "final",
        position: 1,
        first_display_name: null,
        second_display_name: null,
        score: { first_participant_wins: 0, second_participant_wins: 0 },
        scheduled_at: null,
        state: "planned",
        winner_display_name: null,
      },
    ],
  },
  live_series: [
    {
      series_id: seriesId,
      format: "bo3",
      state: "active",
      first_display_name: "alice",
      second_display_name: "bob",
      score: { first_wins: 1, second_wins: 0 },
      current_game: null,
      current_game_position: 2,
      stage: "swiss",
      round_number: 1,
      scheduled_at: null,
    },
  ],
  official_results: [
    {
      revision_id: revisionId,
      series_id: seriesId,
      state: "completed",
      winner_display_name: "alice",
      score: { first_wins: 2, second_wins: 0 },
      recorded_at: "2026-09-08T10:05:00Z",
    },
  ],
  live_draft: null,
  swiss_rounds: [
    {
      bye: null,
      round_number: 1,
      state: "active",
    },
  ],
  next_cursor: {
    projection_revision: projectionRevision,
    event_sequence: eventSequence,
  },
});

const realtimeEnvelope = (
  sequence: number,
  projectionRevision: number,
  eventId = firstEventId,
  envelopeTournamentId = tournamentId,
  occurredAt = "2026-09-08T10:06:00Z",
) => ({
  schema_version: 1,
  tournament_id: envelopeTournamentId,
  sequence,
  event_id: eventId,
  occurred_at: occurredAt,
  projection_revision: projectionRevision,
  resume_id: resumeId,
  public: {
    revision: projectionRevision,
    last_sequence: sequence,
    tournament: {
      tournament_id: envelopeTournamentId,
      preset: "tournament_v1",
      state: "swiss",
      roster_size: 8,
      started_at: "2026-09-08T10:00:00Z",
      finished_at: null,
    },
    scoreboard: restSnapshot().scoreboard.entries,
    bracket: [
      {
        format: "bo1",
        stage: "semifinal",
        position: 1,
        first_display_name: "alice",
        second_display_name: "bob",
        score: { first_wins: 1, second_wins: 0 },
        scheduled_at: null,
        state: "active",
        winner_display_name: null,
      },
      {
        format: "bo1",
        stage: "semifinal",
        position: 2,
        first_display_name: "charlie",
        second_display_name: "dana",
        score: { first_wins: 0, second_wins: 0 },
        scheduled_at: null,
        state: "planned",
        winner_display_name: null,
      },
      {
        format: "bo3",
        stage: "final",
        position: 1,
        first_display_name: null,
        second_display_name: null,
        score: { first_wins: 0, second_wins: 0 },
        scheduled_at: null,
        state: "planned",
        winner_display_name: null,
      },
    ],
    live_series: restSnapshot().live_series,
    official_results: restSnapshot().official_results,
    swiss_rounds: restSnapshot().swiss_rounds,
  },
});

const publicRealtimeMessage = (
  sequence: number,
  projectionRevision: number,
  eventId = firstEventId,
  envelopeTournamentId = tournamentId,
) => ({
  type: "tournament.public",
  payload: {
    envelope: realtimeEnvelope(
      sequence,
      projectionRevision,
      eventId,
      envelopeTournamentId,
    ),
  },
});

const operatorRealtimeEnvelope = (
  sequence: number,
  projectionRevision: number,
  eventId = firstEventId,
  envelopeTournamentId = tournamentId,
) => ({
  type: "tournament.operator",
  payload: {
    envelope: {
      schema_version: 1,
      tournament_id: envelopeTournamentId,
      sequence,
      event_id: eventId,
      occurred_at: "2026-09-08T10:06:00Z",
      projection_revision: projectionRevision,
      resume_id: resumeId,
      operator: {
        tournament_id: envelopeTournamentId,
        revision: projectionRevision,
        last_sequence: sequence,
        waves: [],
        presence: [],
        replays: [],
        pause: null,
        audit_links: [],
        golden: [],
      },
    },
  },
});

test("fresh connection accepts one complete public recovery snapshot", () => {
  const snapshot = restSnapshot();
  expect(isPublicRecoverySnapshot(snapshot)).toBe(true);

  const state = recoverPublicTournament(snapshot);
  expect(state.tournamentId).toBe(tournamentId);
  expect(state.cursor).toEqual({ projection_revision: 4, event_sequence: 7 });
  expect(state.display.liveSeries).toHaveLength(1);
  expect(state.display.officialResults).toHaveLength(1);
});

test("FE-041 accepts redacted current game and live draft projections", () => {
  const snapshot = restSnapshot() as unknown as {
    live_series: Array<Record<string, unknown>>;
    live_draft: Record<string, unknown> | null;
  };
  snapshot.live_series[0].current_game = {
    category: "web",
    effective_deadline: "2026-09-08T10:03:00Z",
    finished_at: null,
    first_connection_status: "connected",
    position: 2,
    result_reason: null,
    second_connection_status: "unknown",
    started_at: "2026-09-08T10:00:00Z",
    state: "active",
    winner_display_name: null,
  };
  snapshot.live_draft = {
    actions: [],
    auto_action_pending: false,
    current_action: "ban",
    current_actor_display_name: "bob",
    current_turn: 1,
    first_actor_display_name: "alice",
    format: "bo3",
    pool: ["web", "crypto", "forensics", "reverse", "pwn"],
    projection_revision: 4,
    selected_categories: [],
    series_id: seriesId,
    state: "active",
    tournament_id: tournamentId,
    turn_deadline: "2026-09-08T10:02:00Z",
  };

  expect(isPublicRecoverySnapshot(snapshot)).toBe(true);

  const currentGame = snapshot.live_series[0].current_game;
  if (!currentGame || typeof currentGame !== "object") {
    throw new Error("Current game fixture is missing");
  }
  (currentGame as Record<string, unknown>).task_id = "private-task";
  expect(isPublicRecoverySnapshot(snapshot)).toBe(false);
});

test("FE-041 validates the optional authoritative solve time", () => {
  const withSolveTime = (solveTimeMs: unknown): Record<string, unknown> => {
    const snapshot = restSnapshot() as unknown as {
      live_series: Array<Record<string, unknown>>;
    };
    const series = snapshot.live_series[0];
    if (series === undefined) {
      throw new Error("Live series fixture is missing");
    }
    series.current_game = {
      category: "web",
      effective_deadline: null,
      finished_at: "2026-09-08T10:05:00Z",
      first_connection_status: "connected",
      position: 2,
      result_reason: "solved",
      second_connection_status: "unknown",
      started_at: "2026-09-08T10:00:00Z",
      state: "completed",
      winner_display_name: "alice",
    };
    const currentGame = series.current_game;
    (currentGame as Record<string, unknown>).solve_time_ms = solveTimeMs;
    return snapshot as unknown as Record<string, unknown>;
  };

  expect(isPublicRecoverySnapshot(restSnapshot())).toBe(true);
  expect(isPublicRecoverySnapshot(withSolveTime(42_000))).toBe(true);
  expect(isPublicRecoverySnapshot(withSolveTime(0))).toBe(true);
  expect(isPublicRecoverySnapshot(withSolveTime(-1))).toBe(false);

  const withUnknownField = withSolveTime(42_000);
  const currentGame = (withUnknownField.live_series as Array<Record<string, unknown>>)[0]?.current_game;
  if (!currentGame || typeof currentGame !== "object") {
    throw new Error("Current game fixture is missing");
  }
  (currentGame as Record<string, unknown>).unexpected = true;
  expect(isPublicRecoverySnapshot(withUnknownField)).toBe(false);
});

test("fresh WebSocket connection accepts an initial zero sequence snapshot", () => {
  const state = openPublicRealtime(realtimeEnvelope(0, 1));

  expect(state.cursor).toEqual({ projection_revision: 1, event_sequence: 0 });
  expect(state.display.liveSeries).toHaveLength(1);
  expect(state.resumeId).toBe(resumeId);
  expect(state.serverTimestamp).toBe("2026-09-08T10:06:00Z");
});

test("a full realtime snapshot safely closes a missed sequence gap", () => {
  const state = recoverPublicTournament(restSnapshot(4, 7));
  const applied = applyPublicRealtime(state, realtimeEnvelope(10, 5));

  expect(applied.outcome).toBe("applied");
  expect(applied.state.cursor).toEqual({ projection_revision: 5, event_sequence: 10 });
  expect(applied.state.resumeId).toBe(resumeId);
});

test("reconnect keeps the durable resume identity for the same tournament", () => {
  const connected = applyPublicRealtime(
    recoverPublicTournament(restSnapshot(4, 7)),
    realtimeEnvelope(8, 4),
  ).state;
  const recovered = recoverPublicTournament(restSnapshot(5, 9), connected);

  expect(recovered.resumeId).toBe(resumeId);
  expect(recovered.cursor.event_sequence).toBe(9);
  expect(recovered.seenEventIds).toEqual([]);
});

test("a stale or equal request cursor is replaced by the returned full snapshot", () => {
  const staleClient = recoverPublicTournament(restSnapshot(2, 3));
  const fresh = recoverPublicTournament(restSnapshot(4, 7), staleClient);
  const equal = recoverPublicTournament(restSnapshot(4, 7), fresh);

  expect(fresh.cursor).toEqual({ projection_revision: 4, event_sequence: 7 });
  expect(equal.cursor).toEqual(fresh.cursor);
});

test("future cursor conflicts preserve requested and authoritative watermarks", () => {
  const problem = {
    type: "about:blank",
    title: "Conflict",
    status: 409,
    requested_cursor: { projection_revision: 6, event_sequence: 12 },
    current_cursor: { projection_revision: 5, event_sequence: 10 },
  };
  const error = new ApiError(new Response(null, { status: 409 }), problem);

  expect(isPublicRecoveryCursorConflict(error)).toBe(true);
});

test("duplicate and out-of-order realtime events cannot roll state backward", () => {
  const initial = openPublicRealtime(realtimeEnvelope(7, 4, firstEventId, tournamentId, "2026-09-08T10:06:00Z"));
  const applied = applyPublicRealtime(
    initial,
    realtimeEnvelope(8, 4, secondEventId, tournamentId, "2026-09-08T10:06:01Z"),
  );
  const duplicate = applyPublicRealtime(
    applied.state,
    realtimeEnvelope(9, 5, secondEventId, tournamentId, "2026-09-08T10:06:02Z"),
  );
  const outOfOrder = applyPublicRealtime(
    applied.state,
    realtimeEnvelope(6, 4, revisionId, tournamentId, "2026-09-08T10:05:59Z"),
  );

  expect(duplicate.outcome).toBe("duplicate");
  expect(duplicate.state).toBe(applied.state);
  expect(outOfOrder.outcome).toBe("out_of_order");
  expect(outOfOrder.state).toBe(applied.state);
  expect(applied.state.serverTimestamp).toBe("2026-09-08T10:06:01Z");
  expect(duplicate.state.serverTimestamp).toBe("2026-09-08T10:06:01Z");
  expect(outOfOrder.state.serverTimestamp).toBe("2026-09-08T10:06:01Z");
});

test("FE-012 public realtime requires its wrapper, keeps public data, and detects gaps", () => {
  const initial = openPublicRealtimeMessage(publicRealtimeMessage(7, 4), tournamentId);
  const applied = applyPublicRealtime(
    initial,
    parsePublicRealtimeMessage(publicRealtimeMessage(8, 5, secondEventId), tournamentId),
  );
  const gap = parsePublicRealtimeMessage(publicRealtimeMessage(12, 9, revisionId), tournamentId);
  const revisionJump = parsePublicRealtimeMessage(publicRealtimeMessage(7, 9, revisionId), tournamentId);
  const malformed = { ...publicRealtimeMessage(8, 5), type: "tournament.operator" };
  const revisionApplied = applyPublicRealtime(applied.state, revisionJump);

  expect(initial.tournamentId).toBe(tournamentId);
  expect(applied.outcome).toBe("applied");
  expect(applied.state.display.scoreboard).toHaveLength(1);
  expect(revisionApplied.outcome).toBe("applied");
  expect(revisionApplied.state.cursor).toEqual({ projection_revision: 9, event_sequence: 7 });
  expect(isPublicRealtimeGap(applied.state, gap)).toBe(true);
  expect(isPublicRealtimeGap(applied.state, revisionJump)).toBe(false);
  expect(isPublicRealtimeRejection({
    type: "tournament.rejected",
    code: "tournament.forbidden",
    message: "Публичный просмотр",
  })).toBe(true);
  expect(isPublicRealtimeRejection({
    type: "tournament.rejected",
    code: "anonymous_only",
    message: "Публичный просмотр",
  })).toBe(false);
  expect(() => parsePublicRealtimeMessage(malformed, tournamentId)).toThrow(
    "Invalid public realtime envelope",
  );
  expect(() => parsePublicRealtimeMessage(
    publicRealtimeMessage(9, 6, revisionId, otherTournamentId),
    tournamentId,
  )).toThrow("Public realtime envelope has the wrong tournament");
  expect(publicRealtimeUrl(
    tournamentId,
    resumeId,
    "https://public.example.test:8443/control",
  )).toBe(
    `wss://public.example.test:8443/api/v1/tournaments/${tournamentId}/realtime?resume_id=${resumeId}`,
  );
});

test("public recovery rejects private and cross-tournament fields", () => {
  const privateSnapshot = restSnapshot() as Record<string, unknown>;
  privateSnapshot.live_series = [
    {
      ...restSnapshot().live_series[0],
      participant_id: "00000000-0000-4000-8000-000000000099",
      task: { flag: "secret" },
    },
  ];

  expect(isPublicRecoverySnapshot(privateSnapshot)).toBe(false);
  expect(() => recoverPublicTournament(privateSnapshot)).toThrow(
    "Invalid public recovery snapshot",
  );

  const incompleteBracket = restSnapshot();
  incompleteBracket.bracket.matches = incompleteBracket.bracket.matches.slice(0, 2);
  expect(isPublicRecoverySnapshot(incompleteBracket)).toBe(false);
  expect(() => recoverPublicTournament(incompleteBracket)).toThrow(
    "Invalid public recovery snapshot",
  );

  const state = recoverPublicTournament(restSnapshot());
  expect(
    applyPublicRealtime(state, realtimeEnvelope(8, 4, firstEventId, otherTournamentId)).outcome,
  ).toBe("wrong_tournament");
});

test("one role-aware REST boundary recovers public, participant, and operator snapshots", () => {
  const timestamp = "2026-09-13T10:00:00Z";
  const publicState = recoverRoleSnapshot({
    role: "public",
    tournamentId,
    snapshot: restSnapshot(4, 7),
    serverTimestamp: timestamp,
  });
  const participantState = recoverRoleSnapshot({
    role: "participant",
    tournamentId: tournamentFixtureIds.tournament,
    snapshot: participantRecovery(4),
    serverTimestamp: timestamp,
  });
  const operatorState = recoverRoleSnapshot({
    role: "operator",
    tournamentId: tournamentFixtureIds.tournament,
    snapshot: operatorSnapshot(4),
    serverTimestamp: timestamp,
  });

  expect(publicState.outcome).toBe("initialized");
  expect(participantState.outcome).toBe("initialized");
  expect(operatorState.outcome).toBe("initialized");
  expect(participantState.state?.cursor).toEqual({
    projection_revision: 4,
    participant_view_revision: 5,
    event_sequence: 14,
  });
  expect(operatorState.state?.cursor).toEqual({
    projection_revision: 4,
    authority_revision: 4,
    audit_sequence: 14,
  });
});

test("operator recovery keeps projection, tournament, and execution authority revisions independent", () => {
  const snapshot = operatorSnapshot(4);
  snapshot.next_cursor.projection_revision = 1;
  snapshot.next_cursor.authority_revision = 1;

  const recovered = recoverRoleSnapshot({
    role: "operator",
    tournamentId: tournamentFixtureIds.tournament,
    snapshot,
    serverTimestamp: "2026-09-13T10:00:00Z",
  });

  expect(recovered.outcome).toBe("initialized");
  expect(recovered.state?.cursor).toEqual({
    projection_revision: 1,
    authority_revision: 1,
    audit_sequence: 14,
  });
});

test("an authoritative newer snapshot closes a recovery gap without rolling back", () => {
  const initial = recoverRoleSnapshot({
    role: "public",
    tournamentId,
    snapshot: restSnapshot(4, 7),
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  expect(initial.state).not.toBeNull();

  const newer = applyRoleRecoverySnapshot(initial.state, {
    role: "public",
    tournamentId,
    snapshot: restSnapshot(6, 12, tournamentId, "carol"),
    serverTimestamp: "2026-09-13T10:00:01Z",
  });
  const stale = applyRoleRecoverySnapshot(newer.state, {
    role: "public",
    tournamentId,
    snapshot: restSnapshot(5, 9, tournamentId, "bob"),
    serverTimestamp: "2026-09-13T10:00:02Z",
  });

  expect(newer.outcome).toBe("replaced");
  expect(newer.state?.cursor).toEqual({ projection_revision: 6, event_sequence: 12 });
  if (newer.state === null || !isPublicRecoverySnapshot(newer.state.snapshot)) {
    throw new Error("Expected a public recovery snapshot after replacement");
  }
  expect(newer.state.snapshot.scoreboard.entries[0]?.display_name).toBe("carol");
  expect(stale.outcome).toBe("stale");
  expect(stale.state).toBe(newer.state);
  expect(newer.state.snapshot.scoreboard.entries[0]?.display_name).toBe("carol");
});

test("role, tournament, unknown schema, malformed, duplicate, and future cursor stay explicit", () => {
  const initial = recoverRoleSnapshot({
    role: "public",
    tournamentId,
    snapshot: restSnapshot(4, 7),
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  expect(initial.state).not.toBeNull();
  const state = initial.state;

  const wrongRole = applyRoleRecoverySnapshot(state, {
    role: "participant",
    tournamentId,
    snapshot: participantRecovery(4),
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  const wrongTournament = applyRoleRecoverySnapshot(state, {
    role: "public",
    tournamentId: otherTournamentId,
    snapshot: restSnapshot(4, 7),
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  const unknownSchema = applyRoleRecoverySnapshot(state, {
    role: "public",
    tournamentId,
    snapshot: { schema_version: 99 },
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  const malformed = applyRoleRecoverySnapshot(state, {
    role: "public",
    tournamentId,
    snapshot: {},
    serverTimestamp: "not-a-time",
  });
  const duplicate = applyRoleRecoverySnapshot(state, {
    role: "public",
    tournamentId,
    snapshot: restSnapshot(4, 7),
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  const futureProblem = {
    type: "about:blank",
    title: "Conflict",
    status: 409,
    requested_cursor: { projection_revision: 6, event_sequence: 12 },
    current_cursor: { projection_revision: 5, event_sequence: 10 },
  };
  const future = classifyRoleRecoveryError(
    state,
    new ApiError(new Response(null, { status: 409 }), futureProblem),
  );

  expect(wrongRole.outcome).toBe("wrong_role");
  expect(wrongTournament.outcome).toBe("wrong_tournament");
  expect(unknownSchema.outcome).toBe("unknown_schema");
  expect(malformed.outcome).toBe("malformed");
  expect(duplicate.outcome).toBe("duplicate");
  expect(duplicate.changed).toBe(false);
  expect(duplicate.state).toBe(state);
  expect(future.outcome).toBe("future_cursor");
});

test("FE-011 operator realtime accepts only the wrapped strict DTO and fences stale events", () => {
  const initial = openOperatorRealtime(operatorRealtimeEnvelope(7, 4), tournamentId);
  const applied = applyOperatorRealtime(
    initial,
    operatorRealtimeEnvelope(8, 5, secondEventId),
  );
  const duplicate = applyOperatorRealtime(
    applied.state,
    operatorRealtimeEnvelope(9, 6, secondEventId),
  );
  const outOfOrder = applyOperatorRealtime(
    applied.state,
    operatorRealtimeEnvelope(6, 4, revisionId),
  );
  const wrongTournament = applyOperatorRealtime(
    applied.state,
    operatorRealtimeEnvelope(10, 7, revisionId, otherTournamentId),
  );
  const malformed = {
    ...operatorRealtimeEnvelope(10, 7, revisionId),
    type: "tournament.public",
  };
  const operatorBase = operatorRealtimeEnvelope(11, 8);
  const validProjection = {
    ...operatorBase,
    payload: {
      ...operatorBase.payload,
      envelope: {
        ...operatorBase.payload.envelope,
        operator: {
          ...operatorBase.payload.envelope.operator,
          waves: [{
            wave_id: seriesId,
            state: "active",
            window_deadline: "2026-09-08T10:10:00Z",
            members: [{
              participant_id: tournamentId,
              ready: true,
              readiness_revision: 2,
              series_id: seriesId,
            }],
          }],
          presence: [{
            participant_id: tournamentId,
            series_id: seriesId,
            state: "connected",
            presence_epoch: 1,
            updated_at: "2026-09-08T10:06:00Z",
          }],
          replays: [{
            series_id: seriesId,
            slot_id: revisionId,
            failed_game_id: firstEventId,
            replacement_game_id: secondEventId,
            replacement_wave_id: resumeId,
            state: "planned",
            revision: 1,
          }],
          pause: {
            pause_id: revisionId,
            state: "active",
            reason: "operator_requested",
            paused_at: "2026-09-08T10:06:00Z",
            graph_revision: 3,
            game_id: firstEventId,
            frozen_remaining_ms: 90_000,
            reconnect_deadline: "2026-09-08T10:10:00Z",
          },
          audit_links: [{
            audit_event_id: firstEventId,
            entity_kind: "game",
            entity_id: secondEventId,
            official_result_revision_id: revisionId,
          }],
        },
      },
    },
  };
  const pauseWithTaskId = {
    ...validProjection,
    payload: {
      ...validProjection.payload,
      envelope: {
        ...validProjection.payload.envelope,
        operator: {
          ...validProjection.payload.envelope.operator,
          pause: {
            ...validProjection.payload.envelope.operator.pause,
            task_id: "private",
          },
        },
      },
    },
  };
  const pauseWithSubmittedFlag = {
    ...validProjection,
    payload: {
      ...validProjection.payload,
      envelope: {
        ...validProjection.payload.envelope,
        operator: {
          ...validProjection.payload.envelope.operator,
          pause: {
            ...validProjection.payload.envelope.operator.pause,
            submitted_flag: true,
          },
        },
      },
    },
  };
  const pauseWithMismatchedTimer = {
    ...validProjection,
    payload: {
      ...validProjection.payload,
      envelope: {
        ...validProjection.payload.envelope,
        operator: {
          ...validProjection.payload.envelope.operator,
          pause: {
            ...validProjection.payload.envelope.operator.pause,
            game_id: null,
          },
        },
      },
    },
  };
  const pauseWithZeroRemaining = {
    ...validProjection,
    payload: {
      ...validProjection.payload,
      envelope: {
        ...validProjection.payload.envelope,
        operator: {
          ...validProjection.payload.envelope.operator,
          pause: {
            ...validProjection.payload.envelope.operator.pause,
            frozen_remaining_ms: 0,
          },
        },
      },
    },
  };

  expect(parseOperatorRealtimeMessage(operatorRealtimeEnvelope(7, 4), tournamentId).sequence).toBe(7);
  expect(parseOperatorRealtimeMessage(validProjection, tournamentId).operator.pause?.pause_id)
    .toBe(revisionId);
  expect(applied.outcome).toBe("applied");
  expect(applied.state.projectionRevision).toBe(5);
  expect(applied.state.resumeId).toBe(resumeId);
  expect(duplicate.outcome).toBe("duplicate");
  expect(duplicate.state).toBe(applied.state);
  expect(outOfOrder.outcome).toBe("out_of_order");
  expect(outOfOrder.state).toBe(applied.state);
  expect(wrongTournament.outcome).toBe("wrong_tournament");
  expect(wrongTournament.state).toBe(applied.state);
  expect(() => parseOperatorRealtimeMessage(malformed, tournamentId)).toThrow(
    "Invalid operator realtime envelope",
  );
  expect(() => parseOperatorRealtimeMessage(pauseWithTaskId, tournamentId)).toThrow(
    "Invalid operator realtime envelope",
  );
  expect(() => parseOperatorRealtimeMessage(pauseWithSubmittedFlag, tournamentId)).toThrow(
    "Invalid operator realtime envelope",
  );
  expect(() => parseOperatorRealtimeMessage(pauseWithMismatchedTimer, tournamentId)).toThrow(
    "Invalid operator realtime envelope",
  );
  expect(() => parseOperatorRealtimeMessage(pauseWithZeroRemaining, tournamentId)).toThrow(
    "Invalid operator realtime envelope",
  );
  expect(isOperatorRealtimeRejection({
    type: "tournament.rejected",
    code: "tournament.forbidden",
    message: "Недостаточно прав",
  })).toBe(true);
  expect(isOperatorRealtimeRejection({
    type: "tournament.rejected",
    code: "tournament.forbidden",
    message: "",
  })).toBe(false);
});

test("FE-011 operator realtime URL is a read-only admin websocket endpoint", () => {
  expect(operatorRealtimeUrl(
    tournamentId,
    resumeId,
    "https://admin.example.test:8443/control",
  )).toBe(
    `wss://admin.example.test:8443/api/v1/admin/tournaments/${tournamentId}/realtime?resume_id=${resumeId}`,
  );
  expect(operatorRealtimeUrl(
    tournamentId,
    null,
    "http://127.0.0.1:8080",
  )).toBe(
    `ws://127.0.0.1:8080/api/v1/admin/tournaments/${tournamentId}/realtime`,
  );
});

test("invalid cursor responses stay distinct from a future public cursor", () => {
  const initial = recoverRoleSnapshot({
    role: "participant",
    tournamentId: tournamentFixtureIds.tournament,
    snapshot: participantRecovery(4),
    serverTimestamp: "2026-09-13T10:00:00Z",
  });
  const revisionProblem = {
    type: "about:blank",
    title: "Conflict",
    status: 409,
    expected_revision: 3,
    current_revision: 4,
  };
  const error = new ApiError(new Response(null, { status: 409 }), revisionProblem);
  const invalidQuery = new ApiError(new Response(null, { status: 400 }), {
    type: "about:blank",
    title: "Bad Request",
    status: 400,
  });

  const classified = classifyRoleRecoveryError(initial.state, error);
  const invalidQueryClassified = classifyRoleRecoveryError(initial.state, invalidQuery);

  expect(classified.outcome).toBe("invalid_cursor");
  expect(classified.changed).toBe(false);
  expect(classified.state).toBe(initial.state);
  expect(invalidQueryClassified.outcome).toBe("invalid_cursor");
  expect(invalidQueryClassified.state).toBe(initial.state);
});

test("role-aware recovery rejects a valid other-tournament snapshot without carrying resume identity", () => {
  const initial = recoverRoleSnapshot({
    role: "public",
    tournamentId,
    snapshot: restSnapshot(4, 7),
    serverTimestamp: "2026-09-13T10:00:00Z",
    resumeId,
  });
  const state = initial.state;
  expect(state).not.toBeNull();

  const foreignSnapshot = restSnapshot(5, 9, otherTournamentId);
  expect(isPublicRecoverySnapshot(foreignSnapshot)).toBe(true);

  const foreign = applyRoleRecoverySnapshot(state, {
    role: "public",
    tournamentId: otherTournamentId,
    snapshot: foreignSnapshot,
    serverTimestamp: "2026-09-13T10:00:01Z",
  });

  expect(foreign.outcome).toBe("wrong_tournament");
  expect(foreign.changed).toBe(false);
  expect(foreign.state).toBe(state);
  expect(foreign.state?.tournamentId).toBe(tournamentId);
  expect(foreign.state?.resumeId).toBe(resumeId);
});

test("server countdown ignores system time changes and awaits server at zero", async ({ page }) => {
  const countdown = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:00Z",
      deadline: "2026-09-13T10:00:05Z",
    },
    () => 1000,
  );
  expect(viewServerCountdown(countdown, 3000)).toEqual({
    remainingMs: 3000,
    status: "running",
    commandsEnabled: true,
  });
  expect(viewServerCountdown(countdown, 6000)).toEqual({
    remainingMs: 0,
    status: "awaiting_server",
    commandsEnabled: false,
  });

  await page.setContent(`<main><p id="status">running</p></main>`);
  const result = await page.evaluate(() => {
    let monotonic = 1000;
    const deadline = 5000;
    const receipt = monotonic;
    let remaining = deadline - monotonic;
    let visibilityRefreshes = 0;
    const refresh = (): void => {
      visibilityRefreshes += 1;
      remaining = Math.max(0, deadline - monotonic);
    };
    document.addEventListener("visibilitychange", refresh);
    const originalNow = Date.now;
    Date.now = () => originalNow() + 86_400_000;
    monotonic = 6000;
    document.dispatchEvent(new Event("visibilitychange"));
    Date.now = originalNow;
    return { remaining, receipt, visibilityRefreshes };
  });

  expect(result.receipt).toBe(1000);
  expect(result.remaining).toBe(0);
  expect(result.visibilityRefreshes).toBe(1);
});

test("countdown zero never creates an official result locally", () => {
  const countdown = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:00Z",
      deadline: "2026-09-13T10:00:00Z",
    },
    () => 1000,
  );
  const view = viewServerCountdown(countdown, 1000);

  expect(view.status).toBe("awaiting_server");
  expect(view.commandsEnabled).toBe(false);
  expect("winner" in view).toBe(false);
});

test("a refreshed server timestamp cannot extend the original deadline", () => {
  const beforeRefresh = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:00Z",
      deadline: "2026-09-13T10:00:05Z",
    },
    () => 1000,
  );
  const afterRefresh = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:03Z",
      deadline: "2026-09-13T10:00:05Z",
    },
    () => 2000,
  );

  expect(viewServerCountdown(beforeRefresh, 1000).remainingMs).toBe(5000);
  expect(viewServerCountdown(afterRefresh, 2000).remainingMs).toBe(2000);
});

test("an equal server timestamp cannot extend a countdown but a newer timestamp may change its deadline", () => {
  const current = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:00Z",
      deadline: "2026-09-13T10:00:05Z",
    },
    () => 0,
  );
  const equalTimestampWithLaterDeadline = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:00Z",
      deadline: "2026-09-13T10:01:00Z",
    },
    () => 4000,
  );
  const newerTimestampWithLaterDeadline = createServerCountdown(
    {
      serverTimestamp: "2026-09-13T10:00:01Z",
      deadline: "2026-09-13T10:01:00Z",
    },
    () => 4000,
  );

  expect(
    keepCurrentCountdownUnlessCandidateIsShorter(
      current,
      equalTimestampWithLaterDeadline,
      4000,
    ),
  ).toBe(current);
  expect(
    keepCurrentCountdownUnlessCandidateIsShorter(
      current,
      newerTimestampWithLaterDeadline,
      4000,
    ),
  ).toBe(newerTimestampWithLaterDeadline);
});

test("a hook re-render cannot extend countdown from an older server timestamp", async ({ page }) => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  const countdown = page.getByTestId("server-countdown");
  const countdownState = page.getByTestId("countdown-state");
  const readRemainingSeconds = async (): Promise<number> => {
    const text = await countdown.textContent();
    const [minutes, seconds] = (text ?? "0:00").split(":").map(Number);
    return (minutes * 60) + seconds;
  };

  await expect.poll(async () => {
    return readRemainingSeconds();
  }).toBeLessThan(30);
  const beforeSeconds = await readRemainingSeconds();

  await page.getByRole("button", { name: "Повторить устаревший ответ" }).click();
  await expect(countdownState).toHaveAttribute("data-server-timestamp", staleServerTimestamp);
  await expect(countdownState).toHaveAttribute("data-deadline", staleDeadline);
  await expect.poll(readRemainingSeconds).toBeLessThanOrEqual(beforeSeconds);
});

test("a newer server timestamp accepts a changed deadline", async ({ page }) => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  const countdown = page.getByTestId("server-countdown");
  const countdownState = page.getByTestId("countdown-state");

  await expect.poll(async () => {
    const text = await countdown.textContent();
    const [minutes, seconds] = (text ?? "0:00").split(":").map(Number);
    return (minutes * 60) + seconds;
  }).toBeLessThan(30);

  await page.getByRole("button", { name: "Повторить новый ответ" }).click();
  await expect(countdownState).toHaveAttribute("data-server-timestamp", newerServerTimestamp);
  await expect(countdownState).toHaveAttribute("data-deadline", newerDeadline);
  await expect.poll(async () => {
    const text = await countdown.textContent();
    const [minutes, seconds] = (text ?? "0:00").split(":").map(Number);
    return (minutes * 60) + seconds;
  }).toBeGreaterThan(30);
});

test("mounted live panel stays server-authoritative in both themes and mobile width", async ({ page }) => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Живое соревнование" })).toBeVisible();
  await expect(page.getByRole("status")).toHaveText("На связи");

  await expect.poll(async () => {
    const text = await page.getByTestId("server-countdown").textContent();
    return Number.parseInt(text?.split(":").at(-1) ?? "0", 10);
  }).toBeLessThan(30);

  const darkBackground = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(darkBackground).toBe("rgb(16, 20, 25)");

  await page.evaluate(() => {
    const originalNow = Date.now;
    const shiftedNow = Date.now() + 86_400_000;
    Date.now = () => shiftedNow;
    try {
      document.dispatchEvent(new Event("visibilitychange"));
    } finally {
      Date.now = originalNow;
    }
  });
  await expect(page.getByTestId("server-countdown")).not.toHaveText("0:00");

  const command = page.getByRole("button", { name: "Отправить команду" });
  await expect(command).toBeEnabled();
  await command.click();
  await expect(page.getByTestId("action-count")).toHaveText("Команд отправлено: 1");

  await page.getByRole("button", { name: "Показать устаревшее состояние" }).click();
  await expect(page.getByRole("status")).toHaveText("Данные устарели");
  await expect(command).toBeDisabled();
  await expect(page.getByText("Не удалось обновить данные. Повторите попытку.")).toBeVisible();

  await page.getByRole("button", { name: "Светлая" }).click();
  const lightBackground = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(lightBackground).toBe("rgb(245, 247, 250)");

  await page.setViewportSize({ width: 390, height: 844 });
  const commandBox = await command.boundingBox();
  expect(commandBox?.width).toBeGreaterThan(300);

  await page.getByRole("button", { name: "Завершить отсчет" }).click();
  await expect(page.getByRole("status")).toHaveText("Ожидаем подтверждения");
  await expect(command).toBeDisabled();
  await expect(page.getByText("Время истекло. Ожидаем результат.")).toBeVisible();
  await expect(page.getByText(/побед|техническое поражение/i)).toHaveCount(0);
});

test("FE-013 participant route renders the readiness deadline from the snapshot", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-13T10:00:00Z";
  const deadline = "2026-09-13T10:01:00Z";
  let snapshotURL = "";

  await page.clock.install({ time: "2026-09-14T10:00:00Z" });
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaParticipantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    snapshotURL = route.request().url();
    await fulfillJSON(
      route,
      participantRecoveryWithDeadline(9, serverTimestamp, deadline),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/participant/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const playerPanel = page.getByTestId("participant-player-panel");
  await expect(playerPanel).toBeVisible();
  const deadlineLabel = playerPanel.locator('[data-ready-window-state="open"]');
  await expect(deadlineLabel).toBeVisible();
  await expect(deadlineLabel).toContainText("До");
  await expect(playerPanel.getByTestId("participant-ready-button")).toBeEnabled();
  expect(new URL(snapshotURL).search).toBe("");
});

test("FE-013 participant route retries with no cursor after a cursor conflict", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const deadline = "2026-09-15T10:01:00Z";
  let cursorRejected = false;
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: serverTimestamp });
  await installArenaAccessRoutes(page, fixtureSet);
  await installParticipantWebSocketRejection(page);
  await page.route(`**${arenaParticipantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);

    if (requestURL.search === "") {
      await fulfillJSON(
        route,
        participantRecoveryWithDeadline(cursorRejected ? 10 : 9, serverTimestamp, deadline),
        { date: new Date(serverTimestamp).toUTCString() },
      );
      return;
    }

    cursorRejected = true;
    await fulfillProblem(route, 409);
  });

  await page.goto(`/arena/participant/${arenaTournamentId}`, {
    waitUntil: "domcontentloaded",
  });
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-recovery-fallback")).toBeVisible();
  snapshotRequests.length = 0;
  await page.getByRole("button", { name: "Повторить", exact: true }).click();

  await expect.poll(() => snapshotRequests.length).toBe(2);
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-projection-revision",
    "10",
  );
  expect(cursorRejected).toBe(true);
  expect(snapshotRequests[0]?.searchParams.get("cursor[projection_revision]")).toBe("9");
  expect(snapshotRequests[0]?.searchParams.get("cursor[participant_view_revision]")).toBe("5");
  expect(snapshotRequests[0]?.searchParams.get("cursor[event_sequence]")).toBe("14");
  expect(snapshotRequests[1]?.search).toBe("");
});

test("FE-013 participant route retries once without a cursor after an unknown schema", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const deadline = "2026-09-15T10:01:00Z";
  let initialLoaded = false;
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: serverTimestamp });
  await installArenaAccessRoutes(page, fixtureSet);
  await installParticipantWebSocketRejection(page);
  await page.route(`**${arenaParticipantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);

    if (requestURL.search !== "") {
      await fulfillJSON(
        route,
        { schema_version: 99 },
        { date: new Date(serverTimestamp).toUTCString() },
      );
      return;
    }

    await fulfillJSON(
      route,
      participantRecoveryWithDeadline(initialLoaded ? 10 : 9, serverTimestamp, deadline),
      { date: new Date(serverTimestamp).toUTCString() },
    );
    initialLoaded = true;
  });

  await page.goto(`/arena/participant/${arenaTournamentId}`, {
    waitUntil: "domcontentloaded",
  });
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-recovery-fallback")).toBeVisible();
  snapshotRequests.length = 0;

  await page.getByRole("button", { name: "Повторить", exact: true }).click();

  await expect.poll(() => snapshotRequests.length).toBe(2);
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-projection-revision",
    "10",
  );
  expect(snapshotRequests[0]?.searchParams.get("cursor[projection_revision]")).toBe("9");
  expect(snapshotRequests[1]?.search).toBe("");
});

test("FE-013 unknown schema retry stops after one fresh response and keeps stale state", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const deadline = "2026-09-15T10:01:00Z";
  let initialLoaded = false;
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: serverTimestamp });
  await installArenaAccessRoutes(page, fixtureSet);
  await installParticipantWebSocketRejection(page);
  await page.route(`**${arenaParticipantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);

    if (requestURL.search === "" && !initialLoaded) {
      await fulfillJSON(
        route,
        participantRecoveryWithDeadline(9, serverTimestamp, deadline),
        { date: new Date(serverTimestamp).toUTCString() },
      );
      initialLoaded = true;
      return;
    }

    await fulfillJSON(
      route,
      { schema_version: 99 },
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/participant/${arenaTournamentId}`, {
    waitUntil: "domcontentloaded",
  });
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-recovery-fallback")).toBeVisible();
  snapshotRequests.length = 0;

  await page.getByRole("button", { name: "Повторить", exact: true }).click();

  await expect.poll(() => snapshotRequests.length).toBe(2);
  await expect(page.getByTestId("participant-recovery-fallback")).toBeVisible();
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-projection-revision",
    "9",
  );
  expect(snapshotRequests[1]?.search).toBe("");
});

test("FE-013 mounted participant recovery exposes stale status while its deadline remains", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const deadline = "2026-09-15T10:01:00Z";
  let initialLoaded = false;

  await page.clock.install({ time: serverTimestamp });
  await installArenaAccessRoutes(page, fixtureSet);
  await installParticipantWebSocketRejection(page);
  await page.route(`**${arenaParticipantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    await fulfillJSON(
      route,
      participantRecoveryWithDeadline(initialLoaded ? 8 : 9, serverTimestamp, deadline),
      { date: new Date(serverTimestamp).toUTCString() },
    );
    if (requestURL.search === "") {
      initialLoaded = true;
    }
  });

  await page.goto(`/arena/participant/${arenaTournamentId}`, {
    waitUntil: "domcontentloaded",
  });
  await expect(page.getByTestId("participant-player-panel")).toBeVisible();
  await expect(page.getByTestId("participant-recovery-fallback")).toBeVisible();

  await page.getByRole("button", { name: "Повторить", exact: true }).click();

  await expect(page.getByTestId("participant-recovery-fallback")).toBeVisible();
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-state",
    "assigned",
  );
  await expect(page.getByTestId("participant-player-panel")).toHaveAttribute(
    "data-projection-revision",
    "9",
  );
  await expect(page.getByTestId("server-countdown")).toHaveCount(0);
});

test("FE-013 spectator route mounts public recovery and keeps only the public cursor", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);
    await fulfillJSON(
      route,
      publicRecovery(requestURL.search === "" ? 9 : 10),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const livePanel = page.getByRole("region", { name: "Трансляция соревнования" });
  await expect(livePanel.getByText("Трансляция", { exact: true })).toBeVisible();
  await expect(page.getByTestId("public-realtime-summary")).toHaveAttribute(
    "data-projection-revision",
    "9",
  );
  await expect.poll(() => snapshotRequests.length).toBeGreaterThan(0);
  const initialRequestCount = snapshotRequests.length;
  expect(snapshotRequests.every((requestURL) => requestURL.search === "")).toBe(true);

  await setTheme(page, "Темная тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await setTheme(page, "Светлая тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  const headingBox = await livePanel.boundingBox();
  expect(headingBox).not.toBeNull();
  if (headingBox === null) {
    throw new Error("Public recovery panel heading is missing from the mobile Arena route");
  }
  expect(headingBox.x).toBeGreaterThanOrEqual(0);
  expect(headingBox.x + headingBox.width).toBeLessThanOrEqual(390);

  await page.getByRole("button", { name: "Обновить трансляцию" }).click();
  await expect.poll(() => snapshotRequests.length).toBe(initialRequestCount + 1);
  const retryURL = snapshotRequests.at(-1);
  expect(retryURL?.searchParams.get("cursor[projection_revision]")).toBe("9");
  expect(retryURL?.searchParams.get("cursor[event_sequence]")).toBe("14");
  expect(retryURL?.searchParams.get("cursor[participant_view_revision]")).toBeNull();
  expect(retryURL?.searchParams.get("cursor[authority_revision]")).toBeNull();
  expect(retryURL?.searchParams.get("cursor[audit_sequence]")).toBeNull();
  await expect(page.getByTestId("public-realtime-summary")).toHaveAttribute(
    "data-projection-revision",
    "10",
  );
});

test("FE-013 public equal-cursor recovery refreshes the full match list without reload", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const snapshotRequests: URL[] = [];
  const initialSnapshot = publicRecovery(9, 14);
  initialSnapshot.bracket.matches = [];
  initialSnapshot.live_series = [];
  initialSnapshot.swiss_rounds = [];
  const refreshedSnapshot = publicRecovery(9, 14);
  refreshedSnapshot.bracket.matches = [];
  refreshedSnapshot.live_series = [
    {
      ...refreshedSnapshot.live_series[0]!,
      first_display_name: "Чарли",
      second_display_name: "Дана",
      series_id: tournamentFixtureIds.bo1Series,
      format: "bo1",
      state: "active",
      score: { first_wins: 0, second_wins: 0 },
    },
    {
      ...refreshedSnapshot.live_series[0]!,
      first_display_name: "Ева",
      second_display_name: "Федор",
      series_id: tournamentFixtureIds.bo3Series,
    },
  ];
  refreshedSnapshot.swiss_rounds = [{ round_number: 1, state: "active", bye: null }];

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);
    await fulfillJSON(
      route,
      requestURL.search === "" ? initialSnapshot : refreshedSnapshot,
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });
  const broadcast = page.getByTestId("tournament-broadcast");
  const initialRealtime = publicRealtimeMessage(14, 9, firstEventId, arenaTournamentId);
  initialRealtime.payload.envelope.public.bracket = [];
  initialRealtime.payload.envelope.public.live_series = [];
  initialRealtime.payload.envelope.public.swiss_rounds = [];
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(1);
  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, initialRealtime);
  await expect(page.getByTestId("public-realtime-summary")).toHaveAttribute("data-connection", "connected");
  await expect(broadcast.getByText("Матчи пока не опубликованы.", { exact: true })).toBeVisible();
  let navigations = 0;
  page.on("framenavigated", () => { navigations += 1; });

  const livePanel = page.getByRole("region", { name: "Трансляция соревнования" });
  await livePanel.getByRole("button", { name: "Обновить трансляцию" }).click();

  await expect.poll(() => snapshotRequests.length).toBe(2);
  await expect(broadcast.getByRole("button", { name: /Чарли.*Дана/ })).toBeVisible();
  expect(snapshotRequests.at(-1)?.searchParams.get("cursor[projection_revision]")).toBe("9");
  expect(snapshotRequests.at(-1)?.searchParams.get("cursor[event_sequence]")).toBe("14");
  expect(navigations).toBe(0);
});

test("FE-038 public match center keeps the selected server match in a direct link", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(
      route,
      publicRecovery(9),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast).toBeVisible();
  await expect(broadcast).toHaveAttribute("data-phase", "technical_pause");
  await expect(broadcast.getByTestId("broadcast-phase-title")).toHaveText("Техническая пауза");
  await expect(broadcast.getByRole("status")).toHaveCount(0);
  await expect(broadcast.getByRole("button", { name: /Квалификация.*Раунд 1/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(broadcast.getByText("Не объявлено", { exact: true })).toBeVisible();

  const bracketMatch = broadcast.getByRole("button", { name: /Полуфинал.*Матч 1/ });
  await bracketMatch.click();
  await expect(bracketMatch).toHaveAttribute("aria-pressed", "true");
  await expect.poll(() => new URL(page.url()).searchParams.get("match")).toBe(
    "bracket:semifinal:1",
  );

  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByTestId("tournament-broadcast").getByRole("button", {
    name: /Полуфинал.*Матч 1/,
  })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("heading", { name: "Алиса - Боб" })).toBeVisible();

  await broadcast.getByRole("tab", { name: "Плей-офф" }).click();
  await expect(broadcast.getByRole("tabpanel")).toContainText("Полуфинал");
  await expect(broadcast.getByTestId("server-countdown")).toHaveCount(0);
  await expect(broadcast.getByText(/серверного дедлайна/i)).toHaveCount(0);

  await setTheme(page, "Темная тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  if (process.env.IMPECCABLE_CAPTURE === "1") {
    const reviewDirectory = resolve(frontendRoot, "../.impeccable/review");
    await mkdir(reviewDirectory, { recursive: true });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.screenshot({
      fullPage: true,
      path: resolve(reviewDirectory, "desktop.png"),
    });
  }
  await setTheme(page, "Светлая тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  if (process.env.IMPECCABLE_CAPTURE === "1") {
    await page.screenshot({
      fullPage: true,
      path: resolve(frontendRoot, "../.impeccable/review/mobile.png"),
    });
  }
  const mobileBox = await broadcast.boundingBox();
  expect(mobileBox).not.toBeNull();
  if (mobileBox !== null) {
    expect(mobileBox.x).toBeGreaterThanOrEqual(0);
    expect(mobileBox.x + mobileBox.width).toBeLessThanOrEqual(390);
  }
});

test("FE-039 public scoreboard renders the complete server-owned zero-state roster", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(
      route,
      publicRecoveryWithRoster(16),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast).toBeVisible();
  const table = broadcast.getByRole("table", { name: "Публичная таблица соревнования" });
  await expect(table.getByRole("row")).toHaveCount(17);
  await expect(table.getByRole("row").nth(1)).toContainText("Участник 01");
  await expect(table.getByRole("row").nth(1)).toContainText("Ожидает решения");
  await expect(table.getByRole("row").nth(1)).toContainText("00:00");
  await expect(table.getByRole("row").nth(16)).toContainText("Участник 16");
  await expect(table.getByText("00:00", { exact: true })).toHaveCount(16);

  for (const theme of ["Темная тема", "Светлая тема"] as const) {
    await setTheme(page, theme);
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme",
      theme === "Темная тема" ? "dark" : "light",
    );
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const box = await broadcast.boundingBox();
  expect(box).not.toBeNull();
  if (box !== null) {
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
  }
});

test("FE-039 public scoreboard applies cutoff, Golden, bye, and correction from one revision", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const cutoffBase = publicRecoveryWithRoster(16, 9, 14);
  const cutoffSnapshot = {
    ...cutoffBase,
    scoreboard: {
      ...cutoffBase.scoreboard,
      entries: cutoffBase.scoreboard.entries.map((entry, index) => {
        if (index === 3 || index === 4) {
          return {
            ...entry,
            losses: 1,
            points: 2,
            provisional_tie: true,
            wins: 2,
          };
        }
        if (index === 5) {
          return {
            ...entry,
            bye_count: 1,
            losses: 1,
            points: 1,
          };
        }
        return entry;
      }),
    },
  };
  const resolvedBase = publicRecoveryWithRoster(16, 10, 15);
  const resolvedEntries = resolvedBase.scoreboard.entries.map((entry, index) => {
    if (index === 0) {
      return {
        ...entry,
        losses: 1,
        points: 2,
        qualification_status: "qualified" as const,
        wins: 2,
      };
    }
    if (index < 3) {
      return { ...entry, qualification_status: "qualified" as const };
    }
    if (index === 3) {
      return {
        ...entry,
        losses: 1,
        points: 2,
        provisional_tie: false,
        qualification_status: "eliminated" as const,
        rank: 5,
        wins: 2,
      };
    }
    if (index === 4) {
      return {
        ...entry,
        losses: 1,
        points: 2,
        provisional_tie: false,
        qualification_status: "qualified" as const,
        rank: 4,
        wins: 2,
      };
    }
    if (index === 5) {
      return {
        ...entry,
        bye_count: 1,
        losses: 1,
        points: 1,
        qualification_status: "eliminated" as const,
      };
    }
    return { ...entry, qualification_status: "eliminated" as const };
  });
  const resolvedSnapshot = {
    ...resolvedBase,
    scoreboard: {
      ...resolvedBase.scoreboard,
      entries: [...resolvedEntries].sort((first, second) => first.rank - second.rank),
    },
  };
  let currentSnapshot = cutoffSnapshot;

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(
      route,
      currentSnapshot,
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const table = page.getByRole("table", { name: "Публичная таблица соревнования" });
  const fourthAtCutoff = table.getByRole("row").filter({ hasText: "Участник 04" });
  const fifthAtCutoff = table.getByRole("row").filter({ hasText: "Участник 05" });
  const byeEntry = table.getByRole("row").filter({ hasText: "Участник 06" });
  await expect(fourthAtCutoff.getByRole("cell").nth(1)).toHaveText("2");
  await expect(fourthAtCutoff.getByRole("cell").nth(2)).toHaveText("2");
  await expect(fourthAtCutoff.getByRole("cell").nth(3)).toHaveText("1");
  await expect(fourthAtCutoff).toContainText("Тай-брейк не решен");
  await expect(fifthAtCutoff).toContainText("Тай-брейк не решен");
  await expect(byeEntry.getByRole("cell").nth(4)).toHaveText("1");

  currentSnapshot = resolvedSnapshot;
  await page.getByRole("button", { name: "Обновить трансляцию" }).click();

  const qualifiedAfterGolden = table.getByRole("row").filter({ hasText: "Участник 05" });
  const eliminatedAfterGolden = table.getByRole("row").filter({ hasText: "Участник 04" });
  const correctedEntry = table.getByRole("row").filter({ hasText: "Участник 01" });
  await expect(table.getByRole("row")).toHaveCount(17);
  await expect(qualifiedAfterGolden.getByRole("cell").nth(0)).toHaveText("4");
  await expect(qualifiedAfterGolden).toContainText("Прошел дальше");
  await expect(qualifiedAfterGolden).not.toContainText("Тай-брейк не решен");
  await expect(eliminatedAfterGolden.getByRole("cell").nth(0)).toHaveText("5");
  await expect(eliminatedAfterGolden).toContainText("Выбыл");
  await expect(eliminatedAfterGolden).not.toContainText("Тай-брейк не решен");
  await expect(correctedEntry.getByRole("cell").nth(1)).toHaveText("2");
  await expect(correctedEntry.getByRole("cell").nth(2)).toHaveText("2");
  await expect(correctedEntry.getByRole("cell").nth(3)).toHaveText("1");
  await expect(byeEntry.getByRole("cell").nth(4)).toHaveText("1");
  await expect(page.getByTestId("public-realtime-summary")).toHaveAttribute(
    "data-projection-revision",
    "10",
  );
});

test("FE-040 public Swiss history and Single Elimination follow server snapshots", async ({ page }) => {
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const base = publicRecoveryWithRoster(8, 11, 20);
  const names = ["Алиса", "Боб", "Чарли", "Дана", "Ева", "Федор", "Глеб", "Ирина"];
  const seriesIds = [
    "00000000-0000-4000-8000-000000000201",
    "00000000-0000-4000-8000-000000000202",
    "00000000-0000-4000-8000-000000000203",
    "00000000-0000-4000-8000-000000000204",
    "00000000-0000-4000-8000-000000000205",
  ];

  const scoreboard = base.scoreboard.entries.map((entry, index) => ({
    ...entry,
    display_name: names[index] ?? entry.display_name,
    qualification_status: index < 4 ? "qualified" as const : "eliminated" as const,
    rank: index + 1,
  }));
  const swissRounds = [
    { bye: null, round_number: 1, state: "completed" as const },
    {
      bye: { display_name: "Ева", points_awarded: 1 },
      round_number: 2,
      state: "active" as const,
    },
    { bye: null, round_number: 3, state: "planned" as const },
    { bye: null, round_number: 4, state: "superseded" as const },
  ];
  const swissSeries = [
    {
      ...base.live_series[0]!,
      series_id: seriesIds[0]!,
      first_display_name: "Алиса",
      second_display_name: "Боб",
      score: { first_wins: 2, second_wins: 0 },
      round_number: 1,
      state: "completed" as const,
    },
    {
      ...base.live_series[0]!,
      series_id: seriesIds[1]!,
      first_display_name: "Чарли",
      second_display_name: "Дана",
      score: { first_wins: 2, second_wins: 1 },
      round_number: 1,
      state: "completed" as const,
    },
    {
      ...base.live_series[0]!,
      series_id: seriesIds[2]!,
      first_display_name: "Алиса",
      second_display_name: "Чарли",
      score: { first_wins: 1, second_wins: 0 },
      round_number: 2,
      state: "active" as const,
    },
    {
      ...base.live_series[0]!,
      series_id: seriesIds[3]!,
      first_display_name: "Боб",
      second_display_name: "Дана",
      score: { first_wins: 0, second_wins: 0 },
      round_number: 2,
      state: "planned" as const,
    },
    {
      ...base.live_series[0]!,
      series_id: seriesIds[4]!,
      first_display_name: "Ева",
      second_display_name: "Федор",
      score: { first_wins: 0, second_wins: 0 },
      round_number: 4,
      state: "cancelled" as const,
    },
  ];

  const bracketFor = (
    finalScore: readonly [number, number],
    champion: string | null,
  ) => ({
    ...base.bracket,
    matches: [
      {
        first_display_name: "Алиса",
        second_display_name: "Боб",
        format: "bo1" as const,
        position: 1,
        score: { first_participant_wins: 1, second_participant_wins: 0 },
        scheduled_at: null,
        stage: "semifinal" as const,
        state: champion ? "completed" as const : "active" as const,
        winner_display_name: champion ? "Алиса" : null,
      },
      {
        first_display_name: "Чарли",
        second_display_name: "Дана",
        format: "bo1" as const,
        position: 2,
        score: { first_participant_wins: 1, second_participant_wins: 0 },
        scheduled_at: null,
        stage: "semifinal" as const,
        state: champion ? "completed" as const : "active" as const,
        winner_display_name: champion ? "Чарли" : null,
      },
      {
        first_display_name: champion ? "Алиса" : null,
        second_display_name: champion ? "Чарли" : null,
        format: "bo3" as const,
        position: 1,
        score: {
          first_participant_wins: finalScore[0]!,
          second_participant_wins: finalScore[1]!,
        },
        scheduled_at: null,
        stage: "final" as const,
        state: champion ? "completed" as const : "planned" as const,
        winner_display_name: champion,
      },
    ],
  });

  const snapshotFor = (
    projectionRevision: number,
    eventSequence: number,
    tournamentState: "swiss" | "playoffs" | "completed",
    finalScore: readonly [number, number],
    champion: string | null,
  ): ReturnType<typeof publicRecoveryWithRoster> => ({
    ...base,
    bracket: { ...bracketFor(finalScore, champion), projection_revision: projectionRevision },
    live_series: swissSeries,
    next_cursor: { event_sequence: eventSequence, projection_revision: projectionRevision },
    scoreboard: { ...base.scoreboard, entries: scoreboard, projection_revision: projectionRevision },
    swiss_rounds: swissRounds,
    tournament: {
      ...base.tournament,
      finished_at: champion ? "2026-09-15T10:20:00Z" : null,
      projection_revision: projectionRevision,
      state: tournamentState,
    },
  });

  const beforeFinal = snapshotFor(11, 20, "swiss", [0, 0], null);
  const finalTwoZero = snapshotFor(12, 21, "playoffs", [2, 0], "Алиса");
  const finalTwoOne = snapshotFor(13, 22, "completed", [2, 1], "Алиса");
  let currentSnapshot = beforeFinal;

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, createTournamentFixtureSet());
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, currentSnapshot, { date: new Date(serverTimestamp).toUTCString() });
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, {
    waitUntil: "domcontentloaded",
  });
  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast.getByRole("tab", { name: "Квалификация" })).toBeVisible();
  await expect(broadcast.getByRole("tab", { name: "Плей-офф" })).toBeVisible();

  await broadcast.getByRole("tab", { name: "Квалификация" }).click();
  await expect(broadcast.getByTestId("swiss-round-1")).toContainText("Завершена");
  await expect(broadcast.getByTestId("swiss-round-2")).toContainText("Идет");
  await expect(broadcast.getByTestId("swiss-round-3")).toContainText("Запланирована");
  await expect(broadcast.getByTestId("swiss-round-4")).toContainText("Заменена новой версией");
  await expect(broadcast.getByTestId("swiss-bye-2")).toContainText("Ева");
  await expect(broadcast.getByTestId("swiss-bye-2")).toContainText("+1 очко");
  await expect(broadcast.getByTestId("swiss-round-1")).toContainText("Алиса - Боб");
  await expect(broadcast.getByTestId("swiss-round-4")).toContainText("Отменена");

  await broadcast.getByRole("tab", { name: "Плей-офф" }).click();
  const playoff = broadcast.getByRole("tabpanel");
  await expect(playoff.getByRole("heading", { name: "Top 4" })).toBeVisible();
  await expect(playoff.locator('[data-testid^="playoff-bracket"]')).toHaveCount(2);
  await expect(playoff.getByTestId("playoff-final")).toContainText("Ожидается");
  await expect(playoff.getByTestId("playoff-final")).toContainText("BO3");
  await expect(playoff).not.toContainText("Нижняя сетка");

  const finalButton = playoff.getByTestId("playoff-final").locator("button[data-match-key]");
  await finalButton.focus();
  await expect(finalButton).toBeFocused();
  await expect(finalButton).toHaveAttribute("aria-pressed", "false");
  await finalButton.click();
  await expect(finalButton).toHaveAttribute("aria-pressed", "true");
  await expect.poll(() => new URL(page.url()).searchParams.get("match")).toBe("bracket:final:1");

  for (const theme of ["Темная тема", "Светлая тема"] as const) {
    await setTheme(page, theme);
    await expect(page.locator("html")).toHaveAttribute(
      "data-theme",
      theme === "Темная тема" ? "dark" : "light",
    );
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  currentSnapshot = finalTwoZero;
  await page.getByRole("button", { name: "Обновить трансляцию" }).click();
  await expect(broadcast.getByTestId("playoff-final")).toContainText("2:0");
  await expect(broadcast.getByTestId("playoff-final")).toContainText("Чемпион: Алиса");

  currentSnapshot = finalTwoOne;
  await page.getByRole("button", { name: "Обновить трансляцию" }).click();
  await expect(broadcast.getByTestId("playoff-final")).toContainText("2:1");
  await expect(broadcast.getByTestId("playoff-final")).toContainText("Чемпион: Алиса");
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("FE-038 public match center distinguishes every server tournament state in both themes", async ({ page }) => {
  type PublicState = ReturnType<typeof publicRecovery>["tournament"]["state"];
  const serverTimestamp = "2026-09-15T10:00:00Z";
  let currentState: PublicState = "registration";

  const snapshotForState = () => {
    const snapshot = publicRecovery(9);
    snapshot.tournament.state = currentState;
    snapshot.live_series[0]!.state = currentState === "registration"
      ? "planned"
      : currentState === "swiss"
        ? "active"
        : currentState === "technical_pause"
          ? "technical_pause"
          : currentState === "cancelled"
            ? "cancelled"
            : "completed";
    return snapshot;
  };

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await page.route(`**${arenaPublicPath}`, async (route) => {
    const snapshot = snapshotForState();
    await fulfillJSON(route, snapshot.tournament);
  });
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    await fulfillJSON(
      route,
      snapshotForState(),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  const cases: ReadonlyArray<readonly [PublicState, string, string]> = [
    ["registration", "waiting", "Соревнование ожидает старта"],
    ["swiss", "live", "Соревнование идет"],
    ["technical_pause", "technical_pause", "Техническая пауза"],
    ["cancelled", "cancelled", "Соревнование отменено"],
    ["completed", "completed", "Соревнование завершено"],
  ];

  for (const [serverState, phase, title] of cases) {
    currentState = serverState;
    await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });
    const broadcast = page.getByTestId("tournament-broadcast");
    await expect(broadcast).toHaveAttribute("data-phase", phase);
    await expect(broadcast.getByTestId("broadcast-phase-title")).toHaveText(title);

    await setTheme(page, "Темная тема");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await setTheme(page, "Светлая тема");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  }
});

test("FE-012 public route uses a snapshot-first stream and recovers sequence gaps", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const snapshotRequests: URL[] = [];
  const mutationRequests: string[] = [];

  page.on("request", (request) => {
    const requestURL = new URL(request.url());
    if (requestURL.pathname.startsWith("/api/") && request.method() !== "GET") {
      mutationRequests.push(`${request.method()} ${requestURL.pathname}`);
    }
  });

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);
    await fulfillJSON(
      route,
      publicRecovery(requestURL.search === "" ? 9 : 10),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const state = page.getByTestId("public-realtime-summary");
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(1);

  const initialSocket = await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { get: () => Array<{ url: string; sent: string[] }> };
    }).__operatorWebSocketControl;
    return control.get()[0];
  });
  expect(initialSocket?.url).toBe(
    `ws://127.0.0.1:${new URL(page.url()).port}/api/v1/tournaments/${arenaTournamentId}/realtime`,
  );
  expect(initialSocket?.sent).toEqual([]);

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, publicRealtimeMessage(15, 10, firstEventId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-ready", "true");
  await expect(state).toHaveAttribute("data-connection", "connected");
  await expect(state).toHaveAttribute("data-projection-revision", "10");
  await expect(state).toContainText("На связи");
  await expect(state).toContainText("Результаты");

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, publicRealtimeMessage(99, 99, revisionId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "10");

  await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { close: (index: number, code: number) => void };
    }).__operatorWebSocketControl;
    control.close(0, 1006);
  });
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(2);

  const reconnectSocket = await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { get: () => Array<{ url: string; sent: string[] }> };
    }).__operatorWebSocketControl;
    return control.get()[1];
  });
  expect(reconnectSocket?.url).toContain(`?resume_id=${resumeId}`);
  expect(reconnectSocket?.sent).toEqual([]);

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, publicRealtimeMessage(88, 88, revisionId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "10");

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, publicRealtimeMessage(1, 1, secondEventId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "1");
  await expect(state).toHaveAttribute("data-connection", "connected");

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, publicRealtimeMessage(2, 2, secondEventId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "1");

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, publicRealtimeMessage(0, 1, revisionId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "1");

  const requestCountBeforeGap = snapshotRequests.length;
  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, publicRealtimeMessage(3, 3, firstEventId, arenaTournamentId));
  await expect.poll(() => snapshotRequests.length).toBe(requestCountBeforeGap + 1);
  await expect(state).toHaveAttribute("data-projection-revision", "10");

  await setTheme(page, "Темная тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await setTheme(page, "Светлая тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  const summaryBox = await state.boundingBox();
  expect(summaryBox).not.toBeNull();
  if (summaryBox === null) {
    throw new Error("Public realtime summary is missing from the mobile Arena route");
  }
  expect(summaryBox.x).toBeGreaterThanOrEqual(0);
  expect(summaryBox.x + summaryBox.width).toBeLessThanOrEqual(390);
  expect(mutationRequests).toEqual([]);
});

for (const role of ["public", "operator"] as const) {
  for (const failure of ["invalid frame", "rejection"] as const) {
    test(`${role} realtime closes a browser socket after ${failure}`, async ({ page }) => {
      const fixtureSet = createTournamentFixtureSet();
      const snapshotPath = role === "public" ? arenaPublicSnapshotPath : arenaOperatorSnapshotPath;
      const realtimePath = snapshotPath.replace(/\/snapshot$/, "/realtime");
      const realtimeMessage = role === "public" ? publicRealtimeMessage : operatorRealtimeEnvelope;
      const snapshotRequests: URL[] = [];
      const sockets: WebSocketRoute[] = [];
      const closes: Array<{ code: number | undefined; reason: string | undefined }> = [];
      const pageErrors: string[] = [];
      page.on("pageerror", (error) => pageErrors.push(error.message));

      if (role === "public" && failure === "rejection") {
        await page.clock.install({ time: new Date("2026-09-15T10:00:00Z").getTime() - 60_000 });
        await page.clock.pauseAt("2026-09-15T10:00:00Z");
      } else {
        await page.clock.install({ time: "2026-09-15T10:00:00Z" });
      }
      await installArenaAccessRoutes(page, fixtureSet);
      await page.route(`**${snapshotPath}*`, async (route) => {
        expect(route.request().method()).toBe("GET");
        snapshotRequests.push(new URL(route.request().url()));
        const revision = 8 + snapshotRequests.length;
        await fulfillJSON(
          route,
          role === "public" ? publicRecovery(revision) : operatorSnapshot(revision),
          { date: new Date("2026-09-15T10:00:00Z").toUTCString() },
        );
      });
      await page.routeWebSocket((url) => url.pathname === realtimePath, (socket) => {
        sockets.push(socket);
        socket.onClose(async (code, reason) => {
          closes.push({ code, reason });
          // route.close also emits onClose; record only the client's request, not our acknowledgement.
          socket.onClose(() => {});
          await socket.close({ code, reason });
        });
        socket.send(JSON.stringify(realtimeMessage(
          14 + sockets.length,
          9 + sockets.length,
          sockets.length === 1 ? firstEventId : secondEventId,
          arenaTournamentId,
        )));
      });

      await page.goto(`/arena/${role === "public" ? "spectator" : role}/${arenaTournamentId}`);
      const state = page.getByTestId(`${role}-realtime-summary`);
      await expect(state).toHaveAttribute("data-connection", "connected");
      await expect(state).toHaveAttribute("data-projection-revision", "10");
      expect(sockets).toHaveLength(1);
      const originalSocket = sockets[0]!;
      const snapshotsBeforeFailure = snapshotRequests.length;

      originalSocket.send(failure === "invalid frame" ? "{" : JSON.stringify({
        type: "tournament.rejected",
        code: "tournament.forbidden",
        message: "Realtime access rejected",
      }));
      await expect.poll(() => closes.length + pageErrors.length).toBeGreaterThan(0);
      expect(pageErrors).toEqual([]);
      const reason = failure === "rejection"
        ? role === "public" ? "public realtime cleanup" : `${role} realtime rejected`
        : role === "public" ? "public realtime recovery required" : "invalid operator realtime frame";
      expect(closes).toEqual([{ code: 1000, reason }]);

      if (failure === "rejection") {
        if (role === "public") {
          await expect(state).toHaveAttribute("data-connection", "recovering");
          await expect(state).toHaveAttribute("data-refreshing", "true");
          await expect(state).toContainText("Периодическое обновление");
          await expect(state).toHaveAttribute("data-ready", "true");
        } else {
          await expect(state).toHaveAttribute("data-connection", "rejected");
        }
      } else {
        await expect.poll(() => sockets.length).toBe(2);
        await expect(state).toHaveAttribute("data-connection", "connected");
        await expect(state).toHaveAttribute("data-projection-revision", "11");
        expect(new URL(sockets[1]!.url()).searchParams.get("resume_id")).toBe(resumeId);
      }

      // A frame from the closed generation must not restore access or replace recovered state.
      originalSocket.send(JSON.stringify(realtimeMessage(99, 99, revisionId, arenaTournamentId)));
      await page.clock.fastForward(2_000);
      await expect(state).toHaveAttribute(
        "data-connection",
        failure === "rejection" && role === "public" ? "recovering" : failure === "rejection" ? "rejected" : "connected",
      );
      await expect(state).toHaveAttribute(
        "data-projection-revision",
        failure === "rejection" && role === "public" ? "10" : failure === "rejection" ? "10" : "11",
      );
      expect(sockets).toHaveLength(failure === "rejection" ? 1 : 2);
      expect(snapshotRequests).toHaveLength(
        snapshotsBeforeFailure + (role === "public" ? 1 : 0),
      );
      expect(closes).toEqual([{ code: 1000, reason }]);
      expect(pageErrors).toEqual([]);
    });
  }
}

test("FE-012 public realtime rejection is terminal and visible", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: new Date(serverTimestamp).getTime() - 60_000 });
  await page.clock.pauseAt(serverTimestamp);
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    snapshotRequests.push(new URL(route.request().url()));
    await fulfillJSON(
      route,
      publicRecovery(9),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const state = page.getByTestId("public-realtime-summary");
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(1);
  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, {
    type: "tournament.rejected",
    code: "tournament.forbidden",
    message: "Публичный канал отклонен",
  });
  await expect(state).toHaveAttribute("data-connection", "recovering");
  await expect(state).toHaveAttribute("data-refreshing", "true");
  await expect(state).toContainText("Периодическое обновление");
  await expect(state).toHaveAttribute("data-ready", "true");
  await expect.poll(() => snapshotRequests.length).toBe(2);
  const refreshRequest = snapshotRequests.at(-1);
  expect(refreshRequest?.search).toBe("");
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(1);
});

test("FE-050 public terminal frame recovers the cancelled snapshot", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);
    const snapshot = publicRecovery(requestURL.search === "" ? 9 : 10);
    if (requestURL.search !== "") {
      snapshot.tournament.state = "cancelled";
      snapshot.live_series[0]!.state = "cancelled";
    }
    await fulfillJSON(
      route,
      snapshot,
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });
  const state = page.getByTestId("public-realtime-summary");
  await expect.poll(() => snapshotRequests.length).toBeGreaterThan(0);
  const initialSnapshotRequestCount = snapshotRequests.length;
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(1);

  const terminal = {
    type: "tournament.terminal",
    payload: {
      schema_version: 1,
      tournament_id: arenaTournamentId,
      sequence: 10,
      event_id: secondEventId,
      occurred_at: serverTimestamp,
      state: "cancelled",
    },
  };
  expect(isPublicRealtimeTerminal(terminal, arenaTournamentId)).toBe(true);
  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, terminal);

  await expect.poll(() => snapshotRequests.length)
    .toBe(initialSnapshotRequestCount + 1);
  const recoveredRealtimeMessage = publicRealtimeMessage(
    11,
    10,
    secondEventId,
    arenaTournamentId,
  );
  recoveredRealtimeMessage.payload.envelope.public.tournament.state = "cancelled";
  recoveredRealtimeMessage.payload.envelope.public.live_series[0]!.state = "cancelled";
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(2);
  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, recoveredRealtimeMessage);
  await expect(state).toHaveAttribute("data-connection", "connected");
  await expect(state).toHaveAttribute("data-ready", "true");
  await expect(page.getByTestId("tournament-broadcast").getByTestId("broadcast-phase-title"))
    .toHaveText("Соревнование отменено");
  await page.waitForTimeout(250);
  const socketsAfterRecovery = await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
    }).__operatorWebSocketControl;
    return control?.get() ?? [];
  });
  expect(socketsAfterRecovery).toHaveLength(2);
  expect(snapshotRequests).toHaveLength(initialSnapshotRequestCount + 1);
});

test("FE-013 operator route mounts operator recovery and keeps only the operator cursor", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const snapshotRequests: URL[] = [];

  await page.clock.install({ time: serverTimestamp });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaOperatorSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    const requestURL = new URL(route.request().url());
    snapshotRequests.push(requestURL);
    await fulfillJSON(
      route,
      operatorSnapshot(requestURL.search === "" ? 9 : 10),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/operator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const livePanel = page.getByRole("region", { name: "Состояние соревнования" });
  await expect(livePanel.getByText("Оператор", { exact: true })).toBeVisible();
  await expect.poll(() => snapshotRequests.length).toBeGreaterThan(0);
  await expect(page.getByTestId("operator-realtime-summary")).toHaveAttribute(
    "data-projection-revision",
    "9",
  );
  const initialRequestCount = snapshotRequests.length;
  expect(snapshotRequests.every((requestURL) => requestURL.search === "")).toBe(true);

  await setTheme(page, "Темная тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await setTheme(page, "Светлая тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  const headingBox = await livePanel.boundingBox();
  expect(headingBox).not.toBeNull();
  if (headingBox === null) {
    throw new Error("Operator recovery panel heading is missing from the mobile Arena route");
  }
  expect(headingBox.x).toBeGreaterThanOrEqual(0);
  expect(headingBox.x + headingBox.width).toBeLessThanOrEqual(390);

  await page.getByRole("button", { name: "Повторить", exact: true }).click();
  await expect.poll(() => snapshotRequests.length).toBe(initialRequestCount + 1);
  const retryURL = snapshotRequests.at(-1);
  expect(retryURL?.searchParams.get("cursor[projection_revision]")).toBe("9");
  expect(retryURL?.searchParams.get("cursor[authority_revision]")).toBe("9");
  expect(retryURL?.searchParams.get("cursor[audit_sequence]")).toBe("14");
  expect(retryURL?.searchParams.get("cursor[event_sequence]")).toBeNull();
  expect(retryURL?.searchParams.get("cursor[participant_view_revision]")).toBeNull();
  await expect(page.getByTestId("operator-realtime-summary")).toHaveAttribute(
    "data-projection-revision",
    "10",
  );
});

test("FE-049 operator and spectator refresh preserve a double disconnect operator pause", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const operator = operatorSnapshot(70);
  const pauseGraph = operator.pause_graph;
  if (pauseGraph === null || pauseGraph.reconnect[0] === undefined) {
    throw new Error("Operator recovery fixture must include a pause graph and reconnect record");
  }
  pauseGraph.presence = pauseGraph.presence.map((entry) => ({
    ...entry,
    disconnected_at: "2026-09-20T10:00:10Z",
    state: "disconnected" as const,
    updated_at: "2026-09-20T10:00:10Z",
  }));
  const reconnectTemplate = pauseGraph.reconnect[0];
  pauseGraph.reconnect = [
    {
      ...reconnectTemplate,
      participant_id: tournamentFixtureIds.firstParticipant,
      suspended_by_pause_id: tournamentFixtureIds.activePause,
    },
    {
      ...reconnectTemplate,
      id: tournamentFixtureIds.scoreRevision,
      number: 2,
      participant_id: tournamentFixtureIds.secondParticipant,
      revision: 2,
      suspended_by_pause_id: tournamentFixtureIds.activePause,
    },
  ];
  expect(pauseGraph.presence.every((entry) => entry.state === "disconnected")).toBe(true);
  expect(pauseGraph.reconnect).toHaveLength(2);
  expect(pauseGraph.reconnect.map((entry) => entry.participant_id).sort()).toEqual([
    tournamentFixtureIds.firstParticipant,
    tournamentFixtureIds.secondParticipant,
  ].sort());
  expect(pauseGraph.reconnect.every((entry) => entry.suspended_by_pause_id === tournamentFixtureIds.activePause))
    .toBe(true);
  const publicSnapshot = publicRecovery(70, 70);
  publicSnapshot.official_results = [];
  publicSnapshot.tournament = {
    ...publicSnapshot.tournament,
    projection_revision: 70,
    state: "technical_pause",
  };
  publicSnapshot.live_series = publicSnapshot.live_series.map((series) => ({
    ...series,
    state: "technical_pause",
  }));

  await page.clock.install({ time: "2026-09-20T10:00:10Z" });
  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaOperatorSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, operator, { date: new Date("2026-09-20T10:00:10Z").toUTCString() });
  });
  await page.route(`**${arenaPublicSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(route, publicSnapshot, { date: new Date("2026-09-20T10:00:10Z").toUTCString() });
  });

  await page.goto(`/arena/operator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });
  const operatorRegion = page.getByRole("region", { name: "Управление соревнованием" });
  await expect(operatorRegion).toContainText("Техническая пауза");
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(operatorRegion).toContainText("Техническая пауза");

  await page.goto(`/arena/spectator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });
  const broadcast = page.getByTestId("tournament-broadcast");
  await expect(broadcast.getByTestId("broadcast-phase-title")).toHaveText("Техническая пауза");
  await expect(broadcast.getByText("Не объявлено", { exact: true })).toBeVisible();
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByTestId("tournament-broadcast").getByTestId("broadcast-phase-title"))
    .toHaveText("Техническая пауза");
  await expect(page.getByTestId("tournament-broadcast").getByText("Не объявлено", { exact: true }))
    .toBeVisible();
});

test("FE-011 operator route uses snapshot-first admin realtime and fences old sockets", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const mutationRequests: string[] = [];
  const authRefreshRequests: string[] = [];

  page.on("request", (request) => {
    const requestURL = new URL(request.url());
    if (requestURL.pathname.startsWith("/api/") && request.method() !== "GET") {
      const requestDescription = `${request.method()} ${requestURL.pathname}`;
      if (requestURL.pathname === adminRefreshPath) {
        authRefreshRequests.push(requestDescription);
      } else {
        mutationRequests.push(requestDescription);
      }
    }
  });

  await installOperatorWebSocketStub(page);
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${adminRefreshPath}`, async (route) => {
    expect(route.request().method()).toBe("POST");
    await route.fulfill({ status: 204, body: "" });
  });
  await page.route(`**${arenaOperatorSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(
      route,
      operatorSnapshot(9),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/operator/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });

  const state = page.getByTestId("operator-realtime-summary");
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(1);

  const initialSocket = await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { get: () => Array<{ url: string; sent: string[] }> };
    }).__operatorWebSocketControl;
    return control.get()[0];
  });
  expect(initialSocket?.url).toBe(
    `ws://127.0.0.1:${new URL(page.url()).port}/api/v1/admin/tournaments/${arenaTournamentId}/realtime`,
  );
  expect(initialSocket?.sent).toEqual([]);

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, operatorRealtimeEnvelope(15, 10, firstEventId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-ready", "true");
  await expect(state).toHaveAttribute("data-connection", "connected");
  await expect(state).toHaveAttribute("data-projection-revision", "10");
  await expect(state).toContainText("Соединение");
  await expect(state).toContainText("На связи");

  await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { close: (index: number, code: number) => void };
    }).__operatorWebSocketControl;
    control.close(0, 1006);
  });
  await expect.poll(async () => {
    const sockets = await page.evaluate(() => {
      const control = (window as unknown as {
        __operatorWebSocketControl?: { get: () => Array<{ url: string; sent: string[] }> };
      }).__operatorWebSocketControl;
      return control?.get() ?? [];
    });
    return sockets.length;
  }).toBe(2);

  const reconnectSocket = await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { get: () => Array<{ url: string; sent: string[] }> };
    }).__operatorWebSocketControl;
    return control.get()[1];
  });
  expect(reconnectSocket?.url).toContain(
    `?resume_id=${resumeId}`,
  );
  expect(reconnectSocket?.sent).toEqual([]);

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, operatorRealtimeEnvelope(16, 11, secondEventId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "11");

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(0, message);
  }, operatorRealtimeEnvelope(17, 12, revisionId, arenaTournamentId));
  await expect(state).toHaveAttribute("data-projection-revision", "11");
  await setTheme(page, "Светлая тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  const summaryBox = await state.boundingBox();
  expect(summaryBox).not.toBeNull();
  if (summaryBox === null) {
    throw new Error("Operator realtime summary is missing from the mobile Arena route");
  }
  expect(summaryBox.x).toBeGreaterThanOrEqual(0);
  expect(summaryBox.x + summaryBox.width).toBeLessThanOrEqual(390);

  await page.evaluate((message) => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { emit: (index: number, value: unknown) => void };
    }).__operatorWebSocketControl;
    control.emit(1, message);
  }, {
    type: "tournament.rejected",
    code: "tournament.forbidden",
    message: "Операторская сессия отклонена",
  });
  await expect(state).toHaveAttribute("data-connection", "rejected");
  expect(authRefreshRequests).toEqual([]);
  expect(await page.evaluate(() => {
    const control = (window as unknown as {
      __operatorWebSocketControl: { get: () => unknown[] };
    }).__operatorWebSocketControl;
    return control.get().length;
  })).toBe(2);
  expect(mutationRequests).toEqual([]);
});

test("FE-013 participant route never derives an official result from local time", async ({ page }) => {
  const fixtureSet = createTournamentFixtureSet();
  const serverTimestamp = "2026-09-15T10:00:00Z";
  const deadline = "2026-09-15T10:00:02Z";
  const mutationRequests: string[] = [];

  page.on("request", (request) => {
    const requestURL = new URL(request.url());
    if (requestURL.pathname.startsWith("/api/") && request.method() !== "GET") {
      mutationRequests.push(`${request.method()} ${requestURL.pathname}`);
    }
  });

  await page.clock.install({ time: serverTimestamp });
  await installArenaAccessRoutes(page, fixtureSet);
  await page.route(`**${arenaParticipantSnapshotPath}*`, async (route) => {
    expect(route.request().method()).toBe("GET");
    await fulfillJSON(
      route,
      participantRecoveryWithDeadline(9, serverTimestamp, deadline),
      { date: new Date(serverTimestamp).toUTCString() },
    );
  });

  await page.goto(`/arena/participant/${arenaTournamentId}`, { waitUntil: "domcontentloaded" });
  const playerPanel = page.getByTestId("participant-player-panel");
  await expect(playerPanel).toBeVisible();
  await expect(playerPanel).toHaveAttribute("data-state", "assigned");

  await page.clock.fastForward(3_000);
  await expect(playerPanel).toHaveAttribute("data-state", "assigned");
  await expect(playerPanel.getByTestId("participant-ready-button")).toBeEnabled();
  await expect(playerPanel.getByTestId("participant-runtime-status")).toHaveCount(0);
  await expect(page.getByLabel("Официальный итог")).toHaveCount(0);
  expect(mutationRequests).toEqual([]);

  await setTheme(page, "Светлая тема");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.setViewportSize({ width: 390, height: 844 });
  const panelBox = await playerPanel.boundingBox();
  expect(panelBox).not.toBeNull();
  if (panelBox === null) {
    throw new Error("Participant player panel is missing from the mobile Arena route");
  }
  expect(panelBox.x).toBeGreaterThanOrEqual(0);
  expect(panelBox.x + panelBox.width).toBeLessThanOrEqual(390);
});
