"use client";

import { useEffect, useState } from "react";

import {
  createOperatorCommandIntent,
  createTournament,
  exportTournamentIncident,
  getOperatorSnapshot,
  listTournamentAudit,
  type AuditCursor,
  type OperatorRecoveryCursor,
  type Tournament,
} from "../../../../lib/shared/api";
import type { components } from "../../../../lib/shared/api/schema";

const tournamentId = "00000000-0000-4000-8000-000000000001";
const rosterId = "00000000-0000-4000-8000-000000000002";
const participantId = "00000000-0000-4000-8000-000000000003";
const playerId = "00000000-0000-4000-8000-000000000004";
const secondParticipantId = "00000000-0000-4000-8000-000000000012";
const seriesId = "00000000-0000-4000-8000-000000000005";
const waveId = "00000000-0000-4000-8000-000000000006";
const revisionId = "00000000-0000-4000-8000-000000000007";
const gameId = "00000000-0000-4000-8000-000000000008";
const slotId = "00000000-0000-4000-8000-000000000009";
const auditEventId = "00000000-0000-4000-8000-000000000010";
const resultEventId = "00000000-0000-4000-8000-000000000011";
const snapshotBound = "operator-snapshot-1";

type Snapshot = components["schemas"]["OperatorRecoverySnapshot"];
type FixtureResult =
  | { state: "idle" | "loading" }
  | { state: "success"; value: unknown }
  | { state: "error"; error: SerializedError };
type SerializedError = {
  name: string;
  message: string;
  status?: number;
  kind?: string;
};

const serializeError = (value: unknown): SerializedError => {
  const candidate = value !== null && typeof value === "object"
    ? value as Record<string, unknown>
    : {};
  return {
    name: typeof candidate.name === "string" ? candidate.name : "Error",
    message: typeof candidate.message === "string" ? candidate.message : String(value),
    ...(typeof candidate.status === "number" ? { status: candidate.status } : {}),
    ...(typeof candidate.kind === "string" ? { kind: candidate.kind } : {}),
  };
};

const tournamentBody = (expectedRevision: number) => ({
  content_revision: 7,
  expected_revision: expectedRevision,
  name: "Демо турнир",
  planned_roster_size: 4,
  preset: "tournament_v1" as const,
  public_id: "demo-tournament",
});

const participant = () => ({
  attendance: "checked_in" as const,
  created_at: "2026-09-13T10:00:00Z",
  id: participantId,
  player_id: playerId,
  roster_id: rosterId,
  seed: 1,
  tournament_id: tournamentId,
  updated_at: "2026-09-13T10:00:00Z",
});

const roster = () => ({
  created_at: "2026-09-13T10:00:00Z",
  execution_started: false,
  execution_started_at: null,
  id: rosterId,
  locked: true,
  locked_at: "2026-09-13T10:01:00Z",
  participants: [participant()],
  revision: 2,
  tournament_id: tournamentId,
  updated_at: "2026-09-13T10:01:00Z",
});

const series = () => ({
  current_result_revision_id: null,
  current_score_revision_id: null,
  first_participant_id: participantId,
  format: "bo1" as const,
  id: seriesId,
  score: { first_participant_wins: 0, second_participant_wins: 0 },
  second_participant_id: secondParticipantId,
  slots: [{
    attempts: [{
      attempt_no: 1,
      id: gameId,
      result_reason: null,
      result_revision_id: null,
      slot_id: slotId,
      state: "planned" as const,
      winner_id: null,
    }],
    category: "web" as const,
    id: slotId,
    position: 1,
    score_before: { first_participant_wins: 0, second_participant_wins: 0 },
    series_id: seriesId,
  }],
  state: "planned" as const,
  tournament_id: tournamentId,
  winner_id: null,
});

const wave = () => ({
  id: waveId,
  members: [{
    participant_id: participantId,
    readiness_revision: 1,
    ready: false,
    series_id: seriesId,
  }, {
    participant_id: secondParticipantId,
    readiness_revision: 1,
    ready: false,
    series_id: seriesId,
  }],
  paused_at: null,
  ready_window: null,
  revision: 1,
  revision_id: revisionId,
  started_at: null,
  state: "planned" as const,
  tournament_id: tournamentId,
});

const tournament = (revision: number): Tournament => ({
  content_revision: 7,
  created_at: "2026-09-13T10:00:00Z",
  finished_at: null,
  id: tournamentId,
  name: "Демо турнир",
  paused_from_state: null,
  planned_roster_size: 4,
  preset: "tournament_v1",
  public_id: "demo-tournament",
  revision,
  roster_id: rosterId,
  roster_size: 1,
  started_at: null,
  state: "draft",
  updated_at: "2026-09-13T10:00:00Z",
});

const snapshot = (projectionRevision: number): Snapshot => ({
  next_cursor: {
    audit_sequence: 1,
    authority_revision: projectionRevision,
    projection_revision: projectionRevision,
  },
  pause_graph: null,
  roster: roster(),
  series: [series()],
  tournament: tournament(projectionRevision),
  waves: [wave()],
});

const auditEvent = (revisionNumber: number) => ({
  actor_id: null,
  actor_kind: "server" as const,
  audit_event_id: auditEventId,
  created_at: "2026-09-13T10:00:00Z",
  entity_id: seriesId,
  entity_kind: "series" as const,
  event_type: "series.created",
  is_current: true,
  is_superseded: false,
  occurred_at: "2026-09-13T10:00:00Z",
  official_result_revision_id: revisionId,
  redacted_payload: { entity_id: seriesId },
  result_event_id: resultEventId,
  result_reason: "score_complete",
  result_state: "planned",
  revision_number: revisionNumber,
  roster_id: rosterId,
  series_id: seriesId,
  tournament_id: tournamentId,
  winner_id: null,
});

const auditPage = (cursor: AuditCursor | null, revisionNumber: number) => ({
  events: [auditEvent(revisionNumber)],
  next_cursor: cursor,
});

const serializeSnapshotCursor = (value: OperatorRecoveryCursor): string =>
  `${value.projection_revision}/${value.authority_revision}/${value.audit_sequence}`;

export default function OperatorApiFixture() {
  const [hydrated, setHydrated] = useState(false);
  const [result, setResult] = useState<FixtureResult>({ state: "idle" });
  const [created, setCreated] = useState<Tournament | null>(null);
  const [snapshotState, setSnapshotState] = useState<Snapshot | null>(null);

  useEffect(() => {
    setHydrated(true);
  }, []);

  const invoke = async (operation: () => Promise<unknown>): Promise<void> => {
    setResult({ state: "loading" });
    try {
      setResult({ state: "success", value: await operation() });
    } catch (error) {
      setResult({ state: "error", error: serializeError(error) });
    }
  };

  const runCreate = async (): Promise<unknown> => {
    const response = await createTournament(
      tournamentBody(created?.revision ?? 0),
      createOperatorCommandIntent(),
    );
    setCreated(response);
    return { id: response.id, revision: response.revision };
  };

  const runSnapshot = async (): Promise<unknown> => {
    const response = await getOperatorSnapshot(tournamentId);
    setSnapshotState(response);
    return {
      revision: response.next_cursor.projection_revision,
      cursor: serializeSnapshotCursor(response.next_cursor),
    };
  };

  const runAudit = async (cursor?: AuditCursor): Promise<unknown> => {
    const response = await listTournamentAudit({ tournament_id: tournamentId, cursor });
    return {
      events: response.events.length,
      nextCursor: response.next_cursor,
    };
  };

  const runIncidentExport = async (): Promise<unknown> => {
    const response = await exportTournamentIncident(tournamentId);
    return {
      contentType: response.canonical_content_type,
      revision: response.projection_revision,
    };
  };

  return (
    <main>
      <h1>Контракт API оператора</h1>
      <p>Изолированный экран вызывает production adapter из браузерного runtime.</p>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runCreate); }}>
        Создать соревнование
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runSnapshot); }}>
        Получить снимок оператора
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(() => runAudit()); }}>
        Получить аудит
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(runIncidentExport); }}>
        Экспортировать инцидент
      </button>
      <button
        type="button"
        disabled={!hydrated}
        onClick={() => {
          const cursor = snapshotState?.next_cursor;
          void invoke(() => runAudit(cursor === undefined ? undefined : {
            audit_event_id: auditEventId,
            occurred_at: "2026-09-13T10:00:00Z",
            revision_id: revisionId,
            snapshot_bound: snapshotBound,
          }));
        }}
      >
        Следующая страница аудита
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(() => getOperatorSnapshot(tournamentId, {
        audit_sequence: 99,
        authority_revision: 99,
        projection_revision: 99,
      })); }}>
        Проверить stale revision
      </button>
      <button type="button" disabled={!hydrated} onClick={() => { void invoke(() => getOperatorSnapshot(tournamentId)); }}>
        Проверить доступ оператора
      </button>
      <p>Созданная ревизия: {created?.revision ?? "нет"}</p>
      <p>Снимок ревизия: {snapshotState?.next_cursor.projection_revision ?? "нет"}</p>
      <output aria-label="Результат вызова API" aria-live="polite">
        {result.state === "idle" && "Ожидание"}
        {result.state === "loading" && "Загрузка"}
        {result.state !== "idle" && result.state !== "loading" && JSON.stringify(result)}
      </output>
    </main>
  );
}
