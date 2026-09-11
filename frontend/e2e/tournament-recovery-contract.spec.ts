import { expect, test } from "@playwright/test";

import {
  ApiError,
  applyPublicRealtime,
  isPublicRecoveryCursorConflict,
  isPublicRecoverySnapshot,
  openPublicRealtime,
  recoverPublicTournament,
} from "../lib/shared/api";

const tournamentId = "00000000-0000-4000-8000-000000000001";
const otherTournamentId = "00000000-0000-4000-8000-000000000002";
const seriesId = "00000000-0000-4000-8000-000000000010";
const revisionId = "00000000-0000-4000-8000-000000000011";
const firstEventId = "00000000-0000-4000-8000-000000000020";
const secondEventId = "00000000-0000-4000-8000-000000000021";
const resumeId = "00000000-0000-4000-8000-000000000030";

const restSnapshot = (projectionRevision = 4, eventSequence = 7) => ({
  tournament: {
    tournament_id: tournamentId,
    projection_revision: projectionRevision,
    preset: "tournament_v1",
    state: "swiss",
    roster_size: 8,
    started_at: "2026-09-08T10:00:00Z",
    finished_at: null,
  },
  scoreboard: {
    tournament_id: tournamentId,
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
    tournament_id: tournamentId,
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
