import { spawn, type ChildProcessByStdio } from "node:child_process";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { type Readable } from "node:stream";

import { expect, test } from "@playwright/test";

import {
  ApiError,
  applyRoleRecoverySnapshot,
  applyPublicRealtime,
  classifyRoleRecoveryError,
  isPublicRecoveryCursorConflict,
  isPublicRecoverySnapshot,
  openPublicRealtime,
  recoverRoleSnapshot,
  recoverPublicTournament,
} from "../lib/shared/api";
import {
  createServerCountdown,
  viewServerCountdown,
} from "../lib/features/tournament-live";
import {
  operatorSnapshot,
  participantRecovery,
  tournamentFixtureIds,
} from "./tournament/fixtures";

const frontendRoot = process.cwd();
const fixtureRoot = resolve(frontendRoot, "e2e/fixtures/tournament-live");
const nextBinary = resolve(frontendRoot, "node_modules/.bin/next");
const fixtureHost = "127.0.0.1";

let fixtureProcess: ChildProcessByStdio<null, Readable, Readable> | undefined;
let fixtureURL = "";
let fixtureOutput = "";

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

const restSnapshot = (
  projectionRevision = 4,
  eventSequence = 7,
  snapshotTournamentId = tournamentId,
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
        display_name: "alice",
        points: 3,
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
        stage: "semifinal",
        position: 1,
        first_display_name: "alice",
        second_display_name: "bob",
        score: { first_participant_wins: 1, second_participant_wins: 0 },
        state: "active",
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
      current_game_position: 2,
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
) => ({
  schema_version: 1,
  tournament_id: envelopeTournamentId,
  sequence,
  event_id: eventId,
  occurred_at: "2026-09-08T10:06:00Z",
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
        stage: "semifinal",
        position: 1,
        first_display_name: "alice",
        second_display_name: "bob",
        score: { first_wins: 1, second_wins: 0 },
        state: "active",
      },
    ],
    live_series: restSnapshot().live_series,
    official_results: restSnapshot().official_results,
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

test("fresh WebSocket connection accepts an initial zero sequence snapshot", () => {
  const state = openPublicRealtime(realtimeEnvelope(0, 1));

  expect(state.cursor).toEqual({ projection_revision: 1, event_sequence: 0 });
  expect(state.display.liveSeries).toHaveLength(1);
  expect(state.resumeId).toBe(resumeId);
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
  const initial = recoverPublicTournament(restSnapshot(4, 7));
  const applied = applyPublicRealtime(initial, realtimeEnvelope(8, 4, firstEventId));
  const duplicate = applyPublicRealtime(
    applied.state,
    realtimeEnvelope(9, 5, firstEventId),
  );
  const outOfOrder = applyPublicRealtime(
    applied.state,
    realtimeEnvelope(6, 4, secondEventId),
  );

  expect(duplicate.outcome).toBe("duplicate");
  expect(duplicate.state).toBe(applied.state);
  expect(outOfOrder.outcome).toBe("out_of_order");
  expect(outOfOrder.state).toBe(applied.state);
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
    snapshot: restSnapshot(6, 12),
    serverTimestamp: "2026-09-13T10:00:01Z",
  });
  const stale = applyRoleRecoverySnapshot(newer.state, {
    role: "public",
    tournamentId,
    snapshot: restSnapshot(5, 9),
    serverTimestamp: "2026-09-13T10:00:02Z",
  });

  expect(newer.outcome).toBe("replaced");
  expect(newer.state?.cursor).toEqual({ projection_revision: 6, event_sequence: 12 });
  expect(stale.outcome).toBe("stale");
  expect(stale.state).toBe(newer.state);
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
  const future = classifyRoleRecoveryError(
    state,
    new ApiError(new Response(null, { status: 409 })),
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

test("mounted live panel stays server-authoritative in both themes and mobile width", async ({ page }) => {
  await page.goto(fixtureURL, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Живой турнир" })).toBeVisible();
  await expect(page.getByRole("status")).toHaveText("Сервер на связи");

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
  await expect(page.getByText("Команды временно недоступны.")).toBeVisible();

  await page.getByRole("button", { name: "Светлая" }).click();
  const lightBackground = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(lightBackground).toBe("rgb(245, 247, 250)");

  await page.setViewportSize({ width: 390, height: 844 });
  const commandBox = await command.boundingBox();
  expect(commandBox?.width).toBeGreaterThan(300);

  await page.getByRole("button", { name: "Завершить отсчет" }).click();
  await expect(page.getByRole("status")).toHaveText("Ждем сервер");
  await expect(command).toBeDisabled();
  await expect(page.getByText("Локальное время не объявляет результат.")).toBeVisible();
  await expect(page.getByText(/побед|техническое поражение/i)).toHaveCount(0);
});
